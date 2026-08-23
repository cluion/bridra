import 'package:flutter/services.dart';

const _channel = MethodChannel('dev.cluion.bridra/application');

Future<void> terminateSecondary() {
  return _channel.invokeMethod<void>('terminateSecondary');
}
