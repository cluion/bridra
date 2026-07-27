import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:flutter/material.dart';

import 'bridra_app.dart';

Future<void> main([List<String> arguments = const []]) async {
  WidgetsFlutterBinding.ensureInitialized();
  if (DesktopSingleInstance.isSupported) {
    final instance = await DesktopSingleInstance.acquire(
      applicationId: 'dev.example.bridra',
      arguments: arguments,
    );
    if (!instance.isPrimary) return;
    instance.activations.listen(_handleActivation);
  }
  runApp(const BridraApp());
}

void _handleActivation(DesktopActivation activation) {
  debugPrint('Bridra activation: ${activation.arguments}');
}
