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
- Confirm the `public` remote and the reviewed commit belong to
  `https://github.com/cluion/bridra`.
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

## Prepare the release Pull Request

1. Choose the SemVer and decide independently whether the RPC protocol, Project
   Template, or project metadata schema changes.
2. Synchronize the framework, CLI, Go module, Dart package, project metadata,
   changelogs, and live documentation from one version:

   ```bash
   make release-prepare VERSION=0.2.0
   make release-check VERSION=0.2.0
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
   run `make release-check VERSION=0.2.0 FINAL=1`. The protected release workflow
   repeats this final check and refuses an unfinished changelog.
5. Run the full local verification and attach `coverage/summary.md` plus the CLI
   release manifest/checksums to the Pull Request.
6. Run a repository-external generated consumer with no local Go `replace` or
   Dart `dependency_overrides`.
7. Obtain maintainer review and repository-owner release authorization.

Windows maintainers use:

```powershell
.\tool\windows.ps1 -Task release-prepare -Version 0.2.0
.\tool\windows.ps1 -Task release-check -Version 0.2.0
.\tool\windows.ps1 -Task release-check -Version 0.2.0 -Final
```

Security fixes under embargo use a private advisory and private fork until the
coordinated release commit is ready. Do not expose the fix in a public Pull
Request before the agreed disclosure time.

## Build CLI artifacts

```bash
make cli-release
```

This produces the following under `build/bridra/cli/0.2.0/`:

- macOS amd64 and arm64 `tar.gz` archives
- Linux amd64 and arm64 `tar.gz` archives
- Windows amd64 and arm64 `zip` archives
- `SHA256SUMS`
- schema-versioned `manifest.json`

The source commit and commit timestamp are embedded into each binary. Confirm
the native archive before publishing:

```bash
shasum -a 256 -c build/bridra/cli/0.2.0/SHA256SUMS
bridra version --json
```

Running the release build twice from the same commit, Go toolchain, version, and
build date must produce identical archive checksums.

The release manager records the source commit, build host/toolchain, two checksum
runs, archive smoke-test result, and `bridra version --json` output. Signing,
notarization, installers, and app-store publication are product release layers and
must not be implied by these unsigned CLI archives.

## Tag and publish

For Bridra 0.2.0, create the annotated Go submodule tag only after the release
Pull Request is merged and the repository owner gives final authorization:

```bash
git tag -a backend/v0.2.0 -m "Bridra 0.2.0"
git push public backend/v0.2.0
```

The protected GitHub workflow re-runs version alignment, verification, coverage,
Dart package validation, and deterministic CLI packaging. It creates the matching
GitHub Release and uploads exactly the six archives, `SHA256SUMS`, and
`manifest.json`. Every archive must contain the executable and the MIT `LICENSE`;
the schema-versioned manifest must identify the license as `MIT`. Do not move or
replace a published tag. Fix a bad release with a new patch version.

The Dart package is published separately after ownership and package validation
are complete. The release candidate intentionally omits `publish_to: 'none'`, so
run the hosted package validation before publication and restore the safety lock
if publication is paused:

```bash
cd packages/bridra_flutter
fvm flutter pub publish --dry-run
```

The Dart package version, Go tag, CLI metadata, Flutter constraint, changelog,
and GitHub Release must describe the same framework release. Never publish the
generated application package. A Dart package's first version is published
manually by an authorized uploader and then transferred to the `cluion.com`
verified publisher. Later versions may use pub.dev's GitHub Actions publishing
through the protected release environment. During an initial manual publication,
keep `BRIDRA_PUBDEV_AUTOMATION_ENABLED` unset and complete the package
publication before approving the tag workflow.

## Post-release verification

Verify installation without a repository checkout:

```bash
go install github.com/cluion/bridra/backend/cmd/bridra@v0.2.0
bridra version --json
bridra create release_smoke --module example.com/acme/release-smoke
```

Run the generated project's full verification. The release is incomplete until
the Go module, Dart package, CLI archives, checksums, documentation, and external
consumer all agree on supported versions.

After publication, confirm `SECURITY.md` and `SUPPORT.md` still match the
supported line, verify GitHub installation instructions from a clean
environment, and announce any deprecation or migration window. Keep the release
Pull Request and GitHub Release as the evidence record.

## Failed or compromised release

Never move a published tag or silently replace an archive. Stop further
publication, preserve evidence, and assess whether credentials or artifacts were
compromised. Revoke affected credentials, publish or update a Security Advisory
when appropriate, and correct the release with a new patch version. Dart package
retraction/yanking and GitHub Release warnings reduce exposure but do not make a
published artifact disappear; release notes must point users to the replacement.

## Upgrade policy

Users choose upgrades explicitly:

```bash
go install github.com/cluion/bridra/backend/cmd/bridra@v0.2.0
bridra upgrade --plan --to 0.2.0
bridra upgrade --apply --to 0.2.0
```

Bridra has no silent CLI auto-update. Breaking changes require release notes,
migration instructions, and the version change required by Semantic Versioning.
Apply is permitted only for fully automatic catalog paths and rolls back managed
manifests, lockfiles, and metadata when dependency resolution or verification
fails. Application-owned Controllers, Services, Models, configuration, generated
contracts, and native runners are never overwritten as part of a core package
upgrade.

The project-facing compatibility matrix, deprecation window, manual migration
workflow, and rollback contract are defined in [UPGRADING.md](UPGRADING.md).
