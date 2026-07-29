# Changelog

## Unreleased

## 0.7.0 - 2026-07-29

- Kept the public Dart API and RPC protocol unchanged while aligning the package
  with Bridra 0.7.0's opt-in persistent Go Queue and Scheduler APIs.

## 0.6.1 - 2026-07-29

- Kept the public Dart API and RPC protocol unchanged while aligning the package
  with the Bridra `0.6.1` generated-consumer patch.

## 0.6.0 - 2026-07-29

- Added `RpcStreamEvent`, `RpcStreamData`, `RpcStreamProgress`, and
  `RpcProgress`, with HTTP NDJSON and acknowledged Sidecar streaming.
- Added bounded Sidecar stream windows so paused consumers cannot create
  unbounded response queues.
- Added `RpcFileReference` and `RpcClient.download` with streamed size and
  SHA-256 verification for HTTP and managed Desktop files.
- Added `RpcFileUpload` and `RpcClient.upload` for verified HTTP and Desktop
  uploads, plus bounded byte-range retry for interrupted downloads and uploads.
- Kept RPC protocol version `1` because unary calls remain wire-compatible and
  streaming and file references are opt-in.
- Kept the package version aligned with the Bridra 0.6.0 Go Framework, CLI, and
  project metadata.

## 0.5.0 - 2026-07-27

- Added `DesktopSingleInstance`, `DesktopSingleInstanceSession`, and
  `DesktopActivation` for crash-safe desktop ownership and later-launch
  activation forwarding.
- Kept the package version aligned with the Bridra 0.5.0 Go Framework, CLI, and
  project metadata without changing RPC protocol version `1`.

## 0.4.0 - 2026-07-27

- Added automatic Go Sidecar restart with configurable bounded exponential
  backoff and replacement `system.health` checks.
- Calls already in flight fail without replay after a crash. New calls wait for
  recovery while retaining their timeout and cancellation behavior.
- Added the public `SidecarRestartPolicy` configuration and stable
  `SidecarRestartExhaustedException` failure.
- Kept the package version aligned with the Bridra 0.4.0 Go Framework, CLI, and
  project metadata without changing RPC protocol version `1`.

## 0.3.0 - 2026-07-25

- Kept the package version aligned with the Bridra 0.3.0 Go Framework, CLI, and
  project metadata.
- No public Dart API or RPC protocol changes.

## 0.2.0 - 2026-07-25

- Added `RpcCancellationToken` and `RpcCancelledException`, with optional
  cancellation on generated and transport-neutral RPC calls.
- Added timeout and manual cancellation propagation for HTTP and desktop
  Sidecar transports without changing RPC protocol version `1`.

## 0.1.1 - 2026-07-24

- Kept the package version aligned with the Bridra 0.1.1 framework hotfix.
- No public Dart API changes.

## 0.1.0 - 2026-07-24

- Extracted the reusable RPC client contract.
- Extracted HTTP and desktop sidecar transports.
- Added a conditional platform connector.
- Kept application-specific gateways outside the package.
- Added the MIT license and publication metadata.
- Added framework release automation that keeps the package version aligned with
  the Bridra Go Framework, CLI, metadata, and documentation.
- Registered `cluion.com` as the verified publisher and added hosted release
  validation.
