# Bridra

> A Go-powered application framework for Flutter, by Cluion.

[Documentation](docs/GUIDE.md) ·
[Architecture](docs/ARCHITECTURE.md) ·
[HTTP security](docs/HTTP_SECURITY.md) ·
[Upgrading](docs/UPGRADING.md) ·
[Contributing](CONTRIBUTING.md)

Bridra 0.12 combines Flutter UI with a Laravel-inspired Go
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

Framework version `0.12.0` and the Project Template protocol baseline `1`
evolve independently. Applications own their RPC protocol and may increment it
when regenerating a coordinated Go/Dart contract.
Bridra is licensed under the [MIT License](LICENSE), Copyright (c) 2026 Cluion.

## Design goal

Bridra aims to be reusable application infrastructure, not a finished product
or a layer of hidden framework magic. One typed contract should connect Flutter
to the same explicit Go application pipeline on all six platforms. Laravel-style
names provide familiar boundaries, while Go code, generated source, dependencies,
storage choices, and lifecycle ownership remain visible and testable.

The framework owns transport, Sidecar lifecycle, code generation, application
primitives, development tooling, packaging contracts, upgrades, and release
evidence. Each application continues to own its product UI, domain rules,
database design, authentication policy, deployment, monitoring, signing, and
distribution channels.

## What Bridra provides

- Typed Go and Dart contracts generated from one versioned RPC schema
- Laravel-style Middleware, Controller, Service, Provider, Model, Request, and
  Response application layers
- Typed Config and dependency injection with singleton, transient, scoped, and
  aliased services
- Validation, structured exceptions, route groups, policies, and lifecycle hooks
- HTTP Bearer authentication, request Principals, and permission policies with
  stable unauthenticated/forbidden errors
- Bounded per-Principal/IP HTTP rate limiting with a pluggable limiter interface
  and stable 429 retry responses
- Server-generated HTTP Request IDs, structured redacted audit events, fixed
  metrics, and tracing-compatible observer hooks
- Synchronous Events, queued listeners, in-memory, durable local, shared SQL, or
  shared Redis background Jobs, and process-local, durable local, shared SQL, or
  shared Redis coordinated scheduled Tasks
- `database/sql` lifecycle, transaction boundaries, and migration runner
- Project creation, scaffolding, development supervision, builds, redacted
  support diagnostics, deterministic SPDX release SBOMs, GitHub build
  provenance, and transactional upgrades through one CLI
- Single-instance desktop ownership with later-launch activation forwarding
- Parent-bound desktop Sidecars that shut down when their Flutter owner exits
- Typed server streams with progress events, cancellation, and bounded
  per-stream backpressure
- Out-of-band large-file uploads and resumable downloads with short-lived HTTP
  capabilities, managed Desktop files, bounded retries, and end-to-end size
  plus SHA-256 verification
- Android Emulator, iOS Simulator, and physical-iPhone smoke coverage for
  verified HTTP managed downloads/uploads interrupted at known offsets and
  resumed; Emulator and device gates repeat the flow after backend recovery
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
token is delivered through a bounded stdin launch handshake, not process
arguments. The Sidecar independently watches its Flutter parent and exits if
that owner dies.
Mobile and Web applications connect to a separately deployed Go HTTP backend
through the same typed contract.

## Quick start

Install Go 1.25+, FVM 4.x, and the native toolchain required by your target
platform. Then install the exact Bridra CLI version:

```bash
go install github.com/cluion/bridra/backend/cmd/bridra@v0.12.0
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
bridra diagnose
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
make runtime-stress
make http-fault-test
make android-emulator-smoke
make ios-simulator-smoke
make ios-device-smoke
```

`make verify` checks generated contracts, Go formatting and vet, race-enabled
tests, external public APIs, real Sidecar and HTTP round trips, Chrome, Flutter
tests, and static analysis. `make coverage` enforces the committed
non-regression floors. `make runtime-stress` runs the slower fuzz, repeated
lifecycle, concurrency, persistence, Sidecar recovery, and bounded resource
stability suite.
`make http-fault-test` deterministically injects HTTP latency, timeouts, stream
interruptions, consumer backpressure, and dropped resumable downloads on both
the Dart VM and Chrome without replaying application RPCs.
`make android-emulator-smoke` uses a running Android Emulator to verify Health,
Greeting, ordered Streaming／Progress, interrupted file-transfer resume, a real
backend outage, and the complete flow again after reconnect.
`make ios-simulator-smoke` boots an available iPhone Simulator when needed and
verifies real `system.health`, `greeting.hello`, ordered Streaming／Progress,
and interrupted/resumed managed download and upload HTTP round trips.
`make ios-device-smoke` exercises the same RPCs on a physical iPhone, forces a
backend outage and verifies UI recovery, checks ordered Streaming／Progress both
before and after reconnect, repeats the managed download/upload recovery flow,
then installs a signed Profile app and proves two launches without Flutter
tooling.

## Documentation

- [Complete guide](docs/GUIDE.md): prerequisites, CLI workflows, platform runs,
  builds, configuration, and framework API examples
- [Architecture](docs/ARCHITECTURE.md): package boundaries, lifecycle,
  transports, security, distribution, and verification decisions
- [HTTP security and threat model](docs/HTTP_SECURITY.md): trust boundaries,
  abuse cases, framework controls, residual risks, audit fields, and production
  checklist
- [Runtime stress verification](docs/RUNTIME_STRESS.md): fuzzing, repeated
  lifecycle, persistence, resource stability, scheduled CI, and test limits
- [Runtime diagnostics](docs/RUNTIME_DIAGNOSTICS.md): redacted support bundles,
  Sidecar lifecycle snapshots, and application-owned crash reporting
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
