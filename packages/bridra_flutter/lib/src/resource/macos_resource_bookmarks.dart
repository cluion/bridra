import 'macos_resource_bookmarks_stub.dart'
    if (dart.library.io) 'macos_resource_bookmarks_io.dart'
    as implementation;
import 'resource_bookmark.dart';

abstract final class MacOSResourceBookmarks {
  static bool get isSupported => implementation.isSupported;

  static Future<ResourceBookmark> create({
    required String path,
    ResourceBookmarkScope scope = ResourceBookmarkScope.ephemeral,
    bool readOnly = true,
  }) {
    return implementation.createBookmark(
      path: path,
      scope: scope,
      readOnly: readOnly,
    );
  }
}
