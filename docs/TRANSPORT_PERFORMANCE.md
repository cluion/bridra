# Transport performance evaluation

Updated: 2026-08-09

## Decision

Bridra keeps newline-delimited JSON as the RPC control plane and managed files
as the binary bulk-data path.

- Do not add shared memory now.
- Do not change the Sidecar wire protocol to binary framing without a measured
  application workload that fails an explicit latency or throughput target.
- If that workload appears, prototype length-prefixed binary frames before
  considering shared memory.
- The Bridra 0.12.0 revalidation remains within the 0.6.1 microbenchmark
  baseline. Continue measuring real application workloads rather than replacing
  the transport speculatively.

This is a deferral with measurable reconsideration gates, not a claim that
binary framing can never help.

## Current transport boundary

Bridra uses:

- JSON requests, unary replies, and streaming events for typed RPC metadata;
- a 4 MiB maximum request line;
- acknowledged Sidecar streaming and socket backpressure for HTTP streaming;
- short-lived managed file references for bulk bytes;
- resumable HTTP ranges/offsets and verified local files for Desktop;
- size limits, SHA-256 verification, expiry, cancellation, and bounded retry.

The managed file path already prevents large payloads from expanding inside
JSON or accumulating in an unbounded stream queue.

## Reproducible benchmark

Run:

```bash
make transport-benchmark
```

The benchmark measures three directional costs:

1. `JSONRoundTrip` marshals and unmarshals the current Go response envelope.
2. `LengthPrefixedPipe` sends a four-byte length plus raw bytes through an
   operating-system pipe. It is an optimistic lower bound, not a proposed
   production protocol.
3. `ManagedFileRoundTrip` stages, flushes, and hashes a managed file, then
   reads, hashes, verifies, and deletes it.

The pipe benchmark excludes Dart decoding, authentication, protocol
validation, stream credit accounting, cancellation, and one-time session
buffer allocation. The managed-file benchmark includes `fsync`, so its fixed
cost is intentionally visible. Results are directional microbenchmarks and do
not replace end-to-end measurements from a real Flutter application.

## 2026-08-09 revalidation

Host:

- Apple M4 Pro, macOS arm64
- Go 1.26.5
- Bridra 0.12.0

Median of three 200 ms benchmark samples:

| Payload | JSON round trip | Length-prefixed pipe | Managed file round trip |
| ---: | ---: | ---: | ---: |
| 1 KiB | 0.0046 ms | 0.0013 ms | — |
| 64 KiB | 0.242 ms | 0.0054 ms | 4.80 ms |
| 1 MiB | 3.82 ms | 0.129 ms | 6.08 ms |
| 3 MiB | 11.59 ms | 0.398 ms | 9.51 ms |
| 16 MiB | — | 2.70 ms | 23.38 ms |

The JSON medians remain within 3.1% of the 0.6.1 baseline and the optimistic
pipe medians remain within 5.9%. Managed files improved for 64 KiB through
3 MiB in this short run; the 16 MiB result was about 9.9% slower, where file
synchronization makes host noise visible. There is no framework-level
regression.

At 1 MiB, JSON used a median 2.20 MiB per operation; at 3 MiB it used a median
8.72 MiB. The pipe still allocated no Go memory per operation and managed files
stayed near 34 KiB. This preserves the theoretical binary-frame opportunity but
does not establish a real end-to-end bottleneck.

No current Bridra workload satisfies the reconsideration gates below: no real
application is missing an explicit p95 latency or CPU target, and profiling has
not attributed at least 25% of such a missed budget to serialization or
transport copies. Binary framing and shared memory therefore remain deferred.

## 2026-07-29 baseline

Host:

- Apple M4 Pro, macOS arm64
- Go 1.26.5
- Flutter 3.44.6 pinned by `.fvmrc`
- Bridra 0.6.1

Median of three 200 ms benchmark samples:

| Payload | JSON round trip | Length-prefixed pipe | Managed file round trip |
| ---: | ---: | ---: | ---: |
| 1 KiB | 0.0045 ms | 0.0013 ms | — |
| 64 KiB | 0.238 ms | 0.0051 ms | 5.06 ms |
| 1 MiB | 3.80 ms | 0.122 ms | 6.59 ms |
| 3 MiB | 11.24 ms | 0.383 ms | 9.71 ms |
| 16 MiB | — | 2.59 ms | 21.28 ms |

At 1 MiB, the JSON round trip allocated about 2.6 MiB per operation. At 3 MiB,
it allocated about 7 MiB. The reusable pipe session measured no per-operation
allocation in Go. Managed-file allocation stayed near 34 KiB per operation;
its cost was dominated by file lifecycle, synchronization, and hashing.

## Interpretation

JSON remains appropriate for normal typed requests, replies, progress, and
control messages. Its cost becomes material for repeated MiB-scale values, but
Bridra does not currently expose raw binary values inside JSON.

Managed files have a roughly 5 ms fixed cost on this host and become more
efficient as payload size grows. They retain the properties a raw pipe does
not provide by itself: bounded size, integrity verification, expiry, retry,
resume, and crash cleanup.

The pipe result proves there is room for a future binary data plane. It does
not prove that changing the public protocol would improve a real Flutter
workload by the same ratio.

Shared memory is a later optimization because it adds three distinct native
implementations and lifecycle models:

- Dart must reach native APIs through
  [`dart:ffi`](https://dart.dev/interop/c-interop) and package native code.
- Windows uses file-mapping objects, views, explicit synchronization, access
  control, and handle cleanup as documented by
  [Microsoft](https://learn.microsoft.com/en-us/windows/win32/memory/sharing-files-and-memory).
- Linux can use `memfd_create`, `mmap`, and sealing, including protection
  against peer mutation and truncation described by the
  [Linux man-pages project](https://man7.org/linux/man-pages/man2/memfd_create.2.html).
- macOS uses shared `mmap` regions or platform IPC; sandboxed applications also
  need the relevant App Group and IPC configuration documented by
  [Apple](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.security.application-groups).

Those costs are not justified by the current measured file latency.

## Reconsideration gates

Prototype binary frames only when all of these are true:

1. A real application repeatedly transfers binary values for which managed
   files are too coarse.
2. Cross-platform end-to-end measurements show an explicit p95 latency or CPU
   budget is missed.
3. Profiling attributes at least 25% of the failing budget to serialization or
   transport copies.
4. A prototype improves the failing budget by at least 30% while preserving
   cancellation, bounded backpressure, authentication, and crash recovery.

Consider shared memory only if the binary-frame prototype still misses the
same budget and the application can benefit from reusing mapped regions. Any
implementation must define ownership, permissions, sealing or immutability,
timeouts, peer-crash cleanup, and Windows/macOS/Linux conformance tests before
changing the public protocol.
