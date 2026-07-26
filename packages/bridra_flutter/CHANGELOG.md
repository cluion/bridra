# Changelog

## Unreleased

- Added automatic Go Sidecar restart with configurable bounded exponential
  backoff and replacement `system.health` checks.
- Calls already in flight fail without replay after a crash. New calls wait for
  recovery while retaining their timeout and cancellation behavior.

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
