import 'package:bridra_flutter/bridra_flutter.dart';

import 'generated/bridra_api.g.dart';

export 'generated/bridra_api.g.dart';

abstract interface class BackendGateway implements BridraApi {
  Stream<List<int>> download(
    RpcFileReference file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
  });

  Future<void> close();
}

class RpcBackend implements BackendGateway {
  RpcBackend._(RpcClient client)
    : _client = client,
      _api = BridraRpcApi(client);

  final RpcClient _client;
  final BridraRpcApi _api;
  late final HealthInfo _health;

  static Future<RpcBackend> connect({void Function(String line)? onLog}) async {
    final client = await connectDefaultRpcClient(onLog: onLog);
    final backend = RpcBackend._(client);
    try {
      backend._health = await backend._api.health();
      if (backend._health.protocolVersion != supportedBackendProtocolVersion) {
        throw BackendProtocolException(
          'Unsupported backend protocol ${backend._health.protocolVersion}; '
          'expected $supportedBackendProtocolVersion.',
        );
      }
      return backend;
    } on Object {
      await client.close();
      rethrow;
    }
  }

  @override
  Future<HealthInfo> health({RpcCancellationToken? cancellationToken}) async {
    if (cancellationToken?.isCancelled ?? false) {
      throw const RpcCancelledException(BridraMethods.systemHealth);
    }
    return _health;
  }

  @override
  Future<GreetingResult> greet(
    GreetingRequest request, {
    RpcCancellationToken? cancellationToken,
  }) {
    return _api.greet(request, cancellationToken: cancellationToken);
  }

  @override
  Stream<List<int>> download(
    RpcFileReference file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
  }) {
    return _client.download(
      file,
      timeout: timeout,
      cancellationToken: cancellationToken,
    );
  }

  @override
  Future<void> close() => _client.close();
}
