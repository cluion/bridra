import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'rpc_client.dart';

/// Application-owned native bridge for an in-process Go Core.
///
/// Implementations must route [call] and [cancel] to the same embedded runtime
/// instance. [close] must wait for that runtime's bounded shutdown.
abstract interface class EmbeddedRpcBridge {
  Future<String> call(String requestJSON);

  Stream<String> stream(String requestJSON);

  Future<void> cancel(String requestID);

  Future<void> close();
}

/// Optional bounded file-transfer surface implemented by an embedded bridge.
///
/// File bytes stay outside the JSON RPC envelope. Downloads use opaque native
/// handles and pull one bounded chunk at a time; uploads expose an offset so a
/// failed channel call can resume without replaying accepted bytes.
abstract interface class EmbeddedFileTransferBridge {
  Future<String> openDownload(String fileID, int offset);

  Future<Uint8List?> readDownload(String handle, int maxBytes);

  Future<void> closeDownload(String handle, {required bool commit});

  Future<String> beginUpload({
    required String name,
    required String mediaType,
    required int size,
    required String sha256,
  });

  Future<String> appendUpload(String fileID, int offset, Uint8List chunk);

  Future<String> uploadStatus(String fileID);
}

class EmbeddedRpcUnsupportedException implements Exception {
  const EmbeddedRpcUnsupportedException(this.operation);

  final String operation;

  @override
  String toString() =>
      'Embedded RPC $operation is not available in this transport.';
}

/// RPC client for an application-owned in-process Go Core.
///
/// Unary calls, pull-backed server streams, and managed file transfer share one
/// application-owned runtime and bounded shutdown lifecycle.
final class EmbeddedRpcClient implements RpcClient {
  factory EmbeddedRpcClient({
    required String token,
    required EmbeddedRpcBridge bridge,
    Duration cancellationTimeout = const Duration(seconds: 1),
  }) {
    if (token.isEmpty) {
      throw ArgumentError.value(token, 'token', 'The token cannot be empty.');
    }
    if (cancellationTimeout <= Duration.zero) {
      throw ArgumentError.value(
        cancellationTimeout,
        'cancellationTimeout',
        'Use a positive cancellation timeout.',
      );
    }
    return EmbeddedRpcClient._(token, bridge, cancellationTimeout);
  }

  EmbeddedRpcClient._(this._token, this._bridge, this._cancellationTimeout);

  final String _token;
  final EmbeddedRpcBridge _bridge;
  final Duration _cancellationTimeout;
  final Map<String, Completer<_EmbeddedAbort>> _pending = {};
  final Map<String, _EmbeddedStreamState> _streams = {};
  final Set<Completer<_EmbeddedAbort>> _fileOperations = {};

  var _nextID = 0;
  var _closed = false;
  Future<void>? _closeFuture;

  @override
  Future<RpcReply> call(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(seconds: 5),
    RpcCancellationToken? cancellationToken,
  }) async {
    if (_closed) throw const BackendClosedException();
    if (cancellationToken?.isCancelled ?? false) {
      throw RpcCancelledException(method);
    }

    final id = '${++_nextID}';
    final abort = Completer<_EmbeddedAbort>();
    _pending[id] = abort;
    final timeoutTimer = Timer(
      timeout,
      () => _abort(
        id,
        TimeoutException('Embedded RPC method $method timed out.', timeout),
      ),
    );
    final cancellationSubscription = cancellationToken?.onCancel.listen(
      (_) => _abort(id, RpcCancelledException(method)),
    );
    final requestJSON = encodeRpcRequest(
      id: id,
      method: method,
      params: params,
      token: _token,
    );

    try {
      final responseJSON = await Future.any([
        _invoke(requestJSON),
        abort.future.then<String>(_completeAbort),
      ]);
      if (abort.isCompleted) {
        await _completeAbort(await abort.future);
      }
      try {
        return decodeRpcReply(jsonDecode(responseJSON), expectedID: id);
      } on RpcException {
        rethrow;
      } on Object catch (error) {
        throw BackendProtocolException(
          'The embedded Go backend returned an invalid response.',
          cause: error,
        );
      }
    } finally {
      timeoutTimer.cancel();
      if (cancellationSubscription != null) {
        unawaited(cancellationSubscription.cancel());
      }
      _pending.remove(id);
    }
  }

  Future<String> _invoke(String requestJSON) async {
    try {
      return await _bridge.call(requestJSON);
    } on BackendConnectionException {
      rethrow;
    } on Object catch (error) {
      throw BackendTransportException(
        'Could not call the embedded Go backend.',
        cause: error,
      );
    }
  }

  Future<void> _cancel(String requestID) async {
    try {
      await _bridge.cancel(requestID).timeout(_cancellationTimeout);
    } on Object catch (error) {
      throw BackendTransportException(
        'Could not cancel the embedded Go backend request.',
        cause: error,
      );
    }
  }

  void _abort(String requestID, Object error, {bool cancelNative = true}) {
    final pending = _pending[requestID];
    if (pending == null || pending.isCompleted) return;
    pending.complete(
      _EmbeddedAbort(
        error,
        StackTrace.current,
        cancellation: cancelNative ? _cancel(requestID) : Future<void>.value(),
      ),
    );
  }

  Future<String> _completeAbort(_EmbeddedAbort reason) async {
    await reason.cancellation;
    Error.throwWithStackTrace(reason.error, reason.stackTrace);
  }

  @override
  Stream<RpcStreamEvent<RpcReply>> stream(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(minutes: 5),
    RpcCancellationToken? cancellationToken,
  }) {
    if (_closed) return Stream.error(const BackendClosedException());
    if (cancellationToken?.isCancelled ?? false) {
      return Stream.error(RpcCancelledException(method));
    }

    final id = '${++_nextID}';
    late final StreamController<RpcStreamEvent<RpcReply>> controller;
    late final _EmbeddedStreamState state;
    controller = StreamController<RpcStreamEvent<RpcReply>>(
      onListen: () {
        if (_closed) {
          controller.addError(const BackendClosedException());
          unawaited(controller.close());
          return;
        }
        state = _EmbeddedStreamState(controller);
        _streams[id] = state;
        unawaited(
          _runStream(id, method, params, timeout, cancellationToken, state),
        );
      },
      onCancel: () async {
        final active = _streams[id];
        if (active != null && !active.completed && !active.abortRequested) {
          await _abortStream(
            id,
            active,
            RpcCancelledException(method),
            StackTrace.current,
            report: false,
          );
        }
      },
    );
    return controller.stream;
  }

  Future<void> _runStream(
    String id,
    String method,
    Map<String, Object?> params,
    Duration timeout,
    RpcCancellationToken? cancellationToken,
    _EmbeddedStreamState state,
  ) async {
    final timeoutTimer = Timer(
      timeout,
      () => unawaited(
        _abortStream(
          id,
          state,
          TimeoutException('Embedded RPC stream $method timed out.', timeout),
          StackTrace.current,
        ),
      ),
    );
    final cancellationSubscription = cancellationToken?.onCancel.listen(
      (_) => unawaited(
        _abortStream(
          id,
          state,
          RpcCancelledException(method),
          StackTrace.current,
        ),
      ),
    );
    final requestJSON = encodeRpcRequest(
      id: id,
      method: method,
      params: params,
      token: _token,
      stream: true,
    );

    try {
      var expectedSequence = 1;
      var receivedCompletion = false;
      await for (final responseJSON in _bridge.stream(requestJSON)) {
        if (state.abortRequested) continue;
        final frame = decodeRpcStreamFrame(
          jsonDecode(responseJSON),
          expectedID: id,
        );
        if (frame.sequence != expectedSequence) {
          throw FormatException(
            'Expected stream sequence $expectedSequence but received '
            '${frame.sequence}.',
          );
        }
        expectedSequence++;
        if (receivedCompletion) {
          throw const FormatException(
            'The embedded Go backend emitted data after stream completion.',
          );
        }
        if (frame.done) {
          receivedCompletion = true;
          continue;
        }
        state.controller.add(frame.event!);
      }
      if (state.abortRequested) return;
      if (!receivedCompletion) {
        throw const FormatException(
          'The embedded Go backend ended without a completion frame.',
        );
      }
      state.completed = true;
      await state.controller.close();
    } on RpcException catch (error, stackTrace) {
      await _failStream(state, error, stackTrace);
    } on BackendConnectionException catch (error, stackTrace) {
      await _failStream(state, error, stackTrace);
    } on FormatException catch (error, stackTrace) {
      await _failStream(
        state,
        BackendProtocolException(
          'The embedded Go backend returned an invalid stream.',
          cause: error,
        ),
        stackTrace,
      );
    } on Object catch (error, stackTrace) {
      if (!state.abortRequested) {
        await _failStream(
          state,
          BackendTransportException(
            'Could not stream from the embedded Go backend.',
            cause: error,
          ),
          stackTrace,
        );
      }
    } finally {
      timeoutTimer.cancel();
      if (cancellationSubscription != null) {
        await cancellationSubscription.cancel();
      }
      if (identical(_streams[id], state)) {
        _streams.remove(id);
      }
    }
  }

  Future<void> _abortStream(
    String id,
    _EmbeddedStreamState state,
    Object error,
    StackTrace stackTrace, {
    bool report = true,
    bool cancelNative = true,
  }) async {
    if (state.completed || state.abortRequested) return;
    state.abortRequested = true;
    if (cancelNative) {
      try {
        await _cancel(id);
      } on Object catch (cancelError, cancelStackTrace) {
        error = cancelError;
        stackTrace = cancelStackTrace;
      }
    }
    if (report && !state.controller.isClosed) {
      state.controller.addError(error, stackTrace);
      unawaited(state.controller.close());
    }
  }

  Future<void> _failStream(
    _EmbeddedStreamState state,
    Object error,
    StackTrace stackTrace,
  ) async {
    if (state.abortRequested || state.controller.isClosed) return;
    state.completed = true;
    state.controller.addError(error, stackTrace);
    await state.controller.close();
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
    if (_closed) throw const BackendClosedException();
    if (cancellationToken?.isCancelled ?? false) {
      throw const RpcCancelledException('file.download');
    }
    if (file.isExpired) throw const RpcFileExpiredException();
    if (_bridge is! EmbeddedFileTransferBridge) {
      throw const EmbeddedRpcUnsupportedException('file download');
    }
    final bridge = _bridge as EmbeddedFileTransferBridge;

    await for (final chunk in verifyRpcFileDownload(
      _downloadWithRetries(
        bridge,
        file,
        timeout: timeout,
        cancellationToken: cancellationToken,
        maxAttempts: maxAttempts,
      ),
      file,
    )) {
      yield chunk;
    }
  }

  Stream<List<int>> _downloadWithRetries(
    EmbeddedFileTransferBridge bridge,
    RpcFileReference file, {
    required Duration timeout,
    required RpcCancellationToken? cancellationToken,
    required int maxAttempts,
  }) async* {
    final abort = Completer<_EmbeddedAbort>();
    _fileOperations.add(abort);
    void requestAbort(Object error) {
      if (abort.isCompleted) return;
      abort.complete(
        _EmbeddedAbort(
          error,
          StackTrace.current,
          cancellation: Future<void>.value(),
        ),
      );
    }

    final timeoutTimer = Timer(
      timeout,
      () => requestAbort(
        TimeoutException('Embedded file download timed out.', timeout),
      ),
    );
    final cancellationSubscription = cancellationToken?.onCancel.listen(
      (_) => requestAbort(const RpcCancelledException('file.download')),
    );
    var received = 0;
    var attempt = 0;
    var firstAttempt = true;
    try {
      while (firstAttempt || received < file.size) {
        firstAttempt = false;
        attempt++;
        String? handle;
        try {
          final activeHandle = await _fileCall(
            bridge.openDownload(file.id, received),
            abort,
          );
          handle = activeHandle;
          while (received < file.size) {
            final chunk = await _fileCall(
              bridge.readDownload(activeHandle, _embeddedFileChunkBytes),
              abort,
            );
            if (chunk == null) break;
            if (chunk.isEmpty) {
              throw const BackendProtocolException(
                'The embedded file download returned an empty chunk.',
              );
            }
            received += chunk.length;
            if (received > file.size) {
              throw BackendProtocolException(
                'File ${file.name} exceeds its declared size.',
              );
            }
            yield chunk;
          }
          if (received == file.size) {
            await _fileCall(
              bridge.closeDownload(activeHandle, commit: true),
              abort,
            );
            handle = null;
            return;
          }
          if (attempt >= maxAttempts) {
            throw BackendTransportException(
              'The embedded file download ended before all bytes were received.',
            );
          }
        } on TimeoutException {
          rethrow;
        } on RpcCancelledException {
          rethrow;
        } on RpcFileUnavailableException {
          rethrow;
        } on BackendProtocolException {
          rethrow;
        } on Object catch (error) {
          if (abort.isCompleted) await _throwFileAbort(abort);
          if (error is BackendConnectionException) rethrow;
          if (attempt >= maxAttempts) {
            throw BackendTransportException(
              'Could not download ${file.name} from the embedded Go backend.',
              cause: error,
            );
          }
        } finally {
          if (handle != null) {
            try {
              await bridge.closeDownload(handle, commit: false);
            } on Object {
              // The next attempt reopens from the verified byte offset. A
              // failed best-effort release must not replace the primary error.
            }
          }
        }
        if (received < file.size && file.isExpired) {
          throw const RpcFileExpiredException();
        }
      }
    } finally {
      timeoutTimer.cancel();
      if (cancellationSubscription != null) {
        unawaited(cancellationSubscription.cancel());
      }
      _fileOperations.remove(abort);
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
    if (_closed) throw const BackendClosedException();
    if (cancellationToken?.isCancelled ?? false) {
      throw const RpcCancelledException('file.upload');
    }
    if (_bridge is! EmbeddedFileTransferBridge) {
      throw const EmbeddedRpcUnsupportedException('file upload');
    }
    final bridge = _bridge as EmbeddedFileTransferBridge;

    final abort = Completer<_EmbeddedAbort>();
    _fileOperations.add(abort);
    void requestAbort(Object error) {
      if (abort.isCompleted) return;
      abort.complete(
        _EmbeddedAbort(
          error,
          StackTrace.current,
          cancellation: Future<void>.value(),
        ),
      );
    }

    final timeoutTimer = Timer(
      timeout,
      () => requestAbort(
        TimeoutException('Embedded file upload timed out.', timeout),
      ),
    );
    final cancellationSubscription = cancellationToken?.onCancel.listen(
      (_) => requestAbort(const RpcCancelledException('file.upload')),
    );
    try {
      var state = _decodeEmbeddedUploadState(
        await _fileCall(
          bridge.beginUpload(
            name: file.name,
            mediaType: file.mediaType,
            size: file.size,
            sha256: file.sha256,
          ),
          abort,
        ),
      );
      _validateEmbeddedUploadState(state, file);
      var attempt = 0;
      while (!state.complete) {
        attempt++;
        final startOffset = state.offset;
        try {
          var supplied = startOffset;
          await for (final chunk in _boundedFileChunks(
            file.openRead(startOffset),
          )) {
            if (supplied + chunk.length > file.size) {
              throw const BackendProtocolException(
                'The upload source exceeds its declared size.',
              );
            }
            final next = _decodeEmbeddedUploadState(
              await _fileCall(
                bridge.appendUpload(state.file.id, supplied, chunk),
                abort,
              ),
            );
            _validateEmbeddedUploadState(next, file);
            if (next.offset != supplied + chunk.length) {
              throw const BackendProtocolException(
                'The embedded file upload returned an invalid offset.',
              );
            }
            supplied = next.offset;
            state = next;
            if (state.complete) break;
          }
          if (!state.complete) {
            throw const BackendProtocolException(
              'The upload source ended before its declared size.',
            );
          }
        } on TimeoutException {
          rethrow;
        } on RpcCancelledException {
          rethrow;
        } on RpcFileUnavailableException {
          rethrow;
        } on BackendProtocolException {
          rethrow;
        } on Object catch (error) {
          if (abort.isCompleted) await _throwFileAbort(abort);
          if (error is BackendConnectionException) rethrow;
          if (attempt >= maxAttempts) {
            throw BackendTransportException(
              'Could not upload ${file.name} to the embedded Go backend.',
              cause: error,
            );
          }
          state = _decodeEmbeddedUploadState(
            await _fileCall(bridge.uploadStatus(state.file.id), abort),
          );
          _validateEmbeddedUploadState(state, file);
        }
      }
      return state.file;
    } finally {
      timeoutTimer.cancel();
      if (cancellationSubscription != null) {
        unawaited(cancellationSubscription.cancel());
      }
      _fileOperations.remove(abort);
    }
  }

  Future<T> _fileCall<T>(Future<T> operation, Completer<_EmbeddedAbort> abort) {
    return Future.any([
      operation,
      abort.future.then<T>((reason) {
        Error.throwWithStackTrace(reason.error, reason.stackTrace);
      }),
    ]);
  }

  Future<Never> _throwFileAbort(Completer<_EmbeddedAbort> abort) async {
    final reason = await abort.future;
    Error.throwWithStackTrace(reason.error, reason.stackTrace);
  }

  @override
  Future<void> close() => _closeFuture ??= _performClose();

  Future<void> _performClose() async {
    if (_closed) return;
    _closed = true;
    for (final entry in _pending.entries.toList(growable: false)) {
      _abort(entry.key, const BackendClosedException(), cancelNative: false);
    }
    if (_streams.isNotEmpty) {
      await Future.wait(
        _streams.entries
            .toList(growable: false)
            .map(
              (entry) => _abortStream(
                entry.key,
                entry.value,
                const BackendClosedException(),
                StackTrace.current,
                cancelNative: false,
              ),
            ),
      );
    }
    for (final abort in _fileOperations.toList(growable: false)) {
      if (!abort.isCompleted) {
        abort.complete(
          _EmbeddedAbort(
            const BackendClosedException(),
            StackTrace.current,
            cancellation: Future<void>.value(),
          ),
        );
      }
    }
    try {
      await _bridge.close();
    } on BackendConnectionException {
      rethrow;
    } on Object catch (error) {
      throw BackendTransportException(
        'Could not close the embedded Go backend.',
        cause: error,
      );
    }
  }
}

const _embeddedFileChunkBytes = 64 * 1024;

Stream<Uint8List> _boundedFileChunks(Stream<List<int>> source) async* {
  await for (final values in source) {
    var offset = 0;
    while (offset < values.length) {
      final end = offset + _embeddedFileChunkBytes < values.length
          ? offset + _embeddedFileChunkBytes
          : values.length;
      yield Uint8List.fromList(values.sublist(offset, end));
      offset = end;
    }
  }
}

final class _EmbeddedUploadState {
  const _EmbeddedUploadState({
    required this.file,
    required this.offset,
    required this.complete,
  });

  final RpcFileReference file;
  final int offset;
  final bool complete;
}

_EmbeddedUploadState _decodeEmbeddedUploadState(String encoded) {
  try {
    final decoded = jsonDecode(encoded);
    if (decoded is! Map) {
      throw const FormatException('Upload status must be an object.');
    }
    final values = Map<String, dynamic>.from(decoded);
    final file = values['file'];
    final offset = values['offset'];
    final complete = values['complete'];
    if (file is! Map || offset is! int || offset < 0 || complete is! bool) {
      throw const FormatException('Upload status fields are invalid.');
    }
    return _EmbeddedUploadState(
      file: RpcFileReference.fromJson(Map<String, dynamic>.from(file)),
      offset: offset,
      complete: complete,
    );
  } on BackendConnectionException {
    rethrow;
  } on Object catch (error) {
    throw BackendProtocolException(
      'The embedded Go backend returned an invalid upload status.',
      cause: error,
    );
  }
}

void _validateEmbeddedUploadState(
  _EmbeddedUploadState state,
  RpcFileUpload expected,
) {
  final file = state.file;
  if (file.name != expected.name ||
      file.mediaType != expected.mediaType ||
      file.size != expected.size ||
      file.sha256 != expected.sha256 ||
      state.offset > expected.size ||
      state.complete != (state.offset == expected.size)) {
    throw const BackendProtocolException(
      'The embedded Go backend returned mismatched upload metadata.',
    );
  }
}

final class _EmbeddedStreamState {
  _EmbeddedStreamState(this.controller);

  final StreamController<RpcStreamEvent<RpcReply>> controller;
  var completed = false;
  var abortRequested = false;
}

final class _EmbeddedAbort {
  const _EmbeddedAbort(
    this.error,
    this.stackTrace, {
    required this.cancellation,
  });

  final Object error;
  final StackTrace stackTrace;
  final Future<void> cancellation;
}
