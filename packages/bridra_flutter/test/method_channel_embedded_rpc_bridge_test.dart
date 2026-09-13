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

  test('pulls ordered native stream frames and disposes the handle', () async {
    final calls = <MethodCall>[];
    var nextCalls = 0;
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      switch (call.method) {
        case 'streamStart':
          return 'native-stream-1';
        case 'streamNext':
          nextCalls++;
          if (nextCalls == 1) {
            return '{"id":"1","stream":{"sequence":1,"kind":"complete"}}';
          }
          return null;
        case 'streamDispose':
          return null;
      }
      fail('Unexpected method ${call.method}.');
    });
    final bridge = MethodChannelEmbeddedRpcBridge(channel: channel);

    final frames = await bridge.stream('{"id":"1"}').toList();

    expect(frames, hasLength(1));
    expect(calls.map((call) => call.method), [
      'streamStart',
      'streamNext',
      'streamNext',
      'streamDispose',
    ]);
    expect(calls.first.arguments, {'requestJSON': '{"id":"1"}'});
    expect(calls[1].arguments, {'streamID': 'native-stream-1'});
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

  test('uses typed bytes and opaque handles for file transfer', () async {
    final calls = <MethodCall>[];
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      switch (call.method) {
        case 'downloadOpen':
          return 'download-1';
        case 'downloadRead':
          return Uint8List.fromList([1, 2, 3]);
        case 'downloadClose':
          return null;
        case 'uploadBegin':
        case 'uploadAppend':
        case 'uploadStatus':
          return '{"file":{},"offset":0,"complete":false}';
      }
      fail('Unexpected method ${call.method}.');
    });
    final bridge = MethodChannelEmbeddedRpcBridge(channel: channel);

    final handle = await bridge.openDownload('file-1', 7);
    final chunk = await bridge.readDownload(handle, 64 * 1024);
    await bridge.closeDownload(handle, commit: true);
    await bridge.beginUpload(
      name: 'input.bin',
      mediaType: 'application/octet-stream',
      size: 3,
      sha256: 'checksum',
    );
    await bridge.appendUpload('upload-1', 0, Uint8List.fromList([1, 2, 3]));
    await bridge.uploadStatus('upload-1');

    expect(chunk, [1, 2, 3]);
    expect(calls[0].arguments, {'fileID': 'file-1', 'offset': 7});
    expect(calls[1].arguments, {'handle': 'download-1', 'maxBytes': 64 * 1024});
    expect(calls[2].arguments, {'handle': 'download-1', 'commit': true});
    expect(calls[3].arguments, {
      'name': 'input.bin',
      'mediaType': 'application/octet-stream',
      'size': 3,
      'sha256': 'checksum',
    });
    expect(calls[4].arguments['chunk'], isA<Uint8List>());
    expect(calls[5].arguments, {'fileID': 'upload-1'});
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

  test('rejects malformed native stream handles and frames', () async {
    var invalidHandle = true;
    messenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'streamStart') {
        return invalidHandle ? null : 'native-stream-1';
      }
      if (call.method == 'streamNext') return 42;
      return null;
    });
    final bridge = MethodChannelEmbeddedRpcBridge(channel: channel);

    await expectLater(bridge.stream('{}'), emitsError(isA<FormatException>()));
    invalidHandle = false;
    await expectLater(bridge.stream('{}'), emitsError(isA<FormatException>()));
  });
}
