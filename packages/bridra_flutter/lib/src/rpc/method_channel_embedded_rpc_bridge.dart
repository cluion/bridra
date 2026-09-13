import 'package:flutter/services.dart';

import 'embedded_rpc_client.dart';

/// Method-channel implementation of the application-owned embedded RPC bridge.
///
/// The iOS host must install a [BridraEmbeddedRuntime] through the native
/// `BridraFlutterPlugin` API. Bridra deliberately does not create or select an
/// application Core automatically.
final class MethodChannelEmbeddedRpcBridge
    implements EmbeddedRpcBridge, EmbeddedFileTransferBridge {
  MethodChannelEmbeddedRpcBridge({
    MethodChannel? channel,
    this.closeTimeout = const Duration(seconds: 5),
  }) : _channel = channel ?? const MethodChannel(channelName) {
    if (closeTimeout <= Duration.zero) {
      throw ArgumentError.value(
        closeTimeout,
        'closeTimeout',
        'Use a positive close timeout.',
      );
    }
  }

  static const channelName = 'dev.cluion.bridra/embedded_rpc';

  final MethodChannel _channel;
  final Duration closeTimeout;

  @override
  Future<String> call(String requestJSON) async {
    final response = await _channel.invokeMethod<Object?>('call', {
      'requestJSON': requestJSON,
    });
    if (response is! String) {
      throw const FormatException(
        'The embedded RPC channel returned a non-string response.',
      );
    }
    return response;
  }

  @override
  Stream<String> stream(String requestJSON) async* {
    final streamID = await _channel.invokeMethod<Object?>('streamStart', {
      'requestJSON': requestJSON,
    });
    if (streamID is! String || streamID.isEmpty) {
      throw const FormatException(
        'The embedded RPC channel returned an invalid stream id.',
      );
    }
    try {
      while (true) {
        final response = await _channel.invokeMethod<Object?>('streamNext', {
          'streamID': streamID,
        });
        if (response == null) return;
        if (response is! String) {
          throw const FormatException(
            'The embedded RPC channel returned a non-string stream frame.',
          );
        }
        yield response;
      }
    } finally {
      await _channel.invokeMethod<void>('streamDispose', {
        'streamID': streamID,
      });
    }
  }

  @override
  Future<void> cancel(String requestID) async {
    if (requestID.isEmpty) {
      throw ArgumentError.value(
        requestID,
        'requestID',
        'The id cannot be empty.',
      );
    }
    final matched = await _channel.invokeMethod<Object?>('cancel', {
      'requestID': requestID,
    });
    if (matched is! bool) {
      throw const FormatException(
        'The embedded RPC channel returned a non-boolean cancellation result.',
      );
    }
  }

  @override
  Future<String> openDownload(String fileID, int offset) async {
    final handle = await _channel.invokeMethod<Object?>('downloadOpen', {
      'fileID': fileID,
      'offset': offset,
    });
    if (handle is! String || handle.isEmpty) {
      throw const FormatException(
        'The embedded file channel returned an invalid download handle.',
      );
    }
    return handle;
  }

  @override
  Future<Uint8List?> readDownload(String handle, int maxBytes) async {
    final chunk = await _channel.invokeMethod<Object?>('downloadRead', {
      'handle': handle,
      'maxBytes': maxBytes,
    });
    if (chunk == null) return null;
    if (chunk is! Uint8List) {
      throw const FormatException(
        'The embedded file channel returned an invalid download chunk.',
      );
    }
    return chunk;
  }

  @override
  Future<void> closeDownload(String handle, {required bool commit}) =>
      _channel.invokeMethod<void>('downloadClose', {
        'handle': handle,
        'commit': commit,
      });

  @override
  Future<String> beginUpload({
    required String name,
    required String mediaType,
    required int size,
    required String sha256,
  }) async {
    final status = await _channel.invokeMethod<Object?>('uploadBegin', {
      'name': name,
      'mediaType': mediaType,
      'size': size,
      'sha256': sha256,
    });
    return _requireUploadStatus(status);
  }

  @override
  Future<String> appendUpload(
    String fileID,
    int offset,
    Uint8List chunk,
  ) async {
    final status = await _channel.invokeMethod<Object?>('uploadAppend', {
      'fileID': fileID,
      'offset': offset,
      'chunk': chunk,
    });
    return _requireUploadStatus(status);
  }

  @override
  Future<String> uploadStatus(String fileID) async {
    final status = await _channel.invokeMethod<Object?>('uploadStatus', {
      'fileID': fileID,
    });
    return _requireUploadStatus(status);
  }

  String _requireUploadStatus(Object? status) {
    if (status is! String) {
      throw const FormatException(
        'The embedded file channel returned an invalid upload status.',
      );
    }
    return status;
  }

  @override
  Future<void> close() => _channel.invokeMethod<void>('close', {
    'timeoutMilliseconds': closeTimeout.inMilliseconds,
  });
}
