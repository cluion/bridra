# bridra_flutter

Reusable Flutter transport package for Bridra 0.1 applications.

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

Application-specific methods and response models do not belong in this package.
Define those in the consuming application's typed gateway.

## Install

    flutter pub add bridra_flutter

## Common transport

    import 'package:bridra_flutter/bridra_flutter.dart';

    final client = await connectDefaultRpcClient();
    final reply = await client.call('system.health');
    await client.close();

## Desktop sidecar

Desktop-only code may import the explicit sidecar library:

    import 'package:bridra_flutter/bridra_flutter_sidecar.dart';

    final client = await SidecarClient.start(
      executablePath: executablePath,
      token: SidecarClient.createToken(),
    );
