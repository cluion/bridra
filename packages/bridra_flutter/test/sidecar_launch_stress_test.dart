import 'dart:async';
import 'dart:io';

import 'package:bridra_flutter/bridra_flutter_sidecar.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final desktop =
      !kIsWeb && (Platform.isLinux || Platform.isMacOS || Platform.isWindows);

  test(
    'stress launches real desktop sidecars without handshake failures',
    () async {
      final configuredPath = Platform.environment['BRIDRA_SIDECAR_PATH'];
      expect(configuredPath, isNotNull);
      expect(await File(configuredPath!).exists(), isTrue);
      final cycles =
          int.tryParse(Platform.environment['BRIDRA_STRESS_CYCLES'] ?? '') ??
          100;
      final concurrency =
          int.tryParse(
            Platform.environment['BRIDRA_STRESS_CONCURRENCY'] ?? '',
          ) ??
          8;
      expect(cycles, inInclusiveRange(1, 1000));
      expect(concurrency, inInclusiveRange(1, 32));

      final launchDurations = <Duration>[];
      for (var offset = 0; offset < cycles; offset += concurrency) {
        final batchSize = concurrency < cycles - offset
            ? concurrency
            : cycles - offset;
        await Future.wait(
          List.generate(batchSize, (batchIndex) async {
            final launch = offset + batchIndex + 1;
            final logs = <String>[];
            final stopwatch = Stopwatch()..start();
            SidecarClient? client;
            _RecordedSidecarProcess? process;
            try {
              client = await SidecarClient.start(
                executablePath: configuredPath,
                token: 'stress-token-$launch',
                onLog: logs.add,
                restartPolicy: const SidecarRestartPolicy.disabled(),
                processStarter: (executablePath, arguments) async {
                  final started = await Process.start(
                    executablePath,
                    arguments,
                    mode: ProcessStartMode.normal,
                  );
                  return process = _RecordedSidecarProcess(started);
                },
              );
              launchDurations.add(stopwatch.elapsed);
              final health = await client.call('system.health');
              expect((health.result as Map)['status'], 'ok');
            } on Object catch (error, stackTrace) {
              Error.throwWithStackTrace(
                StateError(
                  'Real Sidecar launch $launch/$cycles failed after '
                  '${stopwatch.elapsedMilliseconds}ms. '
                  'Exit: ${process?.completedExitCode ?? 'running'}. '
                  'Error: $error. Logs: ${logs.join(' | ')}',
                ),
                stackTrace,
              );
            } finally {
              stopwatch.stop();
              await client?.close();
            }
          }),
        );
      }

      launchDurations.sort();
      final percentileIndex = ((launchDurations.length - 1) * 0.95).round();
      debugPrint(
        'Real Sidecar launch stress passed: $cycles launches, '
        'p95=${launchDurations[percentileIndex].inMilliseconds}ms, '
        'max=${launchDurations.last.inMilliseconds}ms.',
      );
    },
    skip: desktop && Platform.environment['BRIDRA_STRESS'] == '1'
        ? false
        : 'Set BRIDRA_STRESS=1 on a desktop host to run launch stress.',
  );
}

class _RecordedSidecarProcess implements SidecarProcess {
  _RecordedSidecarProcess(this._process) {
    unawaited(
      _process.exitCode.then((exitCode) {
        completedExitCode = exitCode;
      }),
    );
  }

  final Process _process;
  int? completedExitCode;

  @override
  Future<int> get exitCode => _process.exitCode;

  @override
  IOSink get stdin => _process.stdin;

  @override
  Stream<List<int>> get stdout => _process.stdout;

  @override
  Stream<List<int>> get stderr => _process.stderr;

  @override
  bool kill([ProcessSignal signal = ProcessSignal.sigterm]) {
    return _process.kill(signal);
  }
}
