## Summary

Describe the user-visible problem and the chosen framework/application boundary.

## Verification

List exact commands, platforms, and results.

```text
make verify
make coverage
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
- [ ] `make verify` passes.
- [ ] `make coverage` passes without lowering floors to hide a regression.
- [ ] Changelog, architecture, upgrade, security, or release docs were updated as needed.
- [ ] No secrets, credentials, private data, or unredacted production logs are included.
- [ ] Breaking behavior includes an explicit migration and rollback path.
