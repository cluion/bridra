# Bridra governance

Bridra is stewarded by Cluion. This document defines how technical decisions,
maintenance, security response, and releases are owned while the framework is
pre-1.0.

## Principles

- Compatibility and user-owned application code take priority over convenience.
- Decisions are recorded in Issues, Pull Requests, release notes, or committed
  architecture/upgrade documents.
- Framework behavior must remain testable without privileged infrastructure.
- Security reports and embargoed fixes remain private until coordinated release.
- Release authority is explicit; a green build alone cannot publish artifacts.

## Roles

### Repository owners

Cluion repository administrators control access, repository settings, security
features, package ownership, licenses, branch/ruleset policy, and final release
authorization. Only an owner may authorize the first public release, change the
canonical package identity, or approve a license.

### Maintainers

Maintainers have repository write access and sustained responsibility for one or
more framework surfaces. They triage Issues, review Pull Requests, preserve public
contracts, keep CI healthy, and may merge changes within approved scope.

### Release manager

One maintainer is named in each release Pull Request as release manager. The
release manager runs the checklist in
[RELEASING.md](docs/RELEASING.md), verifies the source commit and artifacts,
coordinates package publication, and records the final evidence. This role does
not override repository-owner authorization.

### Security responders

Repository owners and explicitly invited maintainers handle private vulnerability
reports. They follow [SECURITY.md](SECURITY.md), restrict advisory access to the
smallest useful group, and coordinate disclosure and fixes.

### Contributors

Contributors report problems, propose designs, improve documentation, or submit
changes under [CONTRIBUTING.md](CONTRIBUTING.md). Contribution does not itself
grant merge, maintainer, or release authority.

## Decisions

Routine fixes and internal refactors are decided through normal Pull Request
review. The following require a public design Issue or equivalent recorded
proposal before merge:

- exported Go or Dart API additions/removals;
- RPC protocol, stable error code, or generated-schema changes;
- Project Template ownership or automatic migration behavior;
- package/module identity, dependency, license, or support-policy changes;
- security-boundary, authentication-default, signing, or release-process changes;
- changes that remove a supported platform.

The proposal should state the problem, alternatives, compatibility impact,
migration path, security impact, and rollback. Maintainers seek consensus; when
consensus is not reached, repository owners make the final recorded decision.
Embargoed security fixes may use a private advisory instead of a public Issue.

## Review and merge

- All required CI checks must pass.
- Non-trivial changes require maintainer review.
- Public contract changes require explicit compatibility and migration review.
- Security-sensitive changes should receive a second qualified review when one
  is available.
- When only one maintainer is active, that maintainer records self-review evidence
  in the Pull Request and may merge after all automated checks pass; public
  releases still require the separate release checklist and owner authorization.

Force-pushes to shared branches, moving published tags, and replacing published
release assets are prohibited. Correct published mistakes with a new version.

## Maintainer changes

Repository owners appoint or remove maintainers based on sustained, constructive
work, judgment around compatibility/security, and ability to review the relevant
languages/platforms. Access is least-privilege and should be reviewed after role
changes or security incidents.

Exact maintainer handles and teams must be confirmed before adding CODEOWNERS;
an invalid owner silently weakens review automation. Repository rulesets should
then require the verified CODEOWNERS review for protected release surfaces.

## Release status

Bridra is licensed under MIT and maintained as a pre-1.0 framework. Every public
release requires a reviewed source commit, passing release checks, explicit
repository-owner authorization, and the evidence defined in `docs/RELEASING.md`.
Licensing and publication do not imply production support or an LTS commitment.

Changes to this governance document use the same proposal and review process as
other public contract changes.
