# Changelog

All notable Bridra changes will be documented in this file. Bridra follows
Semantic Versioning; the RPC wire protocol is versioned independently.

## [0.23.0] - 2026-10-19

### Added

- Opt-in long-lived subscriptions over existing server streams, with idle
  heartbeats, cursor-based replay, finite connection rotation, bounded reconnect
  backoff and cancellation. A bounded in-memory Go replay source reports cursor
  gaps and restart epochs explicitly; durable event history remains app-owned.

### Fixed

- Pause Embedded stream pulls when the Flutter consumer pauses, and release a
  paused stream on cancellation. Distinguish premature stream EOF and HTTP status
  errors so subscriptions retry outages while preserving terminal protocol and
  authorization failures.
- Suspend a subscription's idle watchdog even when a read is already pending at
  pause time. Resume starts a fresh idle interval; cancellation and connection or
  total deadlines remain effective.

### Fixed

- Check application RPC protocol consistency in Doctor using the upgrade
  planner's shared validation. Metadata schema 2/3, schema and generated Go/Dart
  constants must agree; custom application protocols remain valid. Generated
  `make verify` now catches these mismatches. Legacy metadata schema 1 is
  explicitly unchecked and warns instead of implying contract validation.
- Disable automatic VCS stamping for desktop Sidecar Go builds, retaining
  trimmed paths, native macOS build flags, signing and final artifact checksums.
  Checkout/archive comparisons no longer depend on implicit Git metadata;
  product release evidence must retain the source revision explicitly.

### Changed

- Keep support/security policies version-independent, tied to the latest
  published minor line. Release checks still require the policy's eligibility,
  publication boundary and supported/unsupported claims.
- Accept an explicit `release prepare --date YYYY-MM-DD` (Make `RELEASE_DATE`,
  PowerShell `-ReleaseDate`) to synchronize both changelog dates while preserving
  notes and historical entries. Final checks reject invalid or differing dates;
  omitting the option retains the `Unreleased` workflow.
- Combine CI verification and coverage into one race-enabled test pass, select
  representative checks for ordinary application/CLI changes, and retain complete
  platform/runtime verification for releases and high-risk shared changes.
- Split long platform and packaging chains, cache pinned Flutter/Pub/Gradle
  dependencies, and warm trusted SDK caches through existing weekly maintenance.
  Preserve required checks, exact-source artifact identity, coverage floors,
  protected publication, OIDC and provenance/SBOM attestations.
- Concentrate final-SHA release validation in tag CI and public installation
  acceptance in one fresh external consumer rather than repeated local rehearsals.
- Reuse complete, recent, trusted PR platform/coverage verification only when its
  tested merge tree exactly matches the tag. Missing, partial or mismatched
  evidence falls back to full verification. Exact-tag package dry-run,
  reproducible builds and protected publication remain mandatory.

### Compatibility

- Go subscription producers/replay buffers and Flutter `RpcSubscriptions.subscribe`
  are additive APIs. Existing `RpcClient` implementers and generated streaming
  methods remain compatible; subscription envelopes are opt-in application data.
- The registered `0.22.0` to `0.23.0` dependency transition is automatic after
  earlier required adoption is complete. It preserves application-owned code,
  schema, runners, protocol and the adopted template baseline. New projects still
  use Template `9`; RPC protocol baseline `1`, project metadata schema `3` and
  CLI upgrade-report schema `5` are unchanged.
- Resume uses the last delivered cursor, not a durable processing acknowledgment.
  Applications own authorization, persistent replay/checkpoints, idempotent
  handling and explicit snapshot/resync after cursor expiry. See
  [the upgrade guide](docs/UPGRADING.md#framework-0220-to-0230).

### Support

- The latest `0.23.x` release receives best-effort security fixes until the next
  minor line is published. The `0.22.x` line remains supported until this release
  is published, then becomes unsupported.

## [0.22.0] - 2026-10-17

### Added

- An application-owned Windows/Linux title bar with keyboard-accessible window
  controls, title drag and double-click regions, resize handles and native state
  updates. Generated projects select its desktop dependency by platform.
- An optional modular Flutter starter with application composition, shared
  theme/layout, and feature views backed by observable presentation state.
  Workspace, Runtime, Guide and Settings support adaptive, collapsible sidebar/bar navigation,
  System/Light/Dark appearance, retained request drafts, and gateway lifetime
  cleanup. Existing application UI and runtime contracts remain unchanged.
- A unified product toolbar with bounded Back/Forward section history and
  animated sidebar transitions that honor reduced motion. Its toggle remains
  available at every size, with overlay navigation in narrow windows. The taller
  header shares the sidebar color and displays the current page title without a
  repeated Workspace heading or introductory subtitle. Settings participates in
  the same history and offers appearance controls inside the content area; backend
  health remains in Runtime without a persistent sidebar status block.
- An integrated macOS title bar that hides the separate background and title
  while retaining native window controls, with narrow-layout spacing to avoid
  covering application actions.
- Floating top copy confirmations with slide/fade transitions, automatic or
  manual dismissal, replacement of repeated notifications and reduced motion.
- Preserve application-owned UI and starter test files during template
  rendering, and verify generated consumers with the same UI regressions.

### Fixed

- Replace Flutter's default entrypoint and counter test when creating a new
  project, while preserving application-owned files during later rendering.
- Keep platform smoke checks compatible with the starter's optional runtime
  status display by waiting for request readiness and reconnect controls.
- Await secondary-instance termination in the reference application entrypoint,
  matching the generated starter's existing desktop lifecycle.

### Compatibility

- Project Template advances from `8` to `9` to identify the modular starter and
  desktop window adapters. Existing applications may keep Template `8` and their
  own UI. The registered `0.21.0` to `0.22.0` dependency transition is automatic;
  it does not rewrite application-owned UI, runners or the adopted template baseline.
- Public Go/Dart runtime APIs, RPC payloads, protocol baseline `1`, project metadata
  schema `3`, and CLI upgrade-report schema `5` remain unchanged. Earlier missing
  mandatory adoption still blocks automatic upgrades.

### Support

- The latest `0.22.x` release receives best-effort security fixes until the next
  minor line is published. The `0.21.x` line is no longer supported after this
  release is published.

## [0.21.0] - 2026-10-13

### Changed

- Advance the upgrade CLI JSON report from schema `4` to `5`. Reports include
  selected project platforms and revised migration/adoption semantics; schema-4
  consumers must explicitly support schema `5` before switching CLI versions.
  Plan steps, automatic eligibility, diagnostics, and exit results can differ
  when only optional starter changes remain.
- Use scoped Pull Request verification with a fixed `ci-gate`, squash merges,
  and no routine `main` push CI. Tag releases verify the exact source and all
  platforms, prepare reproducible CLI artifacts before environment approval,
  and publish the same immutable candidate after checking its identity and
  digests. Prepare necessary version metadata in substantive PRs instead of
  requiring a separate release branch, commit, and PR.

### Fixed

- Limit release preparation to required version metadata and changelogs. General
  documentation links to `VERSION` or uses version-independent examples and is
  never rewritten during preparation. Preserve historical migration references
  and promote pending `Unreleased` changelog notes without losing them.
- Filter upgrade requirements by the project's selected platforms and avoid
  redundant whole-template migrations. Preserve the recorded template baseline
  during automatic dependency updates; shared and unclassified manual changes
  continue to block automatic application.
- Generate Windows native window titles with ASCII Unicode escapes so MSVC's
  source codepage cannot corrupt non-ASCII display names.
- Add explicit Linux CJK font fallback families to the generated starter theme
  for ARM64 Flutter engines that do not discover fallback fonts through
  Fontconfig. A matching system font must be installed.

### Compatibility

- The `0.20.0` to `0.21.0` framework dependency transition is automatic for
  projects with the required earlier adoption complete. Go and Dart runtime APIs
  and RPC payloads remain compatible; metadata schema `3`, Template `8`, and
  Template RPC protocol baseline `1` are unchanged. Existing application runners
  and themes need the documented manual edits to adopt the desktop Unicode fixes.
- CLI JSON consumers require a separate schema-aware update. Automatic dependency
  upgrades do not update those scripts or infer application-owned template adoption.

### Support

- The latest `0.21.x` release receives best-effort security fixes until the next
  minor line is published. The `0.20.x` line is no longer supported after this
  release.

## [0.20.0] - 2026-10-10

### Fixed

- Exit acknowledged secondary desktop instances on Windows and Linux after
  releasing their single-instance session.
- Share the Application shutdown deadline across remaining providers and continue
  cleanup in reverse order after caller cancellation, with bounded grace periods
  and accurate provider failure attribution. Shutdown callers remain bounded by
  their context.
- Apply `create --display-name` to desktop runner titles and native product
  metadata while preserving binary names, bundle paths, and application IDs.
- Enable the macOS runner's sandbox client and server socket entitlements in
  Debug, Profile, and Release so single-instance loopback IPC can listen and
  forward secondary activations.
- Discover Windows Visual Studio C++ toolchains through `vswhere` rather than
  requiring MSVC, CMake, and Ninja in the shell PATH.
- Make generated HTTP entrypoints own the Application lifecycle and file-transfer
  provider, including startup failure cleanup and graceful HTTP shutdown.

### Added

- Added process-local runtime task controls with `StartTask` and
  `ScheduledTaskHandle.SetInterval`, `TriggerNow`, `Stop`, and `Done`. Dynamic
  tasks serialize their invocations, cancel on Scheduler shutdown, and reject
  configured Stores rather than bypassing persistent coordination.
- Added `bridra schema baseline --update` to validate and explicitly replace an
  application-owned contract baseline, including before its first release.
  Generation and compatibility checks never update the baseline automatically.
- Generated unary Dart methods accept an optional `timeout`, retaining the
  five-second default. Handwritten API implementations need the new named parameter
  when regenerating their contract; the RPC wire protocol remains unchanged.

### Changed

- Advanced the Project Template to version `8` for Application-owned HTTP
  lifecycle, macOS single-instance socket entitlements, native display metadata,
  and generated unary Dart timeout parameters. The `0.19.0` to `0.20.0`
  migration requires manual review of application-owned files. Project metadata
  schema `3` and Template RPC protocol baseline `1` remain unchanged.

### Support

- The latest `0.20.x` release receives best-effort security fixes until the next
  minor line is published. The `0.19.x` line is no longer supported after this
  release.

## [0.19.0] - 2026-10-03

### Added

- Schema codegen accepts camelCase RPC method actions and snake_case JSON field
  names while preserving their wire names. Snake_case fields generate
  PascalCase Go and camelCase Dart identifiers; collisions with other fields or
  generated methods are rejected in the affected Go／Dart scope before code
  generation.

### Changed

- Schema validation now rejects generated-member collisions in the affected
  Go／Dart scope before emitting code. This also rejects previously accepted
  fields such as request `toJson` or `validatePayload` that produced
  uncompilable generated code; names remain valid in scopes without a collision.
- Project Template `7`, project metadata schema `3`, and Template RPC protocol
  baseline `1` remain unchanged. The `0.18.0` to `0.19.0` dependency-only
  migration is automatic and preserves each application's RPC protocol.
  Adopting new schema names requires explicit contract generation; renaming an
  existing wire method or JSON field still requires compatibility review.

### Support

- The latest `0.19.x` release receives best-effort security fixes until the next
  minor line is published. The `0.18.x` line is no longer supported after this
  release.

## [0.18.0] - 2026-09-13

### Added

- Added RPC schema `number` fields for finite JSON numeric values. Codegen maps
  scalars, nullable fields, and arrays to Go `float64` and Dart `double`.
- Dart response decoding accepts integer and fractional JSON numbers as
  `double` and rejects non-numeric or non-finite values.

### Changed

- `integer` ↔ `number` field changes are treated as wire-shape changes and
  require an application-owned Protocol bump. Schema `minimum`／`maximum` remain
  integer-only; coordinate and distance bounds remain application validation.
- Project Template `7`, project metadata schema `3`, and Template RPC protocol
  baseline `1` remain unchanged. Existing application schemas do not need edits.

### Support

- The latest `0.18.x` release receives best-effort security fixes until the next
  minor line is published. The `0.17.x` line is no longer supported after this
  release.

## [0.17.0] - 2026-09-13

### Added

- Added an opt-in application-owned iOS Embedded Core transport that runs the Go
  Core in the app process without opening an HTTP socket. The generated seed
  includes gomobile XCFramework packaging, a Swift adapter, and explicit runtime
  installation from the application-owned `AppDelegate`.
- Added pull-backed Embedded RPC streaming with ordered frames, exact request-id
  cancellation, bounded native and Dart queues, and bounded runtime shutdown.
- Added Embedded managed downloads and uploads using opaque native handles,
  bounded typed-byte chunks, resume from verified offsets, and declared size and
  SHA-256 validation.
- Added application-owned security-scoped document-directory handoff. Swift
  retains the native URL while Dart and RPC receive only a process-local opaque
  `ResourceBroker` capability; detach, explicit release, and runtime close revoke
  the authority.

### Changed

- Advanced the Project Template to version `7` for the application-owned iOS
  Embedded Core wrapper, Swift bridge, XCFramework build target, and lifecycle
  tests. Existing projects require the documented manual migration. Project
  metadata schema `3` and Template RPC protocol baseline `1` remain unchanged.
- The public Flutter barrel now conditionally exports the MethodChannel bridge,
  preserving pure-Dart desktop helper compatibility without loading `dart:ui`.

### Support

- The latest `0.17.x` release receives best-effort security fixes until the next
  minor line is published. The `0.16.x` line is no longer supported after this
  release.

## [0.16.0] - 2026-08-29

### Added

- Documented the exact raw streaming metadata contract and added Sidecar plus
  HTTP wire-level regressions for non-canonical stream flags and Sidecar window
  fallback behavior.
- Added `bridra build macos --macos-sidecar-entitlements <path>` for
  application-owned Sidecar sandbox and capability contracts.
- Added opt-in `bridra build macos --macos-sidecar-native`, which builds both
  universal Sidecar architectures with CGO and Bridra's tagged Foundation
  adapter. Build manifest schema 2 records this as `sidecarNative`.
- Added a bounded Go `ResourceBroker`, opaque authenticated capabilities,
  lifecycle provider, and tagged Foundation resolver for ephemeral and
  persistent macOS bookmark leases.
- Added the macOS Swift bookmark creator, public Dart bookmark and Sidecar grant
  APIs, and authenticated reserved Sidecar grant／release controls. Capability
  values are session-bound and redacted from string output.
- Added `make macos-sandbox-smoke` and a hosted macOS CI gate that signs isolated
  creator／receiver harness apps and proves outside-container raw-path denial,
  ephemeral bookmark access, and denial again after release.

### Changed

- Advanced the Project Template to version `6` for the application-owned macOS
  bookmark bridge and ResourceBroker-enabled Sidecar entrypoint. Existing
  projects require the documented manual migration. Project metadata schema `3`
  and Template RPC protocol baseline `1` remain unchanged.

### Fixed

- `bridra generate` now canonicalizes generated Dart with the project-pinned
  formatter before writing or checking output. Valid schemas no longer force
  generated-file freshness and Dart formatting gates into conflicting states.
- macOS Sidecar builds now preserve and verify the Flutter app's existing
  entitlements when re-applying the ad-hoc bundle signature. Requested Sidecar
  entitlements are also read back and compared before final artifact checksums
  are written.

### Support

- The latest `0.16.x` release receives best-effort security fixes until the next
  minor line is published. The `0.15.x` line is no longer supported after this
  release.

## [0.15.0] - 2026-08-29

### Added

- Generated Go applications now expose `app.Build`, and generated Sidecars own
  the resulting `Application` lifecycle. EOF, Serve failures, and cancellation
  all execute application shutdown exactly once.
- Added generated Sidecar regression tests for EOF, Serve errors, blocking stdin,
  and providers that do not return before the shutdown deadline.
- Added `DesktopSingleInstance.terminateSecondary` plus a generated macOS
  lifecycle channel and native regression tests so acknowledged secondary
  launches terminate before they can enter `runApp`.
- Added scalar integer `minimum`／`maximum` schema constraints, generated Go
  request validation through public `framework.Minimum`／`Maximum` rules, and
  compatibility checks that require a Protocol bump when request bounds change.
- Added top-level reusable schema `types`, explicit object `ref` fields, and
  structured object arrays across generated Go requests／responses and Dart
  encoding／decoding. Reused request objects retain nested validation and
  normalization with indexed violation paths.
- Added public Go `framework.NestedListField` and
  `framework.OptionalNestedListField` rules for generated and application-owned
  structured lists.
- Added `bridra create --platforms` with `all`, `desktop`, `mobile`, and custom
  selections. Generated projects persist the canonical scope and conditionally
  include platform-owned runner patches, smoke scripts, Make targets, and docs.

### Changed

- Advanced the Project Template to version `4`. Sidecar stdio stop and
  `Application.Shutdown` waits are independently bounded so a blocking reader or
  provider cannot prevent the generated process from exiting. Existing projects
  require a manual migration because their app and Sidecar entrypoints are
  application-owned.
- Generated macOS projects now expose `make macos-native-test`, and hosted macOS
  verification runs it before the bundle smoke test.
- Invalid RPC method names now report the required lowercase dot-separated
  format and a valid example instead of only reporting that the value is invalid.

Project metadata schema advances to `3` and Project Template to `5` for persisted
platform scope; template RPC protocol baseline `1` remains unchanged. The Dart
and Go public API changes are additive and do not alter the RPC wire protocol.

### Support

- The latest `0.15.x` release receives best-effort security fixes until the next
  minor line is published. The `0.14.x` line is no longer supported after this
  release.

## [0.14.0] - 2026-08-23

### Added

- Generated projects now keep an application-owned
  `schema/bridra.baseline.json` and run `bridra schema check` from `make verify`,
  blocking breaking RPC wire changes that reuse the deployed protocol version.
  Contract generation and framework upgrades never replace the baseline.

### Changed

- Advanced the Project Template to version `3` and registered the manual
  `0.13.0` to `0.14.0` migration. Existing applications must select their own
  reviewed deployed schema as the baseline; the template protocol baseline
  remains `1` and does not constrain custom application protocols.

### Support

- The latest `0.14.x` release receives best-effort security fixes until the next
  minor line is published. The `0.13.x` line is no longer supported after this
  release.

## [0.13.0] - 2026-08-15

### Added

- Added `bridra schema check` with human-readable and versioned JSON reports for
  comparing an application RPC schema with a reviewed baseline. The command
  rejects protocol regressions and breaking wire changes that do not increase
  the application-owned RPC protocol version.

### Changed

- Registered the automatic `0.12.0` to `0.13.0` dependency migration. The
  public Go Codegen API additively exposes schema compatibility reports; the
  public Dart runtime API, Project Template version `2`, project metadata
  schema `2`, and template protocol baseline `1` remain unchanged.

### Support

- The latest `0.13.x` release receives best-effort security fixes until the next
  minor line is published. The `0.12.x` line is no longer supported after this
  release.

## [0.12.0] - 2026-08-09

### Added

- Added a generated-project iOS Simulator integration smoke test and hosted
  macOS CI gate that exercise real `system.health`, `greeting.hello`, and
  ordered Streaming/Progress HTTP round trips plus interrupted managed downloads
  and uploads resumed from their stored offsets against the Go backend.
- Added a physical-iPhone smoke target with LAN address discovery, occupied-port
  avoidance, Health/Greeting integration coverage, ordered Streaming/Progress,
  byte-range-resumed downloads, and resumable uploads consumed and re-verified
  by Go before and after forced backend-loss recovery, plus two standalone
  signed Profile cold launches. Local Apple Team and bundle identifier overrides
  stay outside Git.
- Added a generated-project Android Emulator integration smoke test and hosted
  Linux CI gate covering real health, greeting, ordered streaming/progress,
  interrupted file-transfer resume, and backend-loss recovery.

### Changed

- Desktop Sidecars now receive their random launch token through a bounded,
  versioned stdin handshake instead of process arguments. The Flutter client
  falls back once for older generated Sidecars that do not recognize the new
  non-secret launch flag; the RPC protocol version is unchanged.
- Updated the release provenance action to its reviewed immutable `v4.2.2`
  commit, including upstream dependency security backports.

### Fixed

- Prevented the connected-status header from overflowing at iPhone widths.
- Kept the iOS local-network permission purpose consistent across build modes
  and added the scoped ATS local-network exception required by current iOS,
  without allowing arbitrary public-network HTTP in Profile or Release.
- Retried transient `devicectl` termination failures and verified the process
  exits so physical-iPhone smoke cleanup does not silently leave Runner behind.
- Corrected the Android Emulator CI cache dependency path so the hosted gate
  completes without the previous cache warning annotation.

### Support

- The latest `0.12.x` release receives best-effort security fixes until the next
  minor line is published. The `0.11.x` line is no longer supported after this
  release.

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
