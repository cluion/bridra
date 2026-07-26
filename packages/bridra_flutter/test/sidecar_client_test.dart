import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:bridra_flutter/bridra_flutter_sidecar.dart';

void main() {
  test(
    'matches concurrent replies by id and preserves RPC error data',
    () async {
      final process = FakeSidecarProcess();
      final client = await _startClient(process);
      addTearDown(client.close);

      final first = client.call('first');
      final second = client.call('second');
      final firstRequest = await process.nextRequest();
      final secondRequest = await process.nextRequest();

      final firstExpectation = expectLater(
        first,
        throwsA(
          isA<RpcException>()
              .having((error) => error.code, 'code', 'validation_error')
              .having((error) => error.data['field'], 'field', 'name'),
        ),
      );
      process.respond({
        'id': secondRequest['id'],
        'result': {'value': 2},
        'meta': <String, Object?>{},
      });
      process.respond({
        'id': firstRequest['id'],
        'error': {
          'code': 'validation_error',
          'message': 'Invalid.',
          'data': {'field': 'name'},
        },
      });

      expect((await second).result, {'value': 2});
      await firstExpectation;
    },
  );

  test('fails pending and future calls when the process exits', () async {
    final process = FakeSidecarProcess();
    final client = await _startClient(process);
    addTearDown(client.close);

    final pending = client.call('slow');
    await process.nextRequest();
    final pendingExpectation = expectLater(
      pending,
      throwsA(
        isA<SidecarExitedException>().having(
          (error) => error.exitCode,
          'exitCode',
          17,
        ),
      ),
    );

    await process.exit(17);

    await pendingExpectation;
    await expectLater(
      client.call('after-exit'),
      throwsA(isA<SidecarExitedException>()),
    );
  });

  test('close is shared and immediately fails pending calls', () async {
    final process = FakeSidecarProcess();
    final client = await _startClient(process);

    final pending = client.call('slow');
    await process.nextRequest();
    final pendingExpectation = expectLater(
      pending,
      throwsA(isA<BackendClosedException>()),
    );

    final firstClose = client.close();
    final secondClose = client.close();

    expect(identical(firstClose, secondClose), isTrue);
    await firstClose;
    await pendingExpectation;
  });

  test('invalid protocol responses terminate the transport', () async {
    final process = FakeSidecarProcess();
    final client = await _startClient(process);
    addTearDown(client.close);

    final pending = client.call('broken');
    await process.nextRequest();
    final pendingExpectation = expectLater(
      pending,
      throwsA(isA<BackendProtocolException>()),
    );

    process.sendRaw('["not-an-object"]');

    await pendingExpectation;
    expect(process.killCount, 1);
    await expectLater(
      client.call('after-protocol-error'),
      throwsA(isA<BackendProtocolException>()),
    );
  });

  test('malformed responses fail their known call before recovery', () async {
    final process = FakeSidecarProcess();
    final client = await _startClient(process);
    addTearDown(client.close);

    final call = client.call('malformed');
    final request = await process.nextRequest();
    final callExpectation = expectLater(
      call,
      throwsA(isA<BackendProtocolException>()),
    );
    process.respond({'id': request['id'], 'meta': <String, Object?>{}});

    await callExpectation;
    expect(process.killCount, 1);
  });

  test('late responses after timeout are ignored and logged', () async {
    final process = FakeSidecarProcess();
    final logs = <String>[];
    final client = await _startClient(process, onLog: logs.add);
    addTearDown(client.close);

    final call = client.call('slow', timeout: const Duration(milliseconds: 10));
    final request = await process.nextRequest();

    await expectLater(call, throwsA(isA<TimeoutException>()));
    final cancellation = await process.nextRequest();
    expect(cancellation['id'], request['id']);
    expect(cancellation['method'], 'rpc.cancel');
    expect((cancellation['meta'] as Map)['token'], 'test-token');
    process.respond({
      'id': request['id'],
      'result': 'late',
      'meta': <String, Object?>{},
    });
    await Future<void>.delayed(Duration.zero);

    expect(
      logs,
      contains(
        'Ignored sidecar response for unknown request ${request['id']}.',
      ),
    );
  });

  test(
    'caller cancellation sends a control request and keeps transport',
    () async {
      final process = FakeSidecarProcess();
      final client = await _startClient(process);
      addTearDown(client.close);
      final cancellationToken = RpcCancellationToken();

      final call = client.call('slow', cancellationToken: cancellationToken);
      final request = await process.nextRequest();
      cancellationToken.cancel();

      await expectLater(call, throwsA(isA<RpcCancelledException>()));
      final cancellation = await process.nextRequest();
      expect(cancellation['id'], request['id']);
      expect(cancellation['method'], 'rpc.cancel');

      final next = client.call('next');
      final nextRequest = await process.nextRequest();
      process.respond({
        'id': nextRequest['id'],
        'result': 'ok',
        'meta': <String, Object?>{},
      });
      expect((await next).result, 'ok');
    },
  );

  test('already cancelled calls do not write to the sidecar', () async {
    final process = FakeSidecarProcess();
    final client = await _startClient(process);
    addTearDown(client.close);
    final cancellationToken = RpcCancellationToken()..cancel();

    await expectLater(
      client.call('cancelled', cancellationToken: cancellationToken),
      throwsA(isA<RpcCancelledException>()),
    );
    expect(process.receivedRequestCount, 0);
  });

  test(
    'write failures terminate the transport without leaking the call',
    () async {
      final process = FailingWriteSidecarProcess();
      final client = await SidecarClient.start(
        executablePath: '/fake/sidecar',
        token: 'test-token',
        restartPolicy: const SidecarRestartPolicy.disabled(),
        processStarter: (_, _) async => process,
      );
      addTearDown(client.close);

      await expectLater(
        client.call('write-failure'),
        throwsA(isA<BackendTransportException>()),
      );
      expect(process.killCount, 1);
    },
  );

  test('restart policy applies exponential backoff with a cap', () {
    const policy = SidecarRestartPolicy(
      maxAttempts: 5,
      initialDelay: Duration(milliseconds: 100),
      maxDelay: Duration(milliseconds: 450),
      backoffFactor: 2,
    );

    expect(policy.delayForAttempt(1), const Duration(milliseconds: 100));
    expect(policy.delayForAttempt(2), const Duration(milliseconds: 200));
    expect(policy.delayForAttempt(3), const Duration(milliseconds: 400));
    expect(policy.delayForAttempt(4), const Duration(milliseconds: 450));
    expect(policy.delayForAttempt(5), const Duration(milliseconds: 450));
    expect(() => policy.delayForAttempt(0), throwsRangeError);

    final disabled = SidecarRestartPolicy.disabled();
    expect(disabled.isEnabled, isFalse);
    expect(disabled.maxAttempts, 0);
  });

  test('rejects invalid restart policies before starting a process', () async {
    const policies = [
      SidecarRestartPolicy(maxAttempts: -1),
      SidecarRestartPolicy(initialDelay: Duration(microseconds: -1)),
      SidecarRestartPolicy(
        initialDelay: Duration(milliseconds: 2),
        maxDelay: Duration(milliseconds: 1),
      ),
      SidecarRestartPolicy(backoffFactor: 0.5),
      SidecarRestartPolicy(healthCheckTimeout: Duration.zero),
    ];
    var startCount = 0;

    for (final policy in policies) {
      await expectLater(
        SidecarClient.start(
          executablePath: '/fake/sidecar',
          token: 'test-token',
          restartPolicy: policy,
          processStarter: (_, _) async {
            startCount++;
            return FakeSidecarProcess();
          },
        ),
        throwsArgumentError,
      );
    }

    expect(startCount, 0);
  });

  test('restarts after a crash without replaying the in-flight call', () async {
    final first = FakeSidecarProcess();
    final replacement = FakeSidecarProcess();
    final starter = FakeSidecarStarter([first, replacement]);
    final logs = <String>[];
    final client = await SidecarClient.start(
      executablePath: '/fake/sidecar',
      token: 'test-token',
      onLog: logs.add,
      restartPolicy: const SidecarRestartPolicy(
        initialDelay: Duration.zero,
        maxDelay: Duration.zero,
      ),
      processStarter: starter.start,
    );
    addTearDown(client.close);

    final inFlight = client.call('unsafe-write');
    await first.nextRequest();
    final inFlightExpectation = expectLater(
      inFlight,
      throwsA(isA<SidecarExitedException>()),
    );
    await first.exit(17);
    await inFlightExpectation;

    final recovered = client.call('after-restart');
    final health = await replacement.nextRequest();
    expect(health['method'], 'system.health');
    replacement.respond({
      'id': 'unexpected-during-recovery',
      'result': 'ignored',
      'meta': <String, Object?>{},
    });
    replacement.respond({
      'id': health['id'],
      'result': {'status': 'ok'},
      'meta': <String, Object?>{},
    });
    final request = await replacement.nextRequest();
    expect(request['method'], 'after-restart');
    replacement.respond({
      'id': request['id'],
      'result': 'recovered',
      'meta': <String, Object?>{},
    });

    expect((await recovered).result, 'recovered');
    expect(starter.callCount, 2);
    expect(replacement.receivedRequestCount, 2);
    expect(
      logs,
      contains('The Go sidecar restarted successfully (attempt 1/3).'),
    );
    expect(
      logs,
      contains('Ignored sidecar response while the process was recovering.'),
    );
    expect(logs.join('\n'), isNot(contains('test-token')));
  });

  test('retries when a replacement fails its health check', () async {
    final first = FakeSidecarProcess();
    final unhealthy = FakeSidecarProcess();
    final healthy = FakeSidecarProcess();
    final starter = FakeSidecarStarter([first, unhealthy, healthy]);
    final logs = <String>[];
    final client = await SidecarClient.start(
      executablePath: '/fake/sidecar',
      token: 'test-token',
      onLog: logs.add,
      restartPolicy: const SidecarRestartPolicy(
        maxAttempts: 2,
        initialDelay: Duration.zero,
        maxDelay: Duration.zero,
      ),
      processStarter: starter.start,
    );
    addTearDown(client.close);

    final inFlight = client.call('slow');
    await first.nextRequest();
    final inFlightExpectation = expectLater(
      inFlight,
      throwsA(isA<SidecarExitedException>()),
    );
    await first.exit(19);
    await inFlightExpectation;

    final recovered = client.call('after-health-retry');
    final unhealthyCheck = await unhealthy.nextRequest();
    unhealthy.respond({
      'id': unhealthyCheck['id'],
      'result': {'status': 'down'},
      'meta': <String, Object?>{},
    });
    final healthyCheck = await healthy.nextRequest();
    healthy.respond({
      'id': healthyCheck['id'],
      'result': {'status': 'ok'},
      'meta': <String, Object?>{},
    });
    final request = await healthy.nextRequest();
    healthy.respond({
      'id': request['id'],
      'result': 'ok',
      'meta': <String, Object?>{},
    });

    expect((await recovered).result, 'ok');
    expect(starter.callCount, 3);
    expect(unhealthy.killCount, 1);
    expect(
      logs.any((line) => line.startsWith('Go sidecar restart attempt 1/2')),
      isTrue,
    );
  });

  test(
    'exhausts replacements that error, corrupt, or exit during health',
    () async {
      final first = FakeSidecarProcess();
      final rpcFailure = FakeSidecarProcess();
      final corrupt = FakeSidecarProcess();
      final exits = FakeSidecarProcess();
      final starter = FakeSidecarStarter([first, rpcFailure, corrupt, exits]);
      final client = await SidecarClient.start(
        executablePath: '/fake/sidecar',
        token: 'test-token',
        restartPolicy: const SidecarRestartPolicy(
          maxAttempts: 3,
          initialDelay: Duration.zero,
          maxDelay: Duration.zero,
        ),
        processStarter: starter.start,
      );
      addTearDown(client.close);

      final inFlight = client.call('slow');
      await first.nextRequest();
      final inFlightExpectation = expectLater(
        inFlight,
        throwsA(isA<SidecarExitedException>()),
      );
      await first.exit(37);
      await inFlightExpectation;

      final waiting = client.call('waits-for-health');
      final waitingExpectation = expectLater(
        waiting,
        throwsA(isA<SidecarRestartExhaustedException>()),
      );
      final rpcHealth = await rpcFailure.nextRequest();
      rpcFailure.respond({
        'id': rpcHealth['id'],
        'error': {'code': 'unhealthy', 'message': 'Health failed.'},
      });
      await corrupt.nextRequest();
      corrupt.sendRaw('not-json');
      await exits.nextRequest();
      await exits.exit(41);

      await waitingExpectation;
      expect(starter.callCount, 4);
      expect(corrupt.killCount, 1);
    },
  );

  test('returns a stable error when restart attempts are exhausted', () async {
    final first = FakeSidecarProcess();
    final starter = FakeSidecarStarter([
      first,
      StateError('start failed once with test-token'),
      StateError('start failed twice with test-token'),
    ]);
    final logs = <String>[];
    final client = await SidecarClient.start(
      executablePath: '/fake/sidecar',
      token: 'test-token',
      onLog: logs.add,
      restartPolicy: const SidecarRestartPolicy(
        maxAttempts: 2,
        initialDelay: Duration.zero,
        maxDelay: Duration.zero,
      ),
      processStarter: starter.start,
    );
    addTearDown(client.close);

    final inFlight = client.call('slow');
    await first.nextRequest();
    final inFlightExpectation = expectLater(
      inFlight,
      throwsA(isA<SidecarExitedException>()),
    );
    await first.exit(23);
    await inFlightExpectation;

    final matcher = isA<SidecarRestartExhaustedException>()
        .having((error) => error.attempts, 'attempts', 2)
        .having(
          (error) => error.cause,
          'cause',
          isA<BackendTransportException>(),
        );
    await expectLater(client.call('after-exhaustion'), throwsA(matcher));
    await expectLater(client.call('still-exhausted'), throwsA(matcher));
    expect(starter.callCount, 3);
    expect(logs.join('\n'), isNot(contains('test-token')));
    expect(logs.join('\n'), contains('[REDACTED]'));
  });

  test('timeouts and cancellation remain active during recovery', () async {
    final first = FakeSidecarProcess();
    final replacement = FakeSidecarProcess();
    final starter = FakeSidecarStarter([first, replacement]);
    final client = await SidecarClient.start(
      executablePath: '/fake/sidecar',
      token: 'test-token',
      restartPolicy: const SidecarRestartPolicy(
        initialDelay: Duration(milliseconds: 30),
        maxDelay: Duration(milliseconds: 30),
      ),
      processStarter: starter.start,
    );
    addTearDown(client.close);

    final inFlight = client.call('slow');
    await first.nextRequest();
    final inFlightExpectation = expectLater(
      inFlight,
      throwsA(isA<SidecarExitedException>()),
    );
    await first.exit(29);
    await inFlightExpectation;

    final timedOut = client.call(
      'times-out-while-waiting',
      timeout: const Duration(milliseconds: 5),
    );
    final cancellationToken = RpcCancellationToken();
    final cancelled = client.call(
      'cancelled-while-waiting',
      cancellationToken: cancellationToken,
    );
    final timedOutExpectation = expectLater(
      timedOut,
      throwsA(isA<TimeoutException>()),
    );
    final cancelledExpectation = expectLater(
      cancelled,
      throwsA(isA<RpcCancelledException>()),
    );
    cancellationToken.cancel();

    await timedOutExpectation;
    await cancelledExpectation;
    final health = await replacement.nextRequest();
    replacement.respond({
      'id': health['id'],
      'result': {'status': 'ok'},
      'meta': <String, Object?>{},
    });
    await Future<void>.delayed(Duration.zero);

    expect(replacement.receivedRequestCount, 1);
  });

  test(
    'close cancels a pending restart before another process starts',
    () async {
      final first = FakeSidecarProcess();
      final replacement = FakeSidecarProcess();
      final starter = FakeSidecarStarter([first, replacement]);
      final client = await SidecarClient.start(
        executablePath: '/fake/sidecar',
        token: 'test-token',
        restartPolicy: const SidecarRestartPolicy(
          maxAttempts: 1,
          initialDelay: Duration(seconds: 1),
          maxDelay: Duration(seconds: 1),
        ),
        processStarter: starter.start,
      );

      final inFlight = client.call('slow');
      await first.nextRequest();
      final inFlightExpectation = expectLater(
        inFlight,
        throwsA(isA<SidecarExitedException>()),
      );
      await first.exit(31);
      await inFlightExpectation;

      await client.close();

      expect(starter.callCount, 1);
      await expectLater(
        client.call('after-close'),
        throwsA(isA<BackendClosedException>()),
      );
    },
  );

  test('createToken returns 256 bits of lowercase hexadecimal data', () {
    final token = SidecarClient.createToken();

    expect(token, hasLength(64));
    expect(RegExp(r'^[0-9a-f]{64}$').hasMatch(token), isTrue);
  });
}

Future<SidecarClient> _startClient(
  FakeSidecarProcess process, {
  void Function(String line)? onLog,
}) {
  return SidecarClient.start(
    executablePath: '/fake/sidecar',
    token: 'test-token',
    onLog: onLog,
    restartPolicy: const SidecarRestartPolicy.disabled(),
    processStarter: (executablePath, arguments) async {
      expect(executablePath, '/fake/sidecar');
      expect(arguments, ['--token', 'test-token']);
      return process;
    },
  );
}

class FakeSidecarProcess implements SidecarProcess {
  FakeSidecarProcess() {
    stdin = IOSink(_inputController.sink);
    _inputController.stream
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen(_receiveRequest, onDone: () => unawaited(exit(0)));
  }

  final _inputController = StreamController<List<int>>();
  final _stdoutController = StreamController<List<int>>();
  final _stderrController = StreamController<List<int>>();
  final _exitCode = Completer<int>();
  final _requests = <Map<String, dynamic>>[];
  final _requestWaiters = <Completer<Map<String, dynamic>>>[];

  @override
  late final IOSink stdin;

  var killCount = 0;
  var _receivedRequestCount = 0;

  int get receivedRequestCount => _receivedRequestCount;

  @override
  Stream<List<int>> get stderr => _stderrController.stream;

  @override
  Stream<List<int>> get stdout => _stdoutController.stream;

  @override
  Future<int> get exitCode => _exitCode.future;

  Future<Map<String, dynamic>> nextRequest() {
    if (_requests.isNotEmpty) {
      return Future.value(_requests.removeAt(0));
    }
    final waiter = Completer<Map<String, dynamic>>();
    _requestWaiters.add(waiter);
    return waiter.future;
  }

  void respond(Map<String, Object?> response) {
    sendRaw(jsonEncode(response));
  }

  void sendRaw(String response) {
    if (!_stdoutController.isClosed) {
      _stdoutController.add(utf8.encode('$response\n'));
    }
  }

  Future<void> exit(int code) async {
    if (_exitCode.isCompleted) return;
    _exitCode.complete(code);
    await _stdoutController.close();
    await _stderrController.close();
  }

  @override
  bool kill([ProcessSignal signal = ProcessSignal.sigterm]) {
    killCount++;
    unawaited(exit(signal == ProcessSignal.sigkill ? 137 : 143));
    return true;
  }

  void _receiveRequest(String line) {
    _receivedRequestCount++;
    final request = Map<String, dynamic>.from(jsonDecode(line) as Map);
    if (_requestWaiters.isNotEmpty) {
      _requestWaiters.removeAt(0).complete(request);
    } else {
      _requests.add(request);
    }
  }
}

class FakeSidecarStarter {
  FakeSidecarStarter(this._outcomes);

  final List<Object> _outcomes;
  var callCount = 0;

  Future<SidecarProcess> start(
    String executablePath,
    List<String> arguments,
  ) async {
    expect(executablePath, '/fake/sidecar');
    expect(arguments, ['--token', 'test-token']);
    final index = callCount++;
    if (index >= _outcomes.length) {
      throw StateError('Unexpected sidecar start $callCount.');
    }
    final outcome = _outcomes[index];
    if (outcome is SidecarProcess) return outcome;
    throw outcome;
  }
}

class FailingWriteSidecarProcess implements SidecarProcess {
  FailingWriteSidecarProcess() : stdin = IOSink(FailingStreamConsumer());

  final _stdoutController = StreamController<List<int>>();
  final _stderrController = StreamController<List<int>>();
  final _exitCode = Completer<int>();

  @override
  final IOSink stdin;

  var killCount = 0;

  @override
  Future<int> get exitCode => _exitCode.future;

  @override
  Stream<List<int>> get stderr => _stderrController.stream;

  @override
  Stream<List<int>> get stdout => _stdoutController.stream;

  @override
  bool kill([ProcessSignal signal = ProcessSignal.sigterm]) {
    killCount++;
    if (!_exitCode.isCompleted) {
      _exitCode.complete(143);
      unawaited(_stdoutController.close());
      unawaited(_stderrController.close());
    }
    return true;
  }
}

class FailingStreamConsumer implements StreamConsumer<List<int>> {
  @override
  Future<void> addStream(Stream<List<int>> stream) async {
    throw StateError('write failed');
  }

  @override
  Future<void> close() async {}
}
