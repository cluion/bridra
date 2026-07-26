# bridra_flutter

Reusable Flutter transport package for Bridra applications.

This package is licensed under the MIT License, Copyright (c) 2026 Cluion. It
is the Flutter-facing runtime package for the Bridra framework.

It provides:

- the common RPC client contract and error types;
- an HTTP RPC client for mobile, Web, and remote backends;
- a managed Go sidecar client for Windows, macOS, and Linux;
- a conditional default connector that selects the platform transport.

Desktop executable discovery checks `BRIDRA_SIDECAR_PATH`, the application
`libexec` directory, `build/sidecar`, then `backend/bin`. Web builds select the
HTTP connector through a conditional import and never import `dart:io`.

The desktop client automatically restarts an unexpectedly terminated Sidecar.
Calls that were in flight fail and are never replayed automatically. Calls made
during recovery wait for a replacement process to pass `system.health`, while
their own timeout and cancellation remain active.

Application-specific methods and response models do not belong in this package.
Define those in the consuming application's typed gateway.

## Install

    flutter pub add bridra_flutter

## Common transport

    import 'package:bridra_flutter/bridra_flutter.dart';

    final client = await connectDefaultRpcClient();
    final reply = await client.call('system.health');
    await client.close();

Calls accept an optional cancellation token. Timeouts use the same transport
cancellation path automatically.

    final cancellationToken = RpcCancellationToken();
    final reply = client.call(
      'report.build',
      cancellationToken: cancellationToken,
    );
    cancellationToken.cancel();

## Desktop sidecar

Desktop-only code may import the explicit sidecar library:

    import 'package:bridra_flutter/bridra_flutter_sidecar.dart';

    final client = await SidecarClient.start(
      executablePath: executablePath,
      token: SidecarClient.createToken(),
      restartPolicy: const SidecarRestartPolicy(
        maxAttempts: 3,
        initialDelay: Duration(milliseconds: 250),
        maxDelay: Duration(seconds: 2),
      ),
    );

The default policy uses three restart attempts. Set
`SidecarRestartPolicy.disabled()` only when the application owns recovery.
