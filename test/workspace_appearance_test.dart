import 'dart:convert';

import 'package:bridra/bridra_app.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fake_backend.dart';

void main() {
  testWidgets(
    'appearance previews update every page without losing the request',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 820);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final backend = FakeBackend();
      await tester.pumpWidget(BridraApp(connector: () async => backend));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const Key('name-field')),
        'Theme cards',
      );
      await tester.tap(find.byKey(const Key('call-button')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('destination-settings')));
      await tester.pumpAndSettle();
      for (final mode in [ThemeMode.dark, ThemeMode.light, ThemeMode.system]) {
        await tester.tap(find.byKey(Key('appearance-preview-${mode.name}')));
        await tester.pumpAndSettle();
        expect(
          tester.widget<MaterialApp>(find.byType(MaterialApp)).themeMode,
          mode,
        );
        expect(
          tester
              .widget<RadioGroup<ThemeMode>>(find.byType(RadioGroup<ThemeMode>))
              .groupValue,
          mode,
        );
      }
      await tester.tap(find.byKey(const Key('navigation-back')));
      await tester.pumpAndSettle();
      expect(find.text('Hello, Theme cards!'), findsOneWidget);
      expect(
        tester
            .widget<TextField>(find.byKey(const Key('name-field')))
            .controller!
            .text,
        'Theme cards',
      );
      expect(backend.healthCalls, 1);
      expect(backend.greetCalls, 1);
      expect(backend.closeCalls, 0);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('selects a request example and sends its actual payload', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1280, 820);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final backend = FakeBackend();
    await tester.pumpWidget(BridraApp(connector: () async => backend));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('request-example')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Bridra greeting').hitTestable());
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<TextField>(find.byKey(const Key('name-field')))
          .controller!
          .text,
      'Bridra',
    );
    final payload = tester.widget<SelectableText>(
      find.byKey(const Key('request-payload')),
    );
    expect(jsonDecode(payload.data!), {'name': 'Bridra'});
    final submit = find.byKey(const Key('call-button'));
    await tester.ensureVisible(submit);
    await tester.tap(submit);
    await tester.pumpAndSettle();
    expect(find.text('Hello, Bridra!'), findsOneWidget);
    expect(backend.greetCalls, 1);
  });

  testWidgets(
    'retains custom input when dismissing the menu in both appearances',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 820);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await tester.pumpWidget(BridraApp(connector: () async => FakeBackend()));
      await tester.pumpAndSettle();
      final field = find.byKey(const Key('name-field'));
      await tester.enterText(field, 'My own draft');
      await tester.pumpAndSettle();
      final example = find.byKey(const Key('request-example'));
      final selectedField = find.descendant(
        of: example,
        matching: find.byType(TextField),
      );
      expect(
        tester.widget<TextField>(selectedField).controller!.text,
        'Custom input',
      );
      for (var i = 0; i < 2; i++) {
        await tester.tap(find.byKey(const Key('appearance-toggle')));
        await tester.pumpAndSettle();
        await tester.tap(example);
        await tester.pumpAndSettle();
        expect(find.text('Bridra greeting').hitTestable(), findsOneWidget);
        await tester.sendKeyEvent(LogicalKeyboardKey.escape);
        await tester.pumpAndSettle();
        expect(find.text('Bridra greeting').hitTestable(), findsNothing);
        expect(
          tester.widget<TextField>(field).controller!.text,
          'My own draft',
        );
        expect(
          tester.widget<TextField>(selectedField).controller!.text,
          'Custom input',
        );
        expect(tester.takeException(), isNull);
      }
    },
  );

  testWidgets('switches appearance without losing the request or connection', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1280, 820);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.platformBrightnessTestValue = Brightness.dark;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.platformDispatcher.clearPlatformBrightnessTestValue);
    final backend = FakeBackend();
    await tester.pumpWidget(BridraApp(connector: () async => backend));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('name-field')), 'Appearance');
    await tester.tap(find.byKey(const Key('call-button')));
    await tester.pumpAndSettle();

    for (final mode in [ThemeMode.light, ThemeMode.dark]) {
      await tester.tap(find.byKey(const Key('appearance-toggle')));
      await tester.pumpAndSettle();
      expect(
        tester.widget<MaterialApp>(find.byType(MaterialApp)).themeMode,
        mode,
      );
      expect(find.text('Hello, Appearance!'), findsOneWidget);
      expect(
        tester
            .widget<TextField>(find.byKey(const Key('name-field')))
            .controller!
            .text,
        'Appearance',
      );
      expect(find.text('Connected'), findsOneWidget);
      expect(backend.healthCalls, 1);
      expect(backend.greetCalls, 1);
      expect(backend.closeCalls, 0);
      expect(tester.takeException(), isNull);
    }
  });

  testWidgets(
    'previews escaped input and submits the same value on narrow screens',
    (tester) async {
      tester.view.physicalSize = const Size(320, 720);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      String? submittedName;
      final backend = FakeBackend(
        onGreet: (request, _) async {
          submittedName = request.name;
          return greetingFor(request);
        },
      );
      await tester.pumpWidget(BridraApp(connector: () async => backend));
      await tester.pumpAndSettle();
      final name = '工作台 "Pro" \\ ${'long input ' * 12}';
      final field = find.byKey(const Key('name-field'));
      await tester.ensureVisible(field);
      await tester.enterText(field, name);
      await tester.pump();
      final payload = tester.widget<SelectableText>(
        find.byKey(const Key('request-payload')),
      );
      expect(jsonDecode(payload.data!), {'name': name});
      final submit = find.byKey(const Key('call-button'));
      await tester.ensureVisible(submit);
      await tester.tap(submit);
      await tester.pumpAndSettle();
      expect(submittedName, name);
    },
  );
}
