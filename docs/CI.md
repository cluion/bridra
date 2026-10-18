# CI and merge policy

Bridra uses one long-lived `main`, short-lived work branches, squash merges,
and tag-driven releases. Merging does not publish a version. There is no routine
`main` push verification, release branch, or release-only Pull Request.

## Pull Request checks

`verify.yml` runs for Pull Requests targeting `main`, including updates and
`ready_for_review`, and for merge groups. It checks out GitHub's merge result,
compares immutable base/head SHAs and selects work through `tool/ci.py`. Renames
include both the old and new paths. Drafts use the same selection and newer PR
updates cancel superseded runs.

| Changed surface | Required work |
| --- | --- |
| Root Markdown or Markdown under `docs/` | CI helper regressions and the fixed gate |
| Application `lib/`, UI tests/assets, application/CLI/tooling Go, or runtime tests | Core verification with coverage plus representative Linux bundle smoke |
| `lib/ui/window/` | Core plus Linux, macOS and Windows native checks |
| Native platform directories or platform-specific `tool/` scripts | The affected platform; Android/iOS changes include the corresponding real emulator/simulator |
| Platform-specific Go (`_windows`, `_darwin`, `_linux`) | Core plus the corresponding native host |
| Android/iOS integration tests | Core and the corresponding build/emulator or build/simulator |
| Runtime production Go/Dart, RPC schema, SDK/dependencies, build/CI configuration, or unknown paths | Core and every platform, including both mobile runtimes |
| Embedded starter templates | Core plus the selection for the template's corresponding application path |

Ordinary UI/CLI changes therefore do not start mobile simulators or every native
runner. Transport/runtime changes remain conservative before merge. Every release
still validates every target. Some platform integration failures in ordinary
application changes can be discovered at tag verification rather than the PR;
those failures block publication.

The fixed `ci-gate` requires successful planning and all selected work. The
reusable workflow's `check-results` validates every expected job, including
intentional skips. Failures, cancellations, missing results and unexpected skips
block it. The reusable jobs and their gate consume the same authoritative JSON
plan, so a missing forwarded plan fails rather than silently disabling checks.
The full tag fallback does not depend on PR selection and requires every job.
A separate release evidence check may reuse complete PR verification for the
identical source tree; partial PR selection never satisfies that check.
Do not use workflow-level `paths` filters for this required workflow.

## Execution and test reuse

`checks.yml` separates these independently runnable jobs:

- `core-static`: schema/codegen/format/vet/analysis and watchdog contracts through
  `make verify-checks`.
- `core-go`: SQL-store integration and the complete race-enabled Go suite with
  atomic coverage. PostgreSQL and Redis services remain on this runner.
- `core-flutter`: both Flutter coverage suites and Chrome, sharing one backend
  build on this runner.
- `core`: require all three core shards, download their three coverage profiles
  from this run/attempt and enforce all eight original floors with
  `make coverage-report`. Missing profiles or below-floor results block the gate;
  collecting coverage does not rerun tests.
- `linux`: native bundle build and Sidecar smoke.
- `android-web`: selected Android/Web release builds; these short builds share
  one runner to avoid an extra SDK setup for each target.
- `android-emulator`: real HTTP recovery, streaming and resumable transfers.
- `macos-lifecycle`, `macos-bundle`: native lifecycle and signed sandbox handoff
  run separately from the universal bundle build/smoke, with independent VMs and
  build directories. `macos` requires both. XCTest hosts remain serial because
  the application enforces a single instance; parallel hosts can exit before
  connecting to XCTest.
- `ios-watchdog`, `ios-simulator`, `ios-build`: native watchdog contracts, bounded
  real Simulator smoke and the unsigned release build run independently.
  `ios` requires each selected shard, allowing only plan-authorized skips.
- `windows-verify`, `windows-bundle`: native Go vet/tests, SQL-store tests and both
  Flutter suites run separately from bundle build/smoke. `windows` requires both.
  When the authoritative plan also requires `core`, common metadata,
  code generation, formatting and Dart analysis run there once. Windows-only
  plans retain those checks locally. Environment/doctor checks remain native.
- `release-package`: final metadata, Dart publication dry-run, reproducible
  six-target CLI artifacts and native archive identity; release runs only. Its
  implementation is shared through `release-package.yml` by both tag paths.

Each shard has its own checkout and build outputs. The `core`, `macos`, `ios` and `windows`
group gates run even after a dependency fails. Both these gates and the final
`check-results` independently reject missing, failed, cancelled or unexpectedly
skipped shards. Source-equivalent release verification also requires every shard,
not just the group gates.

This graph adds ten jobs to a complete PR plan compared with the serial graph:
six Linux jobs, three macOS jobs and one Windows job, including the group gates.
It adds five Flutter setup/cache restores. Keep the smaller Android/Web builds
together to bound that overhead. Shorter critical paths can still cost more
runner time, and account concurrency limits can increase queueing. Measure wall
time, queueing, retries and per-OS runner time before claiming a five-minute result.

`make verify-coverage` runs the common static checks, race-enabled Go tests with
an atomic coverage profile, SQL integration, both Flutter suites with coverage,
and Chrome exactly once each. External-package public API tests are included in
the complete Go test pass. It does not run `make verify` and then rerun tests with
`make coverage`. Existing developer `make verify`, standalone `make coverage`
and Windows PowerShell entrypoints remain available. CI runs the same Make
components on separate runners; only the final coverage report is collected.

Windows verification runs the complete Go suite once, including public API tests.
`tool/windows.ps1 -Task verify` remains a complete standalone check; only CI's
explicit `-SharedChecksInCore` mode delegates common checks to the selected,
required core job. No Windows tests or native build checks are delegated.
`BRIDRA_WINDOWS_TIMINGS_PATH` opts into JSON-lines timing for checked Go/FVM/gofmt
commands and the whole task, including failed commands. CI appends the timings to
its step summary and uploads `windows-verify-timings-ATTEMPT` and
`windows-bundle-timings-ATTEMPT` even on failure. Command and task totals overlap;
do not sum them together as runner usage.
The Windows verify command also sets `GOFLAGS=-v` to report individual native
test durations and compilation progress. This adds logging without a test filter,
short mode or another test invocation; `(cached)` results remain distinguishable.

Windows x64 setup installs FVM from its
[fixed-version standalone GitHub release](https://github.com/btwld/fvm/releases/tag/4.1.2),
using the committed SHA-256, a bounded archive size and an
exact runtime version check before exporting its path. The archive's license is
retained. This avoids Chocolatey's separate Dart SDK installation; project SDK
installation, Pub resolution, platform precaching and native verification still
run. `tool/ci_fvm.py` is also the source of the pinned FVM version used by the Unix
installer. Download, checksum, extraction or version failures block setup.
Other Windows architectures retain the existing Chocolatey path; the x64 ZIP
layout is not assumed for ARM64's different standalone packaging.

On a Unix SDK cache miss, the hosted-only `seed-flutter` helper clones only the
exact `.fvmrc` release tag from the official Flutter repository, with depth one.
It requires the exact checked-out tag and an executable `bin/flutter`, then
atomically moves the staged source into the FVM versions cache. Existing entries
are retained for FVM's checks; partial clones are never published as cache entries.
The original `fvm install`, SDK initialization, Pub resolution and full platform
precache still run. This avoids downloading Flutter's entire Git history on cold
ephemeral runners; it does not restore compiled application or test results.

SDK and Pub caches are saved only after installation, resolution and platform
precaching succeed; NDK/CMake caches are saved after `sdkmanager` succeeds.
Separate pinned restore/save actions preserve these validated dependencies even
if a later native test fails. A failed setup stage does not save that stage's cache. Gradle
remains a post-build cache. Cache writes do not replace any native assertion or
release gate. Controlled warm measurements require exact cache matches, including
Pub/Gradle, rather than accepting a prefix fallback as fully warm.

On ephemeral GitHub-hosted runners, `tool/ios_ci_prepare.py` starts three
independent prerequisites after pinned Go and Flutter SDK setup: `make macos-check`,
`make backend-server-build`, and strict Simulator discovery/readiness. They run
concurrently in one owned process group. SDK cache extraction and Simulator
initialization are ordered separately for the hosted timing trial: overlapping
them coincided with much slower SDK setup than the same-profile iOS build job.
The measurements do not by themselves establish CPU, memory or I/O as the cause. Native
selection still uses `tool/ios_simulator_prepare.py`: 120-second discovery and
180-second readiness bounds, one existing available iPhone and no device creation.
A borrowed booted device is checked without requesting another boot.

The Simulator timing trial uses the standard `macos-26-intel` image and selects
its installed Xcode 26.4.1 explicitly, with SDK setup completed before Simulator
preparation. Unsigned iOS release and native macOS verification retain `macos-15`
ARM runners. Earlier Intel trials overlapped cold SDK and Simulator startup and
were cancelled without native acceptance; one owned shutdown also timed out.
They are negative samples for that ordering, not successful Intel qualification.
The SDK-first macOS 15 Intel trial also hit the original 120-second discovery
limit. Apple's [Xcode 26.4 notes](https://developer.apple.com/documentation/Xcode-Release-Notes/xcode-26_4-release-notes)
describe a fixed `simdiskimaged` issue that could hang `simctl`; this does not
establish the cause of our earlier timeouts. The modern image/toolchain trial
must independently pass every native assertion and cleanup check. Initial SDK/Go
population must be separated from warm runs. The default-branch cache-warming
matrix includes this Intel runner, so future PRs can restore its SDK dependencies
from main instead of depending only on a previous run of the same PR.
Host CPU/memory and swap snapshots are logged before preparation and after its
join. Cached dependencies do not replace fresh native builds, and hosted timing
is not a latency guarantee.

Each prerequisite has a recorded exit code and monotonic duration; Xcode/backend
commands have 180-second limits and the combined native initialization command has
a 330-second limit. Native command start markers precede captured command output,
so a stalled command can be identified before its output is flushed; elapsed
markers alone never prove successful process teardown. The foreground smoke joins the worker within 360 seconds,
inside its original twelve-minute boundary, and requires every result to pass.
Preparation evidence binds the worker PID/token, checked-out source and selected
Xcode directory. The wrapper verifies the selected UUID and SHA-256 of the actual
executable backend before consuming its prepared prerequisites. Nothing is cached
across jobs and no preparation result replaces transport assertions.

A failed first attempt still permits exactly one targeted reset and twelve-minute
retry. Reset stops the owned preparation group before touching the selected
device; missing selection erases nothing. Retry repeats strict readiness and the
original foreground Xcode/backend checks. An `always()` step stops the preparation
group and shuts down only the owned device, including setup failures. Process
identity is checked before signals; an unverifiable live group blocks device
mutation. A bounded retained group leader allows cleanup to stop descendants even
if a prerequisite command timed out. Cleanup failures block the job.

The helpers reject local/self-hosted runners and state outside `RUNNER_TEMP`.
Tests use mocks and isolated fake native commands, including real owned-group
cancellation tests, without operating on developer Simulators. The host/path
restriction uses GitHub's documented
[`RUNNER_ENVIRONMENT` and `RUNNER_TEMP` variables](https://docs.github.com/en/actions/reference/workflows-and-actions/variables).

The iOS watchdog starts its background attach query only after a Simulator launch
is requested. Slow log queries cannot block the foreground deadline checks.
Total, no-output and post-VM-service attach deadlines use elapsed wall-clock
timestamps, including work between sleeps. The launch barrier, first-RPC guard,
all transport receipts and the existing retry remain required. Cleanup terminates
owned probe/Flutter child processes; failure log collection has a bounded 10s
budget (configurable with `BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_TIMEOUT_SECONDS`)
and retains partial output when that budget expires. Starter projects
use the same watchdog and cleanup behavior.

CI runs `tool/ios_ci_smoke.sh` after readiness and validated device selection.
Its optional preparation-state argument requires the successful source-bound
results instead of executing the same prerequisites again. Without that argument
(retry and the separate preboot experiment), it runs the original foreground
`macos-check` and `backend-server-build` before the unchanged native smoke.
Prerequisite durations come from monotonic worker timers; native smoke retains
its shell timer. Both print `Bridra iOS STAGE seconds: ...` and exit codes.
The Flutter invocation adds `--verbose` to expose tool-reported phases; assertions,
launch barrier and watchdogs are unchanged. Buffered log timestamps cannot be
mistaken for internal build-start times. The optional preboot experiment retains
its prior 20-second optional discovery probe and serial prerequisite control;
results from its earlier implementation are not measurements of the new pipeline.

macOS lifecycle XCTest and the signed Go sandbox fixture run serially on their
own runner, concurrently with the separate release bundle runner. Each has a
workload-specific Go cache fingerprint so a smaller first writer cannot freeze
the other's complete compiler profile. Both use ARM `macos-15`; XCTest retains
one host (`-parallel-testing-enabled NO`). Both shards are required by `macos`,
`check-results` and complete source-equivalent release verification. This adds
one macOS setup and a short Linux group gate; measure that overhead against the
shorter critical path rather than assuming lower runner usage.

## SDK and dependency caches

`.github/actions/setup-flutter` keeps FVM pinned, restores the exact Flutter SDK
by OS/architecture/version, and restores Pub and optional Gradle dependencies by
OS/architecture and dependency hashes. Resolution still runs on cache hits;
caches are an optimization and never stand in for successful checks. SDK cache
misses install normally. Flutter initializes its SDK artifacts before publication,
and its exact SDK/Dart paths are resolved through FVM rather than assuming a
project symlink exists.

Only SDK versions, Pub hosted/git packages and Gradle dependencies are cached.
SDK keys identify complete host profiles, and each writer pre-caches the entire
profile before saving. macOS/iOS therefore cannot save different SDK artifact
sets under the same immutable key. See the optional
[XCTest comparison](IOS_XCTEST_EXPERIMENT.md) for controlled cold/warm measurements.
Credentials, OIDC files, repository checkouts, test outcomes and release candidates
are not dependency-cache entries. Go's existing setup-action cache remains enabled.

Android jobs also cache the exact Flutter-selected NDK and the CMake version in
`tool/ci.py`, under a separate SDK/architecture/tool-version key. Only those two
component directories are included, not every preinstalled NDK. The NDK version
is read from the pinned Flutter SDK; `sdkmanager` installs/verifies both tools on
every run before saving, so a first writer cannot freeze a partial profile.
Its executable is resolved and checked under the configured Android SDK's
`cmdline-tools/latest/bin`, without relying on the runner's default PATH.
Normal release/debug builds and the real Emulator HTTP flow still run. The
existing default-branch warm-up populates this same profile. First population
and later restored measurements must be reported separately.

Windows verification includes `tool/windows.ps1` in its Go cache fingerprint,
separating its complete generated-consumer/cross-compile profile from the bundle
job's smaller compilation workload. The first run for a new profile is cold;
later verification restores the cache written by that same workload rather than
an immutable entry first written by the bundle job. Verification still executes
every original command. Independent generated-consumer name/target subtests run
in parallel, with separate scratch projects or read-only source and Go's normal
compiler-cache coordination; no test case or target is filtered out.

On hosted Windows, dependency caches and build/test scratch use directories under
`RUNNER_TEMP`, avoiding repeated small-file work on the system tool-cache disk.
The environment is exported before Go setup and includes `GOCACHE`, `GOMODCACHE`,
`GOTMPDIR`, `TEMP` and `TMP`. SDK/Pub caches use `bridra-dependencies`; Go and test
scratch use `bridra-work`. Windows' SDK profile is `windows-temp-v1`, with the
same required Windows artifacts. Changed paths also give Pub/Go caches distinct
cache versions, so the first relocated run is cold; measure later restores
separately. Existing cache entries are not deleted. Linux/macOS locations,
toolchain versions, antivirus settings and validation requirements are unchanged.

PR merge-ref caches cannot seed a later main/tag run. The existing weekly/manual
Runtime Stress workflow therefore has a default-branch-only `cache-warm` matrix
for Linux/macOS/Windows. It warms SDK platform artifacts, Pub resolution and the
Linux Gradle wrapper; it does not introduce a main push workflow or a daily full
matrix. Gradle project dependencies are cached when Android jobs resolve them.
The warm-up adds maintenance runner usage, and cold or evicted caches remain
possible. A manually requested non-main run cannot write these default-branch
warm-up entries. See GitHub's [cache access restrictions](https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching#restrictions-for-accessing-a-cache).

The existing scheduled/manual Runtime Stress validation remains independent.
Neither cached dependencies nor a scheduled green run establish release acceptance
for another source SHA.

## Required GitHub settings

- Require a Pull Request and `ci-gate` from GitHub Actions.
- Require an up-to-date base, or enable a merge queue.
- Allow squash merges, require linear history and disable merge/rebase methods.
- Use the PR title for the squash title and a blank squash message (`PR_TITLE`
  and `BLANK`). All commits have one title line and no body; details belong in PRs.
- Restrict direct pushes, force pushes, deletions and protection bypasses.
- Delete completed work branches automatically; set appropriate review counts.

These settings are managed separately from YAML. GitHub documents the
[PR merge result](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#pull_request),
[strict checks](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches#require-status-checks-before-merging)
and [merge queues](https://docs.github.com/en/repositories/configuring-pull-request-merges/managing-a-merge-queue).

## Tag verification and publication

`release.yml` is triggered by `backend/v*.*.*` when public releases are enabled.
It always matches `VERSION`, requires the exact tag SHA to be reachable from
`main`, and creates fresh release artifacts for that SHA. It chooses one path:

| Evidence available for this source | Tag work |
| --- | --- |
| A matching, complete, successful PR verification record | Reuse platform/coverage verification; run exact-tag release packaging |
| Partial, missing, expired, mismatched or unverifiable evidence | Run `checks.yml` with `full: true`, including every platform and packaging |

The fixed PR gate records `verification.json` only after every selected check
passes, and only for a same-repository PR into `main` whose plan includes all
platforms, both real mobile runtimes and core coverage. The record identifies
the tested merge SHA, complete Git tree, base/head SHAs, repository ID, workflow,
run ID and attempt. It is uploaded as `pr-verification-RUN-ATTEMPT` with 14-day
retention. An unavailable optional evidence upload does not invalidate otherwise
successful PR checks; the later tag falls back to full verification.

`tool/release_verification.py` uses read-only GitHub APIs to require all of:

- The tag is exactly the merge/squash commit of one merged PR into `main`, with
  both branches in this repository. Branch names and commit titles are not evidence.
- The latest `verify.yml` run for that PR head is a successful `pull_request` run
  from the expected repository/workflow, created within the last seven days.
  A newer failed or running run is never bypassed in favor of an older success.
- Every required core, platform, emulator/simulator and final gate job in that
  exact attempt has one successful result. Missing/duplicate jobs, unexpected
  skips and incomplete API pages are rejected.
- The run's immutable artifact has the expected name, run/head identity and a
  matching SHA-256 digest. Only its bounded `verification.json` is read; no PR
  artifact is executed or used as a release binary.
- GitHub's commit data independently confirms that the tested merge has the
  recorded base/head parents and its **entire Git tree equals the tag's tree**.
  A successful gate step whose name GitHub evaluates with `github.sha` also binds
  that merge SHA to the actual run, independently of the artifact's claims.
  Squash commits may have different commit SHAs; every tracked file, dependency,
  workflow and script must still be identical. The source checkout must be clean.

The lookup has a 45-second budget with at most 10 seconds per API request.
Unavailable APIs, insufficient permission, deleted repositories/commits or
artifacts, old records, fork PRs, merge-group-only evidence, partial plans and
any other uncertainty select full verification. Existing PR runs without the
new record also fall back; this is expected during rollout. The decision and
matching run/tree identity are retained as a release artifact and step summary.
No workflow-dispatch label or manual boolean can force verification reuse.

Both paths use the same read-only `release-package.yml`: final changelogs/version
checks, Dart publication dry-run, two reproducible builds of all six CLI archives,
checksums/manifest/SBOM comparison and the native Linux CLI identity smoke test.
Artifacts embed the actual tag SHA, not the PR's different merge/head SHA.
The `candidate` gate requires source verification, the selected path's success,
the other path's intentional skip and a valid immutable candidate artifact ID.
Failed, cancelled, missing or unexpected results block publication.

Only then can `public-release` request the protected environment approval and gain
OIDC/publication permissions. It downloads the exact candidate by artifact ID,
rechecks the unchanged tag/SHA, digests, sizes and exact nine-file set, then attests
and publishes those same bytes without rebuilding. Owner authorization, OIDC,
provenance/SBOM attestations and post-release public-registry/external-consumer
acceptance remain required; see [RELEASING.md](RELEASING.md).

This removes a second complete platform matrix when valid PR evidence exists.
The parallel graph above targets shorter warm-cache PR waits, but a five-minute
PR or release remains unmeasured until hosted comparisons pass. Queue time, cold
SDK caches, reproducible packaging and environment approval add time independently.

GitHub documents the [workflow run/attempt APIs](https://docs.github.com/en/rest/actions/workflow-runs),
[job results for an attempt](https://docs.github.com/en/rest/actions/workflow-jobs#list-jobs-for-a-workflow-run-attempt)
and [artifact identity/digests](https://docs.github.com/en/rest/actions/artifacts).
