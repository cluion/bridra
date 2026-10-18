# Long-lived subscriptions

Subscriptions are an opt-in layer over existing server-streaming RPC. Use them
for continuously updated logs or monitoring state. Ordinary generated streaming
methods keep their current five-minute default timeout and data/progress API.
No new transport, stream frame kind, schema keyword or protocol version is needed.

## Contract

Declare an application method with `stream: true`. Its result is a subscription
envelope carried inside a normal RPC `data` frame:

```json
{"kind":"data","cursor":"opaque-application-cursor","value":"new log entry"}
```

An idle heartbeat has the last delivered cursor and no `value` field:

```json
{"kind":"heartbeat","cursor":"opaque-application-cursor"}
```

Cursors are opaque strings of at most 4096 UTF-8 bytes. They are distinct from
the RPC frame sequence, which restarts at one on each connection. Sources must return events in order, strictly after the supplied
cursor, or explicitly reject an unavailable cursor. Never treat an expired cursor
as a request to start at the current end: that would silently lose events.

A minimal schema, suitable for `bridra generate`, is:

```json
{
  "schemaVersion": 1,
  "protocolVersion": 1,
  "methods": [{
    "name": "logs.watch",
    "clientName": "watchLogs",
    "stream": true,
    "params": {
      "goType": "WatchLogsRequest",
      "dartType": "WatchLogsRequest",
      "fields": [{"name":"afterCursor","type":"string","maxLength":4096}]
    },
    "result": {
      "goType": "WatchLogsResponse",
      "dartType": "WatchLogsResult",
      "fields": [
        {"name":"kind","type":"string","enum":["data","heartbeat"]},
        {"name":"cursor","type":"string"},
        {"name":"value","type":"string","nullable":true}
      ]
    }
  }]
}
```

For structured events, replace `value` with an application-owned object/ref and
decode that object in Flutter. Continue generating request/response DTOs from the
schema; do not edit generated files. The subscription helper validates the
envelope in addition to the schema's field types. It does not automatically
convert an ordinary generated stream into a subscription.

## Go producer and retention

Create the source once per application/audience, not once per request:

```go
buffer, err := framework.NewSubscriptionBuffer[string](
    framework.SubscriptionBufferOptions{Capacity: 256, MaxEventBytes: 64 * 1024},
)
if err != nil {
    return err
}
// Publish from the owning service. Call buffer.Close() during its shutdown.
cursor, err := buffer.Publish("monitor started")
```

After authenticating, authorizing and validating the generated request, a
Controller returns:

```go
return framework.ProduceSubscription(ctx, framework.SubscriptionOptions{
    AfterCursor: request.AfterCursor,
    HeartbeatInterval: 15 * time.Second,
}, buffer.Next)
```

`SubscriptionBuffer` snapshots JSON values and keeps at most `Capacity` events,
each limited by `MaxEventBytes`. Defaults are 256 events and 64 KiB per event;
accepted capacity is 1..4096 and event size is 1 byte..4 MiB. Encoded retained
payload is bounded by their product, plus indexing overhead. A slow reader does
not block publishers or accumulate its own replay queue: when it falls beyond
retention, it receives `subscription_cursor_expired` and must resync. `Close`
wakes readers, lets them drain retained events, then returns `io.EOF`.

An empty initial cursor starts with the oldest retained event. `buffer.Cursor()`
returns the current position, including an empty buffer's position, when a
consumer intentionally wants future events only. Each buffer has a random epoch;
a cursor from a prior instance/restart returns `subscription_cursor_expired`.
Malformed or future cursors return `subscription_cursor_invalid`.

This buffer is **in memory**. Its epoch prevents silent loss across restarts; it
does not recover deleted history. For durable replay, supply an application
`SubscriptionNext[T]` callback backed by its own event store instead. It must:

- honor cancellation and deadlines, including idle heartbeat deadlines;
- return the next event strictly after the cursor, with a non-empty new cursor;
- support repeatable reads because a send can fail after a successful read;
- return `subscription_cursor_expired` for unavailable history and `io.EOF` for
  permanent completion.

The producer performs one bounded read/send at a time and creates no background
producer goroutine or unbounded channel. A read deadline after an idle interval
produces a heartbeat. Other errors are preserved as terminal RPC errors.
Cancellation, disconnect and runtime shutdown use the existing request context.

Bind each source and cursor to its authorized audience. Cursors provide replay
positions, not authorization. Recheck access on every new connection; do not use
one buffer to mix events from unrelated users or workspaces.

## Flutter consumer

The additive `RpcSubscriptions` extension works on HTTP, Sidecar and Embedded
`RpcClient` implementations. Existing implementations need no new interface
method. It owns reconnects; pass method-specific non-cursor parameters as usual:

```dart
final cancellation = RpcCancellationToken();
final events = client.subscribe<String>(
  'logs.watch',
  decode: (value) => value as String,
  afterCursor: savedCursor,
  cancellationToken: cancellation,
);

try {
  await for (final event in events) {
    await applyLogEntry(event.value);
    savedCursor = event.cursor;
  }
} on RpcException catch (error) {
  if (error.code == 'subscription_cursor_expired') {
    // Fetch a fresh snapshot and its matching cursor, then create a subscription.
    await reloadSnapshot();
  } else {
    rethrow;
  }
}
// In the owner/view's disposal: cancellation.cancel().
```

The default cursor parameter is `afterCursor`; `cursorParameter` can select a
different schema field. Do not also put that field in `params`. Set `afterCursor`
when reconnecting a newly created subscription after a view or app restart.

The helper resumes from its **last delivered** cursor. It suppresses a repeated
event at that resume boundary; it does not promise exactly-once processing or
durably acknowledge application side effects. Use sequential `await for`, or
pause the stream during asynchronous processing, to retain backpressure. An
asynchronous `listen(onData)` callback is not automatically awaited by Dart.
Persist a checkpoint only after applying the event. On process failure, an
application can receive that event again and must handle it idempotently.

Snapshot and cursor acquisition must be consistent with the application's event
store. Merely reading a buffer cursor before/after an unrelated database query
does not establish that consistency. Once replay expires, fetch both again rather
than silently clearing the cursor and continuing with incomplete state.

## Deadlines, retry and cleanup

`RpcSubscriptionPolicy` defaults are:

| Setting | Default | Behavior |
| --- | --- | --- |
| `connectionTimeout` | 5 minutes | Finite lifetime of one RPC connection |
| `idleTimeout` | 45 seconds | Maximum wait for the next data/heartbeat frame |
| `totalTimeout` | none | Optional total lifetime, including pauses and retries |
| `initialReconnectDelay` | 250 ms | Exponential retry delay, with 75–100% jitter |
| `maxReconnectDelay` | 5 seconds | Ceiling for client-generated backoff |
| `maxConsecutiveReconnects` | 5 | Reconnects allowed before reporting the error |
| `stableConnectionDuration` | 30 seconds | A stable connection can reset failures |

Keep the Go heartbeat interval below Flutter's idle deadline. A logical
subscription has no default lifetime limit: after a healthy connection reaches
its finite timeout, it reopens with the cursor. Heartbeats prevent idle expiry
but do not extend that individual connection's lifetime. These rotations do not
consume the failure budget. A permanent RPC completion ends the subscription.

Transport outages, premature EOF, timeouts, HTTP 408/5xx and rate limits can be
retried. `Retry-After` is honored even when it exceeds the client backoff ceiling.
Receiving real new data, or maintaining a stable connection, resets the failure
budget. One heartbeat followed immediately by a failure does not reset it.
Authentication/authorization failures, explicit client closure, exhausted
Sidecar restart, cursor expiry and malformed data/sequence frames are terminal.
The helper preserves their errors and never silently resets an expired cursor.

Pausing the consumer pauses pulls without triggering its idle watchdog. The
underlying connection/total deadlines still apply. Sidecar retains its bounded
credit window, HTTP retains response-stream backpressure, and Embedded pauses
native frame pulls. A cancellation token, listener cancellation, or total timeout
interrupts pending reads, pauses and reconnect waits and cancels the exact active
request. No subscription API closes the shared client or cancels unrelated calls.

HTTP deployments must also configure streaming/proxy buffering and server
deadlines appropriately. Heartbeats are ordinary flushed NDJSON data, not a
guarantee that an intermediary permits unlimited connections. Keep finite
connection rotation enabled and retain enough history to cover reconnect delays.
