import 'package:bridra/main.dart' as app;
import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

const smokeClient = String.fromEnvironment(
  'BRIDRA_IOS_SMOKE_CLIENT',
  defaultValue: 'iOS',
);
const smokeReconnect = bool.fromEnvironment('BRIDRA_IOS_SMOKE_RECONNECT');
const smokeStream = bool.fromEnvironment('BRIDRA_IOS_SMOKE_STREAM');
const smokeBackendUrl = String.fromEnvironment('BRIDRA_BACKEND_URL');
const smokeBackendToken = String.fromEnvironment('BRIDRA_BACKEND_TOKEN');
const smokeStreamMethod = 'bridra.smoke.stream';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('iOS completes HTTP smoke flow', (tester) async {
    await app.main();

    if (smokeReconnect) {
      await _retryUntilOnline(tester);
    } else {
      await _pumpUntilFound(tester, find.text('Go core online'));
    }
    await _callGreeting(tester, smokeClient);
    if (smokeStream) {
      await _verifyStreamingProgress();
    }

    if (smokeReconnect) {
      await _waitForBackendLoss(tester);
      await _retryUntilOnline(tester);
      await _callGreeting(tester, '$smokeClient reconnected');
      if (smokeStream) {
        await _verifyStreamingProgress();
      }
    }
  });
}

Future<void> _verifyStreamingProgress() async {
  final client = HttpRpcClient(
    endpoint: Uri.parse(smokeBackendUrl),
    token: smokeBackendToken,
  );
  try {
    final events = await client
        .stream(smokeStreamMethod, timeout: const Duration(seconds: 30))
        .toList();
    expect(events.map((event) => event.sequence), [1, 2, 3, 4, 5]);

    final firstProgress = events[0] as RpcStreamProgress<RpcReply>;
    expect(firstProgress.progress.completed, 0);
    expect(firstProgress.progress.total, 2);
    expect(firstProgress.progress.message, 'Streaming platform smoke');
    expect(firstProgress.progress.unit, 'items');

    final firstData = events[1] as RpcStreamData<RpcReply>;
    final secondProgress = events[2] as RpcStreamProgress<RpcReply>;
    final secondData = events[3] as RpcStreamData<RpcReply>;
    final completeProgress = events[4] as RpcStreamProgress<RpcReply>;
    expect(firstData.value.result, {'item': 'first'});
    expect(secondProgress.progress.completed, 1);
    expect(secondData.value.result, {'item': 'second'});
    expect(completeProgress.progress.completed, 2);
    expect(completeProgress.progress.fraction, 1);
  } finally {
    await client.close();
  }
}

Future<void> _callGreeting(WidgetTester tester, String name) async {
  final nameField = find.byKey(const Key('name-field'));
  final callButton = find.byKey(const Key('call-button'));
  await tester.ensureVisible(nameField);
  await tester.tap(nameField);
  await tester.pump();
  await tester.enterText(nameField, name);
  expect(tester.widget<TextField>(nameField).controller?.text, name);
  await tester.ensureVisible(callButton);
  await tester.tap(callButton);

  await _pumpUntilFound(tester, find.text('Hello, $name!'));
  expect(find.text('200 · CONTROLLER RESPONSE'), findsOneWidget);
}

Future<void> _waitForBackendLoss(WidgetTester tester) async {
  final callButton = find.byKey(const Key('call-button'));
  final unavailable = find.text('Go core unavailable');
  for (
    var attempt = 0;
    attempt < 150 && unavailable.evaluate().isEmpty;
    attempt++
  ) {
    if (callButton.evaluate().isNotEmpty &&
        tester.widget<FilledButton>(callButton).onPressed != null) {
      await tester.ensureVisible(callButton);
      await tester.tap(callButton);
    }
    await tester.pump(const Duration(milliseconds: 200));
  }
  expect(unavailable, findsOneWidget);
  expect(find.byKey(const Key('retry-button')), findsOneWidget);
}

Future<void> _retryUntilOnline(WidgetTester tester) async {
  final retryButton = find.byKey(const Key('retry-button'));
  final online = find.text('Go core online');
  for (var attempt = 0; attempt < 150 && online.evaluate().isEmpty; attempt++) {
    if (retryButton.evaluate().isNotEmpty &&
        tester.widget<OutlinedButton>(retryButton).onPressed != null) {
      await tester.ensureVisible(retryButton);
      await tester.tap(retryButton);
    }
    await tester.pump(const Duration(milliseconds: 200));
  }
  expect(online, findsOneWidget);
}

Future<void> _pumpUntilFound(WidgetTester tester, Finder finder) async {
  for (var attempt = 0; attempt < 150 && finder.evaluate().isEmpty; attempt++) {
    await tester.pump(const Duration(milliseconds: 200));
  }
  expect(finder, findsOneWidget);
}
