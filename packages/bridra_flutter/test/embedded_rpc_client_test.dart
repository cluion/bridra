import 'dart:async';
import 'dart:convert';

import 'package:bridra_flutter/bridra_flutter.dart';
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

  test('fails closed for unsupported streaming and file transfer', () async {
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
      client.stream('reports.build'),
      emitsError(isA<EmbeddedRpcUnsupportedException>()),
    );
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
}
