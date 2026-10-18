import 'dart:async';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'http_smoke_test.dart' as smoke;

void prepareXCTest() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('bridra.test.xctest');
  final ready = Completer<void>();
  channel.setMethodCallHandler((call) async {
    if (call.method != 'ready') {
      throw MissingPluginException();
    }
    ready.complete();
  });
  SemanticsHandle? semantics;
  setUpAll(() async {
    await ready.future.timeout(const Duration(seconds: 30));
    semantics = binding.ensureSemantics();
  });
  tearDownAll(() {
    semantics?.dispose();
    channel.setMethodCallHandler(null);
  });
}

void main() {
  prepareXCTest();
  smoke.main();
}
