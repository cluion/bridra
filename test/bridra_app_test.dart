import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:bridra/api/backend_gateway.dart';
import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:bridra_flutter/bridra_flutter_sidecar.dart';
import 'package:bridra/bridra_app.dart';

import 'repository_version.dart';

class FakeBackend implements BackendGateway {
  var closeCalls = 0;
  var healthCalls = 0;

  @override
  Future<void> close() async {
    closeCalls++;
  }

  @override
  Future<GreetingResult> greet(
    GreetingRequest request, {
    RpcCancellationToken? cancellationToken,
  }) async {
    return GreetingResult(
      message: 'Hello, ${request.name}!',
      servedBy: 'Fake Go GreetingService',
      timestamp: DateTime.utc(2026, 7, 20),
      pipeline: const ['auth:before', 'auth:after'],
    );
  }

  @override
  Stream<List<int>> download(
    RpcFileReference file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
    int maxAttempts = 3,
  }) {
    return const Stream.empty();
  }

  @override
  Future<RpcFileReference> upload(
    RpcFileUpload file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
    int maxAttempts = 3,
  }) {
    throw UnimplementedError();
  }

  @override
  Future<HealthInfo> health({RpcCancellationToken? cancellationToken}) async {
    healthCalls++;
    return HealthInfo(
      status: 'ok',
      frameworkVersion: repositoryFrameworkVersion,
      protocolVersion: 1,
      runtime: 'Go sidecar',
      architecture: 'Middleware -> Controller -> Service',
    );
  }
}

class ExitingBackend extends FakeBackend {
  @override
  Future<GreetingResult> greet(
    GreetingRequest request, {
    RpcCancellationToken? cancellationToken,
  }) async {
    throw const SidecarExitedException(9);
  }
}

void main() {
  testWidgets('renders health and calls the typed backend gateway', (
    tester,
  ) async {
    await tester.pumpWidget(BridraApp(connector: () async => FakeBackend()));
    await tester.pumpAndSettle();

    expect(find.text('BRIDRA'), findsOneWidget);
    expect(find.text('Flutter in front.\nGo at the core.'), findsOneWidget);
    expect(find.text('Go core online'), findsOneWidget);
    expect(find.text('Middleware -> Controller -> Service'), findsOneWidget);
    expect(
      find.text('BRIDRA $repositoryFrameworkVersion · FOUNDATION'),
      findsOneWidget,
    );

    await tester.enterText(find.byKey(const Key('name-field')), 'Codex');
    final callButton = find.byKey(const Key('call-button'));
    await tester.ensureVisible(callButton);
    await tester.tap(callButton);
    await tester.pumpAndSettle();

    expect(find.text('Hello, Codex!'), findsOneWidget);
    expect(find.text('200 · CONTROLLER RESPONSE'), findsOneWidget);
    expect(find.text('auth:before'), findsOneWidget);
    expect(find.text('auth:after'), findsOneWidget);
  });

  testWidgets('retries a failed sidecar connection', (tester) async {
    var attempts = 0;
    final backend = FakeBackend();
    await tester.pumpWidget(
      BridraApp(
        connector: () async {
          attempts++;
          if (attempts == 1) throw StateError('launch failed');
          return backend;
        },
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Go core unavailable'), findsOneWidget);
    expect(find.byKey(const Key('retry-button')), findsOneWidget);

    final retryButton = find.byKey(const Key('retry-button'));
    await tester.ensureVisible(retryButton);
    await tester.tap(retryButton);
    await tester.pumpAndSettle();

    expect(attempts, 2);
    expect(find.text('Go core online'), findsOneWidget);
  });

  testWidgets('offers restart after the sidecar exits', (tester) async {
    final backend = ExitingBackend();
    await tester.pumpWidget(BridraApp(connector: () async => backend));
    await tester.pumpAndSettle();

    final callButton = find.byKey(const Key('call-button'));
    await tester.ensureVisible(callButton);
    await tester.tap(callButton);
    await tester.pumpAndSettle();

    expect(find.text('Go core unavailable'), findsOneWidget);
    expect(find.byKey(const Key('retry-button')), findsOneWidget);
    expect(backend.closeCalls, 1);
  });
}
