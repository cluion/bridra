# Bridra

> A Go-powered application framework for Flutter, by Cluion.

[Documentation](docs/GUIDE.md) ·
[Architecture](docs/ARCHITECTURE.md) ·
[Upgrading](docs/UPGRADING.md) ·
[Contributing](CONTRIBUTING.md)

Bridra 0.4 combines Flutter UI with a Laravel-inspired Go
application pipeline. It provides one project model for Windows, macOS, Linux,
Android, iOS, and Web while keeping application code explicit and testable.

```text
Flutter UI -> typed gateway -> RPC transport
                              |-- Desktop: managed Go sidecar over stdin/stdout
                              `-- Mobile/Web: Go HTTP server
                                                   |
                                                   v
                              Middleware -> Controller -> Service
```

Framework version `0.4.0` and RPC protocol version `1` evolve independently.
Bridra is licensed under the [MIT License](LICENSE), Copyright (c) 2026 Cluion.

## What Bridra provides

- Typed Go and Dart contracts generated from one versioned RPC schema
- Laravel-style Middleware, Controller, Service, Provider, Model, Request, and
  Response application layers
- Typed Config and dependency injection with singleton, transient, scoped, and
  aliased services
- Validation, structured exceptions, route groups, policies, and lifecycle hooks
- Synchronous Events, queued listeners, background Jobs, and scheduled Tasks
- `database/sql` lifecycle, transaction boundaries, and migration runner
- Project creation, scaffolding, development supervision, builds, diagnostics,
  release metadata, and transactional upgrades through one CLI
- Single-instance desktop ownership with later-launch activation forwarding
- Parent-bound desktop Sidecars that shut down when their Flutter owner exits
- Real process, HTTP, browser, widget, race, public API, and coverage tests

## Platform model

| Platform | Flutter runner | Go transport | Typical release |
| --- | --- | --- | --- |
| Windows | Yes | Bundled sidecar | Windows bundle |
| macOS | Yes | Bundled sidecar | macOS app |
| Linux | Yes | Bundled sidecar | Linux bundle |
| Android | Yes | HTTP RPC | APK |
| iOS | Yes | HTTP RPC | Unsigned iOS app |
| Web | Yes | HTTP RPC with CORS | Static Web bundle |

Desktop applications launch and own an ephemeral-token Go child process. The
Sidecar independently watches its Flutter parent and exits if that owner dies.
Mobile and Web applications connect to a separately deployed Go HTTP backend
through the same typed contract.

## Quick start

Install Go 1.25+, FVM 4.x, and the native toolchain required by your target
platform. Then install the exact Bridra CLI version:

```bash
go install github.com/cluion/bridra/backend/cmd/bridra@v0.4.0
bridra version
```

Create and verify a six-platform application:

```bash
bridra create hello_app \
  --module example.com/your-name/hello-backend

cd hello_app
make doctor
make verify
make run
```

The generated project owns its Flutter UI, Go application layers, schema,
native runners, and tests. Framework packages remain versioned dependencies, so
application code can evolve independently.

## Everyday commands

```bash
bridra make controller User
bridra make service Billing
bridra make request CreateUser
bridra generate
bridra dev
bridra build linux
bridra upgrade --plan
bridra upgrade --apply
```

Run `bridra help <command>` for complete options. The CLI does not silently
upgrade itself or overwrite application-owned code.

## Develop Bridra itself

The repository pins Flutter 3.44.6 through the committed `.fvmrc`:

```bash
make setup
make doctor
make verify
make coverage
```

`make verify` checks generated contracts, Go formatting and vet, race-enabled
tests, external public APIs, real Sidecar and HTTP round trips, Chrome, Flutter
tests, and static analysis. `make coverage` enforces the committed
non-regression floors.

## Documentation

- [Complete guide](docs/GUIDE.md): prerequisites, CLI workflows, platform runs,
  builds, configuration, and framework API examples
- [Architecture](docs/ARCHITECTURE.md): package boundaries, lifecycle,
  transports, security, distribution, and verification decisions
- [Upgrading](docs/UPGRADING.md): planning, automatic apply, migrations,
  deprecation, and rollback
- [Release process](docs/RELEASING.md): release authority, validation,
  artifacts, tags, package publication, and failure handling
- [Contributing](CONTRIBUTING.md): development rules and Pull Request contract
- [Security](SECURITY.md): supported versions and private vulnerability reports
- [Support](SUPPORT.md): framework support boundary and Issue expectations
- [Governance](GOVERNANCE.md): roles, decisions, review, and release authority
- [Code of conduct](CODE_OF_CONDUCT.md): community expectations and reporting

## Repository layout

```text
backend/
  framework/            Reusable Go framework
  cmd/bridra/           Framework CLI
  projecttemplate/      Versioned project generator
  scaffold/             Application component generator
packages/
  bridra_flutter/       Flutter RPC and Sidecar runtime
schema/                 Versioned RPC contract
lib/                    Starter Flutter application
docs/                   User, architecture, upgrade, and release documentation
```

Bridra is pre-1.0 without an LTS or production SLA. Public changes
follow Semantic Versioning, documented migrations, and explicit release
authorization.
