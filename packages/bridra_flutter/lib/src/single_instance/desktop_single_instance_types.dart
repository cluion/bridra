import 'dart:async';

final class DesktopActivation {
  DesktopActivation({
    Iterable<String> arguments = const [],
    required this.workingDirectory,
  }) : arguments = List<String>.unmodifiable(arguments) {
    if (workingDirectory.trim().isEmpty) {
      throw ArgumentError.value(
        workingDirectory,
        'workingDirectory',
        'must not be empty',
      );
    }
  }

  final List<String> arguments;
  final String workingDirectory;
}

abstract interface class DesktopSingleInstanceSession {
  bool get isPrimary;

  DesktopActivation get initialActivation;

  Stream<DesktopActivation> get activations;

  Future<void> close();
}

final class DesktopSingleInstanceException implements Exception {
  const DesktopSingleInstanceException(this.message, [this.cause]);

  final String message;
  final Object? cause;

  @override
  String toString() {
    final source = cause;
    if (source == null) return 'DesktopSingleInstanceException: $message';
    return 'DesktopSingleInstanceException: $message ($source)';
  }
}
