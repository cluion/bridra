# Bridra support

Bridra 0.2 is the current pre-1.0 framework line and is maintained on a
best-effort basis until the next minor line is published. Bridra 0.1 is no
longer supported. There
is no production support SLA or LTS version.

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
