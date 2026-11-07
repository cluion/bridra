import 'package:bridra/bridra_app.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fake_backend.dart';

Rect fieldBorder(WidgetTester tester, String key) {
  final editable = find.descendant(
    of: find.byKey(Key(key)),
    matching: find.byType(EditableText),
  );
  final border = InputDecorator.containerOf(tester.element(editable))!;
  return border.localToGlobal(Offset.zero) & border.size;
}

Rect materialSurface(WidgetTester tester, String key) => tester.getRect(
  find
      .descendant(of: find.byKey(Key(key)), matching: find.byType(Material))
      .first,
);

void main() {
  final desktop = TargetPlatformVariant({
    TargetPlatform.macOS,
    TargetPlatform.windows,
    TargetPlatform.linux,
  });

  testWidgets(
    'aligns the painted request field borders in desktop appearances',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 820);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearPlatformBrightnessTestValue);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      await tester.pumpWidget(BridraApp(connector: () async => FakeBackend()));
      await tester.pumpAndSettle();
      for (final scale in [1.0, 1.5, 2.0]) {
        tester.platformDispatcher.textScaleFactorTestValue = scale;
        for (final brightness in Brightness.values) {
          tester.platformDispatcher.platformBrightnessTestValue = brightness;
          await tester.pumpAndSettle();
          final example = fieldBorder(tester, 'request-example');
          final name = fieldBorder(tester, 'name-field');
          expect(
            example.height,
            closeTo(name.height, 0.01),
            reason: '$scale / $brightness',
          );
          if (scale == 1) {
            expect(example.top, closeTo(name.top, 0.01));
            expect(example.bottom, closeTo(name.bottom, 0.01));
          }
          expect(tester.takeException(), isNull);
        }
      }
    },
    variant: desktop,
  );

  testWidgets(
    'aligns the selected Settings surface with the content card bottom',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 820);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearPlatformBrightnessTestValue);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      await tester.pumpWidget(BridraApp(connector: () async => FakeBackend()));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('destination-settings')));
      await tester.pumpAndSettle();
      for (var sidebar = 0; sidebar < 2; sidebar++) {
        for (final scale in [1.0, 1.5, 2.0]) {
          tester.platformDispatcher.textScaleFactorTestValue = scale;
          for (final brightness in Brightness.values) {
            tester.platformDispatcher.platformBrightnessTestValue = brightness;
            await tester.pumpAndSettle();
            final settings = materialSurface(tester, 'destination-settings');
            final content = materialSurface(tester, 'main-content-card');
            expect(
              settings.bottom,
              closeTo(content.bottom, 0.01),
              reason: '$sidebar / $scale / $brightness',
            );
            expect(tester.takeException(), isNull);
          }
        }
        await tester.tap(find.byKey(const Key('sidebar-toggle')));
        await tester.pumpAndSettle();
      }
    },
    variant: desktop,
  );
}
