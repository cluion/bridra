# Bridra release process

Bridra uses Semantic Versioning for the framework and an independent integer
version for the RPC protocol. The Go module lives in `backend/`, so release tags
use the `backend/vMAJOR.MINOR.PATCH` form.

Bridra is licensed under MIT. Its canonical public repository is
`https://github.com/cluion/bridra`, its Go module is
`github.com/cluion/bridra/backend`, and `bridra_flutter` is assigned to the
verified `cluion.com` publisher. No tag or package may be published until every
release blocker is resolved and recorded in a release Pull Request.

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

Every release is prepared through one reviewable release Pull Request. The Pull
Request records the release manager, target version, source commit, support
window, security status, artifact evidence, package owners, and final owner
approval. CI success alone is not publication authorization.

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

## Prepare the release Pull Request

Use one branch named `release-MAJOR.MINOR.PATCH`, one release preparation
commit, and one Pull Request for each version. Before merge, correct a release
preparation error by amending that same commit and pushing with
`--force-with-lease`; do not create a sequence of speculative correction
commits or Pull Requests.

1. Choose the SemVer and decide independently whether the RPC protocol, Project
   Template, or project metadata schema changes.
2. Synchronize the framework, CLI, Go module, Dart package, project metadata,
   changelogs, and live documentation from one version:

   ```bash
   make release-prepare VERSION=0.15.0
   make release-check VERSION=0.15.0
   ```

   The prepare command never creates a tag, publishes a package, or creates a
   release. It refuses a downgrade, inconsistent starting state, or a new
   version while the previous changelog entry is still marked `Unreleased`.
3. Review the changelog, upgrade/rollback guidance, and independently versioned
   protocol, Project Template, and project metadata contracts. Register the new
   release and every required transition in the upgrade migration catalog;
   verification rejects a new current version without a complete path from each
   older registered release.
4. Replace `Unreleased` in both changelogs with the intended release date, then
   run `make release-check VERSION=0.15.0 FINAL=1`. The protected release workflow
   repeats this final check and refuses an unfinished changelog.
5. Run the full local verification and workflow lint. From the clean release
   commit, require a zero-warning Dart publish dry run, build the CLI artifacts
   twice, and confirm both builds have identical archives, manifest, checksums,
   and SBOM. Attach `coverage/summary.md` plus the CLI release
   manifest/checksums and SBOM digest to the Pull Request.
6. Obtain maintainer review and repository-owner release authorization.

The Verify workflow runs for Pull Requests and pushes to `main`, not every
branch push. New commits cancel superseded runs for the same Pull Request or
branch. After merge, wait for the exact `main` commit's Verify workflow before
tagging it.

Windows maintainers use:

```powershell
.\tool\windows.ps1 -Task release-prepare -Version 0.15.0
.\tool\windows.ps1 -Task release-check -Version 0.15.0
.\tool\windows.ps1 -Task release-check -Version 0.15.0 -Final
```

Security fixes under embargo use a private advisory and private fork until the
coordinated release commit is ready. Do not expose the fix in a public Pull
Request before the agreed disclosure time.

## Build CLI artifacts

```bash
make cli-release
```

This produces the following under `build/bridra/cli/0.15.0/`:

- macOS amd64 and arm64 `tar.gz` archives
- Linux amd64 and arm64 `tar.gz` archives
- Windows amd64 and arm64 `zip` archives
- `SHA256SUMS`
- schema-versioned `manifest.json`
- deterministic `bridra_VERSION_cli.spdx.json`

The source commit and commit timestamp are embedded into each binary. Confirm
the native archive before publishing:

```bash
(cd build/bridra/cli/0.15.0 && shasum -a 256 -c SHA256SUMS)
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

For Bridra 0.15.0, create the annotated Go submodule tag only after the release
Pull Request is merged and the repository owner gives final authorization:

```bash
git fetch origin main
git tag -a backend/v0.15.0 <verified-main-sha> -m "Bridra 0.15.0"
git push origin backend/v0.15.0
```

The protected GitHub workflow requires the tag to point at the current `main`
commit and reuses that exact commit's successful Verify workflow instead of
running the full cross-platform suite again. It still repeats final version
alignment, deterministic CLI packaging, Dart package validation, and
provenance/SBOM attestation before creating the matching GitHub Release. The Release
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
go install github.com/cluion/bridra/backend/cmd/bridra@v0.15.0
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
environment, and announce any deprecation or migration window. Keep the release
Pull Request and GitHub Release as the evidence record.

Only after every public verification passes, delete the local and remote
`release-MAJOR.MINOR.PATCH` branch. Finish on a clean local `main` that exactly
matches `origin/main`.

## Failed or compromised release

Never move a published tag or silently replace an archive. Stop further
publication, preserve evidence, and assess whether credentials or artifacts were
compromised. Revoke affected credentials, publish or update a Security Advisory
when appropriate, and correct the release with a new patch version. Dart package
retraction/yanking and GitHub Release warnings reduce exposure but do not make a
published artifact disappear; release notes must point users to the replacement.

If the release Pull Request was already merged but no tag, Dart package, or
GitHub Release is public, prepare one consolidated correction Pull Request and
repeat the exact-main verification gate. Do not stack speculative correction
commits or Pull Requests. If any artifact is already public, use a new patch
version instead.

## Upgrade policy

Users choose upgrades explicitly:

```bash
go install github.com/cluion/bridra/backend/cmd/bridra@v0.15.0
bridra upgrade --plan --to 0.15.0
```

Bridra has no silent CLI auto-update. Breaking changes require release notes,
migration instructions, and the version change required by Semantic Versioning.
Apply is permitted only for fully automatic catalog paths and rolls back managed
manifests, lockfiles, and metadata when dependency resolution or verification
fails. Application-owned Controllers, Services, Models, configuration, generated
contracts, and native runners are never overwritten as part of a core package
upgrade. The `0.6.0` to `0.6.1` path remains manual because projects generated
with `0.6.0` must repair an application-owned `FakeBackend` test that upgrades
never overwrite. The `0.6.1` to `0.13.0` path is automatic through the additive,
opt-in file-, SQL-, and Redis-backed persistence releases, the `0.10.0`
HTTP-security step, the `0.10.1` diagnostics and upgrade-planner patch, and the
runtime-neutral `0.11.0` supply-chain release, the `0.12.0` secure Sidecar launch
update, and the `0.13.0` RPC schema compatibility tooling release. Every path to
`0.15.0` ends with manual application baseline-gate and generated Application
lifecycle steps; adopting authentication, rate
limiting, observability, and server limits in an existing application-owned HTTP
entrypoint remains an explicit deployment decision. The public Dart API adds
`RpcRateLimitedException` in `0.10.0` and `SidecarDiagnostics` in `0.10.1`;
`0.12.0` adds the public Go `ReadSidecarLaunch` helper while preserving legacy
application-owned Sidecar entrypoints; `0.13.0` additively exposes RPC schema
compatibility reports through the CLI and public Go Codegen API. `0.14.0`
advances Project Template version to `3`, adds the application-owned deployed
schema baseline, and gates generated-project verification. `0.15.0` advances
Project Template version to `4` and gives generated Sidecars exactly-once,
bounded Application shutdown. Project metadata
schema `2` and template protocol baseline `1` remain unchanged; every migration
preserves each application's internally consistent RPC protocol.

The project-facing compatibility matrix, deprecation window, manual migration
workflow, and rollback contract are defined in [UPGRADING.md](UPGRADING.md).
