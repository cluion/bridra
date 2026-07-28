import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:bridra_flutter/bridra_flutter.dart';
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
  StreamingResponseClient({this.frames, this.statusCode = 200});

  final List<Map<String, Object?>> Function(Object? id)? frames;
  final int statusCode;
  Map<String, dynamic> payload = {};

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
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
      headers: const {'content-type': 'application/x-ndjson'},
    );
  }
}
