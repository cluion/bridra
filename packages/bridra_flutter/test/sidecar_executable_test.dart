import 'package:bridra_flutter/src/sidecar/sidecar_executable.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('configured executable has highest precedence', () async {
    final checked = <String>[];

    final resolved = await discoverSidecarExecutable(
      isWindows: false,
      pathSeparator: '/',
      environment: const {'BRIDRA_SIDECAR_PATH': '/configured/backend'},
      resolvedExecutableDirectory: '/app/bin',
      currentDirectory: '/workspace',
      fileExists: (path) async {
        checked.add(path);
        return path == '/configured/backend';
      },
    );

    expect(resolved, '/configured/backend');
    expect(checked, ['/configured/backend']);
  });

  test('checks bundle, build, and backend candidates in order', () async {
    final checked = <String>[];

    final resolved = await discoverSidecarExecutable(
      isWindows: false,
      pathSeparator: '/',
      environment: const {},
      resolvedExecutableDirectory: '/app/bin',
      currentDirectory: '/workspace',
      fileExists: (path) async {
        checked.add(path);
        return path == '/workspace/build/sidecar/bridra_backend';
      },
    );

    expect(resolved, '/workspace/build/sidecar/bridra_backend');
    expect(checked, [
      '/app/bin/libexec/bridra_backend',
      '/workspace/build/sidecar/bridra_backend',
    ]);
  });

  test('uses the Windows executable name and separator', () async {
    final checked = <String>[];

    await expectLater(
      discoverSidecarExecutable(
        isWindows: true,
        pathSeparator: r'\',
        environment: const {},
        resolvedExecutableDirectory: r'C:\app',
        currentDirectory: r'D:\workspace',
        fileExists: (path) async {
          checked.add(path);
          return false;
        },
      ),
      throwsA(
        isA<StateError>().having(
          (error) => error.message,
          'message',
          contains('BRIDRA_SIDECAR_PATH'),
        ),
      ),
    );

    expect(checked, [
      r'C:\app\libexec\bridra_backend.exe',
      r'D:\workspace\build/sidecar\bridra_backend.exe',
      r'D:\workspace\backend/bin\bridra_backend.exe',
    ]);
  });
}
