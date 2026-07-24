import 'dart:io';

import 'package:bridra/api/backend_gateway.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:bridra_flutter/bridra_flutter_sidecar.dart';

import 'repository_version.dart';

void main() {
  test('Flutter performs a real round trip through the Go pipeline', () async {
    final separator = Platform.pathSeparator;
    final executable =
        Platform.environment['BRIDRA_SIDECAR_PATH'] ??
        '${Directory.current.path}${separator}build${separator}sidecar$separator'
            'bridra_backend';
    expect(
      File(executable).existsSync(),
      isTrue,
      reason: 'Run `make backend-build` first.',
    );

    final client = await SidecarClient.start(
      executablePath: executable,
      token: 'integration-test-token',
    );
    addTearDown(client.close);
    final api = BridraRpcApi(client);

    final health = await api.health();
    expect(health.status, 'ok');
    expect(health.frameworkVersion, repositoryFrameworkVersion);
    expect(health.protocolVersion, supportedBackendProtocolVersion);

    final greeting = await api.greet(const GreetingRequest(name: 'Codex'));
    expect(greeting.message, 'Hello, Codex!');
    expect(greeting.pipeline, [
      'logging:before',
      'recovery:before',
      'request-id:before',
      'auth:before',
      'auth:after',
      'request-id:after',
      'recovery:after',
      'logging:after',
    ]);

    final concurrentGreetings = await Future.wait(
      List.generate(
        16,
        (index) => api.greet(GreetingRequest(name: 'Codex $index')),
      ),
    );
    for (var index = 0; index < concurrentGreetings.length; index++) {
      expect(concurrentGreetings[index].message, 'Hello, Codex $index!');
    }
  });
}
