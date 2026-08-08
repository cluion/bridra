import 'package:bridra/main.dart' as app;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('iOS Simulator completes health and greeting RPCs', (
    tester,
  ) async {
    await app.main();

    await _pumpUntilFound(tester, find.text('Go core online'));

    final nameField = find.byKey(const Key('name-field'));
    final callButton = find.byKey(const Key('call-button'));
    await tester.ensureVisible(nameField);
    await tester.enterText(nameField, 'iOS Simulator');
    await tester.ensureVisible(callButton);
    await tester.tap(callButton);

    await _pumpUntilFound(tester, find.text('Hello, iOS Simulator!'));
    expect(find.text('200 · CONTROLLER RESPONSE'), findsOneWidget);
  });
}

Future<void> _pumpUntilFound(WidgetTester tester, Finder finder) async {
  for (var attempt = 0; attempt < 100 && finder.evaluate().isEmpty; attempt++) {
    await tester.pump(const Duration(milliseconds: 200));
  }
  expect(finder, findsOneWidget);
}
