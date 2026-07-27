import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:bridra_flutter/src/single_instance/desktop_single_instance_io.dart'
    as single_instance_io;
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('reports desktop support on IO desktop platforms', () {
    expect(DesktopSingleInstance.isSupported, isTrue);
  });

  test('rejects unsafe application identities and invalid timeouts', () async {
    await expectLater(
      DesktopSingleInstance.acquire(
        applicationId: '../unsafe',
        startupTimeout: const Duration(seconds: 1),
      ),
      throwsArgumentError,
    );
    await expectLater(
      DesktopSingleInstance.acquire(
        applicationId: 'com.example.invalid_timeout',
        startupTimeout: Duration.zero,
      ),
      throwsArgumentError,
    );
    expect(
      () => DesktopActivation(workingDirectory: '  '),
      throwsArgumentError,
    );
    expect(
      const DesktopSingleInstanceException('unavailable').toString(),
      'DesktopSingleInstanceException: unavailable',
    );
    expect(
      const DesktopSingleInstanceException('unavailable', 'cause').toString(),
      'DesktopSingleInstanceException: unavailable (cause)',
    );
    await expectLater(
      DesktopSingleInstance.acquire(
        applicationId: _applicationId('oversized_activation'),
        arguments: ['x'.padRight(1024 * 1024, 'x')],
      ),
      throwsArgumentError,
    );
  });

  test('elects one local primary and releases ownership on close', () async {
    final runtimeDirectory = await Directory.systemTemp.createTemp(
      'bridra-single-instance-local-',
    );
    addTearDown(() => runtimeDirectory.delete(recursive: true));
    final applicationId = _applicationId('local');

    final primary = await DesktopSingleInstance.acquire(
      applicationId: applicationId,
      runtimeDirectory: runtimeDirectory.path,
      arguments: const ['first'],
      workingDirectory: '/first',
    );
    addTearDown(primary.close);
    expect(primary.isPrimary, isTrue);
    expect(primary.initialActivation.arguments, const ['first']);
    expect(primary.initialActivation.workingDirectory, '/first');

    await expectLater(
      DesktopSingleInstance.acquire(
        applicationId: applicationId,
        runtimeDirectory: runtimeDirectory.path,
      ),
      throwsStateError,
    );

    await primary.close();
    final replacement = await DesktopSingleInstance.acquire(
      applicationId: applicationId,
      runtimeDirectory: runtimeDirectory.path,
      workingDirectory: '/replacement',
    );
    expect(replacement.isPrimary, isTrue);
    await replacement.close();
  });

  test('uses the platform default per-user runtime directory', () async {
    final applicationId = _applicationId('default_directory');
    final directory = single_instance_io
        .defaultDesktopRuntimeDirectoryForTesting();
    final lockFile = File(_joinPath(directory, '$applicationId.lock'));
    final metadataFile = File(_joinPath(directory, '$applicationId.json'));
    addTearDown(() async {
      for (final file in [lockFile, metadataFile]) {
        if (await file.exists()) await file.delete();
      }
    });

    final primary = await DesktopSingleInstance.acquire(
      applicationId: applicationId,
      workingDirectory: '/default',
    );
    expect(primary.isPrimary, isTrue);
    expect(await lockFile.exists(), isTrue);
    expect(await metadataFile.exists(), isTrue);
    await primary.close();
  });

  test('forwards a later process activation to the primary', () async {
    final runtimeDirectory = await Directory.systemTemp.createTemp(
      'bridra-single-instance-forward-',
    );
    addTearDown(() => runtimeDirectory.delete(recursive: true));
    final applicationId = _applicationId('forward');
    final primary = await DesktopSingleInstance.acquire(
      applicationId: applicationId,
      runtimeDirectory: runtimeDirectory.path,
      workingDirectory: '/primary',
    );
    addTearDown(primary.close);
    final received = primary.activations
        .take(2)
        .toList()
        .timeout(const Duration(seconds: 10));

    expect(
      await single_instance_io.forwardDesktopActivationForTesting(
        runtimeDirectory: runtimeDirectory.path,
        applicationId: applicationId,
        arguments: const ['internal'],
        workingDirectory: '/internal',
      ),
      isTrue,
    );
    final result = await _runHelper(
      runtimeDirectory: runtimeDirectory.path,
      applicationId: applicationId,
      workingDirectory: '/secondary',
      arguments: const ['document.txt', 'bridra://settings/profile'],
    );

    expect(result.exitCode, 0, reason: result.stderr as String);
    expect(_helperResult(result.stdout as String)['role'], 'secondary');
    final activations = await received;
    expect(activations.first.arguments, const ['internal']);
    expect(activations.first.workingDirectory, '/internal');
    expect(activations.last.arguments, const [
      'document.txt',
      'bridra://settings/profile',
    ]);
    expect(activations.last.workingDirectory, '/secondary');
  });

  test('rejects missing and oversized connection metadata', () async {
    final runtimeDirectory = await Directory.systemTemp.createTemp(
      'bridra-single-instance-metadata-',
    );
    addTearDown(() => runtimeDirectory.delete(recursive: true));
    final applicationId = _applicationId('metadata');

    expect(
      await single_instance_io.forwardDesktopActivationForTesting(
        runtimeDirectory: runtimeDirectory.path,
        applicationId: applicationId,
        arguments: const [],
        workingDirectory: '/missing',
      ),
      isFalse,
    );

    final metadata = File(
      _joinPath(runtimeDirectory.path, '$applicationId.json'),
    );
    await metadata.writeAsBytes(List<int>.filled(16 * 1024 + 1, 0));
    expect(
      await single_instance_io.forwardDesktopActivationForTesting(
        runtimeDirectory: runtimeDirectory.path,
        applicationId: applicationId,
        arguments: const [],
        workingDirectory: '/oversized',
      ),
      isFalse,
    );
  });

  test('rejects unauthenticated and malformed activation frames', () async {
    final runtimeDirectory = await Directory.systemTemp.createTemp(
      'bridra-single-instance-invalid-frame-',
    );
    addTearDown(() => runtimeDirectory.delete(recursive: true));
    final applicationId = _applicationId('invalid_frame');
    final primary = await DesktopSingleInstance.acquire(
      applicationId: applicationId,
      runtimeDirectory: runtimeDirectory.path,
      workingDirectory: '/primary',
    );
    addTearDown(primary.close);
    final metadata =
        jsonDecode(
              await File(
                _joinPath(runtimeDirectory.path, '$applicationId.json'),
              ).readAsString(),
            )
            as Map<String, dynamic>;
    final port = metadata['port'] as int;

    expect(
      await _sendRawActivation(
        port,
        jsonEncode({
          'protocolVersion': 1,
          'applicationId': applicationId,
          'token': 'invalid',
          'activation': {
            'arguments': const <String>[],
            'workingDirectory': '/secondary',
          },
        }),
      ),
      containsPair('accepted', false),
    );
    expect(
      await _sendRawActivation(port, '{malformed'),
      containsPair('accepted', false),
    );
  });

  test(
    'public acquire returns a secondary session for another primary',
    () async {
      final runtimeDirectory = await Directory.systemTemp.createTemp(
        'bridra-single-instance-secondary-',
      );
      addTearDown(() => runtimeDirectory.delete(recursive: true));
      final applicationId = _applicationId('secondary');
      final readyFile = File(_joinPath(runtimeDirectory.path, 'primary.ready'));
      final process = await _startHelper(
        runtimeDirectory: runtimeDirectory.path,
        applicationId: applicationId,
        workingDirectory: '/helper-primary',
        arguments: const ['primary'],
        environment: {
          'BRIDRA_SINGLE_INSTANCE_HELPER_MODE': 'coordinate',
          'BRIDRA_SINGLE_INSTANCE_HELPER_READY_FILE': readyFile.path,
        },
      );
      await _waitForFile(readyFile);

      final secondary = await DesktopSingleInstance.acquire(
        applicationId: applicationId,
        runtimeDirectory: runtimeDirectory.path,
        workingDirectory: '/test-secondary',
        arguments: const ['secondary'],
      );
      expect(secondary.isPrimary, isFalse);
      expect(secondary.initialActivation.arguments, const ['secondary']);
      expect(await secondary.activations.isEmpty, isTrue);
      await secondary.close();

      final result = await _collectHelper(process);
      expect(result.exitCode, 0, reason: result.stderr);
      final received = _helperResults(
        result.stdout,
      ).singleWhere((entry) => entry.containsKey('receivedArguments'));
      expect(received['receivedArguments'], const ['secondary']);
      expect(received['receivedWorkingDirectory'], '/test-secondary');
    },
  );

  test('concurrent processes elect one primary and one secondary', () async {
    final runtimeDirectory = await Directory.systemTemp.createTemp(
      'bridra-single-instance-concurrent-',
    );
    addTearDown(() => runtimeDirectory.delete(recursive: true));
    final applicationId = _applicationId('concurrent');
    const environment = {'BRIDRA_SINGLE_INSTANCE_HELPER_MODE': 'coordinate'};

    final processes = await Future.wait([
      _startHelper(
        runtimeDirectory: runtimeDirectory.path,
        applicationId: applicationId,
        workingDirectory: '/first',
        arguments: const ['first'],
        environment: environment,
      ),
      _startHelper(
        runtimeDirectory: runtimeDirectory.path,
        applicationId: applicationId,
        workingDirectory: '/second',
        arguments: const ['second'],
        environment: environment,
      ),
    ]);
    final results = await Future.wait(processes.map(_collectHelper));

    for (final result in results) {
      expect(result.exitCode, 0, reason: result.stderr);
    }
    final structured = results
        .expand((result) => _helperResults(result.stdout))
        .toList(growable: false);
    expect(
      structured.where((entry) => entry['role'] == 'primary'),
      hasLength(1),
    );
    expect(
      structured.where((entry) => entry['role'] == 'secondary'),
      hasLength(1),
    );
    final received = structured.singleWhere(
      (entry) => entry.containsKey('receivedArguments'),
    );
    expect(
      received['receivedArguments'],
      anyOf([
        equals(const ['first']),
        equals(const ['second']),
      ]),
    );
  });

  test(
    'recovers ownership after a primary process exits without close',
    () async {
      final runtimeDirectory = await Directory.systemTemp.createTemp(
        'bridra-single-instance-stale-',
      );
      addTearDown(() => runtimeDirectory.delete(recursive: true));
      final applicationId = _applicationId('stale');

      final abandoned = await _runHelper(
        runtimeDirectory: runtimeDirectory.path,
        applicationId: applicationId,
        workingDirectory: '/abandoned',
        environment: const {'BRIDRA_SINGLE_INSTANCE_HELPER_MODE': 'abandon'},
      );
      expect(abandoned.exitCode, 0, reason: abandoned.stderr as String);
      expect(_helperResult(abandoned.stdout as String)['role'], 'primary');

      final replacement = await DesktopSingleInstance.acquire(
        applicationId: applicationId,
        runtimeDirectory: runtimeDirectory.path,
        workingDirectory: '/replacement',
      );
      expect(replacement.isPrimary, isTrue);
      await replacement.close();
    },
  );
}

Future<ProcessResult> _runHelper({
  required String runtimeDirectory,
  required String applicationId,
  required String workingDirectory,
  Iterable<String> arguments = const [],
  Map<String, String> environment = const {},
}) {
  final packageRoot = _packageRoot();
  return Process.run(
    _dartExecutable(),
    [
      'run',
      'test/fixtures/single_instance_helper.dart',
      runtimeDirectory,
      applicationId,
      workingDirectory,
      ...arguments,
    ],
    workingDirectory: packageRoot.path,
    environment: {...Platform.environment, ...environment},
  );
}

Map<String, dynamic> _helperResult(String output) {
  for (final result in _helperResults(output).reversed) {
    if (result['role'] is String) return result;
  }
  fail('Helper produced no structured role result:\n$output');
}

List<Map<String, dynamic>> _helperResults(String output) {
  final results = <Map<String, dynamic>>[];
  for (final line in const LineSplitter().convert(output)) {
    try {
      final decoded = jsonDecode(line);
      if (decoded is Map<String, dynamic>) results.add(decoded);
    } on FormatException {
      continue;
    }
  }
  return results;
}

Future<Process> _startHelper({
  required String runtimeDirectory,
  required String applicationId,
  required String workingDirectory,
  Iterable<String> arguments = const [],
  Map<String, String> environment = const {},
}) {
  final packageRoot = _packageRoot();
  return Process.start(
    _dartExecutable(),
    [
      'run',
      'test/fixtures/single_instance_helper.dart',
      runtimeDirectory,
      applicationId,
      workingDirectory,
      ...arguments,
    ],
    workingDirectory: packageRoot.path,
    environment: {...Platform.environment, ...environment},
  );
}

Future<_HelperProcessResult> _collectHelper(Process process) async {
  final stdout = process.stdout.transform(utf8.decoder).join();
  final stderr = process.stderr.transform(utf8.decoder).join();
  return _HelperProcessResult(
    exitCode: await process.exitCode,
    stdout: await stdout,
    stderr: await stderr,
  );
}

Future<void> _waitForFile(File file) async {
  final deadline = DateTime.now().add(const Duration(seconds: 10));
  while (!await file.exists()) {
    if (!DateTime.now().isBefore(deadline)) {
      throw TimeoutException('Helper did not become primary.');
    }
    await Future<void>.delayed(const Duration(milliseconds: 20));
  }
}

Future<Map<String, dynamic>> _sendRawActivation(
  int port,
  String payload,
) async {
  final socket = await Socket.connect(InternetAddress.loopbackIPv4, port);
  try {
    socket.add(utf8.encode('$payload\n'));
    await socket.flush();
    final response = await socket
        .cast<List<int>>()
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .first
        .timeout(const Duration(seconds: 5));
    return jsonDecode(response) as Map<String, dynamic>;
  } finally {
    socket.destroy();
  }
}

Directory _packageRoot() {
  final current = Directory.current;
  if (_isPackageRoot(current)) return current;
  final nested = Directory(
    _joinPath(_joinPath(current.path, 'packages'), 'bridra_flutter'),
  );
  if (_isPackageRoot(nested)) return nested;
  throw StateError('Cannot locate the bridra_flutter package root.');
}

bool _isPackageRoot(Directory directory) {
  final pubspec = File(_joinPath(directory.path, 'pubspec.yaml'));
  return pubspec.existsSync() &&
      pubspec.readAsStringSync().contains('name: bridra_flutter');
}

String _dartExecutable() {
  final resolved = File(Platform.resolvedExecutable);
  final name = resolved.uri.pathSegments.last.toLowerCase();
  if (name == 'dart' || name == 'dart.exe') return resolved.path;

  final executableName = Platform.isWindows ? 'dart.exe' : 'dart';
  final flutterRoot = Platform.environment['FLUTTER_ROOT'];
  if (flutterRoot != null && flutterRoot.isNotEmpty) {
    final candidate = File(
      _joinPath(
        _joinPath(
          _joinPath(_joinPath(flutterRoot, 'bin'), 'cache'),
          'dart-sdk',
        ),
        _joinPath('bin', executableName),
      ),
    );
    if (candidate.existsSync()) return candidate.path;
  }

  var directory = resolved.parent;
  for (var depth = 0; depth < 10; depth++) {
    final candidate = File(
      _joinPath(
        _joinPath(
          _joinPath(_joinPath(directory.path, 'bin'), 'cache'),
          'dart-sdk',
        ),
        _joinPath('bin', executableName),
      ),
    );
    if (candidate.existsSync()) return candidate.path;
    directory = directory.parent;
  }
  return executableName;
}

String _applicationId(String suffix) {
  return 'com.cluion.bridra.single_instance_${suffix}_${pid}_'
      '${DateTime.now().microsecondsSinceEpoch}';
}

String _joinPath(String parent, String child) {
  if (parent.endsWith(Platform.pathSeparator)) return '$parent$child';
  return '$parent${Platform.pathSeparator}$child';
}

final class _HelperProcessResult {
  const _HelperProcessResult({
    required this.exitCode,
    required this.stdout,
    required this.stderr,
  });

  final int exitCode;
  final String stdout;
  final String stderr;
}
