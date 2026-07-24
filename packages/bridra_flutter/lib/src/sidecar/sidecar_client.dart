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

enum _SidecarState { running, closing, closed, exited }

class SidecarClient implements RpcClient {
  SidecarClient._(this._process, this._token, {this._onLog}) {
    _stdoutSubscription = _process.stdout
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen(_handleResponse, onError: _handleStreamError);
    _stderrSubscription = _process.stderr
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen(_emitLog, onError: _handleLogStreamError);
    _exitCode = _process.exitCode;
    unawaited(_exitCode.then(_handleExit, onError: _handleStreamError));
  }

  final SidecarProcess _process;
  final String _token;
  final void Function(String line)? _onLog;
  final Map<String, _PendingSidecarCall> _pending = {};
  late final StreamSubscription<String> _stdoutSubscription;
  late final StreamSubscription<String> _stderrSubscription;
  late final Future<int> _exitCode;
  Future<void> _writeQueue = Future.value();
  Future<void>? _closeFuture;
  Object? _terminalError;
  var _nextID = 0;
  var _processExited = false;
  var _state = _SidecarState.running;

  static Future<SidecarClient> start({
    required String executablePath,
    required String token,
    void Function(String line)? onLog,
    SidecarProcessStarter? processStarter,
  }) async {
    final starter = processStarter ?? _startSystemProcess;
    final process = await starter(executablePath, ['--token', token]);
    return SidecarClient._(process, token, onLog: onLog);
  }

  @override
  Future<RpcReply> call(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(seconds: 5),
    RpcCancellationToken? cancellationToken,
  }) {
    if (_state != _SidecarState.running) {
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
      _sendRequest(
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

    final processAlreadyExited = _processExited;
    _state = _SidecarState.closing;
    _terminalError = const BackendClosedException();
    _failPending(_terminalError!, StackTrace.current);

    if (!processAlreadyExited) {
      try {
        await _writeQueue;
      } on Object {
        // Write failures already transition the client to an unavailable state.
      }
      try {
        await _process.stdin.close();
      } on Object catch (error) {
        _emitLog('Could not close sidecar stdin: $error');
      }
      await _waitForExit();
    }

    await Future.wait([
      _stdoutSubscription.cancel(),
      _stderrSubscription.cancel(),
    ]);
    _state = _SidecarState.closed;
  }

  Future<void> _writeRequest(String request) {
    final write = _writeQueue.then((_) async {
      if (_state != _SidecarState.running) {
        throw _terminalError ?? const BackendClosedException();
      }
      _process.stdin.writeln(request);
      await _process.stdin.flush();
    });
    _writeQueue = write.catchError((Object _) {});
    return write;
  }

  Future<void> _sendRequest(String id, String request) async {
    try {
      await _writeRequest(request);
    } on Object catch (error, stackTrace) {
      final shouldKillProcess = _state == _SidecarState.running;
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
      _markUnavailable(transportError, stackTrace);
      if (shouldKillProcess) {
        _process.kill();
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
    try {
      await _writeRequest(encodeRpcCancellation(id: id, token: _token));
    } on Object catch (error, stackTrace) {
      if (_state != _SidecarState.running) return;
      final transportError = error is BackendConnectionException
          ? error
          : BackendTransportException(
              'Could not cancel a Go sidecar request.',
              cause: error,
            );
      _markUnavailable(transportError, stackTrace);
      _process.kill();
    }
  }

  Future<void> _waitForExit() async {
    try {
      await _exitCode.timeout(const Duration(seconds: 2));
      return;
    } on TimeoutException {
      _process.kill();
    }

    try {
      await _exitCode.timeout(const Duration(seconds: 2));
      return;
    } on TimeoutException {
      if (!Platform.isWindows) {
        _process.kill(ProcessSignal.sigkill);
      }
    }

    try {
      await _exitCode.timeout(const Duration(seconds: 2));
    } on TimeoutException {
      _emitLog('The Go sidecar did not exit after being killed.');
    }
  }

  void _handleResponse(String line) {
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
      if (pending != null && !pending.completer.isCompleted) {
        pending.completeError(protocolError, stackTrace);
      }
      _markUnavailable(protocolError, stackTrace);
      _process.kill();
    }
  }

  void _handleStreamError(Object error, [StackTrace? stackTrace]) {
    final transportError = error is BackendConnectionException
        ? error
        : BackendTransportException(
            'The Go sidecar response stream failed.',
            cause: error,
          );
    _markUnavailable(transportError, stackTrace ?? StackTrace.current);
    _process.kill();
  }

  void _handleLogStreamError(Object error, [StackTrace? stackTrace]) {
    _emitLog('The Go sidecar log stream failed: $error');
  }

  void _handleExit(int exitCode) {
    _processExited = true;
    if (_state != _SidecarState.running) return;
    _markUnavailable(SidecarExitedException(exitCode), StackTrace.current);
  }

  void _markUnavailable(Object error, StackTrace stackTrace) {
    if (_state != _SidecarState.running) return;
    _state = _SidecarState.exited;
    _terminalError = error;
    _failPending(error, stackTrace);
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
      _onLog?.call(line);
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
