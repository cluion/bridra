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
- Use root `VERSION` as the release intent and keep
  `framework.FrameworkVersion`, `bridra_flutter`'s version, generated dependency
  defaults, changelogs, documentation, and the intended tag aligned.
- Run `make release-check FINAL=1`, `make verify`, `make coverage`, and
  `bridra upgrade --plan`; confirm all required GitHub Actions jobs and coverage
  floors pass.
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

<!-- bridra:release-current:start -->
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
   changelogs, and live documentation from one version:

   ```bash
   make release-prepare VERSION=0.20.0
   make release-check VERSION=0.20.0
   ```

   The prepare command never creates a tag, publishes a package, or creates a
   release. It refuses a downgrade, inconsistent starting state, or a new
   version while the previous changelog entry is still marked `Unreleased`.
   A leading unversioned `Unreleased` section is promoted to the new version,
   preserving its notes and all published changelog sections.
   Live version references must be enclosed by the HTML comment markers
   `bridra:release-current:start` and `bridra:release-current:end`.
   Only those blocks follow the release version; historical migration text
   outside them remains unchanged. Missing, nested, or inconsistent blocks
   stop preparation before any managed file is written.
3. Review the changelog, upgrade/rollback guidance, and independently versioned
   protocol, Project Template, and project metadata contracts. Preserve historical
   release references when reviewing the prepared documentation. Register the new
   release and every required transition in the upgrade migration catalog;
   verification rejects a new current version without a complete path from each
   older registered release.
4. Replace `Unreleased` in both changelogs with the intended release date, then
   run `make release-check VERSION=0.20.0 FINAL=1`. The protected release workflow
   repeats this final check and refuses an unfinished changelog.
5. Run relevant local checks and workflow lint for CI changes. Require the PR's
   `ci-gate` against an up-to-date merge result and record verification limits.
   Tag CI later runs the full cross-platform release checks, coverage floors,
   Dart dry run, reproducible CLI builds, and native archive smoke test against
   the actual release SHA before requesting publication approval.
6. Obtain maintainer review and repository-owner release authorization.

The Verify workflow runs for Pull Requests targeting `main` and merge groups;
it does not run for ordinary branch pushes or after a squash merge. It selects
platform checks, cancels superseded runs for the same PR, and reports the fixed
`ci-gate`. Require PR protection and a current base before relying on this
policy. After merge, record the actual squash commit SHA for authorization and
tagging. See [CI.md](CI.md) for scope and required repository settings.

Windows maintainers use:

```powershell
.\tool\windows.ps1 -Task release-prepare -Version 0.20.0
.\tool\windows.ps1 -Task release-check -Version 0.20.0
.\tool\windows.ps1 -Task release-check -Version 0.20.0 -Final
```

Security fixes under embargo use a private advisory and private fork until the
coordinated release commit is ready. Do not expose the fix in a public Pull
Request before the agreed disclosure time.

## Build CLI artifacts

```bash
make cli-release
```

This produces the following under `build/bridra/cli/0.20.0/`:

- macOS amd64 and arm64 `tar.gz` archives
- Linux amd64 and arm64 `tar.gz` archives
- Windows amd64 and arm64 `zip` archives
- `SHA256SUMS`
- schema-versioned `manifest.json`
- deterministic `bridra_VERSION_cli.spdx.json`

The source commit and commit timestamp are embedded into each binary. Confirm
the native archive before publishing:

```bash
(cd build/bridra/cli/0.20.0 && shasum -a 256 -c SHA256SUMS)
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

For Bridra 0.20.0, create the annotated Go submodule tag only after the substantive
Pull Request is merged and the repository owner authorizes the version and SHA:

```bash
git fetch origin main
git tag -a backend/v0.20.0 <verified-main-sha> -m "Bridra 0.20.0"
git push origin backend/v0.20.0
```

The GitHub workflow requires the exact tag SHA to be reachable from `main` and
its version to match `VERSION`. It runs all shared cross-platform release checks
against that SHA; it does not require or search for a `main` push Verify run.
A later merge to `main` does not change the approved release source.

Before environment approval, the read-only verification jobs enforce coverage,
final version alignment, Dart package validation, two identical CLI builds, and
a native Linux CLI smoke test. They upload one immutable candidate artifact.
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
version on a rerun. When automation is disabled, publish the package manually
before approving the protected workflow:

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

Verify installation without a repository checkout:

```bash
go install github.com/cluion/bridra/backend/cmd/bridra@v0.20.0
bridra version --json
bridra create release_smoke --module example.com/acme/release-smoke
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

Run the generated project's full verification. The release is incomplete until
the Go module, Dart package, CLI archives, checksums, SBOM, attestations,
documentation, and external consumer all agree on supported versions.

After publication, confirm `SECURITY.md` and `SUPPORT.md` still match the
supported line, verify GitHub installation instructions from a clean
environment, and announce any deprecation or migration window. Keep the reviewed
substantive PR, workflow artifacts, and GitHub Release as the evidence record.
Delete completed work branches after merge; retain the release tag. There is no
routine release branch to retain or clean up after publication.
<!-- bridra:release-current:end -->

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

Users choose upgrades explicitly:

<!-- bridra:release-current:start -->
```bash
go install github.com/cluion/bridra/backend/cmd/bridra@v0.20.0
bridra upgrade --plan --to 0.20.0
```
<!-- bridra:release-current:end -->

Bridra has no silent CLI auto-update. Breaking changes require release notes,
migration instructions, and the version change required by Semantic Versioning.
Apply is permitted only when all applicable catalog steps are automatic and rolls back managed
manifests, lockfiles, and metadata when dependency resolution or verification
fails. Application-owned Controllers, Services, Models, configuration, generated
contracts, and native runners are never overwritten as part of a core package
upgrade. Platform-specific requirements are filtered by project metadata;
shared requirements and unclassified changes remain manual. Automatic updates
preserve the recorded template baseline. The `0.6.0` to `0.6.1` path remains manual because projects generated
with `0.6.0` must repair an application-owned `FakeBackend` test that upgrades
never overwrite. The `0.6.1` to `0.13.0` path is automatic through the additive,
opt-in file-, SQL-, and Redis-backed persistence releases, the `0.10.0`
HTTP-security step, the `0.10.1` diagnostics and upgrade-planner patch, and the
runtime-neutral `0.11.0` supply-chain release, the `0.12.0` secure Sidecar launch
update, and the `0.13.0` RPC schema compatibility tooling release. Paths from
earlier releases to `0.20.0` still include any required manual application
baseline-gate, generated Application and macOS secondary lifecycle, and native
resource-handoff steps. The direct `0.17.0` to `0.18.0` and `0.18.0` to `0.19.0`
dependency-only transitions are automatic. Adopting authentication, rate
limiting, observability, and server limits in an
existing application-owned HTTP entrypoint remains an explicit deployment
decision. The public Dart API adds
`RpcRateLimitedException` in `0.10.0` and `SidecarDiagnostics` in `0.10.1`;
`0.12.0` adds the public Go `ReadSidecarLaunch` helper while preserving legacy
application-owned Sidecar entrypoints; `0.13.0` additively exposes RPC schema
compatibility reports through the CLI and public Go Codegen API. `0.14.0`
advances Project Template version to `3`, adds the application-owned deployed
schema baseline, and gates generated-project verification. `0.15.0` advances
Project Template version to `4`, gives generated Sidecars exactly-once,
bounded Application shutdown, and adds reliable macOS secondary-instance
termination through the additive `DesktopSingleInstance.terminateSecondary`
API plus a native runner gate. It also adds scalar integer schema bounds,
generated public Go validation rules, compatibility coverage, and actionable
method-name diagnostics. Reusable named schema objects and structured object
arrays generate deduplicated Go／Dart models, indexed nested validation, and
reference-resolved compatibility reports. Project Template `5` and project
metadata schema `3` add canonical platform scope for generated runners, targets,
checks, and documentation. `0.16.0` advances Project Template version to `6`
for the application-owned macOS bookmark bridge and ResourceBroker-enabled
Sidecar entrypoint. `0.17.0` advances Project Template version to `7` for the
application-owned iOS Embedded Core wrapper, Swift adapter, XCFramework build,
managed transfers, and security-scoped resource lifecycle. Project metadata
schema `3` and Template protocol baseline `1` remain unchanged. `0.18.0` adds
opt-in RPC `number` scalar codegen without advancing those independent versions;
`0.19.0` adds opt-in camelCase RPC method actions and snake_case JSON fields,
preserving wire names while validating generated-member collisions in their
actual Go／Dart scopes. Previously accepted schemas that generated conflicting
members now fail before code generation. Template `7`, metadata schema `3`, and
Template protocol baseline `1` remain unchanged. Every migration preserves each
application's internally consistent RPC protocol; adopting new schema names is
an explicit application change, not part of the dependency-only migration.

`0.20.0` advances Project Template to `8` for Application-owned HTTP lifecycle,
macOS single-instance socket entitlements, native display metadata, and unary
Dart timeout parameters. The `0.19.0` to `0.20.0` transition is manual because
the CLI does not overwrite application-owned entrypoints, runners, or generated
API implementations. Metadata schema `3` and Template protocol baseline `1`
remain unchanged. Runtime task controls and explicit schema baseline updates
are opt-in additions.

The project-facing compatibility matrix, deprecation window, manual migration
workflow, and rollback contract are defined in [UPGRADING.md](UPGRADING.md).
