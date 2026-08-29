import 'dart:io';

import 'package:bridra_flutter/bridra_flutter_sidecar.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('resource bookmark copies authority bytes and redacts toString', () {
    final source = <int>[1, 2, 3];
    final bookmark = ResourceBookmark.fromBytes(
      source,
      scope: ResourceBookmarkScope.persistent,
    );
    source[0] = 9;
    final firstCopy = bookmark.bytes;
    expect(firstCopy, [1, 2, 3]);
    firstCopy[0] = 8;
    expect(bookmark.bytes, [1, 2, 3]);
    expect(bookmark.toString(), isNot(contains('1, 2, 3')));
    expect(bookmark.toString(), contains('persistent'));
  });

  test('resource bookmark validates its byte bound without echoing bytes', () {
    for (final bytes in <List<int>>[
      const [],
      Uint8List(maximumResourceBookmarkBytes + 1),
    ]) {
      expect(
        () => ResourceBookmark.fromBytes(
          bytes,
          scope: ResourceBookmarkScope.ephemeral,
        ),
        throwsA(
          isA<ArgumentError>().having(
            (error) => error.toString(),
            'message',
            contains('[REDACTED]'),
          ),
        ),
      );
    }
  });

  test(
    'macOS bridge sends creation policy and returns typed bookmark',
    () async {
      const channel = MethodChannel('dev.cluion.bridra/resources');
      final calls = <MethodCall>[];
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, (call) async {
            calls.add(call);
            return Uint8List.fromList([4, 5, 6]);
          });
      addTearDown(() {
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
            .setMockMethodCallHandler(channel, null);
      });

      final bookmark = await MacOSResourceBookmarks.create(
        path: '/private/selected',
        scope: ResourceBookmarkScope.persistent,
        readOnly: true,
      );
      expect(bookmark.bytes, [4, 5, 6]);
      expect(bookmark.scope, ResourceBookmarkScope.persistent);
      expect(calls.single.method, 'createBookmark');
      expect(calls.single.arguments, {
        'path': '/private/selected',
        'scope': 'persistent',
        'readOnly': true,
      });
    },
    skip: !Platform.isMacOS,
  );
}
