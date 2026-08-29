import 'dart:io';

import 'package:flutter/services.dart';

import 'resource_bookmark.dart';

const _channel = MethodChannel('dev.cluion.bridra/resources');

bool get isSupported => Platform.isMacOS;

Future<ResourceBookmark> createBookmark({
  required String path,
  required ResourceBookmarkScope scope,
  required bool readOnly,
}) async {
  if (!isSupported) {
    throw UnsupportedError('Native resource bookmarks require macOS.');
  }
  if (path.isEmpty || path.contains('\u0000')) {
    throw ArgumentError.value(
      '[REDACTED]',
      'path',
      'Use a non-empty file path.',
    );
  }
  final data = await _channel.invokeMethod<Uint8List>('createBookmark', {
    'path': path,
    'scope': scope.wireName,
    'readOnly': readOnly,
  });
  if (data == null) {
    throw PlatformException(
      code: 'resource_bookmark_failed',
      message: 'macOS did not return resource bookmark data.',
    );
  }
  return ResourceBookmark.fromBytes(data, scope: scope);
}
