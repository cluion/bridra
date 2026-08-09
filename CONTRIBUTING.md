# Contributing to Bridra

Thank you for helping improve Bridra. The project values small, test-backed
changes that preserve its explicit Go/Flutter boundaries and generated contract.

## License and compatibility status

Bridra is licensed under the [MIT License](LICENSE), Copyright (c) 2026 Cluion.
The license permits use, modification, distribution, sublicensing, and commercial
use subject to preserving its copyright and permission notice. Bridra 0.12 is a
pre-1.0 line: public APIs may evolve through documented SemVer releases and there
is no LTS or production SLA.

Unless a separate written agreement applies, an intentionally submitted
contribution is offered under the same MIT License. Contributors retain copyright
in their contributions and represent that they have the right to submit them.
No copyright assignment is implied.

## Before opening work

- Use the Bug or Feature Issue form so the affected surface and compatibility
  impact are visible.
- Discuss large features, new dependencies, protocol changes, public API changes,
  package identity, or template ownership before implementation.
- Report security issues privately according to [SECURITY.md](SECURITY.md).
- Follow the community expectations in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
- Follow [GOVERNANCE.md](GOVERNANCE.md) for decisions and release authority.

## Development setup

Required tools are Go 1.25+, FVM 4.x, the Flutter version pinned by `.fvmrc`, and
Chrome for the browser-specific test. Native target builds require the platform
toolchains listed in [README.md](README.md).

```bash
make setup
make doctor
make verify
make coverage
make runtime-stress
```

`make verify` includes license-copy consistency, generated-file checks,
external-package Go public API tests, race tests, real Sidecar/HTTP integration,
Chrome, widget tests, formatting, vet, and analysis. `make coverage` enforces the
committed non-regression floors.

`make runtime-stress` is the slower opt-in Runtime suite. It actively fuzzes
Sidecar and HTTP RPC input, repeats race-enabled lifecycle and persistence
checks, exercises repeated Sidecar crash recovery, and enforces bounded
goroutine, heap, RSS, file-descriptor, and orphan-process growth. See
[docs/RUNTIME_STRESS.md](docs/RUNTIME_STRESS.md) for tuning and CI policy.

## Change boundaries

- Keep application code under `backend/app` or the generated application's own
  tree; reusable Go behavior belongs in `backend/framework` only when the public
  abstraction is justified.
- Keep transport-neutral Flutter behavior in `packages/bridra_flutter` and
  application-specific typed methods in the application gateway.
- Change `schema/bridra.json`, then run `make generate`; do not hand-edit checked-in
  generated Go or Dart files.
- Preserve RPC stdout for protocol messages and write Sidecar logs only to stderr.
- Do not add reflection scanning, hidden global registration, silent upgrades, or
  automatic overwrites of application-owned code.
- Keep Go framework SemVer, project metadata, Project Template, and RPC protocol
  identities distinct.

Public Go APIs require tests from the external `framework_test` package. Public
Dart entries should be tested through the exported package library. A breaking
change requires changelog, migration, deprecation, and rollback documentation.

## Pull requests

Keep a Pull Request focused enough to review and revert independently. Complete
the repository Pull Request template and include:

- the user problem and chosen boundary;
- tests that fail without the change and pass with it;
- protocol, public API, template, generated-code, security, and platform impact;
- documentation and changelog updates when behavior changes;
- exact verification commands and relevant platform limitations.

Use concise commit subjects such as `feat:`, `fix:`, `test:`, `docs:`, or
`refactor:`. Do not rewrite shared history, publish tags, or create releases as
part of a contribution.

Maintainers may request scope reduction, a design Issue, additional platform
evidence, or a migration plan before review. Passing CI is required but does not
replace maintainer review.
