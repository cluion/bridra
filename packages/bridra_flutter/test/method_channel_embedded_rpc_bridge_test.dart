import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  const channel = MethodChannel(MethodChannelEmbeddedRpcBridge.channelName);
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;

  tearDown(() async {
    messenger.setMockMethodCallHandler(channel, null);
  });

  test('uses the stable native call envelope', () async {
    MethodCall? received;
    messenger.setMockMethodCallHandler(channel, (call) async {
      received = call;
      return '{"id":"1","result":{"status":"ok"}}';
    });
    final bridge = MethodChannelEmbeddedRpcBridge(channel: channel);

    final response = await bridge.call('{"id":"1"}');

    expect(response, contains('"status":"ok"'));
    expect(received?.method, 'call');
    expect(received?.arguments, {'requestJSON': '{"id":"1"}'});
  });

  test('forwards exact cancellation id and accepts an unmatched id', () async {
    MethodCall? received;
    messenger.setMockMethodCallHandler(channel, (call) async {
      received = call;
      return false;
    });
    final bridge = MethodChannelEmbeddedRpcBridge(channel: channel);

    await bridge.cancel('request-42');

    expect(received?.method, 'cancel');
    expect(received?.arguments, {'requestID': 'request-42'});
  });

  test('forwards bounded native shutdown', () async {
    MethodCall? received;
    messenger.setMockMethodCallHandler(channel, (call) async {
      received = call;
      return null;
    });
    final bridge = MethodChannelEmbeddedRpcBridge(
      channel: channel,
      closeTimeout: const Duration(milliseconds: 2750),
    );

    await bridge.close();

    expect(received?.method, 'close');
    expect(received?.arguments, {'timeoutMilliseconds': 2750});
  });

  test('rejects malformed native results and invalid arguments', () async {
    messenger.setMockMethodCallHandler(channel, (call) async => null);
    final bridge = MethodChannelEmbeddedRpcBridge(channel: channel);

    await expectLater(bridge.call('{}'), throwsA(isA<FormatException>()));
    await expectLater(bridge.cancel('id'), throwsA(isA<FormatException>()));
    await expectLater(bridge.cancel(''), throwsArgumentError);
    expect(
      () => MethodChannelEmbeddedRpcBridge(
        channel: channel,
        closeTimeout: Duration.zero,
      ),
      throwsArgumentError,
    );
  });
}
