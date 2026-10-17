# Bridra release process

Bridra uses Semantic Versioning for the framework and an independent integer
version for the RPC protocol. The Go module lives in `backend/`, so release tags
use the `backend/vMAJOR.MINOR.PATCH` form.

Bridra is licensed under MIT. Its canonical public repository is
`https://github.com/cluion/bridra`, its Go module is
`github.com/cluion/bridra/backend`, and `bridra_flutter` is assigned to the
verified `cluion.com` publisher. No tag or package may be published until every
release blocker is resolved and recorded in the reviewed change and release
evidence. The development and merge gates are defined in [CI.md](CI.md).

The tag-triggered workflow is disabled unless the public GitHub repository
variable `BRIDRA_PUBLIC_RELEASE_ENABLED` is exactly `true`. Configure a protected
`public-release` Environment with required owner approval before enabling it.
After the first manual Dart publication and publisher transfer, configure
pub.dev's tag pattern as `backend/v{{version}}` and set
`BRIDRA_PUBDEV_AUTOMATION_ENABLED=true` to let the same workflow publish later
Dart versions.

## Release authority

The roles in [GOVERNANCE.md](../GOVERNANCE.md) apply. A named release manager
prepares and verifies a release, but only a Cluion repository owner may authorize
the first public release, package ownership, license, or canonical identity.

Prepare necessary version data in the last substantive feature/fix Pull Request
planned for the release. Do not create a routine release branch, release-only
commit, or additional release Pull Request. The reviewed PR and release evidence
record the release manager, target version, source SHA, support window, security
status, artifact evidence, package owners, and final owner approval. CI success
alone is not publication authorization.

## Preconditions

- Release from a clean, reviewed commit on the public repository.
- Re-check the current official pub.dev automated-publishing, GitHub Actions,
  and protected-environment instructions before changing or exercising the
  publication workflow.
- Re-check every pinned release action against its official upstream release.
  The current workflow pins `actions/attest` to the reviewed immutable `v4.2.2`
  commit; never infer a new pin from a floating major tag alone.
- Confirm the canonical Git remote, normally `origin`, and the reviewed commit
  belong to `https://github.com/cluion/bridra`.
- Use root [VERSION](../VERSION) as the release intent and keep
  `framework.FrameworkVersion`, `bridra_flutter`'s version, generated dependency
  defaults, changelogs, and the intended tag aligned. General documentation links
  to `VERSION` or uses placeholders; edit it only when its described behavior changes.
- Run `make release-check FINAL=1` and review `bridra upgrade --plan`. Require
  all selected PR checks and the exact-tag release gate to pass. Complete PR
  verification, including coverage floors, may be reused only after the strict
  source/run checks in [CI.md](CI.md); otherwise the tag runs full verification.
  Local changes use focused checks; if a complete local
  rehearsal is needed, run `make verify-coverage` once rather than separate
  verification and coverage passes.
- Confirm `LICENSE`, `backend/LICENSE`, and
  `packages/bridra_flutter/LICENSE` are identical MIT license copies, then
  confirm ownership of `bridra_flutter`.
- Re-test GitHub private vulnerability reporting after the reviewed history is
  published. Review open private advisories and do not release through an
  unresolved embargo.
- Confirm the repository's `SECURITY.md`, `CONTRIBUTING.md`, `GOVERNANCE.md`,
  `SUPPORT.md`, `CODE_OF_CONDUCT.md`, `CHANGELOG.md`, and
  `docs/UPGRADING.md` match the release.
- Confirm the release has an explicit support window and no undocumented
  breaking public API, protocol, project metadata, or template changes.
- If `schema/bridra.json` changed, run `bridra schema check --json` against the
  previous reviewed release schema. Attach the report and require either
  `compatible` or a deliberately coordinated `versioned_break`; never release
  an `incompatible` result.

## Prepare version data in the substantive Pull Request

Use the feature/fix branch that owns the final substantive change planned for
this version. Preparation fixes may be separate commits on that branch; squash
the complete reviewed result into `main`. Do not rewrite shared history or
force a second release PR solely to group version changes.

Choose the release version before this PR merges. If all work has already merged
and the source version is wrong, a reviewed metadata correction is still needed;
never hide it by rewriting `main` or changing files only during publication.

1. Choose the SemVer and decide independently whether the RPC protocol, Project
   Template, or project metadata schema changes.
2. Synchronize the framework, CLI, Go module, Dart package, project metadata,
   and changelogs from one chosen version. Replace `X.Y.Z` below; later commands
   read `VERSION`:

   ```bash
   make release-prepare VERSION=X.Y.Z
   make release-check
   ```

   The prepare command never creates a tag, publishes a package, or creates a
   release. It refuses a downgrade, inconsistent starting state, or a new
   version while the previous changelog entry is still marked `Unreleased`.
   A leading unversioned `Unreleased` section is promoted to the new version,
   preserving its notes and all published changelog sections.
   Preparation updates `VERSION`, the four required Go/Dart/project metadata
   surfaces, and both changelogs; the Make target then refreshes the Flutter
   lockfile. It leaves README, architecture, usage, release, and upgrade documents
   byte-identical. Historical migration examples retain their original versions.
   Update migration guidance only for actual contract changes and support policy
   when the supported minor line changes.
3. Review the changelog, upgrade/rollback guidance, and independently versioned
   protocol, Project Template, and project metadata contracts. Preserve historical
   release references when reviewing the prepared documentation. Register the new
   release and every required transition in the upgrade migration catalog;
   verification rejects a new current version without a complete path from each
   older registered release.
4. Replace `Unreleased` in both changelogs with the intended release date, then
   run `make release-check FINAL=1`. The protected release workflow
   repeats this final check and refuses an unfinished changelog.
5. Run relevant local checks and workflow lint for CI changes. Require the PR's
   `ci-gate` against an up-to-date merge result and record verification limits.
   Complete same-repository PR verification records can later satisfy the tag's
   platform/coverage gate only when the tested merge tree exactly matches the
   tag. Partial plans or unavailable/mismatched evidence trigger full tag
   verification. Dart dry-run, reproducible CLI builds and native archive smoke
   always run against the actual release SHA before publication approval.
6. Obtain maintainer review and repository-owner release authorization.

The Verify workflow runs for Pull Requests targeting `main` and merge groups;
it does not run for ordinary branch pushes or after a squash merge. It selects
platform checks, cancels superseded runs for the same PR, and reports the fixed
`ci-gate`. Require PR protection and a current base before relying on this
policy. After merge, record the actual squash commit SHA for authorization and
tagging. See [CI.md](CI.md) for scope and required repository settings.

### Validation ownership

Local development checks the changed behavior. PR CI checks the reviewed merge
result with coverage and affected platform work. For a complete plan, its fixed
gate also records the exact tested source tree and run identity. The tag workflow
owns the source-equivalence decision, final Dart dry-run and two reproducible CLI
builds. It either reuses verified full PR platform/coverage results or runs the
complete matrix in parallel with packaging. Protected publication waits for the
selected verification path and exact-tag packaging to succeed.

After squash merge, confirm the final SHA and reviewed source tree before tagging.
Do not repeat full local suites and generated-consumer rehearsals for candidate,
squash and tag when only commit identity changed. A changed source tree or failed
gate requires new verification. A partial PR plan cannot replace the release's
full platform evidence. Never reuse another SHA's release artifacts or treat a
PR result as proof of final package identity. Evidence older than seven days,
missing records, unavailable APIs and fork PRs all use the complete tag fallback.
No new release-only PR or `main` push CI is required.

Before environment approval, inspect the release verification decision, its
source/run/tree evidence when reused, successful exact-tag packaging jobs and
immutable artifact ID/manifest. Keep the candidate's SHA, version and digests
in release evidence. A local packaging rehearsal is useful when packaging itself
changed; it is not a routine second full release pipeline.

Windows maintainers use:

```powershell
.\tool\windows.ps1 -Task release-prepare -Version X.Y.Z
.\tool\windows.ps1 -Task release-check
.\tool\windows.ps1 -Task release-check -Final
```

Security fixes under embargo use a private advisory and private fork until the
coordinated release commit is ready. Do not expose the fix in a public Pull
Request before the agreed disclosure time.

## Build CLI artifacts

```bash
make cli-release
```

This produces the following under `build/bridra/cli/<version>/`, using `VERSION`:

- macOS amd64 and arm64 `tar.gz` archives
- Linux amd64 and arm64 `tar.gz` archives
- Windows amd64 and arm64 `zip` archives
- `SHA256SUMS`
- schema-versioned `manifest.json`
- deterministic `bridra_VERSION_cli.spdx.json`

The source commit and commit timestamp are embedded into each binary. The tag
workflow performs the native archive check before requesting publication approval.
For a local packaging change or failed-gate diagnosis, inspect the native archive:

```bash
BRIDRA_VERSION="$(cat VERSION)"
(cd "build/bridra/cli/${BRIDRA_VERSION}" && shasum -a 256 -c SHA256SUMS)
bridra version --json
```

Running the release build twice from the same commit, Go toolchain, version, and
build date must produce identical archives, checksums, manifest, and SBOM. The
SBOM is derived from binary Go build information; a module replacement or
different dependency graph between targets must fail packaging.

The release manager records the source commit, build host/toolchain, two checksum
runs, archive smoke-test result, SBOM digest, and `bridra version --json` output.
GitHub Sigstore attestations establish build provenance and associate the SPDX
SBOM with every archive. Platform executable signing, notarization, installers,
and app-store publication remain product release layers and must not be implied
by these attestations.

## Tag and publish

Create the annotated Go submodule tag only after the substantive Pull Request
is merged and the repository owner authorizes the version and SHA. Replace
`FULL_APPROVED_MAIN_SHA` with that exact commit and read its version, rather than
the version of a possibly different local checkout:

```bash
git fetch origin main
BRIDRA_RELEASE_SHA=FULL_APPROVED_MAIN_SHA
BRIDRA_VERSION="$(git show "${BRIDRA_RELEASE_SHA}:VERSION")" || exit 1
git tag -a "backend/v${BRIDRA_VERSION}" "$BRIDRA_RELEASE_SHA" -m "Bridra ${BRIDRA_VERSION}"
git push origin "backend/v${BRIDRA_VERSION}"
```

The GitHub workflow requires the exact tag SHA to be reachable from `main` and
its version to match `VERSION`. It looks up the merged PR's latest successful
complete Verify run and independently compares its tested Git tree with the tag.
Every required platform/core shard and group/final gate result, run attempt,
repository/workflow and artifact digest must match. Core still collects all three
coverage profiles and enforces all eight floors. A passing group gate cannot
substitute for a missing shard result. If evidence is absent or cannot be
confirmed, it runs all shared cross-platform checks against the tagged SHA. It does not require a
`main` push Verify run. A later merge to `main` does not change the approved source.
See [CI.md](CI.md) for exact reuse and fallback conditions.

Before environment approval, coverage and native platform evidence must be
established by the selected path. Read-only exact-tag packaging always enforces
final version alignment, Dart package validation, two identical CLI builds and
a native Linux CLI smoke test, and uploads one immutable candidate artifact.
The candidate gate rejects failed, cancelled or missing verification/packaging
and unexpected branch results.
Only after all jobs pass does `public-release` request owner approval and gain
publication/OIDC permissions. It downloads the exact artifact by ID, verifies
the tag is unchanged and the manifest's version, full source SHA, digests, sizes,
and file set, then attests and publishes those same CLI bytes without rebuilding.
The Release
contains exactly nine assets: six archives, `SHA256SUMS`, `manifest.json`, and
the SPDX SBOM. Every archive must contain the executable and the MIT `LICENSE`;
the schema-versioned manifest must identify the license as `MIT` and record the
SBOM digest. Do not move or replace a published tag. Fix a bad release with a new
patch version.

The release candidate intentionally omits `publish_to: 'none'`. When
`BRIDRA_PUBDEV_AUTOMATION_ENABLED=true`, the protected workflow publishes a
missing Dart version through pub.dev OIDC and safely skips an already-published
version on a rerun.

Use `fvm flutter pub publish` for both automated and manual publication. The
protected publication job restores the same OS/version-scoped dependency caches
through the shared setup action, while retaining a separate job and approval
boundary. Flutter initializes required SDK artifacts, including `sky_engine`,
before resolving dependencies. `fvm install` alone does not prepare all of these
artifacts. The SDK and Dart paths are resolved through FVM; keep
`dart-lang/setup-dart` for pub.dev OIDC authentication. Credentials/OIDC files
must never enter dependency caches.

When automation is disabled, publish the package manually before approving the
protected workflow:

```bash
cd packages/bridra_flutter
fvm flutter pub publish --dry-run
fvm flutter pub publish
```

The Dart package version, Go tag, CLI metadata, Flutter constraint, changelog,
and GitHub Release must describe the same framework release. Never publish the
generated application package. A Dart package's first version is published
manually by an authorized uploader and then transferred to the `cluion.com`
verified publisher. Later versions may use pub.dev's GitHub Actions publishing
through the protected release environment. Keep
`BRIDRA_PUBDEV_AUTOMATION_ENABLED` unset until pub.dev's tag pattern is
configured as `backend/v{{version}}`.

The `public-release` Environment approval remains a manual owner decision even
when the authorized owner submits it through GitHub's pending-deployments API.
Before approval, verify that the workflow ref, head SHA, environment, and
reviewer match the intended tag and the exact verified `main` commit. API
approval is not an administrative bypass.

## Post-release verification

Verify public installation in one disposable consumer with fresh Go/Pub caches,
without repository dependency overrides. Set `BRIDRA_VERSION` to the exact
published version approved above:

```bash
BRIDRA_ACCEPTANCE_DIR="$(mktemp -d)"
export GOPATH="$BRIDRA_ACCEPTANCE_DIR/go"
export GOMODCACHE="$BRIDRA_ACCEPTANCE_DIR/go-mod"
export GOCACHE="$BRIDRA_ACCEPTANCE_DIR/go-build"
export GOBIN="$BRIDRA_ACCEPTANCE_DIR/bin"
export PUB_CACHE="$BRIDRA_ACCEPTANCE_DIR/pub"
export GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org GOWORK=off
go install "github.com/cluion/bridra/backend/cmd/bridra@v${BRIDRA_VERSION}"
"$GOBIN/bridra" version --json
cd "$BRIDRA_ACCEPTANCE_DIR"
"$GOBIN/bridra" create release_smoke --module example.com/acme/release-smoke
cd release_smoke
make verify
```

Download the public archives and verify all six checksums. Verify both
attestations for at least the native archive, while requiring the release
workflow as signer:

```bash
gh attestation verify bridra_VERSION_OS_ARCH.tar.gz \
  --repo cluion/bridra \
  --signer-workflow cluion/bridra/.github/workflows/release.yml
gh attestation verify bridra_VERSION_OS_ARCH.tar.gz \
  --repo cluion/bridra \
  --signer-workflow cluion/bridra/.github/workflows/release.yml \
  --predicate-type https://spdx.dev/Document/v2.3
```

Require the public Go module origin and archive manifest to match the approved
source SHA. Compare the pub.dev archive/digest with that source separately from
fresh-cache package-index resolution; inspect the consumer's lockfile for the
intended hosted version. Verify the native public archive's version/SHA and both
attestations. One generated consumer's full verification is sufficient; do not
repeat it for local-source, squash-source and public-archive copies of the same
release. Add an actual previous-version upgrade acceptance when migration or
adoption behavior changes or an unresolved compatibility risk warrants it.

The release is incomplete until the Go module, Dart package, CLI archives,
checksums, SBOM, attestations, documentation and that external consumer agree.

After publication, confirm `SECURITY.md` and `SUPPORT.md` still match the
supported line, verify GitHub installation instructions from a clean
environment, and announce any deprecation or migration window. Keep the reviewed
substantive PR, workflow artifacts, and GitHub Release as the evidence record.
Delete completed work branches after merge; retain the release tag. There is no
routine release branch to retain or clean up after publication.

## Failed or compromised release

Never move a published tag or silently replace an archive. Stop further
publication, preserve evidence, and assess whether credentials or artifacts were
compromised. Revoke affected credentials, publish or update a Security Advisory
when appropriate, and correct the release with a new patch version. Dart package
retraction/yanking and GitHub Release warnings reduce exposure but do not make a
published artifact disappear; release notes must point users to the replacement.

If the substantive PR was already merged but no tag, Dart package, or GitHub
Release is public, prepare a focused correction PR and pass the current merge
gate. Record the resulting SHA for release authorization. Never rewrite merged
history. If a tag or artifact is already public, use a new patch version instead.

## Upgrade policy

Users choose upgrades explicitly. Install an exact published CLI through the
[quick start](../README.md#quick-start), then inspect its default framework target:

```bash
bridra version --json
bridra upgrade --plan
```

Bridra has no silent CLI auto-update. Breaking changes require release notes,
migration instructions, and the version change required by Semantic Versioning.
Apply is permitted only when all applicable catalog steps are automatic; it
restores managed manifests, lockfiles, and metadata when dependency resolution
or verification fails. Application-owned Controllers, Services, Models,
configuration, generated contracts, and native runners are never overwritten
as part of a core package upgrade.

Platform-specific requirements are filtered by project metadata. Shared,
unclassified, and previously missed required adoption remain manual. Automatic
updates preserve the adopted template baseline and the application's internally
consistent RPC protocol. Review generated-contract, native, and server changes
explicitly when the applicable migration requires them. Production HTTP controls
remain an application deployment decision; see [HTTP_SECURITY.md](HTTP_SECURITY.md).

Automation must recognize the upgrade report's schema before interpreting its
plan. Updating framework dependencies does not update the installed CLI or its
JSON consumers. Record any reader migration and rollback instructions alongside
the corresponding CLI contract change.

Maintain version-specific migration history, the compatibility matrix,
deprecation window, and rollback instructions in [UPGRADING.md](UPGRADING.md).
General usage and release documents link there rather than duplicating a new
version summary on every release.
