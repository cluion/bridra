Future<void> terminateSecondary() {
  return Future<void>.error(
    UnsupportedError('Native application lifecycle requires a Flutter engine.'),
  );
}
