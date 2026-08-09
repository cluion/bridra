import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:crypto/crypto.dart';

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

enum _SidecarLaunchMode { stdinHandshake, legacyArguments }

const _sidecarLaunchProtocolVersion = 1;
const _sidecarLaunchReadyMessage = 'sidecar: launch ready 1';
const _legacyStdinLaunchFlagError =
    'flag provided but not defined: -token-stdin';
const _sidecarLaunchTimeout = Duration(seconds: 2);
const _maximumSidecarLaunchTokenBytes = 1024;

enum SidecarRuntimeState { running, restarting, closing, closed, failed }

enum SidecarDiagnosticEventType {
  processStarted,
  processExited,
  sessionFailure,
  restartAttempt,
  healthCheckPassed,
  restartFailed,
  restarted,
  restartExhausted,
  closing,
  closed,
}

class SidecarDiagnosticEvent {
  const SidecarDiagnosticEvent({
    required this.timestamp,
    required this.type,
    this.attempt,
    this.exitCode,
    this.errorType,
  });

  final DateTime timestamp;
  final SidecarDiagnosticEventType type;
  final int? attempt;
  final int? exitCode;
  final String? errorType;

  Map<String, Object?> toJson() => {
    'timestamp': timestamp.toUtc().toIso8601String(),
    'type': _diagnosticEventTypeName(type),
    if (attempt != null) 'attempt': attempt,
    if (exitCode != null) 'exitCode': exitCode,
    if (errorType != null) 'errorType': errorType,
  };
}

class SidecarDiagnostics {
  const SidecarDiagnostics({
    required this.state,
    required this.pendingCalls,
    required this.activeStreams,
    required this.processStarts,
    required this.successfulRestarts,
    required this.failedRestartAttempts,
    required this.recentEvents,
    this.lastExitCode,
    this.failureType,
  });

  final SidecarRuntimeState state;
  final int pendingCalls;
  final int activeStreams;
  final int processStarts;
  final int successfulRestarts;
  final int failedRestartAttempts;
  final int? lastExitCode;
  final String? failureType;
  final List<SidecarDiagnosticEvent> recentEvents;

  Map<String, Object?> toJson() => {
    'schemaVersion': 1,
    'state': state.name,
    'pendingCalls': pendingCalls,
    'activeStreams': activeStreams,
    'processStarts': processStarts,
    'successfulRestarts': successfulRestarts,
    'failedRestartAttempts': failedRestartAttempts,
    if (lastExitCode != null) 'lastExitCode': lastExitCode,
    if (failureType != null) 'failureType': failureType,
    'events': recentEvents.map((event) => event.toJson()).toList(),
  };
}

String _diagnosticEventTypeName(SidecarDiagnosticEventType type) =>
    switch (type) {
      SidecarDiagnosticEventType.processStarted => 'process_started',
      SidecarDiagnosticEventType.processExited => 'process_exited',
      SidecarDiagnosticEventType.sessionFailure => 'session_failure',
      SidecarDiagnosticEventType.restartAttempt => 'restart_attempt',
      SidecarDiagnosticEventType.healthCheckPassed => 'health_check_passed',
      SidecarDiagnosticEventType.restartFailed => 'restart_failed',
      SidecarDiagnosticEventType.restarted => 'restarted',
      SidecarDiagnosticEventType.restartExhausted => 'restart_exhausted',
      SidecarDiagnosticEventType.closing => 'closing',
      SidecarDiagnosticEventType.closed => 'closed',
    };

class SidecarClient implements RpcClient {
  SidecarClient._({
    required this._executablePath,
    required this._token,
    required this._processStarter,
    required this._restartPolicy,
    required this._streamWindow,
    this._onLog,
  });

  final String _executablePath;
  final String _token;
  final SidecarProcessStarter _processStarter;
  final SidecarRestartPolicy _restartPolicy;
  final int _streamWindow;
  final void Function(String line)? _onLog;
  final Map<String, _PendingSidecarCall> _pending = {};
  final Map<String, _PendingSidecarStream> _streams = {};
  final List<SidecarDiagnosticEvent> _diagnosticEvents = [];
  final _closingSignal = Completer<void>();
  _SidecarSession? _session;
  Future<void> _writeQueue = Future.value();
  Future<void>? _restartFuture;
  Completer<void>? _restartReady;
  Future<void>? _closeFuture;
  Object? _terminalError;
  var _nextID = 0;
  var _state = _SidecarState.running;
  var _processStarts = 0;
  var _successfulRestarts = 0;
  var _failedRestartAttempts = 0;
  int? _lastExitCode;
  var _launchMode = _SidecarLaunchMode.stdinHandshake;

  static const _maximumDiagnosticEvents = 50;

  static Future<SidecarClient> start({
    required String executablePath,
    required String token,
    void Function(String line)? onLog,
    SidecarProcessStarter? processStarter,
    SidecarRestartPolicy restartPolicy = const SidecarRestartPolicy(),
    int streamWindow = 16,
  }) async {
    restartPolicy._validate();
    if (streamWindow < 1 || streamWindow > 256) {
      throw ArgumentError.value(
        streamWindow,
        'streamWindow',
        'Use a value between 1 and 256.',
      );
    }
    if (token.isEmpty ||
        utf8.encode(token).length > _maximumSidecarLaunchTokenBytes) {
      throw ArgumentError.value(
        token.isEmpty ? token : '[REDACTED]',
        'token',
        'Use a non-empty token no larger than '
            '$_maximumSidecarLaunchTokenBytes UTF-8 bytes.',
      );
    }
    final starter = processStarter ?? _startSystemProcess;
    final client = SidecarClient._(
      executablePath: executablePath,
      token: token,
      processStarter: starter,
      restartPolicy: restartPolicy,
      streamWindow: streamWindow,
      onLog: onLog,
    );
    try {
      await client._startSessionWithCompatibility(replacement: false);
      return client;
    } on Object catch (error, stackTrace) {
      client._terminalError = error;
      client._state = _SidecarState.failed;
      Error.throwWithStackTrace(error, stackTrace);
    }
  }

  SidecarDiagnostics diagnostics() => SidecarDiagnostics(
    state: switch (_state) {
      _SidecarState.running => SidecarRuntimeState.running,
      _SidecarState.restarting => SidecarRuntimeState.restarting,
      _SidecarState.closing => SidecarRuntimeState.closing,
      _SidecarState.closed => SidecarRuntimeState.closed,
      _SidecarState.failed => SidecarRuntimeState.failed,
    },
    pendingCalls: _pending.length,
    activeStreams: _streams.length,
    processStarts: _processStarts,
    successfulRestarts: _successfulRestarts,
    failedRestartAttempts: _failedRestartAttempts,
    lastExitCode: _lastExitCode,
    failureType:
        _state == _SidecarState.failed || _state == _SidecarState.restarting
        ? _terminalError?.runtimeType.toString()
        : null,
    recentEvents: List.unmodifiable(_diagnosticEvents),
  );

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
  Stream<RpcStreamEvent<RpcReply>> stream(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(minutes: 5),
    RpcCancellationToken? cancellationToken,
  }) {
    if (_state == _SidecarState.closing ||
        _state == _SidecarState.closed ||
        _state == _SidecarState.failed) {
      return Stream.error(_terminalError ?? const BackendClosedException());
    }
    if (cancellationToken?.isCancelled ?? false) {
      return Stream.error(RpcCancelledException(method));
    }

    final id = '${++_nextID}';
    final pending = _PendingSidecarStream();
    StreamSubscription<RpcStreamEvent<RpcReply>>? relay;
    late final StreamController<RpcStreamEvent<RpcReply>> output;
    output = StreamController<RpcStreamEvent<RpcReply>>(
      sync: true,
      onListen: () {
        _streams[id] = pending;
        pending.timeout = Timer(
          timeout,
          () => _cancelPendingStream(
            id,
            TimeoutException('Sidecar stream $method timed out.', timeout),
          ),
        );
        pending.cancellationSubscription = cancellationToken?.onCancel.listen(
          (_) => _cancelPendingStream(id, RpcCancelledException(method)),
        );
        relay = pending.controller.stream.listen(
          (event) {
            output.add(event);
            if (_streams[id] == pending) {
              unawaited(_sendStreamAcknowledgement(id, event.sequence));
            }
          },
          onError: output.addError,
          onDone: output.close,
        );
        unawaited(
          _sendRequestWhenReady(
            id,
            encodeRpcRequest(
              id: id,
              method: method,
              params: params,
              token: _token,
              stream: true,
              streamWindow: _streamWindow,
            ),
          ),
        );
      },
      onPause: () => relay?.pause(),
      onResume: () => relay?.resume(),
      onCancel: () async {
        final abandoned = _streams.remove(id);
        if (identical(abandoned, pending)) {
          pending.dispose();
          if (_state == _SidecarState.running) {
            unawaited(_sendCancellation(id));
          }
        }
        await relay?.cancel();
      },
    );
    return output.stream;
  }

  @override
  Stream<List<int>> download(
    RpcFileReference file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
    int maxAttempts = 3,
  }) async* {
    if (maxAttempts < 1) {
      throw ArgumentError.value(
        maxAttempts,
        'maxAttempts',
        'Use at least one attempt.',
      );
    }
    if (_state == _SidecarState.closing ||
        _state == _SidecarState.closed ||
        _state == _SidecarState.failed) {
      throw _terminalError ?? const BackendClosedException();
    }
    if (cancellationToken?.isCancelled ?? false) {
      throw const RpcCancelledException('file.download');
    }
    final localPath = file.localPath;
    if (localPath == null) {
      throw const BackendProtocolException(
        'The Sidecar file reference is missing its managed local path.',
      );
    }
    if (file.isExpired) {
      await _deleteDownloadedFile(localPath);
      throw const RpcFileExpiredException();
    }

    Object? requestedError;
    final timeoutTimer = Timer(timeout, () {
      requestedError = TimeoutException(
        'Sidecar file download timed out.',
        timeout,
      );
    });
    final cancellationSubscription = cancellationToken?.onCancel.listen((_) {
      requestedError = const RpcCancelledException('file.download');
    });
    try {
      final source = _readSidecarFileWithRetries(
        localPath,
        file.size,
        maxAttempts,
        () => requestedError,
      );
      await for (final chunk in verifyRpcFileDownload(source, file)) {
        final error = requestedError;
        if (error != null) throw error;
        yield chunk;
      }
      final error = requestedError;
      if (error != null) throw error;
    } on TimeoutException {
      rethrow;
    } on RpcCancelledException {
      rethrow;
    } on BackendConnectionException {
      rethrow;
    } on FileSystemException catch (error) {
      throw BackendTransportException(
        'Could not read the Sidecar file ${file.name}.',
        cause: error,
      );
    } finally {
      timeoutTimer.cancel();
      if (cancellationSubscription != null) {
        unawaited(cancellationSubscription.cancel());
      }
      await _deleteDownloadedFile(localPath);
    }
  }

  Stream<List<int>> _readSidecarFileWithRetries(
    String path,
    int size,
    int maxAttempts,
    Object? Function() requestedError,
  ) async* {
    var offset = 0;
    var attempt = 0;
    while (offset < size || (size == 0 && attempt == 0)) {
      attempt++;
      try {
        await for (final chunk in File(path).openRead(offset)) {
          final error = requestedError();
          if (error != null) throw error;
          offset += chunk.length;
          yield chunk;
        }
        if (offset < size && attempt >= maxAttempts) {
          throw BackendTransportException(
            'The Sidecar file ended before all bytes were read.',
          );
        }
      } on TimeoutException {
        rethrow;
      } on RpcCancelledException {
        rethrow;
      } on BackendConnectionException {
        rethrow;
      } on FileSystemException catch (error) {
        if (attempt >= maxAttempts) {
          throw BackendTransportException(
            'Could not read the Sidecar file.',
            cause: error,
          );
        }
      }
    }
  }

  @override
  Future<RpcFileReference> upload(
    RpcFileUpload file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
    int maxAttempts = 3,
  }) async {
    if (maxAttempts < 1) {
      throw ArgumentError.value(
        maxAttempts,
        'maxAttempts',
        'Use at least one attempt.',
      );
    }
    if (_state == _SidecarState.closing ||
        _state == _SidecarState.closed ||
        _state == _SidecarState.failed) {
      throw _terminalError ?? const BackendClosedException();
    }
    if (cancellationToken?.isCancelled ?? false) {
      throw const RpcCancelledException('file.upload');
    }

    final stopwatch = Stopwatch()..start();
    final directory = await Directory.systemTemp.createTemp(
      'bridra-sidecar-upload-',
    );
    final staged = File('${directory.path}${Platform.pathSeparator}upload.bin');
    Object? requestedError;
    final timeoutTimer = Timer(timeout, () {
      requestedError = TimeoutException(
        'Sidecar file upload timed out.',
        timeout,
      );
    });
    final cancellationSubscription = cancellationToken?.onCancel.listen((_) {
      requestedError = const RpcCancelledException('file.upload');
    });
    try {
      final digest = _SidecarDigestSink();
      final hashInput = sha256.startChunkedConversion(digest);
      final output = await staged.open(mode: FileMode.write);
      var written = 0;
      try {
        await for (final chunk in file.openRead(0)) {
          final error = requestedError;
          if (error != null) throw error;
          written += chunk.length;
          if (written > file.size) {
            throw BackendProtocolException(
              'File ${file.name} exceeds its declared size.',
            );
          }
          hashInput.add(chunk);
          await output.writeFrom(chunk);
        }
      } finally {
        hashInput.close();
        await output.close();
      }
      if (written != file.size) {
        throw BackendProtocolException(
          'File ${file.name} has $written bytes; expected ${file.size}.',
        );
      }
      if (digest.value?.toString() != file.sha256) {
        throw BackendProtocolException(
          'File ${file.name} failed SHA-256 verification.',
        );
      }

      Object? lastError;
      for (var attempt = 1; attempt <= maxAttempts; attempt++) {
        final error = requestedError;
        if (error != null) throw error;
        final remaining = timeout - stopwatch.elapsed;
        if (remaining <= Duration.zero) {
          throw TimeoutException('Sidecar file upload timed out.', timeout);
        }
        try {
          final reply = await call(
            'rpc.file_upload',
            params: {
              'path': staged.path,
              'name': file.name,
              'mediaType': file.mediaType,
              'size': file.size,
              'sha256': file.sha256,
            },
            timeout: remaining,
            cancellationToken: cancellationToken,
          );
          if (reply.result is! Map) {
            throw const BackendProtocolException(
              'The Sidecar returned an invalid file upload reference.',
            );
          }
          final reference = RpcFileReference.fromJson(
            Map<String, dynamic>.from(reply.result as Map),
          );
          if (reference.name != file.name ||
              reference.mediaType != file.mediaType ||
              reference.size != file.size ||
              reference.sha256 != file.sha256 ||
              reference.localPath != null) {
            throw const BackendProtocolException(
              'The Sidecar file upload reference does not match its source.',
            );
          }
          return reference;
        } on RpcCancelledException {
          rethrow;
        } on TimeoutException {
          rethrow;
        } on BackendProtocolException {
          rethrow;
        } on RpcException {
          rethrow;
        } on BackendConnectionException catch (error) {
          lastError = error;
          if (attempt == maxAttempts) rethrow;
        }
      }
      throw lastError ??
          BackendTransportException(
            'Could not upload ${file.name} to the Sidecar.',
          );
    } finally {
      timeoutTimer.cancel();
      if (cancellationSubscription != null) {
        unawaited(cancellationSubscription.cancel());
      }
      try {
        await directory.delete(recursive: true);
      } on FileSystemException catch (error) {
        _emitLog('sidecar: remove upload staging directory: $error');
      }
    }
  }

  Future<void> _deleteDownloadedFile(String path) async {
    try {
      await File(path).delete();
    } on FileSystemException catch (error) {
      if (await File(path).exists()) {
        _emitLog('sidecar: remove downloaded file: $error');
      }
    }
  }

  @override
  Future<void> close() => _closeFuture ??= _performClose();

  Future<void> _performClose() async {
    if (_state == _SidecarState.closed) return;

    final wasRunning = _state == _SidecarState.running;
    _state = _SidecarState.closing;
    _recordDiagnostic(SidecarDiagnosticEventType.closing);
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
    _recordDiagnostic(SidecarDiagnosticEventType.closed);
  }

  void _attachSession(
    SidecarProcess process, {
    required bool expectsLaunchReady,
  }) {
    _processStarts++;
    _recordDiagnostic(SidecarDiagnosticEventType.processStarted);
    final session = _SidecarSession(
      process,
      expectsLaunchReady: expectsLaunchReady,
    );
    _session = session;
    final launch = session.launchCompleter;
    if (launch != null) {
      unawaited(
        launch.future.then<void>((_) {}, onError: (Object _, StackTrace _) {}),
      );
    }
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
          (line) => _handleLogLine(session, line),
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

  Future<_SidecarSession> _startSessionWithCompatibility({
    required bool replacement,
  }) async {
    try {
      return await _startSession(_launchMode, replacement: replacement);
    } on _LegacySidecarLaunchUnsupported {
      if (_launchMode != _SidecarLaunchMode.stdinHandshake) rethrow;
      _launchMode = _SidecarLaunchMode.legacyArguments;
      _emitLog(
        'The Go sidecar uses the legacy launch protocol; update the '
        'generated backend to keep the launch token out of process arguments.',
      );
      return _startSession(
        _SidecarLaunchMode.legacyArguments,
        replacement: replacement,
      );
    }
  }

  Future<_SidecarSession> _startSession(
    _SidecarLaunchMode mode, {
    required bool replacement,
  }) async {
    SidecarProcess process;
    try {
      process = await _processStarter(
        _executablePath,
        mode == _SidecarLaunchMode.stdinHandshake
            ? const ['--token-stdin']
            : ['--token', _token],
      );
    } on Object catch (error) {
      final details = error.toString().replaceAll(_token, '[REDACTED]');
      throw BackendTransportException(
        replacement
            ? 'Could not start a replacement Go sidecar: $details'
            : 'Could not start the Go sidecar: $details',
      );
    }

    _attachSession(
      process,
      expectsLaunchReady: mode == _SidecarLaunchMode.stdinHandshake,
    );
    final session = _session!;
    if (mode == _SidecarLaunchMode.legacyArguments) return session;

    try {
      process.stdin.writeln(
        jsonEncode({
          'protocolVersion': _sidecarLaunchProtocolVersion,
          'token': _token,
        }),
      );
      await process.stdin.flush();
      await session.launchCompleter!.future.timeout(_sidecarLaunchTimeout);
      return session;
    } on _LegacySidecarLaunchUnsupported {
      await _stopSession(session, graceful: false);
      rethrow;
    } on Object catch (error, stackTrace) {
      await _stopSession(session, graceful: false);
      final safeError = BackendTransportException(
        'The Go sidecar launch handshake failed.',
      );
      Error.throwWithStackTrace(safeError, stackTrace);
    }
  }

  void _handleLogLine(_SidecarSession session, String line) {
    if (!identical(_session, session)) return;
    final launch = session.launchCompleter;
    if (launch != null && !launch.isCompleted) {
      if (line == _sidecarLaunchReadyMessage) {
        session.launchReady = true;
        launch.complete();
        return;
      }
      if (line == _legacyStdinLaunchFlagError) {
        session.legacyLaunchUnsupported = true;
        if (session.launchExitCode == 2) {
          launch.completeError(
            const _LegacySidecarLaunchUnsupported(),
            StackTrace.current,
          );
        }
        return;
      }
    }
    _emitLog(line);
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
      if (!_pending.containsKey(id) && !_streams.containsKey(id)) return;
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
      final pendingStream = _streams.remove(id);
      if (pendingStream != null) {
        pendingStream.completeError(transportError, stackTrace);
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

  void _cancelPendingStream(String id, Object error) {
    final pending = _streams.remove(id);
    if (pending == null) return;
    pending.completeError(error, StackTrace.current);
    if (_state == _SidecarState.running) {
      unawaited(_sendCancellation(id));
    }
  }

  Future<void> _sendStreamAcknowledgement(String id, int sequence) async {
    final session = _session;
    if (_state != _SidecarState.running || session == null) return;

    try {
      await _writeRequest(
        session,
        encodeRpcStreamAcknowledgement(
          id: id,
          sequence: sequence,
          token: _token,
        ),
      );
    } on Object catch (error, stackTrace) {
      if (_state != _SidecarState.running || !identical(_session, session)) {
        return;
      }
      final transportError = error is BackendConnectionException
          ? error
          : BackendTransportException(
              'Could not acknowledge a Go sidecar stream event.',
              cause: error,
            );
      _handleSessionFailure(session, transportError, stackTrace);
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

    final launch = session.launchCompleter;
    if (launch != null && !session.launchReady) {
      if (!launch.isCompleted) {
        launch.completeError(
          const BackendProtocolException(
            'The Go sidecar responded before completing its launch handshake.',
          ),
          StackTrace.current,
        );
      }
      return;
    }

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

      final pendingStream = _streams[id];
      if (pendingStream != null) {
        try {
          final frame = decodeRpcStreamFrame(message, expectedID: id);
          if (frame.sequence != pendingStream.expectedSequence) {
            throw FormatException(
              'Expected stream sequence ${pendingStream.expectedSequence} '
              'but received ${frame.sequence}.',
            );
          }
          pendingStream.expectedSequence++;
          if (frame.done) {
            _streams.remove(id);
            pendingStream.complete();
          } else {
            pendingStream.add(frame.event!);
          }
        } on RpcException catch (error, stackTrace) {
          _streams.remove(id);
          pendingStream.completeError(error, stackTrace);
        }
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
    final launch = session.launchCompleter;
    if (launch != null && !session.launchReady) {
      if (!launch.isCompleted) {
        launch.completeError(transportError, effectiveStackTrace);
      }
      return;
    }
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
    final launch = session.launchCompleter;
    if (launch != null && !session.launchReady) {
      if (!launch.isCompleted) {
        launch.completeError(
          const BackendTransportException(
            'The Go sidecar launch log stream failed.',
          ),
          stackTrace ?? StackTrace.current,
        );
      }
      return;
    }
    _emitLog('The Go sidecar log stream failed: $error');
  }

  void _handleExit(_SidecarSession session, int exitCode) {
    session.exited = true;
    session.launchExitCode = exitCode;
    if (!identical(_session, session)) return;
    _lastExitCode = exitCode;
    _recordDiagnostic(
      SidecarDiagnosticEventType.processExited,
      exitCode: exitCode,
    );
    final launch = session.launchCompleter;
    if (launch != null && !session.launchReady) {
      Timer.run(() {
        if (launch.isCompleted) return;
        launch.completeError(
          session.legacyLaunchUnsupported && exitCode == 2
              ? const _LegacySidecarLaunchUnsupported()
              : SidecarExitedException(exitCode),
          StackTrace.current,
        );
      });
      return;
    }
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

    _recordDiagnostic(SidecarDiagnosticEventType.sessionFailure, error: error);

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
      _recordDiagnostic(
        SidecarDiagnosticEventType.restartAttempt,
        attempt: attempt,
      );
      _emitLog(
        'Restarting the Go sidecar in ${delay.inMilliseconds} ms '
        '(attempt $attempt/${_restartPolicy.maxAttempts}).',
      );
      if (!await _waitForRestartDelay(delay)) return;

      _SidecarSession? session;
      try {
        session = await _startSessionWithCompatibility(replacement: true);
        if (_state != _SidecarState.restarting) {
          await _stopSession(session, graceful: false);
          return;
        }
        if (!identical(_session, session)) {
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
        _successfulRestarts++;
        _recordDiagnostic(
          SidecarDiagnosticEventType.restarted,
          attempt: attempt,
        );
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
        _failedRestartAttempts++;
        _recordDiagnostic(
          SidecarDiagnosticEventType.restartFailed,
          attempt: attempt,
          error: error,
        );
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

  void _finishRestartExhausted(Object error, StackTrace stackTrace) {
    if (_state != _SidecarState.restarting) return;

    final exhausted = SidecarRestartExhaustedException(
      _restartPolicy.maxAttempts,
      cause: error,
    );
    _terminalError = exhausted;
    _state = _SidecarState.failed;
    _recordDiagnostic(
      SidecarDiagnosticEventType.restartExhausted,
      error: error,
    );
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
      _recordDiagnostic(SidecarDiagnosticEventType.healthCheckPassed);
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
    for (final pending in _streams.values) {
      pending.completeError(error, stackTrace);
    }
    _streams.clear();
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

  void _recordDiagnostic(
    SidecarDiagnosticEventType type, {
    int? attempt,
    int? exitCode,
    Object? error,
  }) {
    _diagnosticEvents.add(
      SidecarDiagnosticEvent(
        timestamp: DateTime.now().toUtc(),
        type: type,
        attempt: attempt,
        exitCode: exitCode,
        errorType: error?.runtimeType.toString(),
      ),
    );
    if (_diagnosticEvents.length > _maximumDiagnosticEvents) {
      _diagnosticEvents.removeAt(0);
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
  _SidecarSession(this.process, {required bool expectsLaunchReady})
    : launchCompleter = expectsLaunchReady ? Completer<void>() : null;

  final SidecarProcess process;
  final Completer<void>? launchCompleter;
  late StreamSubscription<String> stdoutSubscription;
  late StreamSubscription<String> stderrSubscription;
  String? healthRequestID;
  Completer<RpcReply>? healthCompleter;
  Future<void>? stopFuture;
  var exited = false;
  var launchReady = false;
  var legacyLaunchUnsupported = false;
  int? launchExitCode;
}

class _LegacySidecarLaunchUnsupported implements Exception {
  const _LegacySidecarLaunchUnsupported();
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

class _PendingSidecarStream {
  final controller = StreamController<RpcStreamEvent<RpcReply>>();
  Timer? timeout;
  StreamSubscription<void>? cancellationSubscription;
  var expectedSequence = 1;
  var _completed = false;

  void add(RpcStreamEvent<RpcReply> event) {
    if (_completed) return;
    controller.add(event);
  }

  void complete() {
    if (_completed) return;
    _completed = true;
    dispose();
    unawaited(controller.close());
  }

  void completeError(Object error, StackTrace stackTrace) {
    if (_completed) return;
    _completed = true;
    dispose();
    controller.addError(error, stackTrace);
    unawaited(controller.close());
  }

  void dispose() {
    timeout?.cancel();
    final subscription = cancellationSubscription;
    if (subscription != null) {
      unawaited(subscription.cancel());
    }
  }
}

class _SidecarDigestSink implements Sink<Digest> {
  Digest? value;

  @override
  void add(Digest data) {
    if (value != null) {
      throw StateError('The SHA-256 digest was emitted more than once.');
    }
    value = data;
  }

  @override
  void close() {}
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
