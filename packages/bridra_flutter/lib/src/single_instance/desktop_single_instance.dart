import 'desktop_single_instance_stub.dart'
    if (dart.library.io) 'desktop_single_instance_io.dart'
    as implementation;
import 'desktop_single_instance_types.dart';

export 'desktop_single_instance_types.dart';

abstract final class DesktopSingleInstance {
  static bool get isSupported => implementation.isSupported;

  static Future<DesktopSingleInstanceSession> acquire({
    required String applicationId,
    Iterable<String> arguments = const [],
    String? workingDirectory,
    String? runtimeDirectory,
    Duration startupTimeout = const Duration(seconds: 5),
  }) {
    return implementation.acquire(
      applicationId: applicationId,
      arguments: arguments,
      workingDirectory: workingDirectory,
      runtimeDirectory: runtimeDirectory,
      startupTimeout: startupTimeout,
    );
  }
}
