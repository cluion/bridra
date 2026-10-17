import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:flutter/material.dart';

import 'bridra_app.dart';
import 'ui/window/desktop_window.dart';

Future<void> main([List<String> arguments = const []]) async {
  WidgetsFlutterBinding.ensureInitialized();
  if (DesktopSingleInstance.isSupported) {
    final instance = await DesktopSingleInstance.acquire(
      applicationId: 'dev.example.bridra',
      arguments: arguments,
    );
    if (!instance.isPrimary) {
      await DesktopSingleInstance.terminateSecondary(instance);
      return;
    }
    instance.activations.listen(_handleActivation);
  }
  final desktopWindowControls = await initializeDesktopWindow();
  runApp(BridraApp(desktopWindowControls: desktopWindowControls));
}

void _handleActivation(DesktopActivation activation) {
  debugPrint('Bridra activation: ${activation.arguments}');
}
