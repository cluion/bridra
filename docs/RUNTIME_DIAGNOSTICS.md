# Runtime diagnostics and crash reporting

Bridra provides a redacted support bundle, a safe Sidecar lifecycle snapshot,
and an application-owned recovered-panic hook. Nothing is uploaded
automatically and no monitoring vendor is built into the framework.

## Create a support bundle

Run the CLI inside a Bridra project:

```bash
bridra diagnose
```

The command creates a new ZIP under
`build/diagnostics/bridra-diagnostics-<UTC>.zip`. It uses owner-only mode `0600`
on Unix-like hosts; Windows access follows the destination filesystem ACL. It
refuses to overwrite an existing bundle. Use an explicit destination when needed:

```bash
bridra diagnose --output build/diagnostics/support-case.zip
```

The archive contains only `diagnostics.json` and `README.txt`. It includes CLI,
Framework, Project Template, protocol, Go, Flutter, FVM, host, and build-tool
status. Project metadata contributes only its version contract.

The default privacy boundary excludes:

- environment variable names and values;
- tokens, credentials, request IDs, RPC methods, parameters, and bodies;
- application and Sidecar logs;
- source files, project identity, application module names, and absolute paths.

Always review `diagnostics.json` before sharing it and delete the archive when
the support case is complete.

## Include a Sidecar lifecycle snapshot

`SidecarClient.diagnostics()` returns immutable counters and at most 50 recent
lifecycle events. It records process starts and exits, restart attempts,
replacement health checks, recovery, exhaustion, pending-call counts, and
error types. It never records the executable path, token, RPC method, request
ID, params, response, or log text.

Application-owned code may export the snapshot temporarily:

```dart
import 'dart:convert';
import 'dart:io';

final snapshot = client.diagnostics();
await File('build/sidecar-diagnostics.json').writeAsString(
  jsonEncode(snapshot.toJson()),
);
```

Validate and include it in the bundle:

```bash
bridra diagnose --runtime build/sidecar-diagnostics.json
```

The CLI accepts only Sidecar diagnostics schema `1`, rejects unknown fields,
bounds the input to 64 KiB and 50 events, and validates every state, event,
timestamp, counter, and error-type name. Arbitrary JSON or log files cannot be
smuggled into the archive through this option.

## Connect a crash reporter

`RecoveryWithReporter` keeps the existing stable `internal_error` response and
notifies an application-owned `CrashReporter` when an RPC handler panics:

```go
reporter := framework.CrashReporterFunc(
    func(ctx context.Context, report framework.CrashReport) {
        crashService.Capture(ctx, report.Recovered, report.Stack)
    },
)

router.Use(framework.RecoveryWithReporter(reporter))
```

The report provides occurrence time, request ID, RPC method, completed pipeline
steps, recovered value, and a copied Go stack. Reporter panics are contained and
cannot replace the stable RPC response. Reporting is synchronous, so adapters
must return quickly and apply their own timeout, buffering, sampling, and
delivery policy.

The recovered value and Go stack can contain sensitive application data. They
are deliberately not written to `bridra diagnose`; the application must redact
and configure retention before forwarding them to Sentry or another provider.

`Recovery()` remains available and behaves exactly as before without reporting.
