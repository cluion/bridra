import 'dart:io';

import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:bridra_flutter/bridra_flutter_sidecar.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final desktop =
      !kIsWeb && (Platform.isLinux || Platform.isMacOS || Platform.isWindows);

  test(
    'public default connector discovers and starts the desktop sidecar',
    () async {
      final configuredPath = Platform.environment['BRIDRA_SIDECAR_PATH'];
      expect(configuredPath, isNotNull);
      expect(await File(configuredPath!).exists(), isTrue);
      expect(await SidecarClient.resolveExecutable(), configuredPath);

      final client = await connectDefaultRpcClient();
      addTearDown(client.close);

      expect(client, isA<SidecarClient>());
      final health = await client.call('system.health');
      expect((health.result as Map)['status'], 'ok');
      expect((health.result as Map)['protocolVersion'], 1);
    },
    skip: desktop ? false : 'Desktop IO connector test.',
  );
}
