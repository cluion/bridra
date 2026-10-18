# Simulator preparation timing experiment

The optional `iOS Simulator preboot experiment` compares two preparation timings
without changing required Verify jobs, release packaging or publication guards:

- **A (`early`)** starts the existing iPhone Simulator before Go/Flutter setup.
- **B (`deferred`)** runs the same preparation command after Go/Flutter setup.

Both use `macos-15`, the same immutable tested source, dependency keys and pinned
toolchains. Two samples per variant run on four fresh hosted VMs, with at most
two jobs in parallel. Matrix entry order counterbalances the variants; actual
scheduling order is recorded rather than assumed. No native build products,
test results or Simulator state are shared between jobs.

## Running the trial

After the workflow exists on the default branch, it can be dispatched manually.
Before merging, add `benchmark-ios-preboot` to a same-repository experiment PR
targeting `main`. Pushing a commit alone does not start this extra matrix. The
normal Verify workflow still checks that commit independently. The existing
`benchmark-ios` label belongs to the separate Flutter/XCTest comparison.

The trial reuses the normal warm Go, Flutter SDK and Pub caches without deleting
shared caches. Go must report an exact hit; Flutter SDK and Pub must restore
successfully. Cache eviction or changed keys fail that sample, which is retained
as overhead and excluded from a warm A/B comparison. Record the actual restored
keys from logs; mismatched cache/toolchain/image identities invalidate a pair.
"Warm" describes dependencies only: every VM has a fresh native build and
Simulator. No claim about cold dependency performance follows from this trial.

## Unchanged acceptance and cleanup

Every sample executes the original `make ios-simulator-smoke` HTTP, streaming,
download and resumable upload flow. The original assertions and all ten backend
receipts, launch/attach/first-RPC guards and elapsed-time watchdogs remain in
force. Readiness stays inside the initial twelve-minute smoke boundary. Failure
permits exactly one targeted reset and one twelve-minute retry. Reset and cleanup
use the existing hosted-only ownership helper; no device is created and no
global erase or shutdown is performed. Cleanup runs even after setup failure.

The preparation command has the same discovery and boot limits in both variants.
The only workflow treatment is its position relative to toolchain setup. Required
watchdog tests and unsigned release builds remain in normal Verify; this extra
matrix measures Simulator smoke, not the entire CI or publication workflow.

## Evidence and interpretation

Each job uploads `context.json`, preparation state/worker logs and any failure
diagnostics. Download raw job logs and attempt-scoped job API data. Verify the
recorded head, tested merge/tree, runner image, architecture and Go/Flutter
versions before pairing samples. Require the ten authentic receipts and successful
owned cleanup; a failed first attempt must be reported even if its retry passes.

Compare discovery, Go setup, Flutter setup, readiness, smoke, retry and cleanup
durations. Shell timing preserves command exit status and records readiness and
smoke separately. Also inspect timestamp gaps around `go env` and cache extraction.
Report each sample and its range, rather than only a mean. Record first-attempt
successes, retries and failures separately. Four positive samples provide initial
repeatability evidence, not a flake-rate estimate or proof of reset recovery when
no retry occurred. A timing association does not establish CPU/memory contention.

Use job `started_at` through `completed_at` for summed macOS runner execution,
including setup and post-job work. Report waiting/queue time separately where
available, and retain failed/excluded work as experiment overhead. Runner seconds
are not billed minutes or a price estimate. The experiment's elapsed time is not
the full Verify duration: Windows or another platform may remain the critical
path even when Simulator smoke improves.
