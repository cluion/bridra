# bridra_flutter

Reusable Flutter transport package for Bridra applications.

This package is licensed under the MIT License, Copyright (c) 2026 Cluion. It
is the Flutter-facing runtime package for the Bridra framework.

It provides:

- the common RPC client contract and error types;
- an HTTP RPC client that sends Bearer credentials for mobile, Web, and remote
  backends;
- typed HTTP 429 handling with optional `Retry-After` duration;
- a managed Go sidecar client for Windows, macOS, and Linux;
- verified out-of-band file uploads and resumable downloads for HTTP and
  Desktop Sidecars;
- desktop single-instance ownership and activation forwarding;
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

Server-streaming calls emit typed data and progress events. Generated APIs
perform application-result decoding; the transport package owns framing:

    await for (final event in api.buildReport(request)) {
      if (event is RpcStreamProgress<ReportPage>) {
        updateProgress(event.progress.fraction);
      } else {
        render((event as RpcStreamData<ReportPage>).value);
      }
    }

The default stream timeout is five minutes. HTTP uses flushed NDJSON. Desktop
Sidecars use a bounded credit window and acknowledge each event only after the
listener consumes it.

Large results use a generated `RpcFileReference` instead of embedding bytes in
JSON. The same API streams HTTP response chunks or reads a Sidecar-managed
temporary file, then verifies the declared byte count and SHA-256 digest:

    final export = await api.exportReport(request);
    await for (final chunk in client.download(export.file)) {
      output.add(chunk);
    }

HTTP downloads resume automatically from the verified byte offset, with three
attempts by default, and capabilities are consumed only after a complete
response. Desktop files are deleted after consumption. If integrity validation
still fails, discard any partial output already written.

Upload a large input before passing its generated `RpcFileReference` to a typed
request:

    final source = File(path);
    final digest = await sha256.bind(source.openRead()).first;
    final upload = RpcFileUpload(
      name: 'archive.zip',
      mediaType: 'application/zip',
      size: await source.length(),
      sha256: digest.toString(),
      openRead: (offset) => source.openRead(offset),
    );
    final file = await client.upload(upload);
    await api.importArchive(ImportArchiveRequest(file: file));

HTTP uploads recover from the server-reported offset. Desktop uploads use a
bounded, verified staging file and the reserved `rpc.file_upload` Sidecar
method; file bytes never enter the JSON RPC envelope.

## Desktop single instance

Acquire ownership once in the root isolate before `runApp`. A later process
forwards its command-line arguments, including file paths or deep-link URIs, to
the primary process and returns `isPrimary == false`.

    Future<void> main([List<String> arguments = const []]) async {
      WidgetsFlutterBinding.ensureInitialized();
      final instance = await DesktopSingleInstance.acquire(
        applicationId: 'com.example.my_app',
        arguments: arguments,
      );
      if (!instance.isPrimary) return;

      instance.activations.listen((activation) {
        openFilesAndLinks(activation.arguments);
      });
      runApp(const MyApp());
    }

The ownership lock is released by the operating system if the primary process
crashes. Activation transport is bound to IPv4 loopback, uses an ephemeral port
and a random token, limits frames to 1 MiB, and waits for an acknowledgement
before the later process exits. Call `acquire` only once from the root isolate;
desktop file locks are process-scoped on Linux and macOS.

## Desktop sidecar

Desktop-only code may import the explicit sidecar library:

    import 'package:bridra_flutter/bridra_flutter_sidecar.dart';

    final client = await SidecarClient.start(
      executablePath: executablePath,
      token: SidecarClient.createToken(),
      streamWindow: 16,
      restartPolicy: const SidecarRestartPolicy(
        maxAttempts: 3,
        initialDelay: Duration(milliseconds: 250),
        maxDelay: Duration(seconds: 2),
      ),
    );

The default policy uses three restart attempts. Set
`SidecarRestartPolicy.disabled()` only when the application owns recovery.

Read an immutable, redacted lifecycle snapshot for support diagnostics:

    final diagnostics = client.diagnostics();
    final json = jsonEncode(diagnostics.toJson());

The snapshot contains state, bounded counters, process exits, restart attempts,
replacement health checks, recovery, and error type names. It never contains
the executable path, token, RPC method, request data, responses, or log text.
See the repository's Runtime diagnostics guide before persisting or sharing it.
