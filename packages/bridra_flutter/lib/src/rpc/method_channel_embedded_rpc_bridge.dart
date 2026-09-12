import 'package:flutter/services.dart';

import 'embedded_rpc_client.dart';

/// Method-channel implementation of the application-owned embedded RPC bridge.
///
/// The iOS host must install a [BridraEmbeddedRuntime] through the native
/// `BridraFlutterPlugin` API. Bridra deliberately does not create or select an
/// application Core automatically.
final class MethodChannelEmbeddedRpcBridge implements EmbeddedRpcBridge {
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
  Future<void> close() => _channel.invokeMethod<void>('close', {
    'timeoutMilliseconds': closeTimeout.inMilliseconds,
  });
}
