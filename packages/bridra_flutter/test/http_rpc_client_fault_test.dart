import 'dart:async';
import 'dart:convert';

import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

void main() {
  group('HTTP weak-network fault injection', () {
    test(
      'slow chunked RPC completes within its deadline without replay',
      () async {
        final transport = SlowChunkedCallClient();
        final client = _client(transport);
        addTearDown(client.close);

        final reply = await client.call(
          'system.health',
          timeout: const Duration(seconds: 1),
        );

        expect(reply.result, {'status': 'ok'});
        expect(transport.sendCount, 1);
      },
    );

    test('timed out RPC aborts once and is never replayed', () async {
      final transport = TimeoutCallClient();
      final client = _client(transport);
      addTearDown(client.close);

      await expectLater(
        client.call('orders.create', timeout: const Duration(milliseconds: 50)),
        throwsA(isA<TimeoutException>()),
      );

      await Future<void>.delayed(Duration.zero);
      expect(transport.sendCount, 1);
      expect(transport.aborted, isTrue);
    });

    test(
      'paused stream propagates backpressure to the network source',
      () async {
        final transport = BackpressureStreamClient();
        final client = _client(transport);
        addTearDown(client.close);
        final events = <RpcStreamEvent<RpcReply>>[];
        final firstEvent = Completer<void>();
        final done = Completer<void>();
        late StreamSubscription<RpcStreamEvent<RpcReply>> subscription;

        subscription = client
            .stream('reports.build')
            .listen(
              (event) {
                events.add(event);
                if (events.length == 1) {
                  subscription.pause();
                  firstEvent.complete();
                }
              },
              onError: (Object error, StackTrace stackTrace) {
                done.completeError(error, stackTrace);
              },
              onDone: done.complete,
            );

        await firstEvent.future.timeout(const Duration(seconds: 1));
        await transport.paused.future.timeout(const Duration(seconds: 1));
        await Future<void>.delayed(const Duration(milliseconds: 20));
        expect(transport.producedFrames, 1);

        subscription.resume();
        await done.future.timeout(const Duration(seconds: 1));

        expect(events, hasLength(2));
        expect(events.first, isA<RpcStreamData<RpcReply>>());
        expect(events.last, isA<RpcStreamProgress<RpcReply>>());
        expect(transport.producedFrames, 3);
        expect(transport.sendCount, 1);
      },
    );

    test(
      'interrupted stream fails as transport error without replay',
      () async {
        final transport = InterruptedStreamClient();
        final client = _client(transport);
        addTearDown(client.close);

        await expectLater(
          client.stream('reports.build').toList(),
          throwsA(isA<BackendTransportException>()),
        );

        expect(transport.sendCount, 1);
      },
    );

    test('dropped download resumes without duplicate bytes', () async {
      final content = utf8.encode('weak-network resumable download');
      final transport = DroppedDownloadClient(content, firstBytes: 9);
      final client = _client(transport);
      addTearDown(client.close);

      final downloaded = await client
          .download(_fileReference(content))
          .expand((chunk) => chunk)
          .toList();

      expect(downloaded, content);
      expect(transport.ranges, [null, 'bytes=9-']);
      expect(transport.sendCount, 2);
    });
  });
}

HttpRpcClient _client(http.Client transport) {
  return HttpRpcClient(
    endpoint: Uri.parse('https://backend.example/rpc'),
    token: 'fault-test-token',
    client: transport,
  );
}

class SlowChunkedCallClient extends http.BaseClient {
  var sendCount = 0;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    sendCount++;
    final id = await _requestID(request);
    final body = utf8.encode(
      jsonEncode({
        'id': id,
        'result': {'status': 'ok'},
      }),
    );
    return http.StreamedResponse(_delayedChunks(body, 3), 200);
  }
}

class TimeoutCallClient extends http.BaseClient {
  var sendCount = 0;
  var aborted = false;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    sendCount++;
    final abortable = request as http.Abortable;
    await abortable.abortTrigger;
    aborted = true;
    throw http.RequestAbortedException(request.url);
  }
}

class BackpressureStreamClient extends http.BaseClient {
  var sendCount = 0;
  var producedFrames = 0;
  final paused = Completer<void>();

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    sendCount++;
    final id = await _requestID(request);
    var sentRemainder = false;
    late final StreamController<List<int>> controller;

    void addFrame(Map<String, Object?> frame) {
      producedFrames++;
      controller.add(utf8.encode('${jsonEncode(frame)}\n'));
    }

    controller = StreamController<List<int>>(
      sync: true,
      onListen: () {
        scheduleMicrotask(() {
          addFrame({
            'id': id,
            'result': {'page': 1},
            'stream': {'sequence': 1, 'kind': 'data'},
          });
        });
      },
      onPause: () {
        if (!paused.isCompleted) paused.complete();
      },
      onResume: () {
        if (sentRemainder) return;
        sentRemainder = true;
        scheduleMicrotask(() {
          addFrame({
            'id': id,
            'result': null,
            'stream': {
              'sequence': 2,
              'kind': 'progress',
              'progress': {'completed': 1, 'total': 1},
            },
          });
          addFrame({
            'id': id,
            'result': null,
            'stream': {'sequence': 3, 'kind': 'complete'},
          });
          controller.close();
        });
      },
    );

    return http.StreamedResponse(
      controller.stream,
      200,
      headers: {'content-type': 'application/x-ndjson'},
    );
  }
}

class InterruptedStreamClient extends http.BaseClient {
  var sendCount = 0;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    sendCount++;
    final id = await _requestID(request);
    return http.StreamedResponse(_interruptedStream(id, request.url), 200);
  }
}

class DroppedDownloadClient extends http.BaseClient {
  DroppedDownloadClient(this.content, {required this.firstBytes});

  final List<int> content;
  final int firstBytes;
  final ranges = <String?>[];
  var sendCount = 0;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    sendCount++;
    final range = request.headers['range'];
    ranges.add(range);
    if (range == null) {
      return http.StreamedResponse(
        _droppedBody(content.sublist(0, firstBytes), request.url),
        200,
        contentLength: content.length,
      );
    }

    final offset = int.parse(range.substring(6, range.length - 1));
    return http.StreamedResponse(
      _delayedChunks(content.sublist(offset), 2),
      206,
      contentLength: content.length - offset,
      headers: {
        'content-range':
            'bytes $offset-${content.length - 1}/${content.length}',
      },
    );
  }
}

Future<Object?> _requestID(http.BaseRequest request) async {
  final payload = jsonDecode(await request.finalize().bytesToString()) as Map;
  return payload['id'];
}

Stream<List<int>> _delayedChunks(List<int> bytes, int count) async* {
  final chunkSize = (bytes.length / count).ceil();
  for (var start = 0; start < bytes.length; start += chunkSize) {
    await Future<void>.delayed(const Duration(milliseconds: 5));
    final end = start + chunkSize < bytes.length
        ? start + chunkSize
        : bytes.length;
    yield bytes.sublist(start, end);
  }
}

Stream<List<int>> _interruptedStream(Object? id, Uri url) async* {
  yield utf8.encode(
    '${jsonEncode({
      'id': id,
      'result': {'page': 1},
      'stream': {'sequence': 1, 'kind': 'data'},
    })}\n',
  );
  await Future<void>.delayed(const Duration(milliseconds: 5));
  throw http.ClientException('connection reset', url);
}

Stream<List<int>> _droppedBody(List<int> bytes, Uri url) async* {
  yield bytes;
  await Future<void>.delayed(const Duration(milliseconds: 5));
  throw http.ClientException('connection reset', url);
}

RpcFileReference _fileReference(List<int> content) {
  return RpcFileReference.fromJson({
    'id': 'f' * 64,
    'name': 'report.bin',
    'mediaType': 'application/octet-stream',
    'size': content.length,
    'sha256': sha256.convert(content).toString(),
    'expiresAt': DateTime.now()
        .add(const Duration(hours: 1))
        .toUtc()
        .toIso8601String(),
  });
}
