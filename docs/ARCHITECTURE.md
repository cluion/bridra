# Bridra architecture decisions

Bridra 0.6 supports Windows, macOS, Linux, Android, iOS, and Web while keeping
one Go application pipeline and one typed Flutter API.

## Layers

```text
Flutter widgets
    |
BackendGateway              connection lifecycle and health caching
    |
BridraRpcApi                generated methods, models, and response decoding
    |
RpcClient                   transport-neutral request/reply contract
    |-- SidecarClient       desktop process and newline-delimited JSON
    `-- HttpRpcClient       Android, iOS, Web, or remote desktop JSON POST
              |
Go Router -> Middleware -> Request validation -> Controller -> Service
                                                   -> Model -> Response DTO
```

Controllers and services never know which transport delivered a request. Named
Request DTOs validate input before Controller orchestration; Services return
transport-independent Models that Controllers map to Response DTOs. The stdio
server and HTTP handler are thin adapters around the same `Router`.

## Package boundaries

`backend/framework` is the reusable public Go package. Application Requests,
Models, Services, Responses, Controllers, and route registration remain under
`backend/app`. `packages/bridra_flutter` owns transport-neutral RPC, HTTP, and
desktop Sidecar clients. The generated `BridraRpcApi` owns the application RPC
contract; `lib/api/backend_gateway.dart` adds connection lifecycle and health
caching. Both packages remain in one Git repository and use Bridra 0.6.1.

## Contract generation

`schema/bridra.json` is the single source for protocol version, RPC methods,
wire DTO fields, presence/nullability, scalar/nested validation, and Dart
decoders. `bridra generate` produces Go protocol/route constants, Go Request and
Response DTOs, and the Dart typed client. Checked-in generated files act as
golden outputs, while `make verify` runs `codegen-check` before compiling either
language.

Generated Requests first validate wire presence so a missing field, explicit
`null`, and a present zero value remain distinct. They then normalize declared
`trim` fields and compose the same `RuleRegistry[T]` API available to
applications. Field rules, nested validators, and cross-field `RuleFunc[T]`
errors are aggregated into structured violations. The Router passes every
returned error through its `ExceptionRenderer`; an `ExceptionRegistry` can map
typed domain errors while retaining the framework's safe fallback behavior.

## Framework CLI

`backend/cmd/bridra` uses an explicit command registry rather than a package scan.
Each command owns its summary, usage text, argument parser, and execution boundary;
global and command-specific help are generated from that registry. `generate`
retains deterministic contract generation, while `doctor` validates the pinned
Go/FVM/Flutter toolchain before normal verification.

Host desktop compilers are advisory in normal doctor mode so transport, contract,
and backend development can continue on a partial machine. Release automation can
select `doctor --strict` to require every host prerequisite.

`release prepare` accepts one framework SemVer and synchronizes the managed Go,
Flutter, project metadata, changelog, and documentation surfaces. `release check`
is read-only and fails when any managed surface drifts. Protocol, Project
Template, and project metadata versions remain independent compatibility
contracts; neither command tags or publishes a release.

`create` asks Flutter to generate the native six-platform runners inside a
same-parent staging directory, then renders Project Template manifest v2 and
generates the typed contract. Go consumer tests, Flutter dependency resolution,
and Dart formatting must succeed before one atomic rename exposes the destination;
every earlier failure removes the staging directory.

Framework dependency coordinates are data, not template constants. Create v0.1
defaults to the running CLI's compatible Go module, Flutter package, framework
version, and pinned Flutter SDK. An explicit `--bridra-root` reads local package
identity, verifies that its Flutter package version matches the CLI, and adds Go
`replace` plus Dart `dependency_overrides` without removing version constraints.
Codegen accepts matching Go framework and Dart runtime import options. The
canonical framework module is `github.com/cluion/bridra/backend`; generated
application modules remain caller-owned and independent from the Cluion namespace.

`.bridra/project.json` is the committed discovery boundary for project-local
commands. Schema 2 records project identity, application and framework modules,
framework SemVer, Project Template version, and RPC protocol version. Schema 1
remains readable by current core commands so an older generated project can run
the read-only upgrade diagnosis before migrating its metadata.

`upgrade` defaults to a read-only plan and retains `--check` as a compatibility
alias. Its versioned release catalog maps each framework release to metadata,
Project Template, and RPC protocol contracts. A deterministic migration graph
resolves every ordered patch and minor hop to `--to`; unknown targets, downgrades,
and missing hops are rejected.

`upgrade --apply` accepts only a complete path whose steps are all marked
automatic. It validates manifest identity, snapshots the Go/Dart
manifests and lockfiles plus project metadata, updates the final target
atomically, resolves both package managers, and runs full project verification.
Every managed file is restored on failure, including removal of newly created
lockfiles. Manual steps and application source rewriting remain outside the
automatic boundary. The complete compatibility, deprecation, migration, and
rollback policy lives in `UPGRADING.md`.

`make` reads a separate versioned scaffold manifest. It renders and formats every
file in memory, rejects the complete operation on a default collision, then stages
files beside the project before publishing them with rename operations. `--force`
backs up every collision in the staging directory; a later publish failure rolls
the whole scaffold back. Generated code remains normal application code and does
not add reflection scanning or hidden registration.

`dev` is the explicit process supervisor for one local development session. Auto mode
selects the Sidecar on the current desktop host and the HTTP server for Web/mobile
devices. HTTP startup waits for the local TCP listener before launching Flutter; an
early backend exit is terminal. Web and desktop HTTP targets may default to loopback,
while mobile/custom targets require a caller-supplied reachable URL whose port matches
the local listener.

Every external command starts in its own process group. Unix shutdown signals the
whole group; Windows first sends CTRL_BREAK to the new process group and falls back to
tree-aware `taskkill`. The supervisor waits for every child, escalates to force cleanup
after a deadline, and reports joined startup, runtime, and cleanup failures. Flutter
keeps interactive stdin for Hot Reload.

The supervisor polls Go build inputs and debounces related filesystem changes. It
compiles a candidate executable before stopping a working process, then replaces the
binary only after the build succeeds. HTTP mode restarts the backend while Flutter
continues running. Sidecar mode restarts the Flutter process so its owned Sidecar uses
the new executable. Failed builds retain the active process and remain recoverable on
the next source change.

`build` is the release-artifact orchestration boundary. It accepts the six Flutter
targets and the Flutter-supported debug, profile, and release modes. Native desktop
targets are host-bound; Android and Web remain cross-host Flutter builds. Desktop uses
a Sidecar unless an explicit backend URL selects HTTP, while mobile and Web always use
HTTP. Profile and release HTTP artifacts require an HTTPS `/rpc` endpoint and an
explicit compile-time token.

Sidecars are built with `CGO_ENABLED=0` for the target OS and host architecture.
macOS combines arm64 and amd64 binaries with `lipo`, installs the universal executable
under the app's `libexec`, then re-applies and verifies an ad-hoc bundle signature.
Linux and Windows install the matching native executable after Flutter creates the
bundle. This post-build installation also works for unmodified runners created by
`bridra create`; application templates do not need repository-specific Xcode or CMake
hooks.

After validating the expected artifact and bundled Sidecar, the command computes a
deterministic SHA-256 over the file or directory tree. A versioned, token-free manifest
under `build/bridra` records target, mode, transport, relative artifact paths, backend
URL, architecture, and checksums. Make targets and the Windows PowerShell entrypoint
delegate release builds to this command so CI and local builds share one policy.

CLI release metadata has one source in `backend/internal/releaseinfo`. The global
help, `bridra version`, generated dependency versions, and release linker flags
read the same CLI/framework version contract. Human-readable and schema-versioned
JSON output expose the framework/template/protocol versions, source commit, build date,
toolchain target, module install path, and Dart compatibility constraint.

`backend/cmd/bridra-release` cross-compiles static amd64/arm64 binaries for macOS,
Linux, and Windows. It removes host paths and VCS stamping, injects explicit
release metadata, writes deterministic `tar.gz`/`zip` archives, and emits one
sorted `SHA256SUMS` plus a versioned `manifest.json`. The source commit date is
the archive timestamp; signing and publishing remain separate release steps.

The schema describes the transport boundary only. Domain Models, Services,
Controllers, provider composition, connection policy, and UI behavior remain
developer-owned because they contain application decisions rather than mechanical
wire mappings. Scaffolds provide compiling starting points, not those decisions.

## Application lifecycle

`framework.Application` owns one typed `Config`, `Container`, and `Router`.
Before construction, `ConfigLoader` resolves declared settings from ordered
`ConfigSource` values. Defaults, environment, and runtime/CLI overrides share one
typed decode/validation path; errors are aggregated. Config provenance is retained
for inspection, while secret keys redact their values and unsafe validation text.

Service Providers first `Register` services and bindings, then `Boot` middleware
and routes. A named `ProviderManifest` is the deterministic discovery boundary:
there is no filesystem or reflection scan, duplicate names fail before lifecycle
execution, and provider names are preserved in startup errors. Provider ordering is
explicit: eager `Provide` factories may resolve only dependencies registered
earlier, while lazy bindings may resolve any dependency registered before their
first use. Config remains mutable during registration and becomes frozen after
every provider boots successfully.

Register and Boot are intentionally fail-stop rather than transactional. A provider
may already have changed the Container or Router before returning an error, so any
provider failure permanently fails that Application instance. Later lifecycle calls
return the original failure without executing providers again; callers must build a
new Application to retry startup. The application bootstrap still invokes Shutdown
after startup failure so partially initialized resources are not abandoned.

Providers that acquire resources implement `TerminableServiceProvider`. Shutdown
executes them once in reverse registration order, including the provider whose
Register failed and providers registered but not yet booted. Concurrent callers
coalesce onto one shutdown run; provider failures are collected rather than stopping
later cleanup and remain inspectable through `ApplicationShutdownErrors`. A completed
shutdown is terminal for the Application. Both stdio and HTTP entrypoints stop their
transport before invoking this lifecycle.

The mixed eager/lazy model is deliberate: startup-critical services can fail fast,
while singleton, transient, and scoped bindings support larger applications without
reflection. Typed aliases connect interface keys to concrete services, and resolution
stacks reject circular dependencies. Application routers create a fresh Scope for
every dispatch and expose it through `Context.Scope()`. The starter accepts additional
providers through `app.Build`, while transport entrypoints can use
`app.BuildFromSources` for environment/runtime precedence.

## Database foundation

`DatabaseServiceProvider` adapts an application-owned `database/sql` pool to the
framework lifecycle. Register places a typed `Database` in the Container, Boot calls
`PingContext` with an optional deadline, and reverse shutdown closes the pool after
higher-level providers finish. Bridra deliberately does not select or bundle a SQL
driver; applications import the driver appropriate to their deployment.

`Database.WithinTransaction` begins one standard-library transaction and places it in
the callback context. Repositories call `Database.Executor(ctx)` for every operation;
it returns the active `*sql.Tx` inside that callback and the root `*sql.DB` otherwise.
This preserves explicit constructor injection while avoiding transaction parameters
through every Service and Repository method.

A callback error rolls back and remains the primary error. A rollback failure is joined
without hiding either cause; a commit failure has its own stable sentinel. A panic
attempts rollback and re-panics the original value. Nested transaction calls fail
explicitly because Database v0.1 does not claim savepoint semantics.

Database v0.1 is a connection and transaction boundary, not an ORM. It does not include
a query builder, model persistence, migrations, schema inspection, read/write routing,
or distributed transaction coordination.

## Migration runner

`MigrationRunner` owns an explicit, version-sorted registry. `MigrationServiceProvider`
resolves `DatabaseKey` and registers the Runner, but neither Register nor Boot changes
the schema. A CLI or deployment command must explicitly call `Migrate`, `Status`, or
`Rollback` after application construction.

`MigrationStore` keeps history separate from execution policy. The included
`SQLMigrationStore` creates a small `bridra_migrations` ledger containing immutable
version, name, and batch values. Question-mark placeholders support SQLite/MySQL-style
drivers, while dollar placeholders support PostgreSQL-style drivers. Table identifiers
are validated before being interpolated; all record values remain bound parameters.

Pending Migrations execute in ascending lexical version order. Each successful command
invocation receives one new batch number, while every Migration commits independently;
therefore a later failure retains earlier successful Migrations and reports that partial
result. Rollback validates the whole latest batch first, then reverts it in descending
version order. Missing definitions, renamed applied Migrations, duplicate history, and
missing Down callbacks fail before schema mutation.

Migration callbacks and their history mutation share one `Database` transaction by
default. Panic values become typed failures and trigger rollback. Drivers without useful
transactional DDL may set `DisableTransaction`, accepting that schema mutation and ledger
updates can no longer be atomic. Migration v0.1 has no savepoints, pretend/dry-run mode,
step-count rollback, schema dump, migration locking, or distributed deployment lease.

## Routing composition

The Router stores handlers together with route-local middleware and method
policies. Named middleware groups can be installed globally or expanded into a
Route Group. Route Groups compose dot-separated prefixes, inherit middleware and
policies through nesting, and snapshot that configuration when a route is
registered.

Dispatch builds the pipeline in this order:

```text
global middleware -> route-group middleware -> group policy -> method policy -> controller
```

Policies return normal framework errors and never call the Controller after a
rejection. Global recovery and logging middleware remain outside the route pipeline,
so they also observe policy failures and unknown methods.

## Event dispatching

`Application` owns one `EventDispatcher` and registers it as the
`EventDispatcherKey` Container instance. Service Providers register exact typed,
named listeners; Controllers and Services dispatch with their existing
`context.Context`, preserving request cancellation and deadlines.

Event v0.1 is deliberately synchronous. A dispatch snapshots listeners, invokes
them in registration order, and stops on the first listener error. The error keeps
both `ErrEventDispatchFailed` and the original cause for `errors.Is`. A listener may
return `ErrStopEventPropagation` for a successful early stop. Concurrent dispatch
and registration are safe, but Listener implementations remain responsible for
protecting their own mutable state.

The application sample emits `GreetingCreated` after the Greeting Service returns.
No event crosses the RPC wire or becomes a Job automatically. `ListenQueued` is an
explicit typed bridge: its mapper runs synchronously with Event dispatch, then
enqueues the mapped Job for independent worker execution. Mapping and enqueue
failures remain in the Event error chain, so a caller never receives a false success
when background work was not accepted.

## Background job queue

`JobQueue` maps each exact Go Job type to one named Handler. Registration happens
during the Service Provider Register phase and freezes when workers Start during
Boot. `DispatchJob` uses a bounded channel for backpressure. `DispatchJobAfter` and
`DispatchJobAt` place accepted delayed Jobs in a due-time heap with a separate
capacity-sized admission bound. Configurable workers execute ready Jobs with a
Queue-owned context and optional per-attempt timeout.

Each Handler may declare a maximum attempt count and fixed retry backoff. Errors,
timeouts, and recovered panics share one retry path. A successful later attempt
produces no failure notification; exhaustion reports the attempt metadata while
preserving the retry sentinel, execution sentinel, and final original cause.
Retrying Handlers own idempotency because the Queue cannot infer which side effects
completed before an error.

`QueueServiceProvider` owns the Queue lifecycle. Its Terminate hook atomically
stops new dispatches, waits for in-flight enqueue operations, promotes pending
delayed Jobs without waiting for their due times, closes the work stream, and waits
for workers to drain every accepted Job. A caller timeout only stops that wait; the
Queue continues draining and exposes the same completion to later callers.

The Queue is intentionally in-memory and single-process. Scheduled times and retry
state are not durable after a crash, and distributed workers require explicit
storage and delivery semantics rather than extending the in-memory channel
implicitly.

## Task scheduling

`Scheduler` owns one independent loop per named Task. Fixed-delay loops wait one
interval after each completed invocation. Cron loops calculate their next wall-clock
occurrence from a five-field expression and the configured Scheduler time zone.
Missed cron occurrences are skipped. Both modes run the Task synchronously with an
optional timeout, making same-Task overlap impossible without global serialization;
different Tasks can still run concurrently. Errors and recovered panics are reported
through a typed failure callback and do not stop later runs.

The Scheduler Service Provider starts loops during Boot and stops them during reverse
shutdown. Provider order is deliberate when Tasks dispatch Jobs:

```text
Register: resource providers -> Queue -> Scheduler -> application Tasks
Shutdown: application Tasks -> Scheduler -> Queue -> resource providers
```

This prevents new scheduled Jobs before the Queue drains and keeps lower-level
resources alive until queued work completes. A shutdown caller timeout stops only
that wait; an invocation already running continues under its Task timeout.

The Scheduler is process-local. It has no persistent schedule state, missed-run
catch-up, distributed leader election, or cross-process overlap lock.

## Transport selection

`packages/bridra_flutter/lib/src/connector/backend_connector.dart` uses
conditional imports so Web never imports
`dart:io`.

| Runtime | Default |
| --- | --- |
| Windows, macOS, Linux | bundled sidecar |
| Android emulator | `http://10.0.2.2:8080/rpc` |
| iOS Simulator | `http://127.0.0.1:8080/rpc` |
| Web | `http://127.0.0.1:8080/rpc` |
| Any platform with `BRIDRA_BACKEND_URL` | configured HTTP endpoint |

Physical mobile devices require a LAN-reachable development URL or a deployed
HTTPS endpoint.

The public default connector is exercised on both conditional-import branches.
Desktop IO tests discover and start a real Go Sidecar through
`BRIDRA_SIDECAR_PATH`; a Chrome runner verifies that the Web build selects
`HttpRpcClient` without importing `dart:io`. Executable discovery delegates to a
pure ordered resolver so environment, bundle, build, backend fallback, and
Windows path behavior can be tested without launching a process.

`DesktopSingleInstance` coordinates the Flutter application process before it
creates a gateway or Sidecar. A stable reverse-domain application identity maps
to a per-user ownership file. The primary holds an exclusive operating-system
file lock, binds an ephemeral IPv4 loopback socket, and writes authenticated
connection metadata beside the lock. A later process cannot take the lock, so it
reads that metadata, forwards a bounded typed activation, waits for
acknowledgement, and exits. The random 256-bit token prevents an unrelated local
process from accidentally speaking the activation protocol.

File locks and listening sockets are released by the operating system on process
death. Stale metadata is therefore safe: the next process takes the released
lock, replaces the metadata, and becomes primary. Acquisition retries the
lock/forward decision for a bounded startup window so concurrent launches do not
both become primary. The API is root-isolate only because Dart documents POSIX
file locks as process-scoped.

`SidecarClient` owns deployed desktop process recovery. An unexpected exit or
terminal transport failure fails the active request set without replay, because
the Go process may already have committed side effects. Replacement processes
start under a configurable, bounded exponential-backoff policy and must answer
`system.health` before queued new calls are written. Call timeout and
cancellation continue while recovery is pending. Exhaustion produces one stable
typed connection error, and closing the client cancels both the backoff wait and
any replacement health check.

The Go Sidecar owns the complementary orphan-protection boundary through
`framework.ParentProcessContext`. It captures the launching process identity
before serving RPC and cancels the server plus Application lifecycle when that
process exits. Linux combines the current parent relationship with the
`/proc/<pid>/stat` start identity, macOS registers an `EVFILT_PROC` `NOTE_EXIT`
event, and Windows waits on a `SYNCHRONIZE` process handle. Stdin EOF remains the
normal shutdown path; parent observation is an independent guard for forced
termination or inherited pipe handles.

## Shared protocol

- `system.health` is the startup handshake and reports `frameworkVersion` plus
  `protocolVersion`.
- Framework SemVer and wire protocol version evolve independently.
- `protocolVersion` changes only for incompatible wire-contract changes.
- Request IDs correlate replies and let Flutter keep concurrent calls pending.
  The stdio Go server dispatches up to eight calls concurrently by default;
  `Server.MaxConcurrentRequests` can set a different positive bound. Up to 64
  additional calls wait in the default bounded queue; overflow receives a
  `server_busy` error. HTTP handlers use the Go HTTP server's concurrency.
- `rpc.cancel` is a reserved stdio control method. It cancels the matching
  request context only when its request ID and launch token both match.
- Methods declared with `stream: true` produce ordered `data`, `progress`, and
  terminal `complete` frames. HTTP transports encode them as NDJSON and flush
  each frame.
- Sidecar streams use an authenticated per-request credit window. The Flutter
  consumer sends reserved `rpc.stream_ack` control messages only after delivery;
  the Go producer blocks when all credits are in flight. The bounded window
  prevents an idle consumer from creating an unbounded response queue while
  leaving other request workers available.
- Request and response `file` fields serialize a short-lived descriptor rather
  than file bytes. The descriptor carries a random capability ID, safe display
  name, media type, byte count, SHA-256 digest, and expiry.
- RPC error `code` values are stable API; `message` is human-readable.
- Params reject unknown fields to catch client/backend schema drift.
- Request bodies and stdio lines are limited to 4 MiB.
- Go stdout is RPC-only in sidecar mode; all logs use stderr.

## Desktop process model

- Flutter owns exactly one Go child process per gateway.
- Every launch receives a random 256-bit token.
- Closing stdin requests a graceful exit; signals are fallback cleanup.
- Parent-process death cancels the stdio server and then runs reverse-order
  Application shutdown before the Sidecar exits.
- Unexpected exits and malformed stdout fail all pending calls.
- Go drains accepted requests after stdin closes, serializes concurrent replies,
  and may return them out of order. Flutter correlates replies by ID and ignores
  late timeout replies.
- Frames within one stream have strictly increasing sequence numbers. Flutter
  rejects gaps, duplicates, malformed progress, and missing completion frames as
  protocol failures.
- Flutter timeouts and explicit `RpcCancellationToken` cancellation send the
  reserved control method so cooperative Go handlers can stop work.
- Sidecar download references contain only paths created inside the
  Sidecar-owned temporary root. Flutter streams, verifies, and deletes each
  managed download; arbitrary application paths are never exposed by the Go
  staging API.
- Sidecar uploads are first written to a bounded Flutter-owned staging file,
  verified locally, then copied and verified into the Sidecar-managed transfer
  store through the reserved `rpc.file_upload` control method.

The launch token prevents accidental messages outside the parent's launch
context. It is not an isolation boundary against another process running as the
same OS user.

## HTTP model

- One RPC request is sent as one JSON `POST /rpc`.
- The Go server requires the same token metadata as the sidecar router.
- Non-browser native requests are not subject to CORS.
- Browser origins are denied unless the server has a matching CORS origin or
  the explicit development wildcard.
- The server has read, write, header, idle, and graceful-shutdown timeouts.
- `GET /rpc/files/<capability>` streams a staged file with `no-store`,
  `nosniff`, and byte-range support. Interrupted reads retain the random
  256-bit capability for retry; a complete response consumes it.
- Authenticated `POST /rpc/files/` creates an upload capability. `PATCH`
  appends only at the server-confirmed offset, while `HEAD` recovers that
  offset after an interrupted request. The completed descriptor is consumed
  through the application request.
- Flutter uses an abortable HTTP request for timeout and explicit cancellation,
  which cancels the request context observed by the Go Router.
- A real integration test starts the compiled Go server on an ephemeral port
  and exercises the full Flutter-to-Go pipeline.

Both file paths retain transport backpressure and verify byte count plus
SHA-256 at completion. They are an out-of-band resumable file transport, not
binary RPC framing, shared memory, client/bidirectional RPC streaming, or
durable object storage.

The [transport performance evaluation](TRANSPORT_PERFORMANCE.md) keeps JSON as
the control plane and managed files as the bulk-data path. A length-prefixed
pipe benchmark shows a useful theoretical binary-frame ceiling, but current
measurements do not justify a protocol change or three platform-specific shared
memory implementations. Binary framing is reconsidered only after a real
cross-platform workload misses an explicit performance budget.

Both transports can handle requests concurrently. Services added to the starter
must therefore be concurrency-safe.

## Network security

- Android Debug and Profile overlay a cleartext-enabled network security
  config for emulator/LAN development.
- Android Release uses a cleartext-disabled network security config.
- iOS Debug uses `Info-Debug.plist` for local HTTP and local-network permission.
- iOS Profile and Release use `Info.plist` with default App Transport Security.
- Web release configuration should use HTTPS and an exact allowed origin.

The default `dev-token` and a token compiled into Web assets are not production
credentials. A production system needs TLS and user/session authentication,
usually at the Go service or a trusted reverse proxy.

## Lifecycle differences

Desktop Flutter owns the Go process, so closing the gateway also closes its
backend. Mobile and Web own only an HTTP client; reconnecting the Flutter UI
does not restart the deployed Go server.

The UI therefore exposes a transport-neutral reconnect action. Process-specific
shutdown behavior stays inside `SidecarClient`.

## Distribution

- Linux output is architecture-specific and receives a matching native Go
  executable under `libexec`.
- macOS output is universal; Xcode builds, combines, and signs the arm64 and
  x86_64 sidecar before signing the application.
- Windows CMake maps the Flutter host architecture to Go `amd64` or `arm64` and
  installs the matching `.exe` under `libexec`.
- Android and iOS package only Flutter; they point at a separately deployed Go
  HTTP service.
- Web produces static assets and also points at the Go HTTP service.

Installer formats, store credentials, product identifiers, notarization, and
backend deployment infrastructure remain product-level choices.

## Verification boundary

`make verify` runs protocol, router, middleware, controller, service, stdio,
HTTP, external-package Go public API, package, public connector, Chrome,
typed-client, widget, and two real executable integration paths.

`make coverage` produces one Go atomic profile plus direct Flutter runtime and
combined app/runtime LCOV profiles. The versioned threshold configuration names
the protected surfaces; a Go checker emits a Markdown report and fails after
reporting any regression below its floor. CI uploads the profiles and summary so
coverage failures remain inspectable.

The GitHub Actions workflow is configured to build:

- Linux, Android, and Web on Ubuntu
- macOS and iOS Simulator on macOS
- Windows on Windows

Verify runs on Pull Requests and the merged `main` commit. The protected tag
workflow accepts only the current `main` commit after its Verify run succeeds,
then performs release-specific version, package, and artifact checks without
repeating the full cross-platform suite.

Native store submission and physical-device end-to-end tests require product
credentials and deployment environments, so they are deliberately outside the
starter's repository checks.
