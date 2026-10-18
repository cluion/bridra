import 'dart:async';
import 'dart:convert';

import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

const fast = RpcSubscriptionPolicy(
  idleTimeout: Duration(milliseconds: 100),
  initialReconnectDelay: Duration(milliseconds: 2),
  maxReconnectDelay: Duration(milliseconds: 8),
  maxConsecutiveReconnects: 2,
);

RpcStreamData<RpcReply> frame(Object? envelope) => RpcStreamData(
  sequence: 1,
  meta: const {},
  value: RpcReply(result: envelope, meta: const {}),
);

RpcStreamData<RpcReply> data(String cursor, int value) =>
    frame({'kind': 'data', 'cursor': cursor, 'value': value});

RpcStreamData<RpcReply> heartbeat([String cursor = '']) =>
    frame({'kind': 'heartbeat', 'cursor': cursor});

int decodeInt(Object? value) => value as int;

void main() {
  test(
    'repeated disposal releases each connection without closing the shared client',
    () async {
      var active = 0;
      var closed = 0;
      final client = ScriptClient((attempt, params, token, timeout) async* {
        active++;
        try {
          for (var i = 1; i <= 10000; i++) {
            yield data('$i', i);
          }
        } finally {
          active--;
          closed++;
        }
      });
      for (var i = 0; i < 30; i++) {
        expect(
          (await client
                  .subscribe('logs.watch', decode: decodeInt)
                  .take(1)
                  .single)
              .value,
          1,
        );
        expect(active, 0);
      }
      expect(closed, 30);
      expect(client.tokens.every((t) => t.isCancelled), isTrue);
    },
  );

  test(
    'resumes from the delivered cursor and suppresses the replay boundary',
    () async {
      final client = ScriptClient((attempt, params, token, timeout) async* {
        if (attempt == 1) {
          expect(params, {'scope': 'logs', 'afterCursor': ''});
          yield data('epoch:1', 1);
          throw const BackendTransportException('disconnected');
        }
        expect(params, {'scope': 'logs', 'afterCursor': 'epoch:1'});
        yield data('epoch:1', 1);
        yield data('epoch:2', 2);
      });
      final events = await client
          .subscribe(
            'logs.watch',
            decode: decodeInt,
            params: {'scope': 'logs'},
            policy: fast,
          )
          .toList();
      expect(events.map((e) => e.value), [1, 2]);
      expect(events.map((e) => e.cursor), ['epoch:1', 'epoch:2']);
      expect(client.calls, 2);
      expect(client.tokens.every((t) => t.isCancelled), isTrue);
    },
  );

  test(
    'supports an application cursor name and an explicit checkpoint',
    () async {
      final client = ScriptClient((attempt, params, token, timeout) async* {
        expect(params, {'afterSequence': 'checkpoint'});
        yield data('next', 7);
      });
      expect(
        (await client
                .subscribe(
                  'logs.watch',
                  decode: decodeInt,
                  cursorParameter: 'afterSequence',
                  afterCursor: 'checkpoint',
                )
                .single)
            .value,
        7,
      );
    },
  );

  test(
    'rotates finite healthy connections without a logical lifetime limit',
    () async {
      final client = ScriptClient((attempt, params, token, timeout) async* {
        expect(timeout, const Duration(milliseconds: 40));
        if (attempt == 4) {
          yield data('last', 9);
          return;
        }
        final watch = Stopwatch()..start();
        while (watch.elapsed < timeout) {
          yield heartbeat();
          await Future<void>.delayed(const Duration(milliseconds: 5));
        }
        throw TimeoutException('finite connection', timeout);
      });
      const policy = RpcSubscriptionPolicy(
        connectionTimeout: Duration(milliseconds: 40),
        idleTimeout: Duration(milliseconds: 100),
        maxConsecutiveReconnects: 0,
      );
      expect(
        (await client
                .subscribe('logs.watch', decode: decodeInt, policy: policy)
                .single)
            .value,
        9,
      );
      expect(client.calls, 4);
    },
  );

  test(
    'idle peers exhaust the retry budget and release every attempt',
    () async {
      var active = 0;
      final client = ScriptClient((attempt, params, token, timeout) {
        active++;
        final controller = StreamController<RpcStreamEvent<RpcReply>>();
        final cancel = token.onCancel.listen((_) {
          active--;
          unawaited(controller.close());
        });
        controller.onCancel = cancel.cancel;
        return controller.stream;
      });
      const policy = RpcSubscriptionPolicy(
        idleTimeout: Duration(milliseconds: 20),
        initialReconnectDelay: Duration(milliseconds: 1),
        maxReconnectDelay: Duration(milliseconds: 2),
        maxConsecutiveReconnects: 2,
      );
      await expectLater(
        client
            .subscribe('logs.watch', decode: decodeInt, policy: policy)
            .toList(),
        throwsA(isA<TimeoutException>()),
      );
      expect(client.calls, 3);
      expect(active, 0);
    },
  );

  test('a heartbeat does not reset a flapping connection budget', () async {
    final client = ScriptClient((attempt, params, token, timeout) async* {
      yield heartbeat();
      throw const BackendTransportException('flap');
    });
    await expectLater(
      client.subscribe('logs.watch', decode: decodeInt, policy: fast).toList(),
      throwsA(isA<BackendTransportException>()),
    );
    expect(client.calls, 3);
  });

  test('cancels a pending read without waiting for another event', () async {
    final started = Completer<void>();
    final client = waitingClient(started);
    final subscription = client
        .subscribe('logs.watch', decode: decodeInt)
        .listen((_) {});
    await started.future;
    await subscription.cancel().timeout(const Duration(seconds: 1));
    expect(client.calls, 1);
    expect(client.tokens.single.isCancelled, isTrue);
  });

  test(
    'external cancellation reports cancellation and stops a pending read',
    () async {
      final started = Completer<void>();
      final client = waitingClient(started);
      final token = RpcCancellationToken();
      final result = expectLater(
        client
            .subscribe(
              'logs.watch',
              decode: decodeInt,
              cancellationToken: token,
            )
            .toList(),
        throwsA(isA<RpcCancelledException>()),
      );
      await started.future;
      token.cancel();
      await result;
      expect(client.tokens.single.isCancelled, isTrue);
    },
  );

  test('pre-cancelled subscriptions never start a request', () async {
    final client = ScriptClient((a, p, t, d) => const Stream.empty());
    await expectLater(
      client
          .subscribe(
            'logs.watch',
            decode: decodeInt,
            cancellationToken: RpcCancellationToken()..cancel(),
          )
          .toList(),
      throwsA(isA<RpcCancelledException>()),
    );
    expect(client.calls, 0);
  });

  test('cancellation interrupts reconnect backoff', () async {
    final failed = Completer<void>();
    final client = ScriptClient((a, p, t, d) async* {
      failed.complete();
      throw const BackendTransportException('offline');
    });
    const policy = RpcSubscriptionPolicy(
      initialReconnectDelay: Duration(seconds: 5),
      maxReconnectDelay: Duration(seconds: 5),
    );
    final subscription = client
        .subscribe('logs.watch', decode: decodeInt, policy: policy)
        .listen((_) {});
    await failed.future;
    await Future<void>.delayed(const Duration(milliseconds: 10));
    await subscription.cancel().timeout(const Duration(seconds: 1));
    expect(client.calls, 1);
  });

  test(
    'optional total lifetime also terminates healthy idle subscriptions',
    () async {
      final started = Completer<void>();
      final client = waitingClient(started);
      const policy = RpcSubscriptionPolicy(
        totalTimeout: Duration(milliseconds: 30),
      );
      await expectLater(
        client
            .subscribe('logs.watch', decode: decodeInt, policy: policy)
            .toList(),
        throwsA(isA<TimeoutException>()),
      );
      expect(client.calls, 1);
      expect(client.tokens.single.isCancelled, isTrue);
    },
  );

  test(
    'a paused consumer does not pull an unbounded queue or trip the idle timer',
    () async {
      var produced = 0;
      final client = ScriptClient((a, p, t, d) async* {
        for (var i = 1; i < 1000; i++) {
          produced++;
          yield data('$i', i);
        }
      });
      final first = Completer<void>();
      final done = Completer<void>();
      final received = <int>[];
      late StreamSubscription<RpcSubscriptionData<int>> subscription;
      const policy = RpcSubscriptionPolicy(
        idleTimeout: Duration(milliseconds: 20),
      );
      subscription = client
          .subscribe('logs.watch', decode: decodeInt, policy: policy)
          .listen((event) {
            received.add(event.value);
            subscription.pause();
            if (!first.isCompleted) {
              first.complete();
            } else {
              done.complete();
            }
          }, onError: done.completeError);
      await first.future;
      await Future<void>.delayed(const Duration(milliseconds: 60));
      expect(produced, lessThanOrEqualTo(2));
      expect(client.calls, 1);
      subscription.resume();
      await done.future;
      expect(received, [1, 2]);
      await subscription.cancel();
    },
  );

  for (final error in <Object>[
    const RpcException('subscription_cursor_expired', 'resync'),
    const RpcException('unauthenticated', 'login'),
    const BackendProtocolException('bad sequence'),
    const BackendClosedException(),
    const RpcHttpStatusException(401),
    const RpcHttpStatusException(403),
  ]) {
    test('does not retry terminal $error', () async {
      final client = ScriptClient((a, p, t, d) => Stream.error(error));
      await expectLater(
        client
            .subscribe('logs.watch', decode: decodeInt, policy: fast)
            .toList(),
        throwsA(same(error)),
      );
      expect(client.calls, 1);
    });
  }

  for (final envelope in <Object?>[
    null,
    {'kind': 'data', 'cursor': '', 'value': 1},
    {'kind': 'data', 'cursor': '1'},
    {'kind': 'unknown', 'cursor': '1'},
    {'kind': 'heartbeat', 'cursor': 'advanced'},
    {'kind': 'heartbeat', 'cursor': '', 'value': 1},
    {'kind': 'data', 'cursor': 1, 'value': 1},
    {'kind': 'data', 'cursor': '1', 'value': 'invalid type'},
  ]) {
    test(
      'rejects malformed envelopes without reconnecting: $envelope',
      () async {
        final client = ScriptClient(
          (a, p, t, d) => Stream.value(frame(envelope)),
        );
        await expectLater(
          client
              .subscribe('logs.watch', decode: decodeInt, policy: fast)
              .toList(),
          throwsA(isA<BackendProtocolException>()),
        );
        expect(client.calls, 1);
      },
    );
  }

  test('honors Retry-After rather than shortening a server cooldown', () async {
    final elapsed = Stopwatch()..start();
    final client = ScriptClient((attempt, p, t, d) async* {
      if (attempt == 1) {
        throw const RpcRateLimitedException(
          retryAfter: Duration(milliseconds: 40),
        );
      }
      expect(elapsed.elapsedMilliseconds, greaterThanOrEqualTo(35));
      yield data('1', 1);
    });
    expect(
      (await client
              .subscribe('logs.watch', decode: decodeInt, policy: fast)
              .single)
          .value,
      1,
    );
  });

  test('HTTP interruption resumes through real stream parsing', () async {
    final transport = SubscriptionHttpClient();
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://example.test/rpc'),
      token: 'token',
      client: transport,
    );
    addTearDown(client.close);
    final events = await client
        .subscribe('logs.watch', decode: decodeInt, policy: fast)
        .toList();
    expect(events.map((e) => e.value), [1, 2]);
    expect(transport.cursors, ['', '1']);
  });

  test('HTTP abort before headers preserves the heartbeat timeout', () async {
    final transport = PendingHeadersClient();
    final client = HttpRpcClient(
      endpoint: Uri.parse('https://example.test/rpc'),
      token: 'token',
      client: transport,
    );
    addTearDown(client.close);
    const policy = RpcSubscriptionPolicy(
      idleTimeout: Duration(milliseconds: 20),
      maxConsecutiveReconnects: 0,
    );
    await expectLater(
      client
          .subscribe('logs.watch', decode: decodeInt, policy: policy)
          .toList(),
      throwsA(isA<TimeoutException>()),
    );
    expect(transport.aborted, isTrue);
  });

  test(
    'HTTP authorization failure is terminal through real status mapping',
    () async {
      final transport = SubscriptionHttpClient(status: 401);
      final client = HttpRpcClient(
        endpoint: Uri.parse('https://example.test/rpc'),
        token: 'token',
        client: transport,
      );
      addTearDown(client.close);
      await expectLater(
        client
            .subscribe('logs.watch', decode: decodeInt, policy: fast)
            .toList(),
        throwsA(isA<RpcHttpStatusException>()),
      );
      expect(transport.cursors.length, 1);
    },
  );

  test('rejects invalid policies and cursor collisions before connecting', () {
    final client = ScriptClient((a, p, t, d) => const Stream.empty());
    for (final policy in [
      const RpcSubscriptionPolicy(idleTimeout: Duration.zero),
      const RpcSubscriptionPolicy(totalTimeout: Duration.zero),
      const RpcSubscriptionPolicy(maxConsecutiveReconnects: -1),
      const RpcSubscriptionPolicy(maxReconnectDelay: Duration.zero),
    ]) {
      expect(
        () => client.subscribe('logs.watch', decode: decodeInt, policy: policy),
        throwsArgumentError,
      );
    }
    expect(
      () => client.subscribe(
        'logs.watch',
        decode: decodeInt,
        params: {'afterCursor': 'hidden'},
      ),
      throwsArgumentError,
    );
    expect(
      () => client.subscribe(
        'logs.watch',
        decode: decodeInt,
        afterCursor: '漢' * 1400,
      ),
      throwsArgumentError,
    );
    expect(client.calls, 0);
  });
}

typedef Script =
    Stream<RpcStreamEvent<RpcReply>> Function(
      int attempt,
      Map<String, Object?> params,
      RpcCancellationToken token,
      Duration timeout,
    );

class ScriptClient implements RpcClient {
  ScriptClient(this.script);
  final Script script;
  var calls = 0;
  final tokens = <RpcCancellationToken>[];
  @override
  Stream<RpcStreamEvent<RpcReply>> stream(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(minutes: 5),
    RpcCancellationToken? cancellationToken,
  }) {
    tokens.add(cancellationToken!);
    return script(++calls, params, cancellationToken, timeout);
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

ScriptClient waitingClient(Completer<void> started) =>
    ScriptClient((a, p, token, d) {
      final controller = StreamController<RpcStreamEvent<RpcReply>>();
      final cancellation = token.onCancel.listen(
        (_) => unawaited(controller.close()),
      );
      controller.onCancel = cancellation.cancel;
      started.complete();
      return controller.stream;
    });

class SubscriptionHttpClient extends http.BaseClient {
  SubscriptionHttpClient({this.status = 200});
  final int status;
  final cursors = <String>[];
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final body = jsonDecode(await request.finalize().bytesToString()) as Map;
    cursors.add((body['params'] as Map)['afterCursor'] as String);
    final attempt = cursors.length;
    final frames = [
      {
        'id': body['id'],
        'result': {'kind': 'data', 'cursor': '$attempt', 'value': attempt},
        'stream': {'sequence': 1, 'kind': 'data'},
      },
      if (attempt > 1)
        {
          'id': body['id'],
          'result': null,
          'stream': {'sequence': 2, 'kind': 'complete'},
        },
    ];
    return http.StreamedResponse(
      Stream.value(utf8.encode('${frames.map(jsonEncode).join('\n')}\n')),
      status,
    );
  }
}

class PendingHeadersClient extends http.BaseClient {
  var aborted = false;
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    await (request as http.AbortableRequest).abortTrigger;
    aborted = true;
    throw http.RequestAbortedException(request.url);
  }
}
