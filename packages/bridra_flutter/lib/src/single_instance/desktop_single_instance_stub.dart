import 'desktop_single_instance_types.dart';

bool get isSupported => false;

Future<void> terminateSecondary(DesktopSingleInstanceSession session) async {
  if (session.isPrimary) {
    throw StateError('The primary desktop instance must remain running.');
  }
  await session.close();
}

Future<DesktopSingleInstanceSession> acquire({
  required String applicationId,
  Iterable<String> arguments = const [],
  String? workingDirectory,
  String? runtimeDirectory,
  Duration startupTimeout = const Duration(seconds: 5),
}) {
  return Future<DesktopSingleInstanceSession>.error(
    UnsupportedError(
      'Desktop single-instance coordination is only available on '
      'Windows, macOS, and Linux.',
    ),
  );
}
