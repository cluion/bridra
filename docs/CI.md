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
Full tag mode does not depend on PR selection and requires every job.
Do not use workflow-level `paths` filters for this required workflow.

## Execution and test reuse

`checks.yml` separates these independently runnable jobs:

- `core`: schema/codegen/format/vet/analysis, SQL-store integration and
  `make verify-coverage`, retaining all eight coverage floors.
- `linux`: native bundle build and Sidecar smoke.
- `android-web`: selected Android/Web release builds; these short builds share
  one runner to avoid an extra SDK setup for each target.
- `android-emulator`: real HTTP recovery, streaming and resumable transfers.
- `macos`: native lifecycle, signed sandbox handoff and bundle smoke.
- `ios`: unsigned release build and, when selected, bounded real simulator smoke
  with the existing single retry and failure diagnostics.
- `windows`: Windows verification and native bundle smoke.
- `release-package`: final metadata, Dart publication dry-run, reproducible
  six-target CLI artifacts and native archive identity; release runs only.

The former long Linux chain and combined macOS/iOS chain run independently.
All selected jobs must pass before the reusable workflow succeeds. More parallel
jobs can use more runner minutes even when wall time falls; keep the smaller
Android/Web builds together and measure both waiting time and runner usage.

`make verify-coverage` runs the common static checks, race-enabled Go tests with
an atomic coverage profile, SQL integration, both Flutter suites with coverage,
and Chrome exactly once each. External-package public API tests are included in
the complete Go test pass. It does not run `make verify` and then rerun tests with
`make coverage`. Existing developer `make verify`, standalone `make coverage`
and Windows PowerShell entrypoints remain available.

## SDK and dependency caches

`.github/actions/setup-flutter` keeps FVM pinned, restores the exact Flutter SDK
by OS/architecture/version, and restores Pub and optional Gradle dependencies by
OS/architecture and dependency hashes. Resolution still runs on cache hits;
caches are an optimization and never stand in for successful checks. SDK cache
misses install normally. Flutter initializes its SDK artifacts before publication,
and its exact SDK/Dart paths are resolved through FVM rather than assuming a
project symlink exists.

Only SDK versions, Pub hosted/git packages and Gradle dependencies are cached.
Credentials, OIDC files, repository checkouts, test outcomes and release candidates
are not dependency-cache entries. Go's existing setup-action cache remains enabled.

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

`release.yml` is triggered by `backend/v*.*.*` when public releases are enabled:

1. Match `VERSION` and require the exact tag SHA to be reachable from `main`.
2. Run the shared checks with `full: true`: every platform, real mobile runtime,
   coverage floor and release-package job is required regardless of PR scope.
3. In parallel with platform testing, validate final changelogs and the Dart
   package dry-run. Build the CLI twice from the same source SHA and timestamp,
   compare the six archives/checksums/manifest/SBOM and run the native Linux CLI.
4. Upload one immutable candidate. Parallel upload alone does not authorize
   publication: all other verification jobs and `check-results` must succeed.
5. Only then request the protected `public-release` environment approval and gain
   OIDC/publication permissions. Download the same artifact by ID, recheck tag/SHA,
   digests, sizes and the exact nine-file set, then attest and publish those bytes.

No CLI rebuild occurs in the publication job. PR success is not substituted for
exact-tag verification. Environment protection, owner authorization, source checks,
OIDC and provenance/SBOM attestations remain required. Post-release public-registry
identity and one fresh external-consumer acceptance remain separate gates; see
[RELEASING.md](RELEASING.md).

## Run counts and local validation

With one successful PR run per change and no retries/queue activity:

| Work | PRs | Event-triggered verification/release runs |
| --- | ---: | ---: |
| Merge one change | 1 | 1 PR run |
| Merge and release one change | 1 | 1 PR run + 1 tag run |
| Merge three changes, then release | 3 | 3 PR runs + 1 tag run |

Reusable jobs consume runners within these runs. Weekly maintenance is separate.
Local work should use the smallest relevant checks. A delivery rehearsal needing
both complete verification and coverage uses `make verify-coverage` once. Do not
repeat full local suites for candidate, squash and tag when the same source tree
is already covered and tag CI will validate the final SHA and package identity.

For CI changes:

```bash
python3 -m unittest discover -s tool -p ci_test.py -v
actionlint
make release-check
```

Changes to the test entrypoint also need one real `make verify-coverage` run.
Hosted cache hits, Windows/mobile runners, protected-environment behavior and
elapsed time require GitHub verification after review/push. Local lint does not
prove them. Do not publish a tag just to test this migration. Initial goals are
5–10 minutes for ordinary PRs and 12–18 minutes for tag runs; these are unproven
measurement targets, not guarantees under cold caches, queueing or retries.
