import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('dispatches a unary request through the embedded bridge', () async {
    final bridge = _TestEmbeddedBridge((request) async {
      return jsonEncode({
        'id': request['id'],
        'result': {'status': 'ok'},
        'meta': {
          'pipeline': ['auth'],
        },
      });
    });
    final client = EmbeddedRpcClient(token: 'embedded-token', bridge: bridge);

    final reply = await client.call('system.health', params: {'probe': true});

    expect(reply.result, {'status': 'ok'});
    expect(reply.meta, {
      'pipeline': ['auth'],
    });
    expect(bridge.requests.single, {
      'id': '1',
      'method': 'system.health',
      'params': {'probe': true},
      'meta': {'token': 'embedded-token'},
    });
    await client.close();
    expect(bridge.closeCalls, 1);
  });

  test(
    'streams ordered data and progress through the embedded bridge',
    () async {
      final bridge = _TestStreamingBridge((request) async* {
        yield jsonEncode({
          'id': request['id'],
          'result': null,
          'stream': {
            'sequence': 1,
            'kind': 'progress',
            'progress': {'completed': 1, 'total': 2},
          },
        });
        yield jsonEncode({
          'id': request['id'],
          'result': {'page': 1},
          'stream': {'sequence': 2, 'kind': 'data'},
        });
        yield jsonEncode({
          'id': request['id'],
          'result': null,
          'stream': {'sequence': 3, 'kind': 'complete'},
        });
      });
      final client = EmbeddedRpcClient(token: 'token', bridge: bridge);

      final events = await client.stream('reports.build').toList();

      expect(events, hasLength(2));
      expect((events[0] as RpcStreamProgress<RpcReply>).progress.completed, 1);
      expect((events[1] as RpcStreamData<RpcReply>).value.result, {'page': 1});
      expect(bridge.requests.single['meta'], {'token': 'token', 'stream': '1'});
      await client.close();
    },
  );

  test('rejects invalid embedded stream sequencing', () async {
    final bridge = _TestStreamingBridge((request) async* {
      yield jsonEncode({
        'id': request['id'],
        'result': {'page': 1},
        'stream': {'sequence': 2, 'kind': 'data'},
      });
    });
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);

    await expectLater(
      client.stream('reports.build'),
      emitsError(isA<BackendProtocolException>()),
    );
    await client.close();
  });

  test('preserves terminal embedded streaming RPC errors', () async {
    final bridge = _TestStreamingBridge((request) async* {
      yield jsonEncode({
        'id': request['id'],
        'result': null,
        'error': {'code': 'failed', 'message': 'Stream failed.'},
        'stream': {'sequence': 1, 'kind': 'complete'},
      });
    });
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);

    await expectLater(
      client.stream('reports.build'),
      emitsError(
        isA<RpcException>()
            .having((error) => error.code, 'code', 'failed')
            .having((error) => error.message, 'message', 'Stream failed.'),
      ),
    );
    await client.close();
  });

  test('stream timeout cancels the exact native request id', () async {
    final bridge = _CancellableStreamingBridge();
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);

    await expectLater(
      client.stream('reports.build', timeout: const Duration(milliseconds: 10)),
      emitsError(isA<TimeoutException>()),
    );
    expect(bridge.cancelledIDs, ['1']);
    await client.close();
  });

  test('close does not wait for a paused embedded stream consumer', () async {
    final bridge = _CancellableStreamingBridge();
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);
    final subscription = client
        .stream('reports.build')
        .listen((_) {}, onError: (_) {});
    subscription.pause();
    await Future<void>.delayed(Duration.zero);

    await client.close().timeout(const Duration(seconds: 1));

    expect(bridge.closeCalls, 1);
    subscription.resume();
    await subscription.cancel();
  });

  test('preserves application RPC errors', () async {
    final bridge = _TestEmbeddedBridge((request) async {
      return jsonEncode({
        'id': request['id'],
        'result': null,
        'error': {'code': 'not_found', 'message': 'Missing.'},
      });
    });
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);

    await expectLater(
      client.call('catalog.get'),
      throwsA(
        isA<RpcException>()
            .having((error) => error.code, 'code', 'not_found')
            .having((error) => error.message, 'message', 'Missing.'),
      ),
    );
    await client.close();
  });

  test('rejects invalid embedded responses', () async {
    final bridge = _TestEmbeddedBridge((_) async => '{"id":"wrong"}');
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);

    await expectLater(
      client.call('system.health'),
      throwsA(isA<BackendProtocolException>()),
    );
    await client.close();
  });

  test('timeout cancels the exact native request id', () async {
    final never = Completer<String>();
    final bridge = _TestEmbeddedBridge((_) => never.future);
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);

    await expectLater(
      client.call('reports.build', timeout: const Duration(milliseconds: 10)),
      throwsA(isA<TimeoutException>()),
    );
    expect(bridge.cancelledIDs, ['1']);
    await client.close();
  });

  test('manual cancellation reaches the native bridge', () async {
    final never = Completer<String>();
    final bridge = _TestEmbeddedBridge((_) => never.future);
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);
    final cancellation = RpcCancellationToken();
    final call = client.call('reports.build', cancellationToken: cancellation);
    await Future<void>.delayed(Duration.zero);

    cancellation.cancel();

    await expectLater(call, throwsA(isA<RpcCancelledException>()));
    expect(bridge.cancelledIDs, ['1']);
    await client.close();
  });

  test('requested cancellation wins a racing native response', () async {
    final bridge = _RacingCancellationBridge();
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);
    final cancellation = RpcCancellationToken();
    final call = client.call('reports.build', cancellationToken: cancellation);
    await Future<void>.delayed(Duration.zero);

    cancellation.cancel();

    await expectLater(call, throwsA(isA<RpcCancelledException>()));
    expect(bridge.cancelledIDs, ['1']);
    await client.close();
  });

  test(
    'close aborts pending calls and closes the bridge exactly once',
    () async {
      final never = Completer<String>();
      final bridge = _TestEmbeddedBridge((_) => never.future);
      final client = EmbeddedRpcClient(token: 'token', bridge: bridge);
      final call = client.call('reports.build');
      await Future<void>.delayed(Duration.zero);

      await Future.wait([client.close(), client.close()]);

      await expectLater(call, throwsA(isA<BackendClosedException>()));
      expect(bridge.cancelledIDs, isEmpty);
      expect(bridge.closeCalls, 1);
      await expectLater(
        client.call('system.health'),
        throwsA(isA<BackendClosedException>()),
      );
    },
  );

  test('fails closed for unsupported file transfer', () async {
    final bridge = _TestEmbeddedBridge((_) async => '{}');
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);
    final file = RpcFileUpload(
      name: 'input.txt',
      mediaType: 'text/plain',
      size: 0,
      sha256:
          'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
      openRead: (_) => const Stream.empty(),
    );
    final reference = RpcFileReference.fromJson({
      'id': 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      'name': 'output.txt',
      'mediaType': 'text/plain',
      'size': 0,
      'sha256':
          'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
      'expiresAt': DateTime.now()
          .toUtc()
          .add(const Duration(minutes: 1))
          .toIso8601String(),
    });

    await expectLater(
      client.download(reference),
      emitsError(isA<EmbeddedRpcUnsupportedException>()),
    );
    await expectLater(
      client.upload(file),
      throwsA(isA<EmbeddedRpcUnsupportedException>()),
    );
    await client.close();
  });

  test('resumes an interrupted embedded download and commits once', () async {
    final content = Uint8List.fromList(utf8.encode('embedded download bytes'));
    final bridge = _TestFileBridge(downloadBytes: content)
      ..failSecondDownloadRead = true;
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);
    final reference = _fileReference(
      id: _TestFileBridge.downloadID,
      name: 'result.txt',
      bytes: content,
    );

    final received = await client
        .download(reference)
        .expand((value) => value)
        .toList();

    expect(received, content);
    expect(bridge.downloadOffsets, [0, 4]);
    expect(bridge.downloadCommits, [false, true]);
    await client.close();
  });

  test('cancels an active embedded download and releases its handle', () async {
    final content = Uint8List.fromList(utf8.encode('embedded download bytes'));
    final bridge = _TestFileBridge(downloadBytes: content)
      ..blockDownloadRead = true;
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);
    final cancellation = RpcCancellationToken();
    final download = client
        .download(
          _fileReference(
            id: _TestFileBridge.downloadID,
            name: 'result.txt',
            bytes: content,
          ),
          cancellationToken: cancellation,
        )
        .drain<void>();
    await bridge.downloadReadStarted.future;

    cancellation.cancel();

    await expectLater(download, throwsA(isA<RpcCancelledException>()));
    expect(bridge.downloadCommits, [false]);
    await client.close();
  });

  test('rejects an embedded download with a mismatched digest', () async {
    final content = Uint8List.fromList(utf8.encode('unexpected'));
    final bridge = _TestFileBridge(downloadBytes: content);
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);
    final reference = RpcFileReference.fromJson({
      'id': _TestFileBridge.downloadID,
      'name': 'result.txt',
      'mediaType': 'text/plain',
      'size': content.length,
      'sha256': sha256.convert(utf8.encode('expected')).toString(),
      'expiresAt': DateTime.now()
          .toUtc()
          .add(const Duration(minutes: 1))
          .toIso8601String(),
    });

    await expectLater(
      client.download(reference).drain<void>(),
      throwsA(isA<BackendProtocolException>()),
    );
    await client.close();
  });

  test('resumes an accepted embedded upload after a lost response', () async {
    final content = Uint8List.fromList(
      List<int>.generate(70 * 1024, (index) => index % 251),
    );
    final bridge = _TestFileBridge()..loseFirstUploadResponse = true;
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);
    final sourceOffsets = <int>[];
    final upload = RpcFileUpload(
      name: 'input.bin',
      mediaType: 'application/octet-stream',
      size: content.length,
      sha256: sha256.convert(content).toString(),
      openRead: (offset) {
        sourceOffsets.add(offset);
        return Stream.value(content.sublist(offset));
      },
    );

    final reference = await client.upload(upload);

    expect(reference.id, _TestFileBridge.uploadID);
    expect(bridge.uploaded, content);
    expect(sourceOffsets, [0, 64 * 1024]);
    expect(bridge.uploadStatusCalls, 1);
    expect(bridge.appendSizes, [64 * 1024, 6 * 1024]);
    await client.close();
  });

  test('validates construction and wraps native bridge failures', () async {
    expect(
      () => EmbeddedRpcClient(token: '', bridge: _TestEmbeddedBridge(null)),
      throwsArgumentError,
    );
    expect(
      () => EmbeddedRpcClient(
        token: 'token',
        bridge: _TestEmbeddedBridge(null),
        cancellationTimeout: Duration.zero,
      ),
      throwsArgumentError,
    );

    final bridge = _TestEmbeddedBridge((_) async => throw StateError('native'));
    final client = EmbeddedRpcClient(token: 'token', bridge: bridge);
    await expectLater(
      client.call('system.health'),
      throwsA(
        isA<BackendTransportException>().having(
          (error) => error.cause,
          'cause',
          isA<StateError>(),
        ),
      ),
    );
    await client.close();
  });
}

RpcFileReference _fileReference({
  required String id,
  required String name,
  required Uint8List bytes,
}) {
  return RpcFileReference.fromJson({
    'id': id,
    'name': name,
    'mediaType': 'text/plain',
    'size': bytes.length,
    'sha256': sha256.convert(bytes).toString(),
    'expiresAt': DateTime.now()
        .toUtc()
        .add(const Duration(minutes: 1))
        .toIso8601String(),
  });
}

typedef _Responder = Future<String> Function(Map<String, dynamic> request);

final class _TestEmbeddedBridge implements EmbeddedRpcBridge {
  _TestEmbeddedBridge(this._responder);

  final _Responder? _responder;
  final List<Map<String, dynamic>> requests = [];
  final List<String> cancelledIDs = [];
  int closeCalls = 0;

  @override
  Future<String> call(String requestJSON) {
    final request = Map<String, dynamic>.from(jsonDecode(requestJSON) as Map);
    requests.add(request);
    final responder = _responder;
    if (responder == null) {
      throw StateError('No responder configured.');
    }
    return responder(request);
  }

  @override
  Future<void> cancel(String requestID) async {
    cancelledIDs.add(requestID);
  }

  @override
  Future<void> close() async {
    closeCalls++;
  }

  @override
  Stream<String> stream(String requestJSON) =>
      Stream.error(const EmbeddedRpcUnsupportedException('streaming'));
}

final class _RacingCancellationBridge implements EmbeddedRpcBridge {
  final List<String> cancelledIDs = [];
  final Completer<String> _response = Completer<String>();
  String? _requestID;

  @override
  Future<String> call(String requestJSON) {
    final request = jsonDecode(requestJSON) as Map<String, dynamic>;
    _requestID = request['id'] as String;
    return _response.future;
  }

  @override
  Future<void> cancel(String requestID) async {
    cancelledIDs.add(requestID);
    _response.complete(
      jsonEncode({
        'id': _requestID,
        'result': {'status': 'cancelled'},
      }),
    );
  }

  @override
  Future<void> close() async {}

  @override
  Stream<String> stream(String requestJSON) =>
      Stream.error(const EmbeddedRpcUnsupportedException('streaming'));
}

typedef _StreamResponder =
    Stream<String> Function(Map<String, dynamic> request);

final class _TestStreamingBridge implements EmbeddedRpcBridge {
  _TestStreamingBridge(this._responder);

  final _StreamResponder _responder;
  final List<Map<String, dynamic>> requests = [];

  @override
  Future<String> call(String requestJSON) =>
      Future.error(StateError('Unary calls are not configured.'));

  @override
  Stream<String> stream(String requestJSON) {
    final request = Map<String, dynamic>.from(jsonDecode(requestJSON) as Map);
    requests.add(request);
    return _responder(request);
  }

  @override
  Future<void> cancel(String requestID) async {}

  @override
  Future<void> close() async {}
}

final class _CancellableStreamingBridge implements EmbeddedRpcBridge {
  final List<String> cancelledIDs = [];
  final Completer<void> _cancelled = Completer<void>();
  int closeCalls = 0;

  @override
  Future<String> call(String requestJSON) =>
      Future.error(StateError('Unary calls are not configured.'));

  @override
  Stream<String> stream(String requestJSON) async* {
    await _cancelled.future;
    throw StateError('native stream cancelled');
  }

  @override
  Future<void> cancel(String requestID) async {
    cancelledIDs.add(requestID);
    if (!_cancelled.isCompleted) _cancelled.complete();
  }

  @override
  Future<void> close() async {
    closeCalls++;
    if (!_cancelled.isCompleted) _cancelled.complete();
  }
}

final class _TestFileBridge
    implements EmbeddedRpcBridge, EmbeddedFileTransferBridge {
  _TestFileBridge({Uint8List? downloadBytes})
    : downloadBytes = downloadBytes ?? Uint8List(0);

  static const downloadID =
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
  static const uploadID =
      'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';

  final Uint8List downloadBytes;
  final List<int> downloadOffsets = [];
  final List<bool> downloadCommits = [];
  final List<int> appendSizes = [];
  final BytesBuilder _uploaded = BytesBuilder(copy: false);
  var failSecondDownloadRead = false;
  var blockDownloadRead = false;
  var loseFirstUploadResponse = false;
  var uploadStatusCalls = 0;
  final Completer<void> downloadReadStarted = Completer<void>();
  var _downloadOffset = 0;
  var _downloadReads = 0;
  String _uploadName = '';
  String _uploadMediaType = '';
  String _uploadSHA256 = '';
  var _uploadSize = 0;

  Uint8List get uploaded => _uploaded.toBytes();

  @override
  Future<String> call(String requestJSON) => Future.error(StateError('unused'));

  @override
  Stream<String> stream(String requestJSON) =>
      Stream.error(StateError('unused'));

  @override
  Future<void> cancel(String requestID) async {}

  @override
  Future<void> close() async {}

  @override
  Future<String> openDownload(String fileID, int offset) async {
    expect(fileID, downloadID);
    downloadOffsets.add(offset);
    _downloadOffset = offset;
    return 'download-${downloadOffsets.length}';
  }

  @override
  Future<Uint8List?> readDownload(String handle, int maxBytes) async {
    _downloadReads++;
    if (!downloadReadStarted.isCompleted) downloadReadStarted.complete();
    if (blockDownloadRead) {
      await Completer<void>().future;
    }
    if (failSecondDownloadRead && _downloadReads == 2) {
      throw StateError('lost download response');
    }
    if (_downloadOffset == downloadBytes.length) return null;
    final end = _downloadOffset + 4 < downloadBytes.length
        ? _downloadOffset + 4
        : downloadBytes.length;
    final chunk = Uint8List.fromList(
      downloadBytes.sublist(_downloadOffset, end),
    );
    _downloadOffset = end;
    return chunk;
  }

  @override
  Future<void> closeDownload(String handle, {required bool commit}) async {
    downloadCommits.add(commit);
  }

  @override
  Future<String> beginUpload({
    required String name,
    required String mediaType,
    required int size,
    required String sha256,
  }) async {
    _uploadName = name;
    _uploadMediaType = mediaType;
    _uploadSize = size;
    _uploadSHA256 = sha256;
    return _uploadState();
  }

  @override
  Future<String> appendUpload(
    String fileID,
    int offset,
    Uint8List chunk,
  ) async {
    expect(fileID, uploadID);
    expect(offset, _uploaded.length);
    appendSizes.add(chunk.length);
    _uploaded.add(chunk);
    if (loseFirstUploadResponse) {
      loseFirstUploadResponse = false;
      throw StateError('lost upload response');
    }
    return _uploadState();
  }

  @override
  Future<String> uploadStatus(String fileID) async {
    expect(fileID, uploadID);
    uploadStatusCalls++;
    return _uploadState();
  }

  String _uploadState() => jsonEncode({
    'file': {
      'id': uploadID,
      'name': _uploadName,
      'mediaType': _uploadMediaType,
      'size': _uploadSize,
      'sha256': _uploadSHA256,
      'expiresAt': DateTime.now()
          .toUtc()
          .add(const Duration(minutes: 1))
          .toIso8601String(),
    },
    'offset': _uploaded.length,
    'complete': _uploaded.length == _uploadSize,
  });
}
