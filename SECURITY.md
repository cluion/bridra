# Security policy

Bridra 0.6 is a pre-1.0 framework line. Security fixes are provided on a
best-effort basis without a production SLA or LTS commitment.

## Supported versions

| Version | Security support |
| --- | --- |
| Latest `0.6.x` patch | Security fixes accepted on a best-effort basis |
| `0.5.x` and older | Unsupported; upgrade to the latest `0.6.x` patch |
| `main` | Active development only |

Before 1.0, only the latest patch of the current documented minor line is
eligible for security fixes. Bridra has no LTS line. Each release may announce a
shorter or longer support window in its release notes; otherwise users should
upgrade to the latest compatible patch.

## Report a vulnerability privately

Do not open a public Issue, Pull Request, Discussion, or social-media post for a
suspected vulnerability.

Use GitHub's private vulnerability reporting for this repository:

<https://github.com/cluion/bridra/security/advisories/new>

Private vulnerability reporting is the canonical confidential intake path. If
the private report button is unavailable, contact `hugo@cluion.com` without
including vulnerability details so a secure channel can be arranged.

Include, when possible:

- affected Bridra version, commit, platform, and transport;
- a minimal reproduction or proof of concept;
- expected and observed impact;
- whether credentials, tokens, or user data are exposed;
- suggested mitigations or a patch, if available;
- any requested disclosure date or credit.

Never include real secrets, production tokens, or personal data. Use synthetic
values and redact logs.

## Response and disclosure

Maintainers aim to acknowledge a complete report within three business days and
provide an initial severity/ownership assessment within seven business days.
These are response targets, not a contractual SLA.

Security responders will:

1. reproduce and classify the report privately;
2. identify affected framework, protocol, template, CLI, and package versions;
3. prepare tests, a fix, upgrade guidance, and rollback guidance;
4. coordinate a release date with the reporter when practical;
5. publish a GitHub Security Advisory and credit the reporter if requested;
6. avoid publishing exploit details before users have a reasonable update path.

If a report is not a vulnerability, maintainers may move a redacted version into
the normal Issue workflow with the reporter's consent.

## Security scope

Reports are welcome for:

- Sidecar process lifecycle, token handling, executable discovery, and stdio RPC;
- HTTP request authentication boundaries, CORS, body limits, and protocol parsing;
- generated Go/Dart contract validation and stable error handling;
- framework configuration secret handling and log redaction;
- CLI project creation, scaffolding, builds, archives, checksums, and upgrades;
- dependency or build-pipeline vulnerabilities that affect Bridra consumers.

Application authentication, authorization rules, TLS termination, database
credentials, deployment topology, signing keys, and store submission remain
application/operator responsibilities. A framework behavior that makes those
responsibilities unsafe or misleading is still in scope.
