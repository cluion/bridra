import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

void main() {
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
