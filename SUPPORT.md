# Bridra support

Bridra is a pre-1.0 framework maintained on a best-effort basis, without a
production support SLA or LTS commitment.

Before 1.0, only the latest patch of the latest published minor line is eligible
for security fixes. Older minor lines and superseded patches are unsupported.
See [GitHub Releases](https://github.com/cluion/bridra/releases) for published
versions; development branches and unreleased candidates are not supported releases.

A new minor line's support policy takes effect at publication; the previously
published line remains supported until then.

## Where to ask

- Reproducible framework bugs: use the GitHub Bug Report form.
- Framework proposals: use the Feature Proposal form.
- Security vulnerabilities: use the private process in [SECURITY.md](SECURITY.md).
- Contribution and local verification questions: read
  [CONTRIBUTING.md](CONTRIBUTING.md) and include command output in an Issue when a
  documented workflow is broken.

Do not use public Issues for secrets, vulnerabilities, private application code,
production data, or credentials.

## Supported scope

Maintainers can triage issues involving framework code and documented workflows:

- the Go framework, CLI, Project Template, Codegen, and scaffold generator;
- the `bridra_flutter` RPC/HTTP/Sidecar runtime;
- supported host checks, six-platform build orchestration, and generated starter;
- documented upgrade, coverage, packaging, and release contracts.

Product UI, domain logic, database schema design, cloud deployment, TLS
termination, identity providers, app-store accounts, signing/notarization,
production operations, and third-party plugins are application responsibilities.
Reports are still useful when Bridra's documented boundary is incorrect or unsafe.

## Issue expectations

Include Bridra/Flutter/Go versions, host and target platform, transport, minimal
reproduction, exact command, expected result, actual result, and redacted logs.
Maintainers may close reports that cannot be reproduced, concern an unsupported
environment, duplicate an existing issue, or belong to an application/provider.

Response times are best-effort until a release explicitly states a support
window. Security response targets are defined separately in `SECURITY.md`.
