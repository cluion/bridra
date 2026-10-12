# CI and merge policy

Bridra uses one long-lived `main`, short-lived work branches, squash merges,
and tag-driven releases. Merging does not publish a version. There is no routine
`main` push workflow, release branch, or release-only Pull Request.

## Pull Request checks

`verify.yml` runs for Pull Requests targeting `main`, including updates and
`ready_for_review`, and for merge groups. It checks out GitHub's merge result,
compares its immutable base and head, and selects checks through `tool/ci.py`.
Renames include both the deleted and added paths.

| Changed surface | Required work |
| --- | --- |
| Root Markdown or Markdown anywhere under `docs/` | CI helper regressions and the fixed gate; no application builds |
| `linux/`, `android/`, `web/`, `macos/`, `ios/`, `windows/` | The corresponding build and smoke tests |
| Platform-specific `tool/` scripts | The corresponding platform checks |
| Shared Go/Dart, schema, tests, dependencies, CI/build configuration, or an unknown path | Core verification and every platform |

Shared changes retain `make verify`, coverage floors, Linux smoke, Android
build/emulator, Web build, macOS lifecycle/sandbox/smoke, iOS build/simulator,
and Windows verification/smoke. Platform selection skips unrelated runners;
it does not defer relevant compatibility or integration checks until release.
Draft PRs use the same selection; draft status does not silently bypass checks.

The reusable `checks.yml` contains these jobs. The fixed `ci-gate` in the caller
requires successful planning and every selected job. Failed, cancelled, missing,
or unexpectedly skipped work blocks the gate. A documentation-only skip is
explicit. Do not apply workflow-level `paths` filters to this required workflow.

Only Pull Requests trigger normal work-branch verification. New updates cancel
superseded runs for that PR. Go dependency caches remain enabled. The existing
weekly/manual Runtime Stress workflow is independent; it does not establish
release acceptance for another SHA.

## Required GitHub settings

Apply these settings before merging the CI migration or relying on the absence
of `main` CI:

- Require a Pull Request and the `ci-gate` check from GitHub Actions.
- Require the branch to be up to date with `main`, or enable a merge queue.
- Allow squash merges, require linear history, and disable merge/rebase methods.
- Restrict direct pushes, force pushes, deletions, and protection bypasses.
- Delete completed work branches automatically; set review counts appropriate
  to the active maintainer team.

The workflow supports `merge_group` if a queue is enabled. Queue checks, branch
updates, retries, and PR revisions can add runs. Repository settings are managed
separately from these YAML files; committing them does not enable protection.

GitHub explains the [PR merge result](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#pull_request),
[strict required checks](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches#require-status-checks-before-merging),
and [merge queue events](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue).

## Tag verification and publication

`release.yml` is triggered by `backend/v*.*.*`. When public releases are enabled:

1. Resolve the version, require `VERSION` to match, and prove the exact tag SHA
   is a commit reachable from `main`. A later merge to `main` does not change
   the selected release source.
2. Invoke the shared checks with `full: true` for that SHA. All platform jobs
   must succeed, regardless of the Pull Request's selected scope.
3. Validate final release metadata and the Dart publish dry run. Build CLI
   artifacts twice from the same SHA and commit timestamp, compare every file,
   validate six archives, checksums and SBOM, and run the native Linux CLI.
4. Upload one immutable candidate artifact. Only after all verification succeeds
   does `public-release` request environment approval and gain publication/OIDC
   permissions.
5. Download that exact artifact by ID. Recheck the tag's SHA, manifest identity,
   all file digests and sizes, and the exact nine-file set before attestation
   and publication. Do not rebuild the CLI in the publication job.

The release gate does not search for a successful `main` push run. It verifies
the actual tag source and artifacts itself. Pull Request success alone is not
proof that another SHA or its final packages are valid. The publication job
keeps the existing pub.dev OIDC environment and artifact attestations; public
installation and external-consumer acceptance remain required after publication.

See [RELEASING.md](RELEASING.md) for version preparation, authorization and
post-release checks. The release workflow does not write version commits back.

## Run counts and local validation

Assuming one successful PR run per change, no updates/retries/queue activity,
and one release:

| Work | Pull Requests | Event-triggered workflow runs |
| --- | ---: | ---: |
| Merge one change | 1 | 1 PR run |
| Merge and release one change | 1 | 1 PR run + 1 tag run |
| Merge three changes, then release together | 3 | 3 PR runs + 1 tag run |

Reusable jobs belong to those runs; each run can consume several runners.
Scheduled work is counted separately. These are baseline counts, not promises
that a change will need no revision or additional testing.

For CI changes, run the focused policy tests and workflow lint:

```bash
python3 -m unittest discover -s tool -p ci_test.py -v
actionlint
make release-check
```

Hosted cross-platform execution and protected-environment/OIDC behavior must be
confirmed on GitHub after the reviewed change is pushed. Local workflow lint
does not prove runner or publication behavior. Do not create a public tag just
to test the migration.
