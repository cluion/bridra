import 'desktop_single_instance_types.dart';

bool get isSupported => false;

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
