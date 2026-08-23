import 'desktop_application_lifecycle_stub.dart'
    if (dart.library.ui) 'desktop_application_lifecycle_flutter.dart'
    as implementation;

Future<void> terminateSecondary() {
  return implementation.terminateSecondary();
}
