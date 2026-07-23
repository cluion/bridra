import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:bridra/api/backend_gateway.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:bridra_flutter/bridra_flutter.dart';

import 'repository_version.dart';

void main() {
  test(
    'Flutter performs a real HTTP round trip through the Go pipeline',
    () async {
      final separator = Platform.pathSeparator;
      final executableName = Platform.isWindows
          ? 'bridra_server.exe'
          : 'bridra_server';
      final executable =
          Platform.environment['BRIDRA_SERVER_PATH'] ??
          '${Directory.current.path}${separator}build${separator}server$separator'
              '$executableName';
      expect(
        File(executable).existsSync(),
        isTrue,
        reason: 'Run `make backend-build` first.',
      );

      const token = 'http-integration-test-token';
      final process = await Process.start(executable, [
        '--listen',
        '127.0.0.1:0',
        '--token',
        token,
        '--cors-origin',
        'http://localhost',
      ]);
      final endpoint = Completer<Uri>();
      final stderrSubscription = process.stderr
          .transform(utf8.decoder)
          .transform(const LineSplitter())
          .listen((line) {
            const prefix = 'server: listening on ';
            if (!endpoint.isCompleted && line.startsWith(prefix)) {
              endpoint.complete(Uri.parse(line.substring(prefix.length)));
            }
          }, onError: endpoint.completeError);
      final stdoutSubscription = process.stdout.listen((_) {});

      HttpRpcClient? client;
      try {
        final uri = await endpoint.future.timeout(const Duration(seconds: 5));
        client = HttpRpcClient(endpoint: uri, token: token);
        final api = BridraRpcApi(client);

        final health = await api.health();
        expect(health.status, 'ok');
        expect(health.frameworkVersion, repositoryFrameworkVersion);
        expect(health.protocolVersion, supportedBackendProtocolVersion);
        expect(health.runtime, 'Go HTTP server');

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
      } finally {
        await client?.close();
        process.kill();
        try {
          await process.exitCode.timeout(const Duration(seconds: 5));
        } on TimeoutException {
          if (!Platform.isWindows) process.kill(ProcessSignal.sigkill);
        }
        await stderrSubscription.cancel();
        await stdoutSubscription.cancel();
      }
    },
  );
}
