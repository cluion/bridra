import 'dart:async';
import 'dart:io';

import 'package:bridra_flutter/bridra_flutter_sidecar.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parses Linux process resource status', () {
    const status = '''
Name:\tbridra_backend
VmRSS:\t   12345 kB
Threads:\t9
''';

    expect(_parseLinuxStatusValue(status, 'VmRSS'), 12345);
    expect(_parseLinuxStatusValue(status, 'Threads'), 9);
    expect(
      () => _parseLinuxStatusValue(status, 'VmSize'),
      throwsFormatException,
    );
  });

  test(
    'real Sidecar resources stay bounded across load and restart cycles',
    () async {
      final cycles = _positiveEnvironmentInteger(
        'BRIDRA_STRESS_CYCLES',
        fallback: 50,
        maximum: 1000,
      );
      final maximumRssGrowth =
          _nonNegativeEnvironmentInteger(
            'BRIDRA_RESOURCE_MAX_RSS_GROWTH_MIB',
            fallback: 32,
          ) *
          1024 *
          1024;
      final maximumFdGrowth = _nonNegativeEnvironmentInteger(
        'BRIDRA_RESOURCE_MAX_FD_GROWTH',
        fallback: 4,
      );
      final executable = Platform.environment['BRIDRA_SIDECAR_PATH'];
      expect(executable, isNotNull);
      expect(File(executable!).existsSync(), isTrue);

      final activeProcesses = <Process>{};
      final restartSignals = StreamController<void>.broadcast(sync: true);
      late Process currentProcess;
      Future<SidecarProcess> start(String path, List<String> arguments) async {
        expect(arguments, ['--token-stdin']);
        expect(arguments, isNot(contains('resource-stress-token')));
        final process = await Process.start(path, arguments);
        currentProcess = process;
        activeProcesses.add(process);
        unawaited(
          process.exitCode.then<void>((_) {
            activeProcesses.remove(process);
          }),
        );
        return _ResourceSidecarProcess(process);
      }

      final client = await SidecarClient.start(
        executablePath: executable,
        token: 'resource-stress-token',
        restartPolicy: const SidecarRestartPolicy(
          maxAttempts: 1,
          initialDelay: Duration.zero,
          maxDelay: Duration.zero,
          healthCheckTimeout: Duration(seconds: 5),
        ),
        processStarter: start,
        onLog: (line) {
          if (line.startsWith('Restarting the Go sidecar')) {
            restartSignals.add(null);
          }
        },
      );
      addTearDown(() async {
        await client.close();
        if (!restartSignals.isClosed) await restartSignals.close();
      });

      for (var warmup = 0; warmup < 3; warmup++) {
        await _runHealthBatch(client);
      }
      final parentBaseline = _readLinuxProcessSnapshot(pid);
      final sidecarBaseline = _readLinuxProcessSnapshot(currentProcess.pid);

      for (var cycle = 0; cycle < cycles; cycle++) {
        await _runHealthBatch(client);
      }
      final sidecarAfterLoad = _readLinuxProcessSnapshot(currentProcess.pid);
      _expectGrowthWithin(
        'Sidecar RSS',
        sidecarBaseline.rssBytes,
        sidecarAfterLoad.rssBytes,
        maximumRssGrowth,
      );
      _expectGrowthWithin(
        'Sidecar file descriptors',
        sidecarBaseline.openFileDescriptors,
        sidecarAfterLoad.openFileDescriptors,
        maximumFdGrowth,
      );

      for (var cycle = 0; cycle < cycles; cycle++) {
        final previous = currentProcess;
        final restartStarted = restartSignals.stream.first;
        expect(previous.kill(ProcessSignal.sigkill), isTrue);
        await restartStarted.timeout(const Duration(seconds: 5));
        await previous.exitCode.timeout(const Duration(seconds: 5));
        await client.call('system.health').timeout(const Duration(seconds: 10));
        await _waitFor(
          () =>
              activeProcesses.length == 1 &&
              identical(activeProcesses.single, currentProcess) &&
              (previous.pid == currentProcess.pid ||
                  !Directory('/proc/${previous.pid}').existsSync()),
          description: 'old Sidecar ${previous.pid} to exit without an orphan',
        );
      }

      final parentFinal = _readLinuxProcessSnapshot(pid);
      final sidecarFinal = _readLinuxProcessSnapshot(currentProcess.pid);
      _expectGrowthWithin(
        'Flutter test process RSS',
        parentBaseline.rssBytes,
        parentFinal.rssBytes,
        maximumRssGrowth,
      );
      _expectGrowthWithin(
        'Flutter test process file descriptors',
        parentBaseline.openFileDescriptors,
        parentFinal.openFileDescriptors,
        maximumFdGrowth,
      );
      expect(activeProcesses, {currentProcess});
      expect(client.diagnostics().processStarts, cycles + 1);
      expect(client.diagnostics().successfulRestarts, cycles);

      debugPrint(
        'runtime resources parent baseline=$parentBaseline final=$parentFinal; '
        'sidecar baseline=$sidecarBaseline after_load=$sidecarAfterLoad '
        'final=$sidecarFinal; restarts=$cycles',
      );

      final finalPID = currentProcess.pid;
      await client.close();
      await _waitFor(
        () =>
            activeProcesses.isEmpty &&
            !Directory('/proc/$finalPID').existsSync(),
        description: 'final Sidecar $finalPID to exit without an orphan',
      );
      await restartSignals.close();
    },
    skip: _resourceStressSkipReason(),
  );
}

Future<void> _runHealthBatch(SidecarClient client) async {
  await Future.wait(List.generate(16, (_) => client.call('system.health')));
}

String? _resourceStressSkipReason() {
  if (Platform.environment['BRIDRA_RESOURCE_STRESS'] != '1') {
    return 'Set BRIDRA_RESOURCE_STRESS=1 to run resource stress tests.';
  }
  if (!Platform.isLinux) {
    return 'Real process resource stress uses the Linux /proc contract.';
  }
  return null;
}

int _positiveEnvironmentInteger(
  String name, {
  required int fallback,
  required int maximum,
}) {
  final value = Platform.environment[name];
  if (value == null || value.trim().isEmpty) return fallback;
  final parsed = int.tryParse(value);
  if (parsed == null || parsed < 1 || parsed > maximum) {
    throw ArgumentError('$name must be between 1 and $maximum, got $value');
  }
  return parsed;
}

int _nonNegativeEnvironmentInteger(String name, {required int fallback}) {
  final value = Platform.environment[name];
  if (value == null || value.trim().isEmpty) return fallback;
  final parsed = int.tryParse(value);
  if (parsed == null || parsed < 0 || parsed > 1000000) {
    throw ArgumentError('$name must be between 0 and 1000000, got $value');
  }
  return parsed;
}

_LinuxProcessSnapshot _readLinuxProcessSnapshot(int processID) {
  final status = File('/proc/$processID/status').readAsStringSync();
  final descriptors = Directory('/proc/$processID/fd').listSync().length;
  return _LinuxProcessSnapshot(
    rssBytes: _parseLinuxStatusValue(status, 'VmRSS') * 1024,
    openFileDescriptors: descriptors,
    threads: _parseLinuxStatusValue(status, 'Threads'),
  );
}

int _parseLinuxStatusValue(String status, String name) {
  final pattern = RegExp('^${RegExp.escape(name)}:\\s+(\\d+)', multiLine: true);
  final match = pattern.firstMatch(status);
  if (match == null) {
    throw FormatException('Linux process status is missing $name');
  }
  return int.parse(match.group(1)!);
}

void _expectGrowthWithin(
  String name,
  int baseline,
  int current,
  int maximumGrowth,
) {
  final growth = current > baseline ? current - baseline : 0;
  expect(
    growth,
    lessThanOrEqualTo(maximumGrowth),
    reason:
        '$name grew by $growth; maximum is $maximumGrowth '
        '(baseline=$baseline current=$current)',
  );
}

Future<void> _waitFor(
  bool Function() condition, {
  required String description,
}) async {
  final deadline = DateTime.now().add(const Duration(seconds: 5));
  while (!condition()) {
    if (DateTime.now().isAfter(deadline)) {
      throw TimeoutException('Timed out waiting for $description');
    }
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
}

class _LinuxProcessSnapshot {
  const _LinuxProcessSnapshot({
    required this.rssBytes,
    required this.openFileDescriptors,
    required this.threads,
  });

  final int rssBytes;
  final int openFileDescriptors;
  final int threads;

  @override
  String toString() =>
      '{rss_bytes:$rssBytes fds:$openFileDescriptors threads:$threads}';
}

class _ResourceSidecarProcess implements SidecarProcess {
  const _ResourceSidecarProcess(this.process);

  final Process process;

  @override
  Future<int> get exitCode => process.exitCode;

  @override
  Stream<List<int>> get stderr => process.stderr;

  @override
  IOSink get stdin => process.stdin;

  @override
  Stream<List<int>> get stdout => process.stdout;

  @override
  bool kill([ProcessSignal signal = ProcessSignal.sigterm]) {
    return process.kill(signal);
  }
}
