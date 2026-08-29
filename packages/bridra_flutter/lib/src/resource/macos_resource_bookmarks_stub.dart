import 'resource_bookmark.dart';

bool get isSupported => false;

Future<ResourceBookmark> createBookmark({
  required String path,
  required ResourceBookmarkScope scope,
  required bool readOnly,
}) {
  return Future<ResourceBookmark>.error(
    UnsupportedError('Native resource bookmarks require macOS.'),
  );
}
