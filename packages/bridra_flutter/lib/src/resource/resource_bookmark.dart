import 'dart:typed_data';

const maximumResourceBookmarkBytes = 1 << 20;

enum ResourceBookmarkScope {
  ephemeral('ephemeral'),
  persistent('persistent');

  const ResourceBookmarkScope(this.wireName);

  final String wireName;
}

/// Authority-bearing bookmark data created by an application-owned native UI.
///
/// Do not log these bytes or include them in diagnostics, deep links, or RPC
/// schema baselines. Persistent bookmark storage remains application-owned.
final class ResourceBookmark {
  ResourceBookmark.fromBytes(List<int> bytes, {required this.scope})
    : _bytes = _validatedCopy(bytes);

  final ResourceBookmarkScope scope;
  final Uint8List _bytes;

  Uint8List get bytes => Uint8List.fromList(_bytes);

  static Uint8List _validatedCopy(List<int> bytes) {
    if (bytes.isEmpty || bytes.length > maximumResourceBookmarkBytes) {
      throw ArgumentError.value(
        '[REDACTED]',
        'bytes',
        'Use non-empty bookmark data no larger than '
            '$maximumResourceBookmarkBytes bytes.',
      );
    }
    return Uint8List.fromList(bytes);
  }

  @override
  String toString() => 'ResourceBookmark([REDACTED], scope: ${scope.name})';
}
