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

  int get receivedRequestCount => _requests.length;

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
    final request = Map<String, dynamic>.from(jsonDecode(line) as Map);
    if (_requestWaiters.isNotEmpty) {
      _requestWaiters.removeAt(0).complete(request);
    } else {
      _requests.add(request);
    }
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
