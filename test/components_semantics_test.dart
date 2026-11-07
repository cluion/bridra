import 'dart:ui' as ui;

import 'package:bridra/app/starter_app.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fake_backend.dart';

class _SemanticsBinding extends AutomatedTestWidgetsFlutterBinding {
  final nodes = <int, List<int>>{};
  final orphans = <int>{};

  @override
  ui.SemanticsUpdateBuilder createSemanticsUpdateBuilder() =>
      _RecordingBuilder(nodes, orphans);
}

class _RecordingBuilder implements ui.SemanticsUpdateBuilder {
  _RecordingBuilder(this.nodes, this.orphans);

  final Map<int, List<int>> nodes;
  final Set<int> orphans;
  final updated = <int>[];

  @override
  dynamic noSuchMethod(Invocation invocation) {
    if (invocation.memberName == #updateNode) {
      final arguments = invocation.namedArguments;
      final id = arguments[#id] as int;
      nodes[id] = List<int>.of(
        arguments[#childrenInTraversalOrder] as Iterable<int>,
      );
      updated.add(id);
    }
    return null;
  }

  @override
  ui.SemanticsUpdate build() {
    // Native accessibility bridges reject updates containing orphan nodes.
    final reachable = <int>{};
    void visit(int id) {
      if (!reachable.add(id)) return;
      for (final child in nodes[id] ?? <int>[]) {
        visit(child);
      }
    }

    visit(0);
    orphans.addAll(updated.where((id) => !reachable.contains(id)));
    return ui.SemanticsUpdateBuilder().build();
  }
}

void main() {
  final binding = _SemanticsBinding();
  testWidgets('gallery semantics updates remain reachable from the root', (
    tester,
  ) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    tester.view.physicalSize = const Size(1280, 1200);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final semantics = tester.ensureSemantics();
    try {
      final backend = FakeBackend();
      await tester.pumpWidget(StarterApp(connector: () async => backend));
      await tester.pumpAndSettle();
      expect(binding.orphans, isEmpty, reason: 'Initial workspace');
      await tester.tap(find.byKey(const Key('destination-components')));
      await tester.pump(const Duration(milliseconds: 350));
      expect(binding.orphans, isEmpty, reason: 'Gallery controls');
      final slider = find.byKey(const Key('gallery-slider'));
      await tester.ensureVisible(slider);
      final gesture = await tester.startGesture(tester.getCenter(slider));
      await gesture.moveBy(const Offset(30, 0));
      await tester.pump(const Duration(milliseconds: 350));
      expect(binding.orphans, isEmpty, reason: 'Slider value indicator');
      await gesture.up();
      for (final tab in ['feedback', 'overlays', 'data', 'controls']) {
        final target = find.byKey(Key('components-tab-$tab'));
        await tester.ensureVisible(target);
        await tester.tap(target);
        await tester.pump(const Duration(milliseconds: 350));
        expect(binding.orphans, isEmpty, reason: 'Gallery $tab');
      }
      await tester.tap(find.byKey(const Key('destination-settings')));
      await tester.pumpAndSettle();
      for (final mode in ['light', 'dark']) {
        await tester.tap(find.byKey(Key('appearance-$mode')));
        await tester.pumpAndSettle();
        expect(binding.orphans, isEmpty, reason: 'Appearance $mode');
      }
    } finally {
      semantics.dispose();
      debugDefaultTargetPlatformOverride = null;
    }
  });
}
