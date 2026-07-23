typedef SidecarFileExists = Future<bool> Function(String path);

Future<String> discoverSidecarExecutable({
  required bool isWindows,
  required String pathSeparator,
  required Map<String, String> environment,
  required String resolvedExecutableDirectory,
  required String currentDirectory,
  required SidecarFileExists fileExists,
}) async {
  final executableName = isWindows ? 'bridra_backend.exe' : 'bridra_backend';
  String join(String left, String right) => '$left$pathSeparator$right';

  final candidates = <String>[
    if (environment['BRIDRA_SIDECAR_PATH'] case final configured?
        when configured.isNotEmpty)
      configured,
    join(join(resolvedExecutableDirectory, 'libexec'), executableName),
    join(join(currentDirectory, 'build/sidecar'), executableName),
    join(join(currentDirectory, 'backend/bin'), executableName),
  ];

  for (final candidate in candidates) {
    if (await fileExists(candidate)) return candidate;
  }
  throw StateError(
    'Go sidecar not found. Checked: ${candidates.join(', ')}. '
    'Run `make backend-build` or set BRIDRA_SIDECAR_PATH.',
  );
}
