import 'package:bridra/api/backend_gateway.dart';
import 'package:bridra_flutter/bridra_flutter.dart';

import '../repository_version.dart';

class FakeBackend implements BackendGateway {
  FakeBackend({this.onHealth, this.onGreet, this.closeError});

  final Future<HealthInfo> Function()? onHealth;
  final Future<GreetingResult> Function(GreetingRequest, RpcCancellationToken?)?
  onGreet;
  final Object? closeError;
  var closeCalls = 0;
  var healthCalls = 0;
  var greetCalls = 0;
  RpcCancellationToken? lastCancellation;

  @override
  Future<void> close() async {
    closeCalls++;
    if (closeError case final error?) throw error;
  }

  @override
  Future<HealthInfo> health({
    Duration timeout = const Duration(seconds: 5),
    RpcCancellationToken? cancellationToken,
  }) async {
    healthCalls++;
    return onHealth?.call() ?? healthyBackend;
  }

  @override
  Future<GreetingResult> greet(
    GreetingRequest request, {
    Duration timeout = const Duration(seconds: 5),
    RpcCancellationToken? cancellationToken,
  }) async {
    greetCalls++;
    lastCancellation = cancellationToken;
    return onGreet?.call(request, cancellationToken) ?? greetingFor(request);
  }

  @override
  Stream<List<int>> download(
    RpcFileReference file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
    int maxAttempts = 3,
  }) => const Stream.empty();

  @override
  Future<RpcFileReference> upload(
    RpcFileUpload file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
    int maxAttempts = 3,
  }) async => throw UnimplementedError();
}

HealthInfo get healthyBackend => HealthInfo(
  status: 'ok',
  frameworkVersion: repositoryFrameworkVersion,
  protocolVersion: 1,
  runtime: 'Go sidecar',
  architecture: 'Middleware -> Controller -> Service',
);

GreetingResult greetingFor(GreetingRequest request) => GreetingResult(
  message: 'Hello, ${request.name}!',
  servedBy: 'Go GreetingService',
  timestamp: DateTime.utc(2026, 7, 20, 10, 30),
  pipeline: const ['auth:before', 'auth:after'],
);
