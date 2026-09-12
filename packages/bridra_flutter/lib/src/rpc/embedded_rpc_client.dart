import 'dart:async';
import 'dart:convert';

import 'rpc_client.dart';

/// Application-owned native bridge for an in-process Go Core.
///
/// Implementations must route [call] and [cancel] to the same embedded runtime
/// instance. [close] must wait for that runtime's bounded shutdown.
abstract interface class EmbeddedRpcBridge {
  Future<String> call(String requestJSON);

  Future<void> cancel(String requestID);

  Future<void> close();
}

class EmbeddedRpcUnsupportedException implements Exception {
  const EmbeddedRpcUnsupportedException(this.operation);

  final String operation;

  @override
  String toString() =>
      'Embedded RPC $operation is not available in the unary transport.';
}

/// Unary RPC client for an application-owned in-process Go Core.
///
/// Streaming and out-of-band file transfer remain unavailable until the native
/// bridge has explicit bounded protocols for them.
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
  }) => Stream.error(const EmbeddedRpcUnsupportedException('streaming'));

  @override
  Stream<List<int>> download(
    RpcFileReference file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
    int maxAttempts = 3,
  }) => Stream.error(const EmbeddedRpcUnsupportedException('file download'));

  @override
  Future<RpcFileReference> upload(
    RpcFileUpload file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
    int maxAttempts = 3,
  }) => Future.error(const EmbeddedRpcUnsupportedException('file upload'));

  @override
  Future<void> close() => _closeFuture ??= _performClose();

  Future<void> _performClose() async {
    if (_closed) return;
    _closed = true;
    for (final entry in _pending.entries.toList(growable: false)) {
      _abort(entry.key, const BackendClosedException(), cancelNative: false);
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
