# Upgrading Bridra

Bridra upgrades are explicit and reviewable. Bridra plans every migration
first and applies only plans made entirely of registered automatic steps. It
never updates the CLI itself, generated application code, native runners, or
application-owned files.

Run the read-only planner from a project root before changing dependencies:

```bash
bridra upgrade
bridra upgrade --plan --to 0.2.0
bridra upgrade --plan --to 0.2.0 --json
bridra upgrade --apply --to 0.2.0
```

When invoking the CLI through the backend dependency, use:

```bash
cd backend
go run github.com/cluion/bridra/backend/cmd/bridra upgrade --plan --root ..
```

`bridra upgrade` defaults to `--plan`; the former `--check` flag remains a
backward-compatible alias. The command is read-only. A `current` report exits
successfully. A
`migration_required`, `unsupported`, invalid, or incomplete contract exits with
a failure after writing its human or JSON diagnostics.

The installed CLI contains a versioned release and migration catalog. `--to`
must name a release in that catalog. The planner resolves every ordered
migration hop from the project's exact framework version to the target. It
refuses the plan if the source, target, or any intermediate hop is missing.
This prevents an apparently convenient direct jump from silently skipping a
required patch or pre-1.0 minor migration.

`--apply` is opt-in. The report exposes `applyAvailable`; Bridra refuses to write
when the plan is incomplete or contains a manual step. An automatic transition
means that the complete release change can be represented by synchronized
dependency and metadata updates. A transition requiring a codemod, Project
Template edit, protocol regeneration, application decision, or database
migration must remain manual.

## Apply transaction

Before writing, `--apply` validates that `backend/go.mod` and `pubspec.yaml`
still declare the versions recorded in `.bridra/project.json`. It snapshots:

- `backend/go.mod` and optional `backend/go.sum`
- `pubspec.yaml` and optional `pubspec.lock`
- `.bridra/project.json`

It then updates the Go and Flutter framework versions together, records the
target metadata contract, runs `go mod tidy`, runs `fvm flutter pub get`, and
runs the generated project's full `make verify`.

Any write, dependency-resolution, or verification failure restores every
snapshot. A lockfile created during the failed attempt is removed when it did
not exist before. Build output and dependency caches are disposable tool state
and are not part of the transaction. Controllers, Services, Middleware, Models,
Flutter UI, generated contracts, native runners, configuration, and database
migrations are never rewritten by `--apply`.

## Maintainer migration registry

Every framework release is registered in
`backend/cmd/bridra/upgrade_catalog.go` with its metadata, Project Template, and
RPC protocol identities. Every supported transition has an explicit directed
migration edge with a stable ID and reviewable description. The CLI test suite
requires every registered older release to have a complete path to the current
release, so bumping the framework version without adding the necessary path
fails verification.

## Version contract

`.bridra/project.json` records four independent compatibility identities:

| Identity | Current | Compatibility rule |
| --- | ---: | --- |
| Project metadata schema | 2 | Schema 1 remains readable by core project commands but requires a metadata migration. A newer schema requires a newer CLI. |
| Framework SemVer | 0.2.0 | The project and selected target must match. An older version requires a complete registered migration path; downgrade plans are rejected. |
| Project Template | 2 | Older templates require manual review. A newer template cannot be evaluated by an older CLI. |
| RPC protocol | 1 | Go and Flutter runtimes must use the same protocol. Upgrade them together. |

Framework SemVer and RPC protocol versions are deliberately separate. A
framework patch may preserve protocol 1, while a protocol change can require
coordinated Go code, Dart code, and regenerated application contracts.

Bridra is pre-1.0. Semantic Versioning permits breaking changes in a minor
release, so the v0.x planner uses exact framework targets and registered edges
instead of assuming minor-version compatibility. Patch releases are not skipped
implicitly either. Every breaking release must include migration and rollback
instructions.

## Project metadata schema 1 to 2

Project Template v1 did not record framework, template, or protocol versions.
The v1-to-v2 migration does not require application code changes. After updating
the Go and Flutter dependencies together, add the version contract:

```json
{
  "schemaVersion": 2,
  "projectName": "your_app",
  "goModule": "example.com/your/app",
  "frameworkModule": "github.com/cluion/bridra/backend",
  "frameworkVersion": "0.2.0",
  "templateVersion": 2,
  "protocolVersion": 1
}
```

Keep the existing project identity and module values. Template 2 adds this
upgrade contract and its documentation; it does not replace Controllers,
Services, Models, configuration, UI, or native runner files.

## Upgrade workflow

1. Start from a clean version-control state and keep the current lockfiles.
2. Install the target CLI explicitly and inspect `bridra version --json`.
3. Run `bridra upgrade --plan --to <version> --json` and retain the report for
   review. Confirm `planAvailable` is true and review every ordered step.
4. If `applyAvailable` is true, run `bridra upgrade --apply --to <version>`.
   Bridra updates both dependencies and metadata, resolves lockfiles, and runs
   full verification as one rollback-safe operation.
5. If `applyAvailable` is false, read the target release notes and complete every
   manual step. Update `.bridra/project.json` only after the dependency,
   template, and protocol changes it records have actually been applied.
6. Run any platform builds required by the application before committing the
   upgrade.

## Framework 0.1.1 to 0.2.0

The `0.2.0` transition is automatic because existing projects only need their Go
and Flutter framework dependencies updated together. Concurrent Sidecar dispatch
is enabled by default. Cancellation is opt-in through
`RpcCancellationToken`; existing RPC calls remain source-compatible.

Project Template version `2`, project metadata schema `2`, and RPC protocol
version `1` remain unchanged. Existing peers can therefore be upgraded together
without regenerating the wire contract or rewriting application-owned files.

For the current line, dependency updates use:

```bash
cd backend
go get github.com/cluion/bridra/backend@v0.2.0
cd ..
fvm flutter pub upgrade bridra_flutter
make generate
make verify
```

Local framework development may retain Go `replace` and Dart
`dependency_overrides`, but the declared dependency versions and project
metadata must still describe the contract under test.

## Deprecation policy

Public deprecations are announced in the changelog and compiler-visible API
documentation, with the replacement and intended removal release. Before 1.0,
Bridra aims to retain deprecated public APIs for at least one subsequent minor
release when security, correctness, or platform requirements permit. Exceptions
must be called out prominently in release notes with a migration path.

Wire fields and stable error codes are not removed solely through a framework
SemVer change. Their compatibility is governed by the protocol version and
generated contract migration.

Bridra has no LTS line before 1.0. Supported v0.x versions and any security-only
maintenance window are stated per release; absence of such a statement means
users should upgrade to the latest compatible patch.

## Rollback

If a manual upgrade fails:

1. Restore `.bridra/project.json`, `backend/go.mod`, `backend/go.sum`,
   `pubspec.yaml`, and `pubspec.lock` from version control.
2. Restore any reviewed generated changes and reinstall the previous CLI.
3. Run the previous version's full verification before redeploying.

`bridra upgrade --apply` automatically performs the managed-file restoration
above when its own verification fails. Application database migrations are a
separate lifecycle. Bridra upgrade commands never run them and cannot determine
whether a schema rollback is safe.
Use the application's migration history and backup policy before rolling back a
deployed release.

Automatic source rewriting may be added only as a future, reviewable codemod. It
must produce an inspectable diff and may not overwrite application-owned code by
default.
