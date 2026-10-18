import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'rpc_client.dart';

class RpcSubscriptionPolicy {
  const RpcSubscriptionPolicy({
    this.connectionTimeout = const Duration(minutes: 5),
    this.idleTimeout = const Duration(seconds: 45),
    this.totalTimeout,
    this.initialReconnectDelay = const Duration(milliseconds: 250),
    this.maxReconnectDelay = const Duration(seconds: 5),
    this.maxConsecutiveReconnects = 5,
    this.stableConnectionDuration = const Duration(seconds: 30),
  });

  /// Each connection is finite; the logical subscription can outlive it.
  final Duration connectionTimeout;
  final Duration idleTimeout;
  final Duration? totalTimeout;
  final Duration initialReconnectDelay;
  final Duration maxReconnectDelay;
  final int maxConsecutiveReconnects;
  final Duration stableConnectionDuration;

  void _validate() {
    if (connectionTimeout <= Duration.zero ||
        idleTimeout <= Duration.zero ||
        (totalTimeout != null && totalTimeout! <= Duration.zero) ||
        initialReconnectDelay <= Duration.zero ||
        maxReconnectDelay < initialReconnectDelay ||
        maxConsecutiveReconnects < 0 ||
        maxConsecutiveReconnects > 100 ||
        stableConnectionDuration <= Duration.zero) {
      throw ArgumentError(
        'Invalid subscription deadlines or reconnect policy.',
      );
    }
  }
}

class RpcSubscriptionData<T> {
  const RpcSubscriptionData({required this.cursor, required this.value});

  final String cursor;
  final T value;
}

extension RpcSubscriptions on RpcClient {
  /// Subscribes to ordinary stream data envelopes {kind,cursor,value}. The
  /// application schema owns the method, cursor parameter, and value decoder.
  /// Resume uses the last delivered cursor, not a durable processing checkpoint.
  Stream<RpcSubscriptionData<T>> subscribe<T>(
    String method, {
    required T Function(Object? value) decode,
    Map<String, Object?> params = const {},
    String cursorParameter = 'afterCursor',
    String afterCursor = '',
    RpcSubscriptionPolicy policy = const RpcSubscriptionPolicy(),
    RpcCancellationToken? cancellationToken,
  }) {
    policy._validate();
    if (utf8.encode(afterCursor).length > 4096) {
      throw ArgumentError('The replay cursor exceeds 4096 bytes.');
    }
    if (cursorParameter.isEmpty || params.containsKey(cursorParameter)) {
      throw ArgumentError(
        'The cursor parameter must be non-empty and reserved.',
      );
    }
    return _Subscription<T>(
      this,
      method,
      decode,
      Map<String, Object?>.unmodifiable(params),
      cursorParameter,
      afterCursor,
      policy,
      cancellationToken,
    ).stream;
  }
}

final class _Subscription<T> {
  _Subscription(
    this.client,
    this.method,
    this.decode,
    this.params,
    this.cursorParameter,
    this.cursor,
    this.policy,
    this.cancellationToken,
  ) {
    output = StreamController<RpcSubscriptionData<T>>(
      sync: true,
      onListen: () => scheduleMicrotask(_run),
      onPause: _pause,
      onResume: _resume,
      onCancel: () {
        _stop();
        return finished.future;
      },
    );
  }

  final RpcClient client;
  final String method;
  final T Function(Object?) decode;
  final Map<String, Object?> params;
  final String cursorParameter;
  final RpcSubscriptionPolicy policy;
  final RpcCancellationToken? cancellationToken;
  late final StreamController<RpcSubscriptionData<T>> output;
  final stopped = Completer<void>();
  final stopEvents = StreamController<void>.broadcast(sync: true);
  final finished = Completer<void>();
  final random = math.Random();
  String cursor;
  RpcCancellationToken? connectionCancellation;
  Completer<void>? resumed;
  Timer? idleTimer;
  void Function()? restartIdle;
  Object? stopError;

  Stream<RpcSubscriptionData<T>> get stream => output.stream;

  void _pause() {
    resumed ??= Completer<void>();
    idleTimer?.cancel();
  }

  void _resume() {
    final waiting = resumed;
    if (waiting == null) return;
    resumed = null;
    waiting.complete();
    if (!stopped.isCompleted) restartIdle?.call();
  }

  void _stop([Object? error]) {
    if (stopped.isCompleted) return;
    stopError = error;
    stopped.complete();
    idleTimer?.cancel();
    stopEvents.add(null);
    connectionCancellation?.cancel();
    _resume();
  }

  Future<R> _untilStopped<R>(Future<R> future) async {
    final interrupt = Completer<R>();
    final listener = stopEvents.stream.listen(
      (_) => interrupt.completeError(const _SubscriptionStopped()),
    );
    if (stopped.isCompleted) {
      interrupt.completeError(const _SubscriptionStopped());
    }
    try {
      return await Future.any([future, interrupt.future]);
    } finally {
      // A never-completing shared Future would retain one callback per event.
      await listener.cancel();
    }
  }

  Future<bool> _moveNext(
    StreamIterator<RpcStreamEvent<RpcReply>> iterator,
    void Function() onIdleTimeout,
  ) async {
    final expired = Completer<bool>();
    void arm() {
      if (stopped.isCompleted || expired.isCompleted) return;
      idleTimer?.cancel();
      idleTimer = Timer(policy.idleTimeout, () {
        onIdleTimeout();
        expired.completeError(
          TimeoutException(
            'Subscription heartbeat timed out.',
            policy.idleTimeout,
          ),
        );
      });
    }

    // A pending read can be paused after it starts. Future.timeout cannot pause
    // its timer, so keep one watchdog which onPause cancels and onResume rearms.
    restartIdle = arm;
    if (resumed == null) arm();
    try {
      return await _untilStopped(
        Future.any([iterator.moveNext(), expired.future]),
      );
    } finally {
      idleTimer?.cancel();
      idleTimer = null;
      restartIdle = null;
    }
  }

  Future<void> _run() async {
    Timer? totalTimer;
    StreamSubscription<void>? cancellation;
    Object? failure;
    StackTrace? failureStack;
    try {
      if (cancellationToken?.isCancelled ?? false) {
        throw RpcCancelledException(method);
      }
      cancellation = cancellationToken?.onCancel.listen(
        (_) => _stop(RpcCancelledException(method)),
      );
      final totalTimeout = policy.totalTimeout;
      if (totalTimeout != null) {
        totalTimer = Timer(
          totalTimeout,
          () => _stop(
            TimeoutException('Subscription $method timed out.', totalTimeout),
          ),
        );
      }
      var failures = 0;
      while (!stopped.isCompleted) {
        final ready = resumed;
        if (ready != null) await _untilStopped(ready.future);
        final token = RpcCancellationToken();
        connectionCancellation = token;
        final lifetime = Stopwatch()..start();
        final initialCursor = cursor;
        var receivedFrame = false;
        var idleExpired = false;
        final iterator = StreamIterator(
          client.stream(
            method,
            params: {...params, cursorParameter: cursor},
            timeout: policy.connectionTimeout,
            cancellationToken: token,
          ),
        );
        Object? connectionError;
        StackTrace? connectionStack;
        try {
          while (!stopped.isCompleted) {
            final ready = resumed;
            if (ready != null) await _untilStopped(ready.future);
            final more = await _moveNext(iterator, () => idleExpired = true);
            if (stopped.isCompleted) throw const _SubscriptionStopped();
            // A completion frame permanently ends the subscription.
            if (!more) return;
            receivedFrame = true;
            _deliver(iterator.current);
          }
        } on _SubscriptionStopped {
          rethrow;
        } on Object catch (error, stack) {
          connectionError = error;
          connectionStack = stack;
        } finally {
          token.cancel();
          try {
            await iterator.cancel();
          } on RpcCancelledException {
            // HTTP can finish an outstanding send with our own cancellation.
            // Preserve the idle/transport failure which initiated cleanup.
          }
          connectionCancellation = null;
        }
        if (stopped.isCompleted) break;
        final error = connectionError!;
        if (!_retryable(error)) {
          Error.throwWithStackTrace(error, connectionStack!);
        }
        final healthyRotation =
            error is TimeoutException &&
            !idleExpired &&
            receivedFrame &&
            lifetime.elapsed >= policy.connectionTimeout;
        if (healthyRotation) {
          failures = 0;
          continue;
        }
        // A heartbeat alone must not reset the budget of a flapping connection.
        if (cursor != initialCursor ||
            (receivedFrame &&
                lifetime.elapsed >= policy.stableConnectionDuration)) {
          failures = 0;
        }
        if (++failures > policy.maxConsecutiveReconnects) {
          Error.throwWithStackTrace(error, connectionStack!);
        }
        var delay = policy.initialReconnectDelay.inMicroseconds;
        for (var i = 1; i < failures; i++) {
          delay = math.min(delay * 2, policy.maxReconnectDelay.inMicroseconds);
        }
        delay = (delay * (0.75 + random.nextDouble() * 0.25)).round();
        if (error is RpcRateLimitedException && error.retryAfter != null) {
          delay = math.max(delay, error.retryAfter!.inMicroseconds);
        }
        final wake = Completer<void>();
        final timer = Timer(Duration(microseconds: delay), wake.complete);
        try {
          await _untilStopped(wake.future);
        } finally {
          timer.cancel();
        }
      }
    } on _SubscriptionStopped {
      // Cancellation wakes pending reads, pauses, and reconnect delays alike.
    } on Object catch (error, stack) {
      failure = error;
      failureStack = stack;
    } finally {
      totalTimer?.cancel();
      await cancellation?.cancel();
      final error = stopError ?? failure;
      if (error != null && output.hasListener) {
        output.addError(error, failureStack);
      }
      _stop();
      await stopEvents.close();
      finished.complete();
      unawaited(output.close());
    }
  }

  void _deliver(RpcStreamEvent<RpcReply> event) {
    if (event is! RpcStreamData<RpcReply> || event.value.result is! Map) {
      throw const BackendProtocolException(
        'A subscription requires data envelopes.',
      );
    }
    final envelope = event.value.result as Map;
    final kind = envelope['kind'];
    final nextCursor = envelope['cursor'];
    if (nextCursor is! String || utf8.encode(nextCursor).length > 4096) {
      throw const BackendProtocolException('Invalid subscription cursor.');
    }
    if (kind == 'heartbeat') {
      if (nextCursor != cursor || envelope.containsKey('value')) {
        throw const BackendProtocolException(
          'A heartbeat cannot advance the replay cursor.',
        );
      }
      return;
    }
    if (kind != 'data' ||
        nextCursor.isEmpty ||
        !envelope.containsKey('value')) {
      throw const BackendProtocolException(
        'Invalid subscription data envelope.',
      );
    }
    // Tolerate repetition of the resume boundary.
    if (nextCursor == cursor) return;
    T value;
    try {
      value = decode(envelope['value']);
    } on Object catch (error) {
      throw BackendProtocolException(
        'The subscription value is invalid.',
        cause: error,
      );
    }
    cursor = nextCursor;
    output.add(RpcSubscriptionData(cursor: cursor, value: value));
  }

  bool _retryable(Object error) {
    if (error is RpcStreamInterruptedException) return true;
    if (error is RpcHttpStatusException) {
      return error.statusCode == 408 || error.statusCode >= 500;
    }
    return error is BackendTransportException ||
        error is RpcRateLimitedException ||
        error is TimeoutException;
  }
}

final class _SubscriptionStopped implements Exception {
  const _SubscriptionStopped();
}
