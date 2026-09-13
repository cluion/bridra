import 'dart:typed_data';

import 'embedded_rpc_client.dart';

/// Pure-Dart placeholder for the Flutter MethodChannel implementation.
///
/// This keeps desktop helper processes able to import the public package
/// barrel without loading `dart:ui`. Flutter runtimes select the real bridge.
final class MethodChannelEmbeddedRpcBridge
    implements EmbeddedRpcBridge, EmbeddedFileTransferBridge {
  MethodChannelEmbeddedRpcBridge({
    Object? channel,
    this.closeTimeout = const Duration(seconds: 5),
  }) {
    if (closeTimeout <= Duration.zero) {
      throw ArgumentError.value(
        closeTimeout,
        'closeTimeout',
        'Use a positive close timeout.',
      );
    }
  }

  static const channelName = 'dev.cluion.bridra/embedded_rpc';

  final Duration closeTimeout;

  UnsupportedError get _unsupported => UnsupportedError(
    'MethodChannel embedded RPC requires a Flutter runtime.',
  );

  @override
  Future<String> call(String requestJSON) => Future.error(_unsupported);

  @override
  Stream<String> stream(String requestJSON) => Stream.error(_unsupported);

  @override
  Future<void> cancel(String requestID) => Future.error(_unsupported);

  @override
  Future<String> openDownload(String fileID, int offset) =>
      Future.error(_unsupported);

  @override
  Future<Uint8List?> readDownload(String handle, int maxBytes) =>
      Future.error(_unsupported);

  @override
  Future<void> closeDownload(String handle, {required bool commit}) =>
      Future.error(_unsupported);

  @override
  Future<String> beginUpload({
    required String name,
    required String mediaType,
    required int size,
    required String sha256,
  }) => Future.error(_unsupported);

  @override
  Future<String> appendUpload(String fileID, int offset, Uint8List chunk) =>
      Future.error(_unsupported);

  @override
  Future<String> uploadStatus(String fileID) => Future.error(_unsupported);

  @override
  Future<void> close() => Future.error(_unsupported);
}
