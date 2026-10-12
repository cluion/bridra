## Summary

Describe the user-visible problem and the chosen framework/application boundary.

## Verification

List the focused commands, selected CI scope, platforms, and results. Explain
any unverified platform or publication behavior.

```text
# Relevant local tests for this change
# CI changes: python3 -m unittest discover -s tool -p ci_test.py -v; actionlint
```

## Contract impact

- Public Go API:
- Public Dart API:
- RPC protocol / stable errors:
- Project metadata / template / generated code:
- Security / privacy:
- Platforms and release artifacts:
- Migration / rollback:

## Checklist

- [ ] The change is focused and has regression coverage.
- [ ] Generated files were updated through `make generate`, not edited manually.
- [ ] External-package tests cover changed public Go APIs.
- [ ] Exported Dart behavior is tested through a public package entry.
- [ ] Relevant local verification and the selected PR checks pass.
- [ ] `ci-gate` passes against the up-to-date merge result.
- [ ] Applicable coverage floors pass without being lowered to hide a regression.
- [ ] Changelog, architecture, upgrade, security, or release docs were updated as needed.
- [ ] No secrets, credentials, private data, or unredacted production logs are included.
- [ ] Breaking behavior includes an explicit migration and rollback path.
