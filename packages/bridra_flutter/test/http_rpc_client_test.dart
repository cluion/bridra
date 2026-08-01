import 'dart:async';
import 'dart:convert';

import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

void main() {
  test('streams NDJSON progress and data in order', () async {
    final transport = StreamingResponseClient();
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: transport,
    );
    addTearDown(client.close);

    final events = await client.stream('reports.build').toList();

    expect(transport.authorization, 'Bearer remote-token');
    expect(transport.payload['method'], 'reports.build');
    expect((transport.payload['meta'] as Map)['stream'], '1');
    final progress = events[0] as RpcStreamProgress<RpcReply>;
    expect(progress.progress.completed, 1);
    expect(progress.progress.total, 2);
    final data = events[1] as RpcStreamData<RpcReply>;
    expect(data.value.result, {'page': 1});
  });

  test('rejects stream calls after close or prior cancellation', () async {
    var sends = 0;
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: MockClient((_) async {
        sends++;
        return http.Response('{}', 200);
      }),
    );
    final cancellationToken = RpcCancellationToken()..cancel();

    await expectLater(
      client
          .stream('reports.build', cancellationToken: cancellationToken)
          .toList(),
      throwsA(isA<RpcCancelledException>()),
    );
    await client.close();
    await expectLater(
      client.stream('reports.build').toList(),
      throwsA(isA<BackendClosedException>()),
    );
    expect(sends, 0);
  });

  test('maps stream status and framing failures', () async {
    final statusClient = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: StreamingResponseClient(statusCode: 503),
    );
    addTearDown(statusClient.close);
    await expectLater(
      statusClient.stream('reports.build').toList(),
      throwsA(isA<BackendTransportException>()),
    );
    final limitedClient = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: StreamingResponseClient(statusCode: 429, retryAfter: '3'),
    );
    addTearDown(limitedClient.close);
    await expectLater(
      limitedClient.stream('reports.build').toList(),
      throwsA(
        isA<RpcRateLimitedException>().having(
          (error) => error.retryAfter,
          'retryAfter',
          const Duration(seconds: 3),
        ),
      ),
    );

    final sequenceClient = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: StreamingResponseClient(
        frames: (id) => [
          {
            'id': id,
            'result': {'page': 1},
            'stream': {'sequence': 2, 'kind': 'data'},
          },
        ],
      ),
    );
    addTearDown(sequenceClient.close);
    await expectLater(
      sequenceClient.stream('reports.build').toList(),
      throwsA(isA<BackendProtocolException>()),
    );

    final incompleteClient = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: StreamingResponseClient(
        frames: (id) => [
          {
            'id': id,
            'result': {'page': 1},
            'stream': {'sequence': 1, 'kind': 'data'},
          },
        ],
      ),
    );
    addTearDown(incompleteClient.close);
    await expectLater(
      incompleteClient.stream('reports.build').toList(),
      throwsA(isA<BackendProtocolException>()),
    );
  });

  test('preserves terminal streaming RPC errors', () async {
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: StreamingResponseClient(
        frames: (id) => [
          {
            'id': id,
            'result': null,
            'error': {'code': 'report_failed', 'message': 'Report failed.'},
            'stream': {'sequence': 1, 'kind': 'complete'},
          },
        ],
      ),
    );
    addTearDown(client.close);

    await expectLater(
      client.stream('reports.build').toList(),
      throwsA(
        isA<RpcException>().having(
          (error) => error.code,
          'code',
          'report_failed',
        ),
      ),
    );
  });

  test('rejects invalid endpoint and token configuration', () {
    expect(
      () => HttpRpcClient(endpoint: Uri.parse('/rpc'), token: 'token'),
      throwsArgumentError,
    );
    expect(
      () => HttpRpcClient(
        endpoint: Uri.parse('ftp://backend.example/rpc'),
        token: 'token',
      ),
      throwsArgumentError,
    );
    expect(
      () => HttpRpcClient(
        endpoint: Uri.parse('https://backend.example/rpc'),
        token: '',
      ),
      throwsArgumentError,
    );
  });

  test('sends a versioned RPC request and decodes the reply', () async {
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: MockClient((request) async {
        expect(request.method, 'POST');
        expect(request.headers['authorization'], 'Bearer remote-token');
        expect(request.headers['content-type'], 'application/json');
        final payload = Map<String, dynamic>.from(
          jsonDecode(request.body) as Map,
        );
        expect(payload['method'], 'system.health');
        expect((payload['meta'] as Map)['token'], 'remote-token');
        return http.Response(
          jsonEncode({
            'id': payload['id'],
            'result': {'status': 'ok'},
            'meta': {
              'pipeline': ['auth:before', 'auth:after'],
            },
          }),
          200,
        );
      }),
    );
    addTearDown(client.close);

    final reply = await client.call('system.health');

    expect((reply.result as Map)['status'], 'ok');
    expect(reply.meta['pipeline'], ['auth:before', 'auth:after']);
  });

  test('preserves structured RPC errors', () async {
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: MockClient((request) async {
        final payload = jsonDecode(request.body) as Map;
        return http.Response(
          jsonEncode({
            'id': payload['id'],
            'error': {
              'code': 'validation_error',
              'message': 'Invalid.',
              'data': {'field': 'name'},
            },
          }),
          200,
        );
      }),
    );
    addTearDown(client.close);

    await expectLater(
      client.call('greeting.hello'),
      throwsA(
        isA<RpcException>()
            .having((error) => error.code, 'code', 'validation_error')
            .having((error) => error.data['field'], 'field', 'name'),
      ),
    );
  });

  test('maps HTTP failures to transport errors', () async {
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: MockClient((_) async => http.Response('unavailable', 503)),
    );
    addTearDown(client.close);

    await expectLater(
      client.call('system.health'),
      throwsA(isA<BackendTransportException>()),
    );

    final limitedClient = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: MockClient(
        (_) async => http.Response('', 429, headers: {'retry-after': '7'}),
      ),
    );
    addTearDown(limitedClient.close);
    await expectLater(
      limitedClient.call('system.health'),
      throwsA(
        isA<RpcRateLimitedException>().having(
          (error) => error.retryAfter,
          'retryAfter',
          const Duration(seconds: 7),
        ),
      ),
    );
  });

  test('maps malformed envelopes to protocol errors', () async {
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: MockClient(
        (_) async => http.Response('{"id":"wrong","result":{}}', 200),
      ),
    );
    addTearDown(client.close);

    await expectLater(
      client.call('system.health'),
      throwsA(isA<BackendProtocolException>()),
    );
  });

  test('supports timeouts and idempotent close', () async {
    final transport = AbortObservingClient();
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: transport,
    );

    await expectLater(
      client.call('system.health', timeout: const Duration(milliseconds: 5)),
      throwsA(isA<TimeoutException>()),
    );
    await Future<void>.delayed(Duration.zero);
    expect(transport.aborted, isTrue);
    await client.close();
    await client.close();
    await expectLater(
      client.call('system.health'),
      throwsA(isA<BackendClosedException>()),
    );
  });

  test('caller cancellation aborts only its HTTP request', () async {
    final transport = AbortObservingClient();
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: transport,
    );
    addTearDown(client.close);
    final cancellationToken = RpcCancellationToken();

    final call = client.call(
      'system.health',
      cancellationToken: cancellationToken,
    );
    cancellationToken.cancel();

    await expectLater(call, throwsA(isA<RpcCancelledException>()));
    await Future<void>.delayed(Duration.zero);
    expect(transport.aborted, isTrue);
  });

  test('already cancelled calls do not send an HTTP request', () async {
    var sends = 0;
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: MockClient((_) async {
        sends++;
        return http.Response('{}', 200);
      }),
    );
    addTearDown(client.close);
    final cancellationToken = RpcCancellationToken()..cancel();

    await expectLater(
      client.call('system.health', cancellationToken: cancellationToken),
      throwsA(isA<RpcCancelledException>()),
    );
    expect(sends, 0);
  });

  test('downloads and verifies a one-time file stream', () async {
    final content = utf8.encode('large report');
    final transport = FileDownloadClient([
      content.sublist(0, 4),
      content.sublist(4),
    ]);
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc?discarded=query'),
      token: 'remote-token',
      client: transport,
    );
    addTearDown(client.close);
    final reference = _httpFileReference(content);

    final downloaded = await client
        .download(reference)
        .expand((chunk) => chunk)
        .toList();

    expect(downloaded, content);
    expect(transport.method, 'GET');
    expect(
      transport.url,
      Uri.parse('https://backend.example/rpc/files/${reference.id}'),
    );
    expect(transport.accept, 'text/plain');
  });

  test('maps missing and cancelled file downloads', () async {
    final content = utf8.encode('large report');
    final missingTransport = FileDownloadClient(const [], statusCode: 404);
    final missingClient = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: missingTransport,
    );
    addTearDown(missingClient.close);
    await expectLater(
      missingClient.download(_httpFileReference(content)).toList(),
      throwsA(isA<RpcFileUnavailableException>()),
    );

    final cancelledTransport = FileDownloadClient([content]);
    final cancelledClient = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: cancelledTransport,
    );
    addTearDown(cancelledClient.close);
    final cancellationToken = RpcCancellationToken()..cancel();
    await expectLater(
      cancelledClient
          .download(
            _httpFileReference(content),
            cancellationToken: cancellationToken,
          )
          .toList(),
      throwsA(isA<RpcCancelledException>()),
    );
    expect(cancelledTransport.sendCount, 0);
  });

  test('resumes an interrupted file download with a byte range', () async {
    final content = utf8.encode('resumable report');
    final transport = ResumableFileDownloadClient(content, firstBytes: 5);
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: transport,
    );
    addTearDown(client.close);

    final downloaded = await client
        .download(_httpFileReference(content))
        .expand((chunk) => chunk)
        .toList();

    expect(downloaded, content);
    expect(transport.ranges, [null, 'bytes=5-']);
  });

  test('resumes a verified file upload from the server offset', () async {
    final content = utf8.encode('resumable upload');
    final transport = ResumableFileUploadClient(content, firstBytes: 6);
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: transport,
    );
    addTearDown(client.close);
    final upload = RpcFileUpload(
      name: 'upload.bin',
      mediaType: 'application/octet-stream',
      size: content.length,
      sha256: sha256.convert(content).toString(),
      openRead: (offset) => Stream.value(content.sublist(offset)),
    );

    final reference = await client.upload(upload);

    expect(reference.name, upload.name);
    expect(reference.sha256, upload.sha256);
    expect(transport.methods, ['POST', 'PATCH', 'HEAD', 'PATCH']);
    expect(transport.patchOffsets, [0, 6]);
    expect(transport.token, 'remote-token');
  });

  test('validates bounded file transfer attempts and expiry', () async {
    final content = utf8.encode('file');
    final transport = FileDownloadClient([content.sublist(0, 1)]);
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: transport,
    );
    final reference = _httpFileReference(content);
    final upload = RpcFileUpload(
      name: 'upload.bin',
      mediaType: 'application/octet-stream',
      size: content.length,
      sha256: sha256.convert(content).toString(),
      openRead: (offset) => Stream.value(content.sublist(offset)),
    );

    await expectLater(
      client.download(reference, maxAttempts: 0).toList(),
      throwsArgumentError,
    );
    await expectLater(
      client.download(reference, maxAttempts: 1).toList(),
      throwsA(isA<BackendTransportException>()),
    );
    await expectLater(
      client
          .download(
            RpcFileReference.fromJson({
              ...reference.toJson(),
              'expiresAt': DateTime.now()
                  .subtract(const Duration(seconds: 1))
                  .toUtc()
                  .toIso8601String(),
            }),
          )
          .toList(),
      throwsA(isA<RpcFileExpiredException>()),
    );
    await expectLater(
      client.upload(upload, maxAttempts: 0),
      throwsArgumentError,
    );
    await expectLater(
      client.upload(
        upload,
        cancellationToken: RpcCancellationToken()..cancel(),
      ),
      throwsA(isA<RpcCancelledException>()),
    );
    await client.close();
    await expectLater(
      client.upload(upload),
      throwsA(isA<BackendClosedException>()),
    );
  });

  test('rejects an upload checksum failure from the backend', () async {
    final content = utf8.encode('upload');
    final transport = ResumableFileUploadClient(
      content,
      firstBytes: 0,
      failFirst: false,
      patchStatusCode: 422,
    );
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: transport,
    );
    addTearDown(client.close);

    await expectLater(
      client.upload(
        RpcFileUpload(
          name: 'upload.bin',
          mediaType: 'application/octet-stream',
          size: content.length,
          sha256: sha256.convert(content).toString(),
          openRead: (offset) => Stream.value(content.sublist(offset)),
        ),
      ),
      throwsA(isA<BackendProtocolException>()),
    );
  });

  test(
    'rejects malformed upload creation, progress, and resume state',
    () async {
      final content = utf8.encode('upload');
      final upload = RpcFileUpload(
        name: 'upload.bin',
        mediaType: 'application/octet-stream',
        size: content.length,
        sha256: sha256.convert(content).toString(),
        openRead: (offset) => Stream.value(content.sublist(offset)),
      );
      for (final mode in MalformedUploadMode.values) {
        final client = HttpRpcClient(
          endpoint: Uri.parse('https://backend.example/rpc'),
          token: 'remote-token',
          client: MalformedFileUploadClient(mode),
        );
        await expectLater(
          client.upload(upload),
          throwsA(isA<BackendProtocolException>()),
          reason: '$mode',
        );
        await client.close();
      }
    },
  );

  test('aborts active file transfers on timeout and cancellation', () async {
    final content = utf8.encode('file');
    final reference = _httpFileReference(content);
    final downloadCancellation = RpcCancellationToken();
    final cancelledDownloadClient = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: AbortObservingClient(),
    );
    final cancelledDownload = cancelledDownloadClient
        .download(reference, cancellationToken: downloadCancellation)
        .toList();
    await Future<void>.delayed(Duration.zero);
    downloadCancellation.cancel();
    await expectLater(cancelledDownload, throwsA(isA<RpcCancelledException>()));
    await cancelledDownloadClient.close();

    final timedOutDownloadClient = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: AbortObservingClient(),
    );
    await expectLater(
      timedOutDownloadClient
          .download(reference, timeout: const Duration(milliseconds: 5))
          .toList(),
      throwsA(isA<TimeoutException>()),
    );
    await timedOutDownloadClient.close();

    final uploadCancellation = RpcCancellationToken();
    final cancelledUploadClient = HttpRpcClient(
      endpoint: Uri.parse('https://backend.example/rpc'),
      token: 'remote-token',
      client: AbortObservingClient(),
    );
    final upload = RpcFileUpload(
      name: 'upload.bin',
      mediaType: 'application/octet-stream',
      size: content.length,
      sha256: sha256.convert(content).toString(),
      openRead: (offset) => Stream.value(content.sublist(offset)),
    );
    final cancelledUpload = cancelledUploadClient.upload(
      upload,
      cancellationToken: uploadCancellation,
    );
    await Future<void>.delayed(Duration.zero);
    uploadCancellation.cancel();
    await expectLater(cancelledUpload, throwsA(isA<RpcCancelledException>()));
    await cancelledUploadClient.close();
  });
}

class AbortObservingClient extends http.BaseClient {
  var aborted = false;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final abortable = request as http.Abortable;
    await abortable.abortTrigger;
    aborted = true;
    throw http.RequestAbortedException(request.url);
  }
}

class StreamingResponseClient extends http.BaseClient {
  StreamingResponseClient({
    this.frames,
    this.statusCode = 200,
    this.retryAfter,
  });

  final List<Map<String, Object?>> Function(Object? id)? frames;
  final int statusCode;
  final String? retryAfter;
  Map<String, dynamic> payload = {};
  String? authorization;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    authorization = request.headers['authorization'];
    payload = Map<String, dynamic>.from(
      jsonDecode(await request.finalize().bytesToString()) as Map,
    );
    final id = payload['id'];
    final responseFrames =
        frames?.call(id) ??
        [
          {
            'id': id,
            'result': null,
            'meta': <String, Object?>{},
            'stream': {
              'sequence': 1,
              'kind': 'progress',
              'progress': {'completed': 1, 'total': 2},
            },
          },
          {
            'id': id,
            'result': {'page': 1},
            'meta': <String, Object?>{},
            'stream': {'sequence': 2, 'kind': 'data'},
          },
          {
            'id': id,
            'result': null,
            'meta': <String, Object?>{},
            'stream': {'sequence': 3, 'kind': 'complete'},
          },
        ];
    final body = responseFrames.map(jsonEncode).join('\n');
    return http.StreamedResponse(
      Stream.value(utf8.encode('$body\n')),
      statusCode,
      headers: {
        'content-type': 'application/x-ndjson',
        'retry-after': ?retryAfter,
      },
    );
  }
}

class FileDownloadClient extends http.BaseClient {
  FileDownloadClient(this.chunks, {this.statusCode = 200});

  final List<List<int>> chunks;
  final int statusCode;
  Uri? url;
  String? method;
  String? accept;
  var sendCount = 0;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    sendCount++;
    url = request.url;
    method = request.method;
    accept = request.headers['accept'];
    return http.StreamedResponse(
      Stream.fromIterable(chunks),
      statusCode,
      contentLength: chunks.fold<int>(
        0,
        (total, chunk) => total + chunk.length,
      ),
    );
  }
}

class ResumableFileDownloadClient extends http.BaseClient {
  ResumableFileDownloadClient(this.content, {required this.firstBytes});

  final List<int> content;
  final int firstBytes;
  final ranges = <String?>[];

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final range = request.headers['range'];
    ranges.add(range);
    if (range == null) {
      return http.StreamedResponse(
        Stream.value(content.sublist(0, firstBytes)),
        200,
      );
    }
    final offset = int.parse(range.substring(6, range.length - 1));
    return http.StreamedResponse(
      Stream.value(content.sublist(offset)),
      206,
      headers: {
        'content-range':
            'bytes $offset-${content.length - 1}/${content.length}',
      },
    );
  }
}

class ResumableFileUploadClient extends http.BaseClient {
  ResumableFileUploadClient(
    this.content, {
    required this.firstBytes,
    this.failFirst = true,
    this.patchStatusCode = 200,
  });

  final List<int> content;
  final int firstBytes;
  final bool failFirst;
  final int patchStatusCode;
  final methods = <String>[];
  final patchOffsets = <int>[];
  String? token;
  late Map<String, dynamic> metadata;

  String get id => 'd' * 64;

  String get expiresAt =>
      DateTime.now().add(const Duration(hours: 1)).toUtc().toIso8601String();

  Map<String, Object?> state(int offset, bool complete) => {
    'file': {'id': id, ...metadata, 'expiresAt': expiresAt},
    'offset': offset,
    'complete': complete,
  };

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    methods.add(request.method);
    switch (request.method) {
      case 'POST':
        token = request.headers['x-bridra-token'];
        metadata = Map<String, dynamic>.from(
          jsonDecode(await request.finalize().bytesToString()) as Map,
        );
        return http.StreamedResponse(
          Stream.value(utf8.encode(jsonEncode(state(0, false)))),
          201,
        );
      case 'HEAD':
        return http.StreamedResponse(
          const Stream.empty(),
          204,
          headers: {
            'upload-offset': '$firstBytes',
            'upload-length': '${content.length}',
            'upload-complete': 'false',
            'upload-expires-at': expiresAt,
          },
        );
      case 'PATCH':
        final offset = int.parse(request.headers['upload-offset']!);
        patchOffsets.add(offset);
        final body = await request.finalize().toBytes();
        expect(body, content.sublist(offset));
        if (patchOffsets.length == 1 && failFirst) {
          throw http.ClientException('connection dropped', request.url);
        }
        if (patchStatusCode != 200) {
          return http.StreamedResponse(const Stream.empty(), patchStatusCode);
        }
        return http.StreamedResponse(
          Stream.value(utf8.encode(jsonEncode(state(content.length, true)))),
          200,
        );
      default:
        throw StateError('Unexpected ${request.method} request.');
    }
  }
}

enum MalformedUploadMode { creation, reference, progress, resume }

class MalformedFileUploadClient extends http.BaseClient {
  MalformedFileUploadClient(this.mode);

  final MalformedUploadMode mode;
  late Map<String, dynamic> metadata;

  Map<String, Object?> state({
    required int offset,
    required bool complete,
    bool mismatch = false,
  }) => {
    'file': {
      'id': 'e' * 64,
      ...metadata,
      if (mismatch) 'name': 'wrong.bin',
      'expiresAt': DateTime.now()
          .add(const Duration(hours: 1))
          .toUtc()
          .toIso8601String(),
    },
    'offset': offset,
    'complete': complete,
  };

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    switch (request.method) {
      case 'POST':
        metadata = Map<String, dynamic>.from(
          jsonDecode(await request.finalize().bytesToString()) as Map,
        );
        final body = mode == MalformedUploadMode.creation
            ? '{}'
            : jsonEncode(
                state(
                  offset: mode == MalformedUploadMode.reference
                      ? metadata['size'] as int
                      : 0,
                  complete: mode == MalformedUploadMode.reference,
                  mismatch: mode == MalformedUploadMode.reference,
                ),
              );
        return http.StreamedResponse(Stream.value(utf8.encode(body)), 201);
      case 'PATCH':
        await request.finalize().drain<void>();
        if (mode == MalformedUploadMode.resume) {
          throw http.ClientException('connection dropped', request.url);
        }
        return http.StreamedResponse(
          Stream.value(
            utf8.encode(jsonEncode(state(offset: 0, complete: false))),
          ),
          200,
        );
      case 'HEAD':
        return http.StreamedResponse(const Stream.empty(), 204);
      default:
        throw StateError('Unexpected ${request.method} request.');
    }
  }
}

RpcFileReference _httpFileReference(List<int> content) {
  return RpcFileReference.fromJson({
    'id': 'b' * 64,
    'name': 'report.txt',
    'mediaType': 'text/plain',
    'size': content.length,
    'sha256': sha256.convert(content).toString(),
    'expiresAt': DateTime.now()
        .add(const Duration(hours: 1))
        .toUtc()
        .toIso8601String(),
  });
}
