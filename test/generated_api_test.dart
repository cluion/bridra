import 'package:bridra/api/backend_gateway.dart';
import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:flutter_test/flutter_test.dart';

class RecordingRpcClient implements RpcClient {
  RecordingRpcClient(this.reply);

  RpcReply reply;
  String? method;
  Map<String, Object?>? params;
  RpcCancellationToken? cancellationToken;

  @override
  Future<RpcReply> call(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(seconds: 5),
    RpcCancellationToken? cancellationToken,
  }) async {
    this.method = method;
    this.params = params;
    this.cancellationToken = cancellationToken;
    return reply;
  }

  @override
  Stream<RpcStreamEvent<RpcReply>> stream(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(minutes: 5),
    RpcCancellationToken? cancellationToken,
  }) {
    return const Stream.empty();
  }

  @override
  Stream<List<int>> download(
    RpcFileReference file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
  }) {
    return const Stream.empty();
  }

  @override
  Future<void> close() async {}
}

void main() {
  test('generated health client decodes the schema result', () async {
    final rpc = RecordingRpcClient(
      const RpcReply(
        result: {
          'status': 'ok',
          'frameworkVersion': '0.1.0',
          'protocolVersion': 1,
          'runtime': 'Go sidecar',
          'architecture': 'Middleware -> Controller -> Service',
        },
        meta: {},
      ),
    );

    final health = await BridraRpcApi(rpc).health();

    expect(rpc.method, BridraMethods.systemHealth);
    expect(rpc.params, isEmpty);
    expect(health.status, 'ok');
    expect(health.protocolVersion, supportedBackendProtocolVersion);
  });

  test(
    'generated greeting client encodes request and decodes result',
    () async {
      final rpc = RecordingRpcClient(
        const RpcReply(
          result: {
            'message': 'Hello, Codex!',
            'servedBy': 'Go GreetingService',
            'timestamp': '2026-07-21T12:00:00Z',
          },
          meta: {
            'pipeline': ['auth:before', 'auth:after'],
          },
        ),
      );
      final cancellationToken = RpcCancellationToken();

      final greeting = await BridraRpcApi(rpc).greet(
        const GreetingRequest(name: 'Codex'),
        cancellationToken: cancellationToken,
      );

      expect(rpc.method, BridraMethods.greetingHello);
      expect(rpc.params, {'name': 'Codex'});
      expect(rpc.cancellationToken, same(cancellationToken));
      expect(greeting.message, 'Hello, Codex!');
      expect(greeting.timestamp, DateTime.utc(2026, 7, 21, 12));
      expect(greeting.pipeline, ['auth:before', 'auth:after']);
      expect(() => greeting.pipeline.add('changed'), throwsUnsupportedError);
    },
  );

  test('generated client rejects responses outside the schema', () async {
    final rpc = RecordingRpcClient(
      const RpcReply(
        result: {
          'message': 'Hello!',
          'servedBy': 'Go GreetingService',
          'timestamp': 'not-a-date',
        },
        meta: {
          'pipeline': ['auth:before'],
        },
      ),
    );

    await expectLater(
      BridraRpcApi(rpc).greet(const GreetingRequest(name: 'Codex')),
      throwsA(isA<BackendProtocolException>()),
    );
  });

  test('generated request encodes nullable enum and nested object fields', () {
    const request = GreetingRequest(
      name: 'Codex',
      tone: 'formal',
      profile: GreetingProfileRequest(nickname: 'C'),
    );

    expect(request.toJson(), {
      'name': 'Codex',
      'tone': 'formal',
      'profile': {'nickname': 'C'},
    });
    expect(const GreetingRequest(name: 'Codex').toJson(), {'name': 'Codex'});
  });
}
