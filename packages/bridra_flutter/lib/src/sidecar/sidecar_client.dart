import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import '../rpc/rpc_client.dart';
import 'sidecar_executable.dart';

class SidecarExitedException extends BackendConnectionException {
  const SidecarExitedException(this.exitCode)
    : super('The Go sidecar exited with code $exitCode.');

  final int exitCode;
}

class SidecarRestartExhaustedException extends BackendConnectionException {
  const SidecarRestartExhaustedException(this.attempts, {this.cause})
    : super('The Go sidecar could not be restarted after $attempts attempts.');

  final int attempts;
  final Object? cause;
}

class SidecarRestartPolicy {
  const SidecarRestartPolicy({
    this.maxAttempts = 3,
    this.initialDelay = const Duration(milliseconds: 250),
    this.maxDelay = const Duration(seconds: 2),
    this.backoffFactor = 2,
    this.healthCheckTimeout = const Duration(seconds: 2),
  });

  const SidecarRestartPolicy.disabled()
    : maxAttempts = 0,
      initialDelay = Duration.zero,
      maxDelay = Duration.zero,
      backoffFactor = 1,
      healthCheckTimeout = const Duration(seconds: 2);

  final int maxAttempts;
  final Duration initialDelay;
  final Duration maxDelay;
  final double backoffFactor;
  final Duration healthCheckTimeout;

  bool get isEnabled => maxAttempts > 0;

  Duration delayForAttempt(int attempt) {
    if (attempt < 1) {
      throw RangeError.range(attempt, 1, null, 'attempt');
    }
    if (initialDelay == Duration.zero) return Duration.zero;

    final multiplier = pow(backoffFactor, attempt - 1).toDouble();
    final microseconds = (initialDelay.inMicroseconds * multiplier).round();
    return Duration(microseconds: min(microseconds, maxDelay.inMicroseconds));
  }

  void _validate() {
    if (maxAttempts < 0) {
      throw ArgumentError.value(maxAttempts, 'maxAttempts');
    }
    if (initialDelay.isNegative) {
      throw ArgumentError.value(initialDelay, 'initialDelay');
    }
    if (maxDelay.isNegative || maxDelay < initialDelay) {
      throw ArgumentError.value(maxDelay, 'maxDelay');
    }
    if (backoffFactor < 1) {
      throw ArgumentError.value(backoffFactor, 'backoffFactor');
    }
    if (healthCheckTimeout <= Duration.zero) {
      throw ArgumentError.value(healthCheckTimeout, 'healthCheckTimeout');
    }
  }
}

abstract interface class SidecarProcess {
  IOSink get stdin;

  Stream<List<int>> get stdout;

  Stream<List<int>> get stderr;

  Future<int> get exitCode;

  bool kill([ProcessSignal signal = ProcessSignal.sigterm]);
}

typedef SidecarProcessStarter =
    Future<SidecarProcess> Function(
      String executablePath,
      List<String> arguments,
    );

enum _SidecarState { running, restarting, closing, closed, failed }

class SidecarClient implements RpcClient {
  SidecarClient._({
    required SidecarProcess process,
    required this._executablePath,
    required this._token,
    required this._processStarter,
    required this._restartPolicy,
    this._onLog,
  }) {
    _attachSession(process);
  }

  final String _executablePath;
  final String _token;
  final SidecarProcessStarter _processStarter;
  final SidecarRestartPolicy _restartPolicy;
  final void Function(String line)? _onLog;
  final Map<String, _PendingSidecarCall> _pending = {};
  final _closingSignal = Completer<void>();
  _SidecarSession? _session;
  Future<void> _writeQueue = Future.value();
  Future<void>? _restartFuture;
  Completer<void>? _restartReady;
  Future<void>? _closeFuture;
  Object? _terminalError;
  var _nextID = 0;
  var _state = _SidecarState.running;

  static Future<SidecarClient> start({
    required String executablePath,
    required String token,
    void Function(String line)? onLog,
    SidecarProcessStarter? processStarter,
    SidecarRestartPolicy restartPolicy = const SidecarRestartPolicy(),
  }) async {
    restartPolicy._validate();
    final starter = processStarter ?? _startSystemProcess;
    final process = await starter(executablePath, ['--token', token]);
    return SidecarClient._(
      process: process,
      executablePath: executablePath,
      token: token,
      processStarter: starter,
      restartPolicy: restartPolicy,
      onLog: onLog,
    );
  }

  @override
  Future<RpcReply> call(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(seconds: 5),
    RpcCancellationToken? cancellationToken,
  }) {
    if (_state == _SidecarState.closing ||
        _state == _SidecarState.closed ||
        _state == _SidecarState.failed) {
      return Future.error(_terminalError ?? const BackendClosedException());
    }
    if (cancellationToken?.isCancelled ?? false) {
      return Future.error(RpcCancelledException(method));
    }

    final id = '${++_nextID}';
    final pending = _PendingSidecarCall();
    _pending[id] = pending;
    pending.timeout = Timer(
      timeout,
      () => _cancelPending(
        id,
        TimeoutException('Sidecar method $method timed out.', timeout),
      ),
    );
    pending.cancellationSubscription = cancellationToken?.onCancel.listen(
      (_) => _cancelPending(id, RpcCancelledException(method)),
    );
    unawaited(
      _sendRequestWhenReady(
        id,
        encodeRpcRequest(id: id, method: method, params: params, token: _token),
      ),
    );
    return pending.completer.future;
  }

  @override
  Future<void> close() => _closeFuture ??= _performClose();

  Future<void> _performClose() async {
    if (_state == _SidecarState.closed) return;

    final wasRunning = _state == _SidecarState.running;
    _state = _SidecarState.closing;
    _terminalError = const BackendClosedException();
    _failPending(_terminalError!, StackTrace.current);
    if (!_closingSignal.isCompleted) {
      _closingSignal.complete();
    }
    final restartReady = _restartReady;
    if (restartReady != null && !restartReady.isCompleted) {
      restartReady.completeError(_terminalError!, StackTrace.current);
    }

    final session = _session;
    if (session != null) {
      final health = session.healthCompleter;
      if (health != null && !health.isCompleted) {
        health.completeError(_terminalError!, StackTrace.current);
      }
      if (wasRunning) {
        try {
          await _writeQueue;
        } on Object {
          // Write failures already fail their calls and start recovery.
        }
      }
      await _stopSession(session, graceful: wasRunning);
    }

    final restartFuture = _restartFuture;
    if (restartFuture != null) {
      await restartFuture;
    }

    final replacement = _session;
    if (replacement != null && !identical(replacement, session)) {
      await _stopSession(replacement, graceful: false);
    }
    _state = _SidecarState.closed;
  }

  void _attachSession(SidecarProcess process) {
    final session = _SidecarSession(process);
    _session = session;
    session.stdoutSubscription = process.stdout
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen(
          (line) => _handleResponse(session, line),
          onError: (Object error, StackTrace stackTrace) {
            _handleStreamError(session, error, stackTrace);
          },
        );
    session.stderrSubscription = process.stderr
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen(
          _emitLog,
          onError: (Object error, StackTrace stackTrace) {
            _handleLogStreamError(session, error, stackTrace);
          },
        );
    unawaited(
      process.exitCode.then(
        (exitCode) => _handleExit(session, exitCode),
        onError: (Object error, StackTrace stackTrace) {
          _handleStreamError(session, error, stackTrace);
        },
      ),
    );
  }

  Future<_SidecarSession> _waitForRunning() async {
    while (true) {
      switch (_state) {
        case _SidecarState.running:
          final session = _session;
          if (session == null) {
            throw BackendTransportException(
              'The Go sidecar process is unavailable.',
            );
          }
          return session;
        case _SidecarState.restarting:
          final ready = _restartReady;
          if (ready == null) {
            await Future<void>.delayed(Duration.zero);
          } else {
            await ready.future;
          }
          continue;
        case _SidecarState.closing:
        case _SidecarState.closed:
          throw const BackendClosedException();
        case _SidecarState.failed:
          throw _terminalError ??
              const BackendTransportException(
                'The Go sidecar process is unavailable.',
              );
      }
    }
  }

  Future<void> _writeRequest(_SidecarSession session, String request) {
    final write = _writeQueue.then((_) async {
      if (_state != _SidecarState.running ||
          !identical(_session, session) ||
          session.exited) {
        throw _terminalError ??
            const BackendTransportException(
              'The Go sidecar process is unavailable.',
            );
      }
      session.process.stdin.writeln(request);
      await session.process.stdin.flush();
    });
    _writeQueue = write.catchError((Object _) {});
    return write;
  }

  Future<void> _sendRequestWhenReady(String id, String request) async {
    _SidecarSession? session;
    try {
      session = await _waitForRunning();
      if (!_pending.containsKey(id)) return;
      await _writeRequest(session, request);
    } on Object catch (error, stackTrace) {
      final transportError = error is BackendConnectionException
          ? error
          : BackendTransportException(
              'Could not write to the Go sidecar.',
              cause: error,
            );
      final pending = _pending.remove(id);
      if (pending != null) {
        pending.completeError(transportError, stackTrace);
      }
      if (session != null &&
          _state == _SidecarState.running &&
          identical(_session, session)) {
        _handleSessionFailure(session, transportError, stackTrace);
      }
    }
  }

  void _cancelPending(String id, Object error) {
    final pending = _pending.remove(id);
    if (pending == null) return;
    pending.completeError(error, StackTrace.current);
    if (_state == _SidecarState.running) {
      unawaited(_sendCancellation(id));
    }
  }

  Future<void> _sendCancellation(String id) async {
    final session = _session;
    if (_state != _SidecarState.running || session == null) return;

    try {
      await _writeRequest(
        session,
        encodeRpcCancellation(id: id, token: _token),
      );
    } on Object catch (error, stackTrace) {
      if (_state != _SidecarState.running || !identical(_session, session)) {
        return;
      }
      final transportError = error is BackendConnectionException
          ? error
          : BackendTransportException(
              'Could not cancel a Go sidecar request.',
              cause: error,
            );
      _handleSessionFailure(session, transportError, stackTrace);
    }
  }

  void _handleResponse(_SidecarSession session, String line) {
    if (!identical(_session, session)) return;

    _PendingSidecarCall? pending;
    try {
      final decoded = jsonDecode(line);
      if (decoded is! Map) {
        throw const FormatException('Response must be a JSON object.');
      }
      final message = Map<String, dynamic>.from(decoded);
      final id = message['id'];
      if (id is! String || id.isEmpty) {
        throw const FormatException('Response id must be a non-empty string.');
      }

      if (id == session.healthRequestID) {
        final health = session.healthCompleter;
        if (health == null || health.isCompleted) return;
        try {
          health.complete(decodeRpcReply(message, expectedID: id));
        } on Object catch (error, stackTrace) {
          health.completeError(error, stackTrace);
        }
        return;
      }

      if (_state != _SidecarState.running) {
        _emitLog('Ignored sidecar response while the process was recovering.');
        return;
      }

      pending = _pending.remove(id);
      if (pending == null) {
        _emitLog('Ignored sidecar response for unknown request $id.');
        return;
      }

      try {
        pending.complete(decodeRpcReply(message, expectedID: id));
      } on RpcException catch (error, stackTrace) {
        pending.completeError(error, stackTrace);
      }
    } on Object catch (error, stackTrace) {
      final protocolError = error is BackendProtocolException
          ? error
          : BackendProtocolException(
              'The Go sidecar returned an invalid response.',
              cause: error,
            );
      final health = session.healthCompleter;
      if (_state == _SidecarState.restarting &&
          health != null &&
          !health.isCompleted) {
        health.completeError(protocolError, stackTrace);
        return;
      }
      if (pending != null && !pending.completer.isCompleted) {
        pending.completeError(protocolError, stackTrace);
      }
      _handleSessionFailure(session, protocolError, stackTrace);
    }
  }

  void _handleStreamError(
    _SidecarSession session,
    Object error, [
    StackTrace? stackTrace,
  ]) {
    if (!identical(_session, session)) return;

    final transportError = error is BackendConnectionException
        ? error
        : BackendTransportException(
            'The Go sidecar response stream failed.',
            cause: error,
          );
    final effectiveStackTrace = stackTrace ?? StackTrace.current;
    final health = session.healthCompleter;
    if (_state == _SidecarState.restarting &&
        health != null &&
        !health.isCompleted) {
      health.completeError(transportError, effectiveStackTrace);
      return;
    }
    _handleSessionFailure(session, transportError, effectiveStackTrace);
  }

  void _handleLogStreamError(
    _SidecarSession session,
    Object error, [
    StackTrace? stackTrace,
  ]) {
    if (!identical(_session, session)) return;
    _emitLog('The Go sidecar log stream failed: $error');
  }

  void _handleExit(_SidecarSession session, int exitCode) {
    session.exited = true;
    if (!identical(_session, session)) return;
    if (_state == _SidecarState.closing ||
        _state == _SidecarState.closed ||
        _state == _SidecarState.failed) {
      return;
    }

    final error = SidecarExitedException(exitCode);
    if (_state == _SidecarState.restarting) {
      final health = session.healthCompleter;
      if (health != null && !health.isCompleted) {
        health.completeError(error, StackTrace.current);
      }
      return;
    }
    _handleSessionFailure(session, error, StackTrace.current);
  }

  void _handleSessionFailure(
    _SidecarSession session,
    Object error,
    StackTrace stackTrace,
  ) {
    if (_state != _SidecarState.running || !identical(_session, session)) {
      return;
    }

    _terminalError = error;
    _failPending(error, stackTrace);
    if (!_restartPolicy.isEnabled) {
      _state = _SidecarState.failed;
      if (!session.exited) {
        session.process.kill();
      }
      return;
    }

    _state = _SidecarState.restarting;
    final ready = Completer<void>();
    _restartReady = ready;
    unawaited(ready.future.catchError((Object _) {}));
    _restartFuture = _restart(session, error).catchError((
      Object restartError,
      StackTrace stackTrace,
    ) {
      _finishRestartExhausted(restartError, stackTrace);
    });
  }

  Future<void> _restart(
    _SidecarSession failedSession,
    Object initialError,
  ) async {
    await _stopSession(failedSession, graceful: false);
    Object lastError = initialError;

    for (var attempt = 1; attempt <= _restartPolicy.maxAttempts; attempt++) {
      final delay = _restartPolicy.delayForAttempt(attempt);
      _emitLog(
        'Restarting the Go sidecar in ${delay.inMilliseconds} ms '
        '(attempt $attempt/${_restartPolicy.maxAttempts}).',
      );
      if (!await _waitForRestartDelay(delay)) return;

      _SidecarSession? session;
      try {
        final process = await _startReplacementProcess();
        if (_state != _SidecarState.restarting) {
          final abandoned = _SidecarSession(process);
          await _stopUnattachedProcess(abandoned);
          return;
        }

        _attachSession(process);
        session = _session;
        if (session == null) {
          throw const BackendTransportException(
            'The restarted Go sidecar process is unavailable.',
          );
        }
        await _verifyRestartedSession(session);
        if (_state != _SidecarState.restarting ||
            !identical(_session, session)) {
          await _stopSession(session, graceful: false);
          return;
        }

        _terminalError = null;
        _state = _SidecarState.running;
        final ready = _restartReady;
        if (ready != null && !ready.isCompleted) {
          ready.complete();
        }
        _emitLog(
          'The Go sidecar restarted successfully '
          '(attempt $attempt/${_restartPolicy.maxAttempts}).',
        );
        return;
      } on Object catch (error, stackTrace) {
        lastError = error;
        _emitLog(
          'Go sidecar restart attempt '
          '$attempt/${_restartPolicy.maxAttempts} failed: $error',
        );
        if (session != null) {
          await _stopSession(session, graceful: false);
        }
        if (_state != _SidecarState.restarting) return;
        if (attempt == _restartPolicy.maxAttempts) {
          _finishRestartExhausted(lastError, stackTrace);
          return;
        }
      }
    }
  }

  Future<SidecarProcess> _startReplacementProcess() async {
    try {
      return await _processStarter(_executablePath, ['--token', _token]);
    } on Object catch (error) {
      final details = error.toString().replaceAll(_token, '[REDACTED]');
      throw BackendTransportException(
        'Could not start a replacement Go sidecar: $details',
      );
    }
  }

  void _finishRestartExhausted(Object error, StackTrace stackTrace) {
    if (_state != _SidecarState.restarting) return;

    final exhausted = SidecarRestartExhaustedException(
      _restartPolicy.maxAttempts,
      cause: error,
    );
    _terminalError = exhausted;
    _state = _SidecarState.failed;
    _failPending(exhausted, stackTrace);
    final ready = _restartReady;
    if (ready != null && !ready.isCompleted) {
      ready.completeError(exhausted, stackTrace);
    }
    _emitLog(exhausted.message);
  }

  Future<bool> _waitForRestartDelay(Duration delay) async {
    if (_state != _SidecarState.restarting) return false;
    if (delay > Duration.zero) {
      await Future.any([Future<void>.delayed(delay), _closingSignal.future]);
    }
    return _state == _SidecarState.restarting;
  }

  Future<void> _verifyRestartedSession(_SidecarSession session) async {
    final id = 'restart-health-${++_nextID}';
    final completer = Completer<RpcReply>();
    session.healthRequestID = id;
    session.healthCompleter = completer;
    unawaited(
      completer.future.then<void>((_) {}, onError: (Object _, StackTrace _) {}),
    );

    try {
      session.process.stdin.writeln(
        encodeRpcRequest(
          id: id,
          method: 'system.health',
          params: const {},
          token: _token,
        ),
      );
      await session.process.stdin.flush();
      final reply = await completer.future.timeout(
        _restartPolicy.healthCheckTimeout,
      );
      final result = reply.result;
      if (result is! Map || result['status'] != 'ok') {
        throw const BackendProtocolException(
          'The restarted Go sidecar returned an unhealthy status.',
        );
      }
    } on BackendConnectionException {
      rethrow;
    } on Object catch (error) {
      throw BackendTransportException(
        'The restarted Go sidecar failed its health check.',
        cause: error,
      );
    } finally {
      session.healthRequestID = null;
      session.healthCompleter = null;
    }
  }

  Future<void> _stopSession(_SidecarSession session, {required bool graceful}) {
    return session.stopFuture ??= _performStopSession(
      session,
      graceful: graceful,
    );
  }

  Future<void> _performStopSession(
    _SidecarSession session, {
    required bool graceful,
  }) async {
    if (!session.exited) {
      if (graceful) {
        try {
          await session.process.stdin.close();
        } on Object catch (error) {
          _emitLog('Could not close sidecar stdin: $error');
        }
      } else {
        session.process.kill();
      }
      await _waitForExit(session);
    }

    await Future.wait([
      session.stdoutSubscription.cancel(),
      session.stderrSubscription.cancel(),
    ]);
    if (identical(_session, session)) {
      _session = null;
    }
  }

  Future<void> _stopUnattachedProcess(_SidecarSession session) async {
    session.process.kill();
    try {
      await session.process.exitCode.timeout(const Duration(seconds: 2));
    } on TimeoutException {
      if (!Platform.isWindows) {
        session.process.kill(ProcessSignal.sigkill);
      }
    }
  }

  Future<void> _waitForExit(_SidecarSession session) async {
    try {
      await session.process.exitCode.timeout(const Duration(seconds: 2));
      session.exited = true;
      return;
    } on TimeoutException {
      session.process.kill();
    }

    try {
      await session.process.exitCode.timeout(const Duration(seconds: 2));
      session.exited = true;
      return;
    } on TimeoutException {
      if (!Platform.isWindows) {
        session.process.kill(ProcessSignal.sigkill);
      }
    }

    try {
      await session.process.exitCode.timeout(const Duration(seconds: 2));
      session.exited = true;
    } on TimeoutException {
      _emitLog('The Go sidecar did not exit after being killed.');
    }
  }

  void _failPending(Object error, StackTrace stackTrace) {
    for (final pending in _pending.values) {
      if (!pending.completer.isCompleted) {
        pending.completeError(error, stackTrace);
      }
    }
    _pending.clear();
  }

  void _emitLog(String line) {
    try {
      final safeLine = _token.isEmpty
          ? line
          : line.replaceAll(_token, '[REDACTED]');
      _onLog?.call(safeLine);
    } on Object {
      // Logging must never break the RPC transport.
    }
  }

  static String createToken() {
    final random = Random.secure();
    final bytes = List<int>.generate(32, (_) => random.nextInt(256));
    return bytes.map((byte) => byte.toRadixString(16).padLeft(2, '0')).join();
  }

  static Future<String> resolveExecutable() async {
    return discoverSidecarExecutable(
      isWindows: Platform.isWindows,
      pathSeparator: Platform.pathSeparator,
      environment: Platform.environment,
      resolvedExecutableDirectory: File(
        Platform.resolvedExecutable,
      ).parent.path,
      currentDirectory: Directory.current.path,
      fileExists: (path) => File(path).exists(),
    );
  }
}

class _SidecarSession {
  _SidecarSession(this.process);

  final SidecarProcess process;
  late StreamSubscription<String> stdoutSubscription;
  late StreamSubscription<String> stderrSubscription;
  String? healthRequestID;
  Completer<RpcReply>? healthCompleter;
  Future<void>? stopFuture;
  var exited = false;
}

class _PendingSidecarCall {
  final completer = Completer<RpcReply>();
  Timer? timeout;
  StreamSubscription<void>? cancellationSubscription;

  void complete(RpcReply reply) {
    _dispose();
    completer.complete(reply);
  }

  void completeError(Object error, StackTrace stackTrace) {
    _dispose();
    completer.completeError(error, stackTrace);
  }

  void _dispose() {
    timeout?.cancel();
    final subscription = cancellationSubscription;
    if (subscription != null) {
      unawaited(subscription.cancel());
    }
  }
}

Future<SidecarProcess> _startSystemProcess(
  String executablePath,
  List<String> arguments,
) async {
  final process = await Process.start(
    executablePath,
    arguments,
    mode: ProcessStartMode.normal,
  );
  return _SystemSidecarProcess(process);
}

class _SystemSidecarProcess implements SidecarProcess {
  const _SystemSidecarProcess(this._process);

  final Process _process;

  @override
  Future<int> get exitCode => _process.exitCode;

  @override
  Stream<List<int>> get stderr => _process.stderr;

  @override
  IOSink get stdin => _process.stdin;

  @override
  Stream<List<int>> get stdout => _process.stdout;

  @override
  bool kill([ProcessSignal signal = ProcessSignal.sigterm]) {
    return _process.kill(signal);
  }
}
