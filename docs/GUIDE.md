# Bridra complete guide

> A Go-powered application framework for Flutter, by Cluion.

Source: [github.com/cluion/bridra](https://github.com/cluion/bridra)

Go module: `github.com/cluion/bridra/backend`

License: [MIT](../LICENSE), Copyright (c) 2026 Cluion

Bridra 0.12 is a six-platform framework starter with a typed
Flutter gateway and a Laravel-inspired Go application pipeline. Windows,
macOS, and Linux bundle Go as a child-process sidecar; Android, iOS, and Web
use the same backend through an HTTP RPC adapter.

```text
Flutter UI -> typed gateway -> RPC client
                              |-- Desktop: bundled Go process over stdin/stdout
                              `-- Mobile/Web: Go HTTP server over JSON
                                                   |
                                                   v
                              Config -> Service Providers -> Container
                                                   |
                                                   v
                              Router -> Middleware -> Controller -> Service
```

The application entrypoint is `lib/main.dart`. Both transports use the same
versioned request, response, error, and health-handshake contract. Framework
SemVer (`0.12.0`), the Project Template protocol baseline (`1`), and each
application's internally consistent RPC protocol evolve independently.

## Platform support

| Platform | Flutter runner | Go transport | Release output |
| --- | --- | --- | --- |
| Windows | Yes | bundled sidecar | Windows bundle |
| macOS | Yes | bundled universal sidecar | macOS app |
| Linux | Yes | bundled native sidecar | Linux bundle |
| Android | Yes | HTTP backend | APK |
| iOS | Yes | HTTP backend | unsigned iOS app |
| Web | Yes | HTTP backend with CORS | static Web bundle |

Desktop launches work without a separately deployed server. Mobile and Web
cannot use the desktop child-process model, so the Go HTTP server is deployed
or run separately.

## Included

- FVM-pinned Flutter 3.44.6 and Dart 3.12.2
- reusable public Go `framework` and `bridra_flutter` packages
- typed Config and Container with eager instances, lazy singleton, transient,
  scoped, and aliased services
- deterministic Service Provider Register, Boot, and reverse-order shutdown
  lifecycle with aggregated cleanup errors
- typed background Jobs with named handlers, in-memory or durable local queues,
  configurable workers, retries, timeouts, failure retention, and graceful shutdown
- typed queued Event listeners that map synchronous domain Events into background Jobs
- named fixed-delay and cron scheduled Tasks with non-overlap, time zones,
  timeouts, failure reporting, panic recovery, and Application lifecycle integration
- typed Flutter gateway with framework- and protocol-version health handshake
- versioned JSON RPC schema with generated Go DTOs/route constants and Dart
  typed client
- `bridra make` scaffold generator for Controller, Service, Middleware, Request,
  Model, Response, Provider, and Test components, with companion tests and
  transactional collision handling
- `bridra dev` orchestration for desktop Sidecar and mobile/Web HTTP development,
  with readiness checks and process-tree cleanup
- `bridra build` orchestration for six targets and three build modes, with host
  validation, desktop Sidecar bundling, release HTTP policy, and SHA-256 manifests
- `bridra version` human/JSON metadata and deterministic installable CLI archives
  for macOS, Linux, and Windows on amd64/arm64
- versioned project upgrade contract with a read-only planner, ordered
  cross-version paths, missing-hop refusal, opt-in automatic apply, full
  verification, and managed-file rollback
- direct IO/Web public connector tests, executable discovery tests, and enforced
  Go/Flutter coverage non-regression floors
- interchangeable stdio and HTTP RPC clients
- request correlation, bounded concurrent stdio dispatch, graceful drain,
  timeouts, transport errors, protocol validation, and idempotent shutdown
- Go router with global/named middleware, nested route groups, method policies,
  controllers, dependency-injected services, and stable RPC errors
- named Request DTOs, `BindAndValidate`, structured field violations, domain
  Models, and Response DTOs
- bounded request bodies and configurable browser CORS
- HTTP Bearer authentication, propagated Principals, exact permission policies,
  stable 401 transport failures, and stable RPC `forbidden` errors
- bounded per-identity HTTP rate limiting with typed 429 retry guidance
- request IDs, redacted JSON audit events, fixed-cardinality HTTP metrics,
  observer hooks, and explicit server timeout/header limits
- real Flutter-to-Go integration tests for both process and HTTP transports
- Windows x64/arm64, Linux x64/arm64, and universal macOS sidecar packaging
- Android, iOS, and Web runners with development and release network policies
- GitHub Actions workflow configured to build all six platforms

## Requirements

All platforms require Go 1.25 or newer and FVM 4.x. Install FVM once from the
[official installation guide](https://fvm.app/documentation/getting-started/installation),
then bootstrap the pinned SDK and both workspace packages:

```bash
make setup
make doctor
```

`make doctor` runs the Bridra CLI and verifies Go 1.25+, FVM 4+, the exact
Flutter version pinned by `.fvmrc`, and the current host architecture. Missing
desktop build tools are reported as warnings so backend and contract work remain
available; use strict mode before preparing a desktop release:

```bash
cd backend && go run ./cmd/bridra doctor --root .. --strict
```

`make setup` reads the committed `.fvmrc`, installs Flutter 3.44.6, and resolves
the root application plus `packages/bridra_flutter`. FVM stores SDKs outside this
repository; the local `.fvm/` link and package lockfiles are ignored.

Additional platform toolchains:

- macOS and iOS: full Xcode with the matching platform and Simulator runtime
- Android: Android Studio, Android SDK, and Java 17
- Web: Chrome for the browser-specific `make verify` test and local `web-run`
- Linux: `clang`, `cmake`, `ninja-build`, `pkg-config`, `libgtk-3-dev`, and C++
  development headers
- Windows: Visual Studio with the **Desktop development with C++** workload

If Xcode reports that an iOS platform is not installed, install it from Xcode
Settings > Components or run:

```bash
xcodebuild -downloadPlatform iOS
```

CocoaPods is only required after adding a Flutter plugin that still uses it;
this starter currently uses Flutter's Swift Package Manager integration.

## Install the Bridra CLI

Install the exact CLI version through Go:

```bash
go install github.com/cluion/bridra/backend/cmd/bridra@v0.12.0
bridra version
bridra version --json
```

GitHub Releases will also provide `tar.gz` archives for macOS/Linux and `zip`
archives for Windows on amd64 and arm64. Verify an archive against
`SHA256SUMS` before installing it. The accompanying `manifest.json` records the
schema, version, MIT license, module tag, source commit, reproducible build date,
target, size, and SHA-256 for every artifact. Every archive includes the CLI
executable and a copy of the MIT `LICENSE`.

Releases containing `bridra_VERSION_cli.spdx.json` also publish an SPDX 2.3
dependency inventory and GitHub Sigstore attestations. After downloading an
archive, verify both its build provenance and SBOM association:

```bash
gh attestation verify bridra_VERSION_OS_ARCH.tar.gz \
  --repo cluion/bridra \
  --signer-workflow cluion/bridra/.github/workflows/release.yml
gh attestation verify bridra_VERSION_OS_ARCH.tar.gz \
  --repo cluion/bridra \
  --signer-workflow cluion/bridra/.github/workflows/release.yml \
  --predicate-type https://spdx.dev/Document/v2.3
```

Use the matching `.zip` filename on Windows. Attestations establish which
workflow and source commit produced the bytes; they do not replace operating
system code signing or notarization.

Upgrade by installing an explicit newer version, then inspect it before updating
projects:

```bash
go install github.com/cluion/bridra/backend/cmd/bridra@v0.12.0
bridra version --json
bridra upgrade --plan --to 0.12.0 --root /path/to/project
```

Bridra does not silently auto-update the CLI. Project compatibility,
migration, deprecation, and rollback rules are documented in
[UPGRADING.md](UPGRADING.md). Maintainer release steps are documented in
[RELEASING.md](RELEASING.md). The `0.9.0` to `0.12.0` path contains the
automatic `0.10.0` HTTP-security step, the `0.10.1` diagnostics and
upgrade-planner patch, the runtime-neutral `0.11.0` supply-chain release, and
the `0.12.0` bounded-stdin Sidecar launch update.
Existing application-owned server entrypoints are not overwritten; adopt the
production controls deliberately using
[HTTP_SECURITY.md](HTTP_SECURITY.md). The planner validates each application's
metadata, schema, and generated Go/Dart contract together. An application
protocol newer than the target Project Template baseline remains valid and is
preserved by framework-only migrations. Projects upgrading from earlier
releases must still complete any manual steps already in their ordered migration
path.

## Prepare one framework release version

Framework maintainers enter the public SemVer once:

```bash
make release-prepare VERSION=0.12.0
make release-check VERSION=0.12.0
make release-check VERSION=0.12.0 FINAL=1
```

`release-prepare` synchronizes the root `VERSION`, Go Framework and CLI metadata,
the `bridra_flutter` package, repository project metadata, changelogs, and live
release documentation. It refreshes Flutter dependency metadata afterward.
Protocol, Project Template, and project metadata schema integers remain
independent and change only when their compatibility contracts change.

The command prepares a reviewable change only. It never creates or pushes a Git
tag, publishes to pub.dev, or creates a GitHub Release. Windows maintainers use
`.\tool\windows.ps1 -Task release-prepare -Version 0.12.0` and the corresponding
`release-check` task. The final check rejects a release while either changelog is
still marked `Unreleased`; on Windows, add `-Final`.

## Build CLI release artifacts

Framework maintainers build all six CLI targets from one clean source commit:

```bash
make cli-release
```

Windows maintainers may run:

```powershell
.\tool\windows.ps1 -Task cli-release
```

The release packager builds with `CGO_ENABLED=0`, `-trimpath`, disabled VCS
stamping, an empty Go build ID, and ldflag-injected version/commit/date metadata.
Archive timestamps come from the source commit date, so identical inputs produce
identical archives and checksums. Outputs are written under `build/bridra/cli/`.
Each version has its own directory, such as `build/bridra/cli/0.12.0/`, so stale
assets from an earlier release cannot be uploaded accidentally.

## Verify

```bash
make verify
```

This checks that publishable package license copies and generated contracts are
current, then runs Go formatting checks, `go vet`, dedicated external-package
public API tests, race-enabled Go tests, root/package/Chrome Flutter tests,
static analysis, and real process and HTTP round trips through the Go
middleware/controller/service pipeline.

Generate coverage profiles and enforce the committed baseline:

```bash
make coverage
```

Thresholds live in `tool/coverage_thresholds.json`. The command checks six Go
framework/tooling surfaces plus the Flutter runtime and combined app/runtime,
then writes `coverage/summary.md`. CI includes the same table in its job summary
and retains the raw profiles as a workflow artifact.

Run the slower Runtime resilience suite before changes to Sidecar lifecycle,
RPC parsing, Queue, Scheduler, or persistent Stores:

```bash
make runtime-stress
```

It combines native Go fuzzing with repeated race-enabled lifecycle,
concurrency, shared-store contention, Sidecar crash recovery, and bounded
goroutine, heap, RSS, file-descriptor, and orphan-process checks. The defaults
and scheduled CI policy are documented in
[Runtime stress verification](RUNTIME_STRESS.md).

Individual build targets still accept `FLUTTER=flutter DART=dart` overrides for
special environments. The complete `make verify` flow intentionally includes
`bridra doctor` and therefore enforces the repository's FVM policy.

## Create a diagnostic support bundle

```bash
bridra diagnose
```

This creates a new redacted ZIP under `build/diagnostics/` with toolchain,
host, and project version-contract information. It does not include environment
variables, credentials, requests, logs, source files, project identity, or
absolute paths.

Desktop applications can export `SidecarClient.diagnostics().toJson()` and pass
that file through `--runtime`; the CLI strictly validates the bounded schema
before including lifecycle counters, restart events, health checks, and error
types. See [Runtime diagnostics and crash reporting](RUNTIME_DIAGNOSTICS.md) for
the export example, privacy boundary, and `CrashReporter` integration.

## Community and governance

Bridra uses committed policies and repository templates to keep maintenance and
release decisions reviewable:

- [CONTRIBUTING.md](../CONTRIBUTING.md) defines development, testing, and Pull
  Request expectations.
- [SECURITY.md](../SECURITY.md) defines private vulnerability reporting, response
  targets, disclosure, and the current supported-version status.
- [SUPPORT.md](../SUPPORT.md) separates framework support from application and
  deployment responsibilities.
- [GOVERNANCE.md](../GOVERNANCE.md) defines owner, maintainer, release manager, and
  security responder authority.
- [CODE_OF_CONDUCT.md](../CODE_OF_CONDUCT.md) defines community expectations and
  confidential conduct reporting.
- [UPGRADING.md](UPGRADING.md) and [RELEASING.md](RELEASING.md) define upgrade,
  compatibility, release evidence, and rollback contracts.

Bug and feature Issue forms plus the Pull Request template live under `.github/`.
Every public release requires a clean reviewed commit, passing hosted CI, working
private vulnerability reporting, finalized changelogs, and explicit repository
owner authorization. A green CI run alone does not authorize a tag or package
publication.

## Create a Bridra project

`bridra create` builds a new six-platform Flutter application and Go backend in
a staging directory, verifies the generated Go consumer, resolves Flutter
dependencies, and only then atomically publishes the destination:

```bash
bridra create hello_app \
  --module example.com/your-name/hello-backend \
  --directory ../hello_app
```

Framework contributors can explicitly use the current checkout while developing
or testing unreleased changes:

```bash
cd backend
go run ./cmd/bridra create hello_app \
  --module example.com/your-name/hello-backend \
  --bridra-root .. \
  --directory ../hello_app
```

The generated starter includes:

- Flutter runners for Android, iOS, Linux, macOS, Windows, and Web
- Go Sidecar and HTTP server entrypoints
- Middleware, Controller, Service, Model, and Response examples
- schema-driven Go/Dart contracts
- FVM, Makefile, tests, and analysis configuration

Project names must use `lower_snake_case`; the application Go module remains
caller-owned. By default, generated `go.mod` and `pubspec.yaml` declare the
running CLI's compatible Bridra versions. `--bridra-root` preserves those version
constraints and adds explicit Go `replace` and Dart `dependency_overrides`
entries. This local mode is for framework development; generated applications
should otherwise consume versioned public packages.

After creation:

```bash
cd ../hello_app
make doctor
make verify
```

Generated projects record project metadata schema, framework, template, and RPC
protocol versions in `.bridra/project.json`. `bridra upgrade` defaults to a
read-only plan; `--to` selects a known target and the CLI refuses incomplete
cross-version paths without modifying any project file. `--apply` is available
only when every step is automatic; it synchronizes Go, Flutter, lockfiles, and
metadata, runs full verification, and restores the managed files on failure.

## Scaffold application components

Every Bridra project commits `.bridra/project.json`. This metadata identifies the
project and its framework import without tying generated projects to a Cluion
repository path. The CLI uses it to render explicit, editable Go application
files:

```bash
cd backend
go run ./cmd/bridra make controller User --root ..
go run ./cmd/bridra make service Billing --root ..
```

Available kinds are `controller`, `service`, `middleware`, `request`, `model`,
`response`, `provider`, and `test`. Component names use PascalCase. Except for a
generic test, each kind generates both the component and a companion test.

The default behavior rejects the whole operation if any destination already
exists, before writing a file:

```bash
go run ./cmd/bridra make request CreateUser --root ..
```

Use `--force` only when every colliding file in that scaffold should be replaced:

```bash
go run ./cmd/bridra make request CreateUser --root .. --force
```

Generation is staged and published as one transaction. If publishing any file
fails, Bridra restores all files that belonged to that scaffold. The versioned
scaffold manifest has golden, compilation, collision, force, and rollback tests.

## Run a development session

`bridra dev` builds the required Go executable and runs Flutter as one supervised
session. On the current desktop host, auto mode uses the Sidecar transport:

```bash
cd backend
go run ./cmd/bridra dev --root ..
```

Web devices use the local HTTP backend automatically:

```bash
go run ./cmd/bridra dev --root .. --device chrome
```

Mobile and custom devices must receive an explicit backend URL reachable from the
device. The listen port and URL port must match; a LAN URL also requires a non-loopback
listener. For an Android emulator:

```bash
go run ./cmd/bridra dev \
  --root .. \
  --device emulator-5554 \
  --listen 0.0.0.0:8080 \
  --backend-url http://10.0.2.2:8080/rpc
```

The command preserves Flutter stdin/stdout for interactive Hot Reload. Interrupting
the CLI forwards shutdown to every managed process tree, waits up to five seconds,
then force-cleans descendants. Unix uses isolated process groups; Windows uses a new
process group with CTRL_BREAK and `taskkill /T` fallback. An unexpected HTTP backend
exit stops Flutter and returns a typed CLI failure.

Go source watching is enabled by default. Changes to non-test `.go` files, Go module
files under `backend/`, or root Go workspace files trigger a debounced rebuild. The
new executable is built separately, so a compilation failure leaves the current
backend running and another save retries the build. HTTP mode restarts only the Go
server and keeps Flutter running. Sidecar mode restarts Flutter after a successful
build because Flutter owns the Sidecar process; this resets transient UI state. Pass
`--watch=false` to disable Go rebuilds.

The default `dev-token`, wildcard CORS option, and `http://` URL policy are local
development defaults, not production authentication or transport security.

## Build application artifacts

`bridra build` is the shared build policy used by the Make and Windows PowerShell
entrypoints. Desktop targets bundle a Go Sidecar; Android, iOS, and Web compile an
HTTP endpoint into Flutter:

```bash
cd backend
go run ./cmd/bridra build macos --root ..
go run ./cmd/bridra build web --root .. \
  --backend-url https://api.example.com/rpc \
  --token replace-me
```

Targets are `linux`, `macos`, `windows`, `android`, `ios`, and `web`. Use
`--mode debug`, `--mode profile`, or `--mode release`; release is the default.
Linux, macOS/iOS, and Windows desktop outputs must be built on their matching
host. Android and Web can be built on any supported desktop host.

Profile and release HTTP builds require an HTTPS `/rpc` URL and an explicit
token. Debug HTTP builds default to the local emulator/loopback endpoint and
`dev-token`. A desktop build can select HTTP instead of Sidecar by supplying
`--backend-url` and `--token`.

Every successful build writes a token-free manifest to
`build/bridra/<target>-<mode>.json`. It records the stable artifact path,
transport, architecture, content-tree SHA-256, and Sidecar checksum when present.
The iOS artifact is unsigned. The macOS command applies an ad-hoc signature after
embedding its universal Sidecar; distribution signing, notarization, installers,
and store submission remain product release tasks.

## Generate the RPC contract

`schema/bridra.json` is the source of truth for RPC method names, protocol
version, request validation, response fields, and Flutter decoding:

```bash
make generate
make codegen-check
```

`make generate` runs the `bridra generate` CLI and writes deterministic Go and
Dart files. Generated files are committed, marked `DO NOT EDIT`, and compiled by
normal tests. `make verify` fails when the schema and checked-in output differ.

Before accepting an RPC contract change, compare it with the reviewed deployed
or released schema:

```bash
bridra schema check \
  --against path/to/released-schema.json \
  --schema schema/bridra.json
```

Use `--json` for report schema `1` in CI. The command exits non-zero when the
current `protocolVersion` regresses or when a breaking wire change keeps the
baseline protocol. A greater protocol produces `versioned_break`: generated
clients and servers will reject the old contract instead of exchanging
incompatible payloads. The command does not modify either schema or generated
code.

Method removal, unary/streaming changes, request parameter and rule changes,
field shape/nullability changes, and required response/meta field changes are
breaking. New methods and nullable response/meta field additions or removals are
wire-compatible. Ordering and generated Go/Dart identifiers do not affect JSON
wire compatibility; identifier renames remain application source migrations
that must be reviewed separately.

Codegen v0.2 supports string, integer, and boolean fields, scalar arrays
(including RFC 3339 date-time arrays), nullable fields, string enums, nested
objects, and generated minimum-length/maximum-length/enum validation.
Non-nullable generated Request fields are required on the wire and reject
explicit `null`; nullable fields are omitted from Flutter JSON when absent.
`trim` normalizes the Go DTO before validation and before the Controller receives
it. Nested presence and validation errors retain dot-separated paths such as
`profile.nickname`. Add richer types through the schema and generator together
instead of hand-editing output.

## Run desktop

```bash
make run
```

`run` selects the current desktop platform. Explicit commands are:

```bash
make macos-run
make linux-run
make windows-run
```

Generated applications acquire a desktop single-instance lease before
`runApp`. The first launch becomes primary. A later launch forwards its
command-line arguments, including file paths and deep-link URIs, through
`DesktopSingleInstanceSession.activations`, receives an acknowledgement, and
exits before calling `runApp`.

Use a stable reverse-domain application identity and attach product routing in
the activation handler:

```dart
Future<void> main([List<String> arguments = const []]) async {
  WidgetsFlutterBinding.ensureInitialized();
  if (DesktopSingleInstance.isSupported) {
    final instance = await DesktopSingleInstance.acquire(
      applicationId: 'com.example.my_app',
      arguments: arguments,
    );
    if (!instance.isPrimary) return;
    instance.activations.listen((activation) {
      openFilesAndLinks(activation.arguments);
    });
  }
  runApp(const MyApp());
}
```

Call `acquire` once from the root isolate. Linux and macOS file locks are
process-scoped, so multiple-isolate acquisition is unsupported. The operating
system releases ownership after a crash; the next launch overwrites stale
connection metadata and becomes primary.

Each desktop launch creates a random 256-bit token and starts one Go child
process. Flutter sends that token as a bounded, versioned first line on stdin,
waits for a token-free readiness marker on stderr, and then uses the remaining
stdin stream for RPC. The token is absent from current Sidecar process
arguments. If an older generated backend rejects this launch mode, Flutter
retries it once through the legacy argument and uses that compatibility mode for
later restarts; regenerate the backend to remove the fallback. Go reserves
stdout for RPC and writes logs to stderr.

The Sidecar also watches the identity of its operating-system parent. If the
Flutter process is force-terminated, the Sidecar cancels active work, runs the
normal application shutdown lifecycle, and exits even when an inherited stdio
handle would otherwise keep it alive. Linux checks the parent process identity
through `/proc`, macOS uses a process `kqueue` event, and Windows waits on the
parent process handle.

If the Sidecar exits unexpectedly, the desktop client fails every in-flight
request without replaying it, then starts a replacement with bounded exponential
backoff. New calls wait for the replacement to pass `system.health`; their own
timeout and cancellation remain active. Three failed restart attempts leave the
client in a stable unavailable state. `close()` cancels pending recovery and
prevents another child from starting.

Windows also has a native entrypoint for machines without GNU Make:

```powershell
.\tool\windows.ps1 -Task run
.\tool\windows.ps1 -Task build
.\tool\windows.ps1 -Task verify
.\tool\windows.ps1 -Task generate
```

## Run the HTTP backend

Android, iOS, and Web need the Go HTTP adapter. Start it in one terminal:

```bash
make backend-serve
```

The development defaults are:

```text
RPC URL:     http://127.0.0.1:8080/rpc
Token:       dev-token
CORS origin: *
```

For a physical phone, listen on the LAN interface and use the Mac or PC's LAN
address in the Flutter build:

```bash
make backend-serve BACKEND_LISTEN=0.0.0.0:8080
```

## Run Web

With the backend running:

```bash
make web-run
```

The release bundle is written to `build/web`. A production build should use an
HTTPS backend and an exact server-side CORS origin:

```bash
make web-build \
  BACKEND_URL=https://api.example.com/rpc \
  BACKEND_TOKEN=replace-me

make backend-serve \
  BACKEND_LISTEN=0.0.0.0:8080 \
  BACKEND_TOKEN=replace-me \
  BACKEND_CORS_ORIGIN=https://app.example.com
```

## Run Android

The Android emulator automatically uses `http://10.0.2.2:8080/rpc`. Development
and profile variants allow cleartext HTTP; release builds explicitly block it.

```bash
make android-run DEVICE=<flutter-device-id>
make android-emulator-smoke
make android-build \
  BACKEND_URL=https://api.example.com/rpc \
  BACKEND_TOKEN=replace-me
```

`android-emulator-smoke` selects the only running Emulator unless `DEVICE=<id>`
is provided. It starts a temporary Go backend on host loopback and reaches it
through Android's `10.0.2.2` host alias. The integration test verifies Health,
Greeting, ordered Streaming／Progress, interrupted and resumed downloads and
uploads, forces the backend offline, then repeats the complete flow after
reconnect. The script stops only its temporary backend; Emulator lifecycle stays
with Android Studio or the CI runner.

For a physical device, pass a reachable LAN or HTTPS URL:

```bash
make android-run \
  DEVICE=<flutter-device-id> \
  BACKEND_URL=http://192.168.1.10:8080/rpc
```

## Run iOS

The iOS Simulator defaults to `http://127.0.0.1:8080/rpc`. Only the Debug
configuration has a local HTTP exception. All configurations use the same
local-network permission purpose. Profile and Release allow local-network loads
but keep App Transport Security enabled for public-network traffic.

```bash
make ios-run DEVICE=<flutter-device-id>
make ios-simulator-build
make ios-simulator-smoke
make ios-device-smoke
make ios-build \
  BACKEND_URL=https://api.example.com/rpc \
  BACKEND_TOKEN=replace-me
```

`ios-simulator-smoke` automatically selects an available iPhone Simulator (or
uses `DEVICE=<flutter-device-id>`), starts a temporary Go HTTP backend, and
asserts real Health, Greeting, ordered Streaming／Progress, and a 60 KiB managed
download through the running iOS app. The download is interrupted after 32 KiB;
Flutter must resume it with `Range: bytes=32768-`, then match its expected name,
media type, byte count, SHA-256, and content. The test also interrupts a 112 KiB
upload after 32 KiB, requires Flutter to recover the stored offset, resumes from
that exact byte, then has Go consume and re-hash the result. The script shuts
down only a Simulator that it booted itself. The Flutter integration phase has
a nine-minute total watchdog and a four-minute no-output watchdog. Override
them with `BRIDRA_IOS_SIMULATOR_TIMEOUT_SECONDS` and
`BRIDRA_IOS_SIMULATOR_NO_PROGRESS_SECONDS`. Set
`BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_DIR` to preserve the Flutter, backend,
Simulator, device, and process snapshots after a failure; hosted CI uploads
that directory before retaining its single reset-and-retry policy.

For this repository, keep developer-only signing outside Git:

```bash
cp ios/Flutter/Local.xcconfig.example ios/Flutter/Local.xcconfig
```

Set `DEVELOPMENT_TEAM` and a unique `BRIDRA_PRODUCT_BUNDLE_IDENTIFIER` in the
ignored local file. `ios-device-smoke` selects the only available physical
iPhone (or uses `DEVICE=<flutter-device-id>`), discovers the Mac's LAN address,
and selects a free port starting at `18081`. It first drives Health and Greeting
through a Debug integration test, stops the backend, verifies the unavailable
state and successful reconnect, preserves the installed app so iOS keeps its
local-network grant, verifies ordered Streaming／Progress before and after the
reconnect, repeats byte-range download recovery and interrupted/resumed upload
with Go-side size/SHA-256 verification, then installs a Profile build and
verifies two cold launches without Flutter tooling. The authenticated smoke-only
routes and fault injection are disabled unless the script passes
`--smoke-stream`, `--smoke-download`, `--smoke-download-resume`, and
`--smoke-upload-resume`. The script uses `flutter drive`, which handles wired
and wireless iPhones, and stops the app and temporary backend when the test
finishes. Keep the iPhone unlocked, in Developer Mode, trusted, and on a network
that can reach the Mac. Accept the one-time Local Network prompt for each new
bundle identifier. Override address discovery with `IOS_DEVICE_HOST=<address>`
when needed.

An iOS Debug app cannot be relaunched from the Home Screen without Flutter
tooling or Xcode. Use Profile for a standalone development launch; Release and
Store signing remain separate application-owned workflows.

A physical device needs a reachable LAN or HTTPS URL:

```bash
make ios-run \
  DEVICE=<flutter-device-id> \
  BACKEND_URL=http://192.168.1.10:8080/rpc
```

`ios-build` creates an unsigned release. Product distribution still requires
your Apple team, bundle identifier, signing, archive, and App Store workflow.

## Build desktop releases

```bash
make macos-smoke
make linux-smoke
make windows-smoke
```

The bundled Go executable locations are:

```text
build/macos/Build/Products/Release/bridra.app/
  Contents/MacOS/libexec/bridra_backend

build/linux/<x64|arm64>/release/bundle/
  libexec/bridra_backend

build/windows/<x64|arm64>/runner/Release/
  libexec/bridra_backend.exe
```

Build Windows and Linux releases on the target OS and architecture. The macOS
build phase produces and ad-hoc signs a universal `arm64`/`x86_64` sidecar.
The `make *-build` and Windows `-Task build` wrappers delegate to `bridra build`.

## Runtime configuration

The Flutter adapter reads compile-time Dart defines:

| Define | Default |
| --- | --- |
| `BRIDRA_BACKEND_URL` | desktop: local sidecar; Android: `10.0.2.2`; iOS/Web: `127.0.0.1` |
| `BRIDRA_BACKEND_TOKEN` | `dev-token` for HTTP transports |

Supplying a backend URL also lets a desktop app use HTTP instead of its bundled
sidecar.

The development token is not production authentication. In particular, values
compiled into a Web app are visible to users. Production deployments should use
HTTPS and a real user/session authentication layer or trusted reverse proxy.

## HTTP authentication and method permissions

`HttpRpcClient` sends its token as an `Authorization: Bearer` credential. The
generated server validates that credential before decoding or dispatching an RPC
request, then adds an authenticated `Principal` to the request Context. Missing,
malformed, or rejected credentials return HTTP `401`, a `WWW-Authenticate: Bearer`
challenge, and the stable `unauthenticated` error code. Authentication-provider
failures are logged server-side and return a generic HTTP `503` without exposing
the provider error.

The starter uses `NewStaticTokenAuthenticator` with the existing backend token so
development and upgrades remain compatible. That adapter identifies one shared
HTTP client and is not user authentication. A production service should set
`HTTPHandler.Authenticator` to an `Authenticator` or `AuthenticatorFunc` that
validates its own session/access token and returns a Principal with the permissions
resolved by the application:

```go
authenticator := framework.AuthenticatorFunc(
    func(ctx context.Context, bearer string) (framework.Principal, error) {
        session, err := sessions.Verify(ctx, bearer)
        if errors.Is(err, ErrSessionNotFound) {
            return framework.Principal{}, framework.ErrAuthenticationFailed
        }
        if err != nil {
            return framework.Principal{}, err
        }
        return framework.NewPrincipal(session.UserID, session.Permissions...)
    },
)

handler := &framework.HTTPHandler{
    Router:        router,
    Authenticator: authenticator,
    AllowedOrigin: "https://app.example.com",
}
```

Use `RequirePermission` on a route or Route Group for exact permission checks:

```go
router.HandleWithPolicies(
    "reports.read",
    reports.Read,
    framework.RequirePermission("reports.read"),
)
```

A missing Principal produces the RPC error `unauthenticated`; an authenticated
Principal without the permission produces `forbidden`. These are application RPC
errors and therefore keep the existing successful HTTP transport envelope instead
of changing it to HTTP `403`. `PrincipalFromContext` lets Controllers and other
policies read the subject without parsing credentials again.

The older `Authenticate` middleware still checks the request-envelope token when
there is no Principal, preserving Sidecar compatibility. A trusted HTTP Principal
satisfies that transport-authentication boundary, so a production user/session
token does not also need to equal the Sidecar secret. Do not treat that local
transport secret, the static HTTP adapter, rate limiting, audit logging, or a
trusted proxy as interchangeable security controls.

### HTTP rate limiting

The reference server also enables `MemoryRateLimiter`. Its default token bucket
allows a burst of 600 requests and replenishes that budget over one minute, with
at most 10,000 active identity keys. Exhausted requests return HTTP `429`, the
stable `rate_limited` error code, and a whole-second `Retry-After` header. One
streaming RPC consumes one token when its HTTP request begins; progress frames do
not consume additional tokens. `HttpRpcClient` maps unary and streaming 429
responses to `RpcRateLimitedException`; its nullable `retryAfter` contains the
server duration when the header is a valid non-negative number of seconds.

The default key is an opaque SHA-256 identity derived from the authenticated
Principal subject. Without a Principal it falls back to the direct socket IP from
`RemoteAddr`. It deliberately ignores `Forwarded` and `X-Forwarded-For`, because
accepting those headers without a trusted-proxy boundary would let clients choose
their own rate-limit identity. A deployment behind a trusted proxy may provide an
explicit `HTTPHandler.RateLimitKey` function after validating its proxy chain.

```go
options := framework.DefaultMemoryRateLimiterOptions()
options.Requests = 120
options.Window = time.Minute

limiter, err := framework.NewMemoryRateLimiter(options)
if err != nil {
    return err
}

handler := &framework.HTTPHandler{
    Router:        router,
    Authenticator: authenticator,
    RateLimiter:   limiter,
}
```

The memory limiter is process-local and bounds its key map to avoid identity-key
memory exhaustion. At capacity, a new identity is denied until an inactive key can
expire. Multi-process or multi-host deployments implement the same `RateLimiter`
interface with an application-owned shared store such as Redis.

Rate limiting runs after successful HTTP authentication and before request-body
decoding. Invalid credential attempts therefore need a separate IP-based control
at the trusted proxy or identity provider; changing an untrusted forwarded header
must never bypass that control.

### HTTP audit, metrics, and tracing

The generated direct HTTP server wraps its complete mux with
`HTTPObservationHandler`. The wrapper generates a fresh `X-Request-ID`, adds it
to the request Context, preserves streaming flushes, and invokes an optional
observer at request start and completion. `HTTPRequestIDFromContext` lets
Controllers, Services, and application logs use the same correlation ID.

The reference server enables `NewJSONHTTPObserver`. Its structured completion
event includes the direct socket IP, RPC method, pseudonymized Principal, status,
stable error code, duration, and response size. It never records the URL path,
query, headers, request/response body, RPC params, credentials, upload token, or
file capability. Protect and retain the resulting security logs according to the
application's privacy and incident-response requirements.

Applications can combine that audit event with fixed-cardinality metrics and a
custom tracing adapter:

```go
audit, err := framework.NewJSONHTTPObserver(os.Stderr)
if err != nil {
    return err
}
metrics := framework.NewHTTPMetrics()
observer := framework.NewHTTPObserverGroup(
    audit,
    metrics,
    applicationTracingObserver,
)

server := &http.Server{
    Handler: &framework.HTTPObservationHandler{
        Handler:  mux,
        Observer: observer,
        Errors:   os.Stderr,
    },
}
```

`HTTPMetrics.Snapshot` exposes active/total requests, success, RPC/client/server
errors, cancellations, 401/forbidden/429 counters, and total/maximum duration.
It deliberately has no Principal, IP, path, or method labels, avoiding an
attacker-controlled cardinality boundary. The application owns exporting the
snapshot and authenticating any metrics endpoint.

A custom `HTTPObserver` may start a vendor tracing span in `BeginHTTP`, return
the span Context, and finish it from `EndHTTP`. Observer panics are contained and
logged, but a blocking hook can still occupy the request goroutine; remote export
therefore belongs behind an application-owned bounded queue.

The complete trust boundaries, threat inventory, residual risks, audit schema,
recommended alerts, and deployment checklist are maintained in
[HTTP security and threat model](HTTP_SECURITY.md).

## Go configuration sources

Go configuration is loaded before the Application lifecycle. `ConfigLoader`
declares typed settings once, validates the final values, and applies sources in
order; later sources override earlier ones:

```go
loader := framework.NewConfigLoader(
    framework.StringConfig(tokenKey, framework.RequiredString("is required")),
    framework.IntConfig(workerCountKey),
)
config, err := loader.Load(
    framework.NewEnvironmentConfigSource("BRIDRA_"),
    framework.NewMapConfigSource("runtime", runtimeOverrides),
)
```

The environment source converts a dotted key such as `backend.token` to
`BRIDRA_BACKEND_TOKEN`. Defaults come from each `ConfigKey`; environment values
override defaults, and explicit runtime/CLI values override the environment.
Decode and validation failures are aggregated in `ConfigLoadErrors` instead of
failing one setting at a time.

Use `NewSecretConfigKey` for credentials. `ConfigValue` still returns the typed
value to application code, while `Config.Entries()` always replaces it with
`[redacted]` and reports which source won. Secret parse and custom validation
errors never include the supplied value.

`app.Build` remains the convenient typed API. `app.BuildFromSources` is available
to entrypoints that need environment/runtime layering; the HTTP server uses it so
an explicit `--token` wins over `BRIDRA_BACKEND_TOKEN`.

## Application lifecycle

Bridra 0.1 boots Go applications in two explicit phases:

1. `Register` places typed services in the Container.
2. `Boot` reads finalized Config and connects middleware, controllers, and
   routes.

`Provide` remains eager for startup-critical services, while lifetime bindings
are resolved lazily. Constructor injection stays explicit. Additional providers
can extend the starter without editing its core application provider:

```go
application, err := app.Build(
    app.Config{Token: token, Runtime: "Go sidecar"},
    CacheServiceProvider{},
)
```

Applications collect providers in a named `ProviderManifest`. Names are unique,
ordering is deterministic, and lifecycle failures include the manifest name.
This is Bridra's explicit alternative to reflection-based package scanning:

```go
manifest := framework.NewProviderManifest()
if err := manifest.Add("cache", CacheServiceProvider{}); err != nil {
    return err
}
if err := application.RegisterManifest(manifest); err != nil {
    return err
}
```

Use `framework.NewConfigKey`, `NewSecretConfigKey`, `SetConfig`, and `ConfigValue`
for typed settings.
Use `framework.NewServiceKey`, `Provide`, `Instance`, and `Resolve` for eager
typed services. `BindSingleton`, `BindTransient`, and `BindScoped` add lazy
lifetimes; `Alias` maps an interface key to a compatible concrete key. Binding
factories receive a `Resolver`, so dependencies use the same scope and circular
dependency detection.

Application routers create one Scope per request and expose it through
`Context.Scope()`. A scoped service is reused within that request and rebuilt for
the next request. Resolving it from the root Container returns `ErrScopeRequired`,
and a singleton cannot capture a scoped dependency. Configuration is frozen after
a successful Boot.

A provider error during Register or Boot puts the Application into a terminal
failed state. `ErrApplicationFailed` identifies that state while preserving the
original provider error for `errors.Is`, and `Application.Failed` reports the
terminal state. Later Register and Boot calls return the same failure without
running providers again; create a new Application to retry.

Providers that own resources implement `TerminableServiceProvider`. Call
`Application.Shutdown(ctx)` after the transport stops accepting work:

```go
func (provider *DatabaseServiceProvider) Terminate(
    ctx context.Context,
    application *framework.Application,
) error {
    return provider.database.Close(ctx)
}
```

Shutdown runs terminable providers once in reverse registration order. Concurrent
callers share the same run and completed calls return the same result. All provider
errors are retained in `ApplicationShutdownErrors`, which matches both
`ErrApplicationShutdownFailed` and each original cause through `errors.Is`.
Providers attempted before a Register failure and every provider registered before
a Boot failure are still terminated, so `Terminate` must tolerate partial
initialization. `app.Build` performs this cleanup automatically when startup fails;
the stdio sidecar and HTTP server also shut down the Application on exit.

## Database and transactions

Database Foundation v0.1 wraps a standard `database/sql` pool without choosing an
application driver. Open the pool with the driver selected by the application, then
register it before providers that run Jobs or Tasks which use the database:

```go
pool, err := sql.Open(driverName, dataSourceName)
if err != nil {
    return err
}

application := framework.NewApplication(config)
if err := application.Register(
    framework.NewDatabaseServiceProvider(
        pool,
        framework.DefaultDatabaseProviderOptions(),
    ),
    framework.NewQueueServiceProvider(framework.DefaultJobQueueOptions()),
    framework.NewSchedulerServiceProvider(framework.DefaultSchedulerOptions()),
); err != nil {
    return err
}
```

The Provider registers `framework.DatabaseKey`, verifies the connection during Boot,
and closes the pool during reverse-order shutdown. Its default Ping timeout is five
seconds; zero disables the additional timeout and a negative value is invalid. After
a Provider is passed to `Application.Register`, the Application lifecycle owns pool
cleanup, including partial startup failure.

Repositories request an executor from each method context. The same Repository then
uses the root pool normally and the active transaction inside `WithinTransaction`:

```go
type AccountRepository struct {
    database *framework.Database
}

func (repository AccountRepository) Create(ctx context.Context, name string) error {
    executor, err := repository.database.Executor(ctx)
    if err != nil {
        return err
    }
    _, err = executor.ExecContext(
        ctx,
        "INSERT INTO accounts (name) VALUES (?)",
        name,
    )
    return err
}

err := database.WithinTransaction(ctx, nil, func(ctx context.Context) error {
    return accounts.Create(ctx, "Bridra")
})
```

Returning an error rolls back; success commits. Rollback and commit failures retain
typed framework sentinels and their driver causes. A panic rolls back and re-panics
the original value. Database v0.1 rejects nested transactions because savepoints are
not yet part of its contract. The Database wrapper does not provide an ORM or query
builder; schema migration is the separate explicit layer below.

## Database migrations

Migration Runner v0.1 stores immutable version, name, and batch records. Configure the
included SQL store for the placeholder syntax used by the application driver, then
register the Database Provider before the Migration Provider:

```go
store, err := framework.NewSQLMigrationStore(
    framework.DefaultSQLMigrationStoreOptions(),
)
if err != nil {
    return err
}

if err := application.Register(
    framework.NewDatabaseServiceProvider(
        pool,
        framework.DefaultDatabaseProviderOptions(),
    ),
    framework.NewMigrationServiceProvider(store),
    ApplicationMigrationsProvider{},
); err != nil {
    return err
}
```

The default store uses the `bridra_migrations` table and question-mark placeholders
for SQLite/MySQL-style drivers. PostgreSQL-style drivers use:

```go
framework.SQLMigrationStoreOptions{
    Table:            "bridra_migrations",
    PlaceholderStyle: framework.SQLPlaceholderDollar,
}
```

Application providers resolve `framework.MigrationRunnerKey` and register versioned
definitions. Lexical version ordering makes timestamp prefixes convenient:

```go
func (ApplicationMigrationsProvider) Register(application *framework.Application) error {
    runner, err := framework.Resolve(
        application.Container(),
        framework.MigrationRunnerKey,
    )
    if err != nil {
        return err
    }
    return runner.Register(framework.Migration{
        Version: "202607220001",
        Name:    "create_accounts",
        Up: func(ctx context.Context, executor framework.SQLExecutor) error {
            _, err := executor.ExecContext(
                ctx,
                "CREATE TABLE accounts (id BIGINT PRIMARY KEY)",
            )
            return err
        },
        Down: func(ctx context.Context, executor framework.SQLExecutor) error {
            _, err := executor.ExecContext(ctx, "DROP TABLE accounts")
            return err
        },
    })
}
```

Schema changes are always explicit; Application Boot does not run them:

```go
result, err := runner.Migrate(ctx)
statuses, err := runner.Status(ctx)
rollback, err := runner.Rollback(ctx)
```

One `Migrate` invocation assigns one batch, applies pending definitions in ascending
version order, and commits each Migration independently. `Rollback` validates and
reverts only the latest batch in reverse order. Applied definitions must remain in the
registry with the same version and name, or the Runner reports history drift before
changing the schema.

Transactions include both the callback and history update by default. Set
`DisableTransaction` only for drivers whose DDL cannot usefully run in a transaction;
that opt-out cannot guarantee atomic schema/history updates. Migration v0.1 does not
include savepoints, dry runs, distributed locks, schema dumps, or step-count rollback.

## Routing and method policies

Register reusable middleware once with `RegisterMiddlewareGroup`, then apply it
globally through `UseMiddlewareGroups` or to a Route Group. `Router.Group` and
nested `RouteGroup.Group` join dot-separated RPC prefixes without changing the
wire protocol. Groups snapshot inherited middleware and policies when created.

`HandleWithPolicies` and `RouteGroup.UsePolicies` run authorization or other
method-specific checks immediately before the Controller. The execution order is
global middleware, route middleware, group policy, method policy, then Controller.
A policy returns an error to stop the request; outer middleware still observes
the rejection.

The application provider uses generated group/action constants, so composing
`system.health` and `greeting.hello` does not duplicate schema method strings.

## Validation and exception rendering

`RuleRegistry[T]` composes typed field rules, nested validators, and
`RuleFunc[T]` cross-field rules. A registry evaluates every validation rule and
returns one `ValidationErrors` value, so the Flutter client receives all known
field violations in one response. `ForField`, `MaxLength`, `OneOf`, `Optional`,
`NestedField`, and `OptionalNestedField` are reusable building blocks; generated
Request DTOs use the same public API as hand-written application validators.

The Router owns one `ExceptionRenderer`. `ExceptionRegistry` preserves the
framework mappings for `RPCError`, `ValidationErrors`, and hidden internal
errors, while `MapException` adds typed domain-error mappings:

```go
renderer := framework.NewExceptionRegistry(
    framework.MapException(func(err *OrderConflict) *framework.RPCError {
        return framework.NewError("order_conflict", "The order cannot be changed.")
    }),
)
if err := application.Router().SetExceptionRenderer(renderer); err != nil {
    return err
}
```

This keeps Controllers and policies transport-independent: they return Go
errors, and one renderer defines the stable wire representation.

## Typed application events

Every Application owns one `EventDispatcher`, exposes it through
`Application.Events()`, and registers the same instance under
`EventDispatcherKey` for constructor injection. Listeners use exact Go event
types and explicit names:

```go
if err := framework.Listen(
    application.Events(),
    "orders.send-confirmation",
    func(ctx context.Context, event OrderPlaced) error {
        return mailer.Send(ctx, event.Order)
    },
); err != nil {
    return err
}

if err := framework.Dispatch(ctx, application.Events(), OrderPlaced{Order: order}); err != nil {
    return err
}
```

Event v0.1 is synchronous and context-aware. Listeners run in registration
order; the first error stops dispatch and is wrapped by `ErrEventDispatchFailed`.
Returning `ErrStopEventPropagation` stops remaining listeners without treating
the dispatch as failed. Registration and dispatch are thread-safe, and each
dispatch uses a listener snapshot so listeners added concurrently begin with the
next event.

The Greeting Controller demonstrates a domain event with `GreetingCreated`.
Additional Service Providers can subscribe without changing the Controller.
Events remain synchronous by default. Applications opt individual listeners into
background work by mapping an Event to a typed Job:

```go
err := framework.ListenQueued(
    application.Events(),
    queue,
    "orders.queue-confirmation",
    func(ctx context.Context, event OrderPlaced) (SendOrderConfirmation, error) {
        return SendOrderConfirmation{OrderID: event.Order.ID}, nil
    },
)
```

The mapper runs inside normal Event dispatch and receives its context. Event
dispatch returns after the Job is accepted; the Job Handler runs independently on
a Queue worker and uses the Handler retry policy. Mapper errors are wrapped by
`ErrQueuedEventMappingFailed`. Backpressure, canceled enqueue contexts, missing Job
Handlers, and stopped Queues are wrapped by `ErrQueuedEventEnqueueFailed` while
preserving `ErrJobDispatchFailed` and the original Queue or context cause. The outer
Event dispatch also retains `ErrEventDispatchFailed` and the queued listener name.

## Background jobs and queues

The Job Queue registers one named Handler for each exact Go Job type. Add the
Queue Service Provider before providers that register handlers:

```go
queueProvider := framework.NewQueueServiceProvider(
    framework.DefaultJobQueueOptions(),
)
if err := application.Register(queueProvider, OrdersJobServiceProvider{}); err != nil {
    return err
}

func (OrdersJobServiceProvider) Register(application *framework.Application) error {
    queue, err := framework.Resolve(application.Container(), framework.JobQueueKey)
    if err != nil {
        return err
    }
    return framework.HandleJob(
        queue,
        "orders.send-confirmation",
        func(ctx context.Context, job SendOrderConfirmation) error {
            return mailer.Send(ctx, job.OrderID)
        },
    )
}
```

Handlers that can retry use an explicit policy:

```go
return framework.HandleJobWithOptions(
    queue,
    "orders.send-confirmation",
    framework.JobHandlerOptions{
        MaxAttempts:  3,
        RetryBackoff: 250 * time.Millisecond,
    },
    sendOrderConfirmation,
)
```

After Application Boot starts the workers, dispatch is typed:

```go
err := framework.DispatchJob(ctx, queue, SendOrderConfirmation{OrderID: order.ID})
```

Delay execution by a duration or until a specific time:

```go
err := framework.DispatchJobAfter(
    ctx,
    queue,
    5*time.Minute,
    SendOrderConfirmation{OrderID: order.ID},
)

err = framework.DispatchJobAt(
    ctx,
    queue,
    scheduledAt,
    SendOrderConfirmation{OrderID: order.ID},
)
```

Zero delays and times in the past dispatch immediately. A negative delay returns
`ErrInvalidJobDelay`.

Without a `JobStore`, ready and delayed Jobs remain in memory. `Capacity` bounds
both admission paths, so dispatch waits for space or returns when its context ends.

For a single-host durable queue, configure `FileJobStore`:

```go
store, err := framework.NewFileJobStore(
    framework.DefaultFileJobStoreOptions(
        filepath.Join(applicationDataDirectory, "queue", "jobs.log"),
    ),
)
if err != nil {
    return err
}

queueOptions := framework.DefaultJobQueueOptions()
queueOptions.Store = store
queueProvider := framework.NewQueueServiceProvider(queueOptions)
```

Dispatch returns only after the append-only log entry is written and synchronized.
Job JSON, its stable Handler name, delayed delivery time, and attempt count survive
process and Sidecar restarts. `FileJobStoreOptions.MaxJobs` bounds ready, reserved,
delayed, and retained failed Jobs; `MaxPayloadBytes` bounds each JSON payload.
`Capacity` applies only to the in-memory queue.

For multiple processes or hosts that share one SQL database, configure
`SQLJobStore` after the application Database Provider is ready and before Queue
workers start:

```go
storeOptions := framework.DefaultSQLJobStoreOptions()
store, err := framework.NewSQLJobStore(database.Pool(), storeOptions)
if err != nil {
    return err
}
if err := store.Ensure(ctx); err != nil {
    return err
}

queueOptions := framework.DefaultJobQueueOptions()
queueOptions.Store = store
queueProvider := framework.NewQueueServiceProvider(queueOptions)
```

`Ensure` is idempotent and creates the default `bridra_jobs` table. Run it during
application migration or deployment before starting workers. PostgreSQL drivers
use dollar placeholders:

```go
storeOptions.PlaceholderStyle = framework.SQLPlaceholderDollar
```

SQLite and MySQL-style drivers use the default question-mark placeholders.
`SQLJobStoreOptions.Table` selects a validated application-owned table name and
`MaxPayloadBytes` bounds each JSON payload. The shared database, rather than an
in-process count, owns total queue capacity, retention, encryption, backup, and
availability.

Reservation uses a conditional SQL update, so separate workers can select the
same candidate but only one can atomically claim its lease. The other workers
retry selection. A shared PostgreSQL or MySQL deployment can therefore coordinate
multiple hosts; a local SQLite file coordinates only processes that can safely
access that same database.

For a shared Redis deployment, use the official `go-redis/v9` client and
`RedisJobStore`:

```go
redisClient := redis.NewClient(&redis.Options{
    Addr:     "redis.internal:6379",
    Username: os.Getenv("REDIS_USERNAME"),
    Password: os.Getenv("REDIS_PASSWORD"),
})
if err := redisClient.Ping(ctx).Err(); err != nil {
    return err
}

storeOptions := framework.DefaultRedisJobStoreOptions()
storeOptions.Namespace = "orders:jobs"
store, err := framework.NewRedisJobStore(redisClient, storeOptions)
if err != nil {
    return err
}

queueOptions := framework.DefaultJobQueueOptions()
queueOptions.Store = store
queueProvider := framework.NewQueueServiceProvider(queueOptions)
```

`RedisJobStore` uses Lua scripts to atomically move Jobs between ready, reserved,
and failed states. Separate workers can therefore coordinate one at-least-once
delivery without process-local locks. All keys use one Redis Cluster hash slot.
`RedisJobStoreOptions.Namespace` isolates applications and cannot contain braces;
`MaxPayloadBytes` bounds each stored JSON payload.

The application owns the Redis client. Ping it before Queue Boot, keep it alive
until Queue shutdown completes, then close it through the resource provider that
created it. `RedisJobStore` deliberately does not close the client. Configure Redis
persistence, replication, memory limits, `noeviction`, ACLs, TLS, backup, and
monitoring for durable production use. Eviction or manual deletion of queue keys
is data loss.

The dispatch context bounds only the enqueue operation; queued work receives a
new background context for each attempt with the configured `JobTimeout`, so ending
an RPC request does not cancel accepted work. A full bounded queue applies
backpressure until capacity becomes available or the dispatch context ends.

`MaxAttempts` defaults to one. When it is larger, Handler errors, attempt timeouts,
and recovered panics retry after the fixed `RetryBackoff`. Only the final exhausted
failure is sent to `ReportFailure`; its `Attempts` and `MaxAttempts` identify the
policy, and its error preserves `ErrJobRetriesExhausted`, `ErrJobExecutionFailed`,
and the last original cause through `errors.Is`.

Persistent delivery is at least once. A worker crash after a side effect but before
`Complete` makes the Job eligible again after `LeaseDuration`, so every persistent
Handler must be idempotent. `LeaseDuration` must be greater than `JobTimeout`.
Handlers must still honor cancellation; a Handler that runs past its lease can
overlap a recovered attempt.

Exhausted persistent Jobs remain available for inspection and operator action:

```go
failed := store.FailedJobs()
err = store.RetryFailed(ctx, failed[0].Job.ID, time.Now())
err = store.ForgetFailed(ctx, failed[0].Job.ID)
```

SQL and Redis inspection perform I/O and therefore accept a Context and return an
error:

```go
failed, err := sqlStore.FailedJobs(ctx)
err = sqlStore.RetryFailed(ctx, failed[0].Job.ID, time.Now())
err = sqlStore.ForgetFailed(ctx, failed[0].Job.ID)

failed, err = redisStore.FailedJobs(ctx)
err = redisStore.RetryFailed(ctx, failed[0].Job.ID, time.Now())
err = redisStore.ForgetFailed(ctx, failed[0].Job.ID)
```

Retry resets the attempt count and schedules the Job at the supplied time. Forget
permanently removes the failed record. `FileJobStore` failed Jobs count toward its
`MaxJobs`; SQL retention is managed by the database operator.

`QueueServiceProvider` implements `TerminableServiceProvider`. Shutdown rejects new
Jobs and waits for active work. The in-memory queue drains accepted work and
promotes delayed Jobs immediately. A persistent queue stops reserving work and
leaves pending or delayed Jobs in storage for the next start. After a successful
shutdown, the Provider closes a configured Store that implements `Close`.

`FileJobStore` is a local, append-only store for exactly one Bridra process at a
time. Do not open the same path from multiple processes or hosts. The log is not
automatically compacted, and payloads are plaintext despite restrictive file
permissions, so place it in protected application data and monitor its size.

`SQLJobStore` is stateless around an application-owned `*sql.DB`; Queue shutdown
does not close that shared pool. Register the Queue after the Database Provider so
reverse shutdown finishes Queue work before closing database connections.

The Redis Stores are stateless around an application-owned `redis.Scripter`; Queue
and Scheduler shutdown do not close that shared client. Register them after the
Redis resource provider so reverse shutdown finishes work before closing Redis
connections.

Persisted Handler names and JSON schemas are data contracts. Keep the name
registered across deployments and evolve payload fields compatibly until all
pending and failed Jobs using the old schema have been completed or forgotten.

## Task scheduler

The Scheduler runs named Tasks using either a fixed delay or a five-field cron
expression. Register the Queue before the Scheduler when scheduled Tasks dispatch
Jobs, then register application Tasks last:

```go
if err := application.Register(
    databaseProvider,
    framework.NewQueueServiceProvider(framework.DefaultJobQueueOptions()),
    framework.NewSchedulerServiceProvider(framework.DefaultSchedulerOptions()),
    ScheduledTasksServiceProvider{},
); err != nil {
    return err
}

func (ScheduledTasksServiceProvider) Register(application *framework.Application) error {
    scheduler, err := framework.Resolve(application.Container(), framework.SchedulerKey)
    if err != nil {
        return err
    }
    queue, err := framework.Resolve(application.Container(), framework.JobQueueKey)
    if err != nil {
        return err
    }
    return framework.ScheduleCronTask(
        scheduler,
        "reports.queue-daily",
        "0 6 * * *",
        func(ctx context.Context) error {
            return framework.DispatchJob(ctx, queue, GenerateDailyReport{})
        },
    )
}
```

`ScheduleTask` waits one fixed interval before its first run and between completed
runs. `ScheduleCronTask` accepts the standard five fields `minute hour day-of-month
month day-of-week`. It supports `*`, comma-separated lists, ranges, steps, case-
insensitive English month and weekday names, and both `0` and `7` for Sunday. When
day-of-month and day-of-week are both restricted, either field may match.

Cron Tasks use `SchedulerOptions.Location`, which defaults to `time.Local`. The next
occurrence is calculated from the wall clock after the current invocation finishes,
so missed occurrences are skipped instead of replayed.

One named Task never overlaps with itself; different Tasks have independent loops and
may run concurrently. `TaskTimeout` supplies each invocation context. Errors and
recovered panics are wrapped by `ErrScheduledTaskExecutionFailed` and sent to
`ReportFailure`.

Without a `SchedulerStore`, next-run and completion state remain process-local. To
preserve them across Sidecar or server restarts, configure `FileSchedulerStore`:

```go
schedulerStore, err := framework.NewFileSchedulerStore(
    framework.DefaultFileSchedulerStoreOptions(
        filepath.Join(applicationDataDirectory, "scheduler", "tasks.log"),
    ),
)
if err != nil {
    return err
}

schedulerOptions := framework.DefaultSchedulerOptions()
schedulerOptions.Store = schedulerStore
schedulerProvider := framework.NewSchedulerServiceProvider(schedulerOptions)
```

For multiple processes or hosts sharing one SQL database, configure
`SQLSchedulerStore` after the Database Provider is ready:

```go
storeOptions := framework.DefaultSQLSchedulerStoreOptions()
storeOptions.PlaceholderStyle = framework.SQLPlaceholderDollar // PostgreSQL

schedulerStore, err := framework.NewSQLSchedulerStore(
    database.Pool(),
    storeOptions,
)
if err != nil {
    return err
}
if err := schedulerStore.Ensure(ctx); err != nil {
    return err
}

schedulerOptions := framework.DefaultSchedulerOptions()
schedulerOptions.Store = schedulerStore
schedulerProvider := framework.NewSchedulerServiceProvider(schedulerOptions)
```

`Ensure` idempotently creates `bridra_scheduled_tasks`; run it during migration or
deployment before Scheduler Boot. SQLite and MySQL-style drivers use the default
question-mark placeholders. `SQLSchedulerStore` does not own or close the supplied
pool, so register the Scheduler after the Database Provider and let reverse shutdown
stop Tasks before database connections close. SQL-persisted Task names are limited
to 255 UTF-8 bytes so their primary key remains portable across the supported schema
styles.

For multiple processes or hosts sharing Redis, configure `RedisSchedulerStore`
after the application-owned Redis client is ready:

```go
storeOptions := framework.DefaultRedisSchedulerStoreOptions()
storeOptions.Namespace = "orders:scheduler"

schedulerStore, err := framework.NewRedisSchedulerStore(
    redisClient,
    storeOptions,
)
if err != nil {
    return err
}

schedulerOptions := framework.DefaultSchedulerOptions()
schedulerOptions.Store = schedulerStore
schedulerProvider := framework.NewSchedulerServiceProvider(schedulerOptions)
```

`RedisSchedulerStore` uses one namespaced Redis hash and Lua-atomic reservation
and completion transitions. All state stays in one Redis Cluster hash slot.
Namespaces cannot contain braces, and Redis-persisted Task names are limited to
255 UTF-8 bytes. Ping the client before Scheduler Boot, keep it alive until
Scheduler shutdown finishes, then close it through the resource provider that
created it. The Store deliberately does not close the shared client.

Persistent scheduling stores each Task's next run, last scheduled and completed
times, last error, and active lease. Startup keeps an existing next-run time instead
of restarting the interval. When the process was down past that time, Bridra recovers
one overdue occurrence, then calculates the following occurrence from its completion
time. It does not replay every missed cron tick. If a Task's interval or cron
expression changes while keeping the same name, its already-persisted next occurrence
runs first and the new schedule controls later occurrences.

Persistent Task execution is at least once. A process crash after a side effect but
before `Complete` makes that occurrence eligible again after `LeaseDuration`.
Handlers must be idempotent, `LeaseDuration` must exceed `TaskTimeout`, and Handlers
must honor cancellation so they do not run past a recovered lease.

`SchedulerStore.Reserve` is the atomic coordination boundary. Multiple Scheduler
instances sharing a correctly implemented database or network Store compete for one
lease, matching Laravel's `onOneServer` behavior. Same-Task execution remains
non-overlapping like Laravel's `withoutOverlapping`.

The built-in `FileSchedulerStore` is for one Bridra process on one host. Do not open
the same path from multiple processes. Use `SQLSchedulerStore` or
`RedisSchedulerStore` for distributed coordination equivalent to Laravel
`onOneServer()`. Their atomic transitions allow only one contender to claim each
occurrence, while an expired lease makes that same occurrence eligible again.
`FileSchedulerStore.States` exposes local persisted state for diagnostics; SQL and
Redis callers use `State` for a known Task name or their deployment operations
tooling. Stable Task names remain persistence keys, and removed names retain state
until the application or storage operator explicitly removes it.

The file Store is append-only, unencrypted, and not automatically compacted. SQL
and Redis Stores delegate capacity, retention, encryption, backup, availability,
and monitoring to their deployments. Redis additionally requires a non-evicting
policy because an evicted Scheduler key loses persisted Task state.

`SchedulerServiceProvider` starts during Application Boot. Shutdown stops pending
timers and waits for running Tasks before the Queue drains, following reverse Provider
order. If the shutdown context expires, running Tasks continue toward their own
timeout and a later Shutdown call can wait again. Tasks must honor context and must
not call Scheduler Shutdown from inside themselves. After successful shutdown, the
Provider closes a configured Store that implements `Close`.

## RPC contract

Stdio uses one JSON object per line; HTTP uses one JSON object per POST. The
payload is identical:

```json
{"id":"1","method":"greeting.hello","params":{"name":"Codex"},"meta":{"token":"..."}}
{"id":"1","result":{"message":"Hello, Codex!"},"meta":{"pipeline":["logging:before","recovery:before","request-id:before","auth:before","auth:after","request-id:after","recovery:after","logging:after"]}}
```

Generated Dart methods accept an optional `RpcCancellationToken`. Cancelling it
aborts HTTP or sends the reserved stdio control message below; ordinary timeout
handling uses the same transport cancellation path.

```json
{"id":"1","method":"rpc.cancel","params":{},"meta":{"token":"..."}}
```

Errors have stable codes and may include structured data:

```json
{"id":"1","error":{"code":"validation_error","message":"The request failed validation.","data":{"violations":[{"field":"name","rule":"max_length","message":"Name must be 64 characters or fewer.","parameters":{"max":64}}]}}}
```

`system.health` returns `frameworkVersion` and `protocolVersion`; Flutter
rejects incompatible wire protocols. Requests are limited to 4 MiB.

### Server streaming, progress, and backpressure

Declare a server-streaming method with `"stream": true` in
`schema/bridra.json`. Code generation changes only that Dart method to return
`Stream<RpcStreamEvent<ResultType>>`; unary methods keep returning `Future`.

Inside the Controller, bind and validate the request before producing the
stream:

```go
func (controller *ReportController) Build(ctx *framework.Context) (any, error) {
	request, err := framework.BindAndValidate[requests.BuildReportRequest](ctx)
	if err != nil {
		return nil, err
	}
	return framework.ProduceStream(ctx, func(stream *framework.StreamWriter) error {
		for page := int64(1); page <= request.Pages; page++ {
			if err := stream.Report(framework.Progress{
				Completed: page - 1,
				Total:     request.Pages,
				Message:   "Rendering report",
				Unit:      "pages",
			}); err != nil {
				return err
			}
			if err := stream.Send(responses.ReportPage{Page: page}); err != nil {
				return err
			}
		}
		return nil
	})
}
```

Consume typed data and progress from the generated Flutter API:

```dart
await for (final event in api.buildReport(request)) {
  if (event is RpcStreamProgress<ReportPage>) {
    setProgress(event.progress.fraction);
  } else {
    final page = (event as RpcStreamData<ReportPage>).value;
    render(page);
  }
}
```

The default stream timeout is five minutes and can be overridden on the
generated method. Cancellation uses the existing `RpcCancellationToken`.
Desktop Sidecar streams start with a 16-event credit window, configurable from
1 to 256 through `SidecarClient.start(streamWindow: ...)`. Flutter acknowledges
an event only after its listener consumes it, so a paused listener cannot create
an unbounded queue. HTTP streams use NDJSON and the response socket's write
backpressure. `rpc.stream_ack` is a reserved Sidecar control method and must not
be registered as an application route.

### Large-file transfers

Use a `"type": "file"` field when an input or result is too large for the 4 MiB
JSON envelope:

```json
{
  "name": "reports.export",
  "clientName": "exportReport",
  "result": {
    "goType": "ExportReportResponse",
    "dartType": "ExportReportResult",
    "fields": [{"name": "file", "type": "file"}]
  }
}
```

The generator maps that field to `framework.FileReference` in Go and
`RpcFileReference` in Dart. Stage output bytes from a Controller or Service
through the request scope:

```go
store, err := framework.Resolve(
    ctx.Scope(),
    framework.FileTransferStoreKey,
)
if err != nil {
    return nil, err
}
file, err := store.Stage(
    ctx,
    "report.pdf",
    "application/pdf",
    reportReader,
)
if err != nil {
    return nil, err
}
return responses.ExportReportResponse{File: file}, nil
```

Consume the generated reference through the gateway without buffering the
complete file:

```dart
final result = await backend.exportReport();
await for (final chunk in backend.download(result.file)) {
  output.add(chunk);
}
```

For large input, declare the same field in `params`, prepare a repeatable range
reader, and upload before calling the generated method:

```dart
final source = File(path);
final digest = await sha256.bind(source.openRead()).first;
final upload = RpcFileUpload(
  name: 'archive.zip',
  mediaType: 'application/zip',
  size: await source.length(),
  sha256: digest.toString(),
  openRead: (offset) => source.openRead(offset),
);
final reference = await backend.upload(upload);
await api.importArchive(ImportArchiveRequest(file: reference));
```

Consume the uploaded capability exactly once inside the Go request:

```go
upload, err := store.ConsumeUpload(request.File)
if err != nil {
    return nil, err
}
defer upload.Close()
// Stream from upload without buffering the complete file.
```

The default store limit is 2 GiB and references expire after 15 minutes.
Desktop Sidecars use framework-verified managed temporary files. HTTP uses
random 256-bit capabilities: download retries send `Range: bytes=<offset>-`,
while uploads recover from the server's `Upload-Offset`. Flutter uses at most
three attempts by default, preserves stream backpressure, and validates the
declared size and SHA-256 digest. A download capability is consumed only after
a complete response; an uploaded reference is consumed by
`FileTransferStore.ConsumeUpload`. Binary RPC frames and shared memory remain
out of scope.

## Add an endpoint

1. Define the method, params, result, metadata, and validation in
   `schema/bridra.json`.
2. Run `make generate` to create Go DTOs/route constants and the Dart typed API.
3. Add domain data under `backend/app/models/` when the use case needs it.
4. Add business logic under `backend/app/services/`.
5. Add a controller under `backend/app/controllers/` using the generated DTOs.
6. Register the service and generated route constant in a Service Provider.
7. Test validation, service, controller, generated client, and relevant transport
   path.

No platform runner change is needed because both transports share the router.

## Layout

```text
backend/
  codegen/                  schema validation and deterministic generators
  app/requests/             typed transport input and validation
  app/contracts/            generated RPC method constants
  app/models/               transport-independent application/domain data
  app/events/               typed domain event payloads
  app/services/             injected business logic
  app/responses/            serialized application output
  app/controllers/          request binding and use-case orchestration
  app/settings/             typed application configuration keys
  app/providers/            service registration and route boot lifecycle
  framework/                Config, Container, events, Router, and transports
  projecttemplate/          embedded, versioned project template manifest
  cmd/bridra/               `create`, `doctor`, and `generate` commands
  cmd/sidecar/              desktop child-process entrypoint
  cmd/server/               mobile/Web HTTP entrypoint
  tool/coveragecheck/       Go and LCOV non-regression gate
packages/
  bridra_flutter/           reusable RPC, HTTP, and Sidecar Flutter package
docs/                       complete, architecture, upgrade, and release guides
lib/
  api/generated/            generated models, decoders, and typed RPC client
  api/backend_gateway.dart  connection lifecycle and generated API adapter
  bridra_app.dart           example application UI
  main.dart                 shared application entrypoint
test/                       widget and real-process integration tests
android/ ios/ web/          mobile and browser runners
linux/ macos/ windows/      desktop runners and sidecar packaging
tool/windows.ps1            native Windows run/build/smoke/verify tasks
tool/coverage_thresholds.json committed coverage floors
schema/bridra.json          versioned source RPC contract
```

Before publishing a product, replace the example Go module path, application
IDs, display name, icons, package name, version, signing settings, endpoint, and
authentication policy.
