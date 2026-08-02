# Runtime stress verification

Bridra keeps long-running Runtime checks separate from `make verify`. The normal
gate stays fast enough for every Pull Request, while `make runtime-stress`
repeatedly exercises concurrency, lifecycle, persistence, and crash recovery.

## What it covers

- Native Go fuzzing feeds malformed and valid input to both the Sidecar line
  protocol and HTTP RPC handler. Every response must remain valid JSON and
  credentials must not leak. It uses Go's built-in fuzzing engine so seed cases
  remain ordinary regression tests even when active mutation is disabled; see
  the [official Go fuzzing documentation](https://go.dev/doc/security/fuzz/).
- Race-enabled Queue and Scheduler tests repeatedly start, process concurrent
  work, drain, and stop.
- Existing Sidecar concurrency, cancellation, backpressure, and real
  parent-death tests run repeatedly.
- SQLite always runs; PostgreSQL and Redis lifecycle and 24-contender tests run
  when their integration-test environment variables are configured.
- The Flutter Sidecar client repeatedly crashes fake processes, verifies that
  in-flight calls are not replayed, performs replacement health checks, and
  resumes new calls.

## Run it

```bash
make runtime-stress
```

The defaults run each fuzz target for 15 seconds, use 50 Queue, Scheduler, and
Sidecar cycles, and repeat the existing concurrency and persistence tests five
times. Tune a local or CI run without editing tracked files:

```bash
make runtime-stress \
  RUNTIME_FUZZ_TIME=1m \
  RUNTIME_STRESS_CYCLES=200 \
  RUNTIME_STRESS_REPEATS=20
```

Run only the fuzz targets with:

```bash
make runtime-fuzz RUNTIME_FUZZ_TIME=30s
```

## CI policy

`.github/workflows/runtime-stress.yml` runs every Monday at 03:37 UTC and can
also be started manually with explicit fuzz time, cycle, and repeat inputs. CI
provides PostgreSQL and Redis, so the shared-store contention checks do not
skip there. A failing Go fuzz corpus is retained as a workflow artifact for
reproduction.

Scheduled workflows use the latest commit on the default branch and can be
delayed by GitHub Actions load. A scheduled green run complements the required
Pull Request gate; it does not replace `make verify`, code review, or release
validation. These semantics come from GitHub's
[workflow event documentation](https://docs.github.com/en/enterprise-cloud@latest/actions/reference/workflows-and-actions/events-that-trigger-workflows).

## Limits

This suite is deterministic repetition and mutation testing, not a production
load generator. It does not establish a throughput SLA, detect every memory
leak, emulate weak networks, or replace physical Android/iOS and distribution
testing. Production-specific capacity, endurance, profiling, and alerting
remain application and operator responsibilities.
