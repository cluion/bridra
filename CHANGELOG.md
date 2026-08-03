# Changelog

All notable Bridra changes will be documented in this file. Bridra follows
Semantic Versioning; the RPC wire protocol is versioned independently.

## [0.11.0] - 2026-08-03

### Added

- Added a deterministic SPDX 2.3 SBOM for the six CLI archives. Release
  manifest schema `3` records its digest and rejects module replacements or
  cross-target dependency drift.
- Added GitHub Sigstore build-provenance and SBOM attestations for release CLI
  archives, with the official `actions/attest` action pinned to its reviewed
  immutable commit.

### Changed

- Registered the automatic `0.10.1` to `0.11.0` dependency migration. The
  public Go／Dart runtime APIs, Project Template version `2`, project metadata
  schema `2`, and template protocol baseline `1` remain unchanged.

## [0.10.1] - 2026-08-02

### Fixed

- Fixed `bridra upgrade` treating the Project Template protocol baseline as an
  application protocol ceiling. The planner now verifies application metadata,
  schema, and generated Go/Dart contracts together, while automatic framework
  migrations preserve application-owned protocol versions. Upgrade JSON schema
  `4` exposes the baseline as `target.templateProtocolVersion`.

### Changed

- Registered the automatic `0.10.0` to `0.10.1` dependency migration. Project
  Template version `2`, project metadata schema `2`, and template protocol
  baseline `1` remain unchanged; the application's verified protocol is
  preserved.

### Added

- Added native Go fuzz targets for malformed Sidecar and HTTP RPC input, with
  response-validity and credential-redaction invariants.
- Added an opt-in Runtime stress target and weekly/manual CI workflow for
  repeated race-enabled Queue, Scheduler, persistence, concurrency,
  backpressure, parent-death, and Sidecar crash-recovery verification.
- Added `bridra diagnose` for new redacted support bundles with optional,
  strictly validated Sidecar lifecycle snapshots.
- Added application-owned recovered-panic reporting through `CrashReporter`,
  while preserving stable RPC errors and containing reporter failures.
- Added immutable Dart Sidecar diagnostics with bounded restart, health,
  process, pending-work, and safe error-type history.
- Added bounded Runtime resource stability checks for goroutines, retained Go
  heap, Linux process RSS, file descriptors, and orphaned Sidecars.

## [0.10.0] - 2026-08-01

### Added

- Added application-owned HTTP Bearer authentication with Principal propagation,
  pluggable authenticators, stable 401/503 responses, and exact permission
  policies for route authorization.
- Added pluggable HTTP rate limiting with opaque Principal/IP keys, a bounded
  concurrency-safe in-memory token bucket, stable 429 responses, and
  `Retry-After` guidance.
- Added server-generated HTTP Request IDs, structured redacted JSON audit events,
  fixed-cardinality HTTP metrics, tracing-compatible observer hooks, and a
  maintained HTTP threat model.

### Changed

- Updated the reference server and Project Template to protect HTTP RPC before
  dispatch while preserving Sidecar envelope-token compatibility.
- Updated `HttpRpcClient` to send its runtime token as a Bearer credential in
  addition to the existing versioned RPC envelope metadata.
- Updated unary and streaming HTTP clients to expose 429 responses as
  `RpcRateLimitedException` with the optional server retry duration.
- Hardened generated HTTP servers with fail-closed direct-server CORS defaults,
  explicit header/read/write/idle limits, bounded headers, `no-store`, and
  `nosniff` responses.
- Registered an automatic `0.9.0` to `0.10.0` dependency and metadata upgrade.
  Existing application-owned HTTP entrypoints remain compatible and unchanged;
  adopting the new authentication, rate-limiting, observability, and server
  hardening controls is explicit.
- Kept Project Template version `2`, project metadata schema `2`, and RPC
  protocol version `1`. The release adds public Go HTTP security APIs and the
  public Dart `RpcRateLimitedException` without changing RPC envelopes.

### Support

- The latest `0.10.x` release receives best-effort security fixes until the next
  minor line is published. The `0.9.x` line is no longer supported after this
  release.

## [0.9.0] - 2026-07-31

### Added

- Added `RedisJobStore` for shared multi-process and multi-host Queue delivery
  through Redis, with Lua-atomic reservation, lease recovery, retry state, and
  retained failed Jobs.
- Added Redis Cluster-compatible namespaced keys, payload bounds, context-aware
  failed Job operations, real Redis integration coverage, and the official
  `go-redis/v9` client contract.
- Added `RedisSchedulerStore` for shared multi-process and multi-host scheduled
  Task coordination, with Lua-atomic leases, expiry recovery, persisted
  completion state, and Laravel-style `onOneServer()` behavior.
- Added a Redis Cluster-compatible single-slot Scheduler namespace, 255-byte
  Task-name bounds, context and corruption handling, and real Redis contention
  coverage with 24 concurrent contenders.

### Changed

- Registered an automatic `0.8.0` to `0.9.0` upgrade because Redis persistence
  is additive and opt-in. The public Dart API, Project Template version `2`,
  project metadata schema `2`, and RPC protocol version `1` remain unchanged.

### Support

- The latest `0.9.x` release receives best-effort security fixes until the next
  minor line is published. The `0.8.x` line is no longer supported after this
  release.

## [0.8.0] - 2026-07-30

### Added

- Added a driver-neutral `SQLJobStore` for shared multi-process and multi-host
  Queue delivery through `database/sql`, with atomic lease reservation, expiry
  recovery, retry state, and retained failed Jobs.
- Added question-mark and PostgreSQL-style dollar placeholders, explicit schema
  initialization, payload bounds, and context-aware failed Job inspection,
  retry, and forget operations.
- Added a driver-neutral `SQLSchedulerStore` for distributed scheduled Task
  coordination through `database/sql`, with atomic leases, expiry recovery, and
  persisted next-run and completion state.
- Added shared SQL scheduling equivalent to Laravel `onOneServer()`, using the
  same question-mark and PostgreSQL-style dollar placeholder support and a
  portable 255-byte Task-name bound.

### Changed

- Registered an automatic `0.7.0` to `0.8.0` upgrade because SQL persistence is
  additive and opt-in. The public Dart API, Project Template version `2`, project
  metadata schema `2`, and RPC protocol version `1` remain unchanged.

### Support

- The latest `0.8.x` release receives best-effort security fixes until the next
  minor line is published. The `0.7.x` line is no longer supported after this
  release.

## [0.7.0] - 2026-07-29

### Added

- Added opt-in `JobStore` persistence and a synchronized append-only
  `FileJobStore` that preserves ready and delayed Jobs, retry attempts, and failed
  Job retention across process and Sidecar restarts.
- Added leased at-least-once delivery plus failed Job inspection, retry, and forget
  operations.
- Added opt-in `SchedulerStore` state, atomic Task leases, one-occurrence restart
  recovery, and a local append-only `FileSchedulerStore`.
- Added persisted next-run, last scheduled, last completed, and last error
  diagnostics for scheduled Tasks.

### Changed

- Persistent Queue shutdown now finishes active work, leaves pending Jobs durable
  for the next start, and closes the configured Store through
  `QueueServiceProvider`.
- Persistent Scheduler shutdown now finishes active Tasks and closes its configured
  Store through `SchedulerServiceProvider`.

### Support

- The latest `0.7.x` release receives best-effort security fixes until the next
  minor line is published. The `0.6.x` line is no longer supported after this
  release.

## [0.6.1] - 2026-07-29

### Fixed

- Fixed generated Flutter widget tests to implement the complete resumable file
  upload and download gateway contract introduced in `0.6.0`.
- Added rendered Flutter consumer compilation to Project Template verification
  so generated application tests cannot drift from generated gateway APIs.

### Changed

- Registered a manual `0.6.0` to `0.6.1` upgrade because projects created with
  `0.6.0` must apply the documented `FakeBackend` test fix and upgrades never
  overwrite application-owned test files.

## [0.6.0] - 2026-07-29

### Added

- Added generated typed server-streaming methods, data and progress events,
  flushed HTTP NDJSON, Sidecar stream cancellation, strict frame sequencing,
  and bounded per-stream credit backpressure.
- Added response `file` schema fields, managed Go file staging, HTTP downloads,
  Desktop out-of-band file reads, and streaming size plus SHA-256 verification
  in Flutter.
- Added request `file` schema fields, resumable HTTP uploads and downloads,
  managed Desktop uploads, bounded retry, and application-side verified upload
  consumption.

### Changed

- Kept RPC protocol version `1`: unary envelopes are unchanged and streaming is
  an opt-in method capability using additive frame metadata. File references
  are additive request/response DTOs whose bytes travel outside the JSON RPC
  envelope.
- Registered an automatic `0.5.0` to `0.6.0` upgrade because the new contracts
  are opt-in and Project Template version `2`, project metadata schema `2`, and
  RPC protocol version `1` remain unchanged.

### Support

- The latest `0.6.x` release receives best-effort security fixes until the next
  minor line is published. The `0.5.x` line is no longer supported after this
  release.

## [0.5.0] - 2026-07-27

### Added

- Added desktop single-instance coordination with per-application ownership,
  crash-safe stale-lock recovery, and authenticated loopback forwarding for
  command-line arguments such as file paths and deep-link URIs.
- Updated generated applications to acquire desktop ownership before `runApp`
  and expose later activations through a typed stream.

### Changed

- Registered a manual `0.4.0` to `0.5.0` upgrade because existing projects must
  adopt single-instance coordination in their application-owned Flutter
  entrypoint. Project Template version `2` and RPC protocol version `1` remain
  unchanged.

### Support

- The latest `0.5.x` release receives best-effort security fixes until the next
  minor line is published. The `0.4.x` line is no longer supported after this
  release.

## [0.4.0] - 2026-07-27

### Added

- Added automatic desktop Sidecar recovery with bounded exponential backoff,
  replacement health checks, stable retry-exhaustion errors, and recovery-aware
  request timeout and cancellation behavior. In-flight calls fail without
  automatic replay when a process crashes.
- Added platform-specific parent-process observation so a desktop Sidecar
  performs graceful shutdown instead of becoming orphaned when Flutter is
  force-terminated.

### Changed

- Registered a manual `0.3.0` to `0.4.0` upgrade because existing projects must
  update their application-owned Sidecar entrypoint to observe the Flutter
  parent process. Project Template version `2` and RPC protocol version `1`
  remain unchanged.

### Support

- The latest `0.4.x` release receives best-effort security fixes until the next
  minor line is published. The `0.3.x` line is no longer supported after this
  release.

## [0.3.0] - 2026-07-25

### Added

- Added automatic Go backend reload to `bridra dev` with debounced source
  watching, rebuild failure recovery, readiness checks, and graceful process
  replacement without restarting Flutter.
- Added `DispatchJobAfter` and `DispatchJobAt` for bounded delayed Job delivery.
  Accepted delayed Jobs preserve due-time order and are promoted immediately
  during graceful Queue shutdown.
- Added `ScheduleCronTask` with five-field cron expressions, lists, ranges,
  steps, English month and weekday names, configurable Scheduler time zones,
  same-Task non-overlap, and missed-run skipping.
- Registered an automatic `0.2.0` to `0.3.0` dependency upgrade while retaining
  Project Template version `2` and RPC protocol version `1`.

### Changed

- Streamlined tag-triggered publication by reusing the exact successful `main`
  Verify run, publishing `bridra_flutter` through pub.dev OIDC when needed, and
  keeping GitHub Release creation in the protected release environment.

### Support

- The latest `0.3.x` release receives best-effort security fixes until the next
  minor line is published. The `0.2.x` line is no longer supported after this
  release.

## [0.2.0] - 2026-07-25

### Added

- Added bounded concurrent desktop Sidecar dispatch with configurable active and
  pending limits, ordered response writes, graceful draining, and stable
  `server_busy` overload errors.
- Added end-to-end RPC cancellation for desktop Sidecar and mobile/Web HTTP
  transports through `RpcCancellationToken`, including timeout propagation to
  Go request contexts.
- Registered an automatic `0.1.1` to `0.2.0` dependency upgrade while retaining
  Project Template version `2` and RPC protocol version `1`.

### Support

- The latest `0.2.x` release receives best-effort security fixes until the next
  minor line is published. The `0.1.x` line is no longer supported after this
  release.

## [0.1.1] - 2026-07-24

### Fixed

- Made `bridra create` resolve hosted Go module dependencies with `go mod tidy`
  before testing the generated backend, so first-time projects receive a valid
  `go.sum` without requiring a local Bridra checkout.

### Changed

- Registered an automatic `0.1.0` to `0.1.1` upgrade path for the synchronized
  Go and Flutter framework dependencies.

## [0.1.0] - 2026-07-24

### Added

- Six-platform Flutter starter and native runners.
- Desktop Go sidecar transport and mobile/Web HTTP RPC transport.
- Shared Router, Middleware, Controller, and Service pipeline.
- Typed Flutter backend gateway and protocol health handshake.
- Public Go `framework` package and reusable `bridra_flutter` transport package.
- FVM-based `make setup` and `make doctor` onboarding for the monorepo.
- Typed Config, Container, eager service factories, and Service Provider lifecycle.
- Named Request DTOs and `BindAndValidate` validation.
- Structured field violations, domain Models, and Response DTOs.
- Framework version in the health response.
- Real stdio and HTTP executable integration tests.
- Public-package lifecycle contract tests, including terminal provider failures.
- Versioned JSON contract schema and `bridra generate` CLI.
- Extensible Bridra CLI command dispatcher with global and command-specific help.
- `bridra doctor` checks Go, FVM, pinned Flutter, host architecture, and optional
  desktop build prerequisites, with a strict mode for release validation.
- `bridra create` validates project identity, generates six Flutter runners in a
  staging directory, renders a versioned project manifest, verifies dependencies,
  and atomically publishes the completed project.
- Project Template v2 with Go Sidecar/HTTP entrypoints, Laravel-style application
  layers, schema-generated contracts, FVM, tests, and Make targets.
- Versioned `.bridra/project.json` metadata for monorepo and generated-project
  discovery without a fixed publisher checkout path.
- Framework, Project Template, and RPC protocol identities in project metadata,
  with schema 1 compatibility for previously generated projects.
- Read-only `bridra upgrade` planner with default/explicit plan modes,
  target-version selection, ordered cross-patch/minor migration paths,
  missing-hop refusal, JSON diagnostics, and `--check` compatibility alias.
- Opt-in `bridra upgrade --apply` for fully automatic paths, with manifest drift
  protection, synchronized Go/Flutter dependency updates, lockfile resolution,
  full verification, application-code isolation, and managed-file rollback.
- Public compatibility, deprecation, migration, and rollback policy in
  `docs/UPGRADING.md`.
- Direct public default-connector tests for desktop IO and Chrome/Web, plus
  deterministic executable discovery coverage across Unix and Windows paths.
- Versioned Go/Flutter coverage floors, Markdown reporting, CI enforcement, and
  retained workflow coverage artifacts.
- Security, support, contribution, conduct, and role-based governance policies
  with explicit ownership and publication gates.
- Registered `cluion.com` as the verified Dart publisher and prepared
  `bridra_flutter` for hosted release validation.
- MIT licensing for the repository, Go module, Flutter package, and generated
  CLI archives, with cross-platform license-copy consistency checks.
- Structured Bug and Feature Issue forms plus a Pull Request contract covering
  verification, compatibility, security, migration, and rollback evidence.
- A release authority, private vulnerability response, disclosure, artifact
  evidence, failed-release, and post-release support process.
- A canonical root `VERSION` plus `bridra release prepare/check` automation that
  synchronizes and verifies Go, Flutter, metadata, changelog, and documentation
  release surfaces without tagging or publishing.
- Cross-platform `make release-prepare/release-check` and Windows PowerShell
  maintainer entrypoints that accept the public framework version once.
- A protected tag-triggered public release workflow, disabled by default through
  repository variables, that re-verifies the release, builds CLI assets,
  optionally publishes Dart through pub.dev OIDC, and creates the GitHub Release.
- `bridra make` scaffolds for Controller, Service, Middleware, Request, Model,
  Response, Service Provider, and Test components.
- Companion tests, Go formatting, default collision rejection, explicit
  transactional `--force` replacement, and rollback-safe scaffold publication.
- `bridra dev` supervision for desktop Sidecar and mobile/Web HTTP development.
- Cross-platform process-tree signal forwarding, HTTP readiness checks, timeout
  escalation, and cleanup of child processes after exit or startup failure.
- `bridra build` target/mode orchestration for all six platforms, including host
  restrictions, desktop Sidecar installation, and unsigned iOS output.
- HTTPS/token release validation and token-free SHA-256 artifact manifests, with
  universal ad-hoc-signed macOS Sidecars and shared Make/PowerShell build policy.
- Configurable Go framework and Dart runtime imports in contract Codegen.
- Generated Go protocol/route constants, Request/Response DTOs, and validation.
- Generated Dart request/result models, response decoders, and typed RPC client.
- Golden-output and stale-generation checks in the default verification flow.
- Lazy singleton, transient, request-scoped, and typed alias Container bindings.
- Circular dependency and singleton-to-scope lifetime validation.
- Automatic per-dispatch Scope access through the request Context.
- Named middleware groups with global or route-group composition.
- Nested dot-prefixed Route Groups and group/method authorization policies.
- Generated route group/action constants used by the application provider.
- Typed validation rule registry with field, optional, nested, and cross-field rules.
- Schema/codegen support for nullable fields, string enums, and nested objects.
- Required/non-null generated Request payload contracts with nested field paths.
- Generated Request normalization and minimum-length validation.
- Dart request/response Codegen support for RFC 3339 date-time arrays.
- Generated enum/max-length validation with dot-prefixed nested field violations.
- Extensible Router exception renderer and typed domain exception mappings.
- Ordered typed Config sources with defaults, environment, and runtime precedence.
- Aggregated Config decoding/validation errors and redacted secret inspection.
- Named Provider Manifests with deterministic ordering and lifecycle diagnostics.
- Shared HTTP application bootstrap through the Config source pipeline.
- Typed synchronous Event Dispatcher with named, ordered listeners.
- Context cancellation, fail-fast listener errors, and explicit event propagation stops.
- Application/Container event dispatcher integration and a Greeting domain event example.
- Application shutdown lifecycle with terminable providers and reverse-order cleanup.
- Idempotent concurrent shutdown, aggregated provider errors, and partial-startup cleanup.
- Unified Application termination in the stdio sidecar and HTTP server entrypoints.
- Typed Jobs with one named Handler per exact Go type.
- Bounded in-memory Queue with configurable workers, Job timeouts, failure reporting,
  and recovered Handler panics.
- Queue Service Provider with Application Boot and graceful reverse-order shutdown integration.
- Per-Handler retry attempts with fixed backoff and per-attempt timeouts.
- Retry exhaustion metadata and errors that preserve framework and original causes.
- Typed queued Event listeners with explicit Event-to-Job mapping.
- Queued listener mapping and enqueue errors that preserve Event, Queue, and context causes.
- Named fixed-delay Scheduler Tasks with same-Task non-overlap and concurrent task loops.
- Scheduler timeouts, failure reporting, panic recovery, and Service Provider lifecycle.
- Standard-library SQL Database wrapper with startup health checks and lifecycle cleanup.
- Context-aware transaction boundaries and transaction-aware Repository execution.
- Ordered, versioned Migration Runner with persistent SQL history and status inspection.
- Per-Migration transactions, panic recovery, latest-batch rollback, and explicit
  non-transactional migration support.

### Changed

- Adopted the Bridra product name and Cluion publisher identity.
- Established `github.com/cluion/bridra` as the canonical repository and
  `github.com/cluion/bridra/backend` as the public Go module identity.
- Made `bridra create` emit versioned Go and Dart framework dependencies by
  default, with explicit local `replace` and `dependency_overrides` support.
- Added `bridra version` human/JSON release metadata and deterministic macOS,
  Linux, and Windows CLI archives with checksums and a release manifest.
- Documented exact Go submodule tagging, GitHub Release contents, verification,
  and explicit CLI/framework upgrade policy.
- Renamed package, binary, runtime configuration, and display-name surfaces.
- Set the pre-stable framework version to `0.1.0`.
- Made Register and Boot provider failures terminal for each Application instance.
