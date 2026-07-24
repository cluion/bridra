import 'dart:async';
import 'dart:convert';

class RpcReply {
  const RpcReply({required this.result, required this.meta});

  final Object? result;
  final Map<String, dynamic> meta;
}

class RpcException implements Exception {
  const RpcException(this.code, this.message, {this.data = const {}});

  final String code;
  final String message;
  final Map<String, dynamic> data;

  @override
  String toString() => 'RpcException($code): $message';
}

class RpcCancelledException implements Exception {
  const RpcCancelledException(this.method);

  final String method;

  @override
  String toString() => 'RPC method $method was cancelled.';
}

class RpcCancellationToken {
  final _controller = StreamController<void>.broadcast(sync: true);
  var _isCancelled = false;

  bool get isCancelled => _isCancelled;

  Stream<void> get onCancel => _controller.stream;

  void cancel() {
    if (_isCancelled) return;
    _isCancelled = true;
    _controller.add(null);
    unawaited(_controller.close());
  }
}

abstract class BackendConnectionException implements Exception {
  const BackendConnectionException(this.message);

  final String message;

  @override
  String toString() => message;
}

class BackendClosedException extends BackendConnectionException {
  const BackendClosedException() : super('The backend connection is closed.');
}

class BackendTransportException extends BackendConnectionException {
  const BackendTransportException(super.message, {this.cause});

  final Object? cause;
}

class BackendProtocolException extends BackendConnectionException {
  const BackendProtocolException(super.message, {this.cause});

  final Object? cause;
}

abstract interface class RpcClient {
  Future<RpcReply> call(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(seconds: 5),
    RpcCancellationToken? cancellationToken,
  });

  Future<void> close();
}

String encodeRpcRequest({
  required String id,
  required String method,
  required Map<String, Object?> params,
  required String token,
}) {
  return jsonEncode({
    'id': id,
    'method': method,
    'params': params,
    'meta': {'token': token},
  });
}

String encodeRpcCancellation({required String id, required String token}) {
  return encodeRpcRequest(
    id: id,
    method: 'rpc.cancel',
    params: const {},
    token: token,
  );
}

RpcReply decodeRpcReply(Object? decoded, {required String expectedID}) {
  if (decoded is! Map) {
    throw const FormatException('Response must be a JSON object.');
  }
  final message = Map<String, dynamic>.from(decoded);
  final id = message['id'];
  if (id is! String || id.isEmpty) {
    throw const FormatException('Response id must be a non-empty string.');
  }
  if (id != expectedID) {
    throw FormatException('Expected response $expectedID but received $id.');
  }

  final error = message['error'];
  if (error != null) {
    if (error is! Map) {
      throw const FormatException('Response error must be an object.');
    }
    final errorData = Map<String, dynamic>.from(error);
    final code = errorData['code'];
    final messageText = errorData['message'];
    if (code is! String || messageText is! String) {
      throw const FormatException('Response error is missing code or message.');
    }
    final data = errorData['data'];
    if (data != null && data is! Map) {
      throw const FormatException('Response error data must be an object.');
    }
    throw RpcException(
      code,
      messageText,
      data: data == null ? const {} : Map<String, dynamic>.from(data),
    );
  }

  if (!message.containsKey('result')) {
    throw const FormatException('Response is missing a result.');
  }
  final meta = message['meta'];
  if (meta != null && meta is! Map) {
    throw const FormatException('Response meta must be an object.');
  }
  return RpcReply(
    result: message['result'],
    meta: meta == null ? const {} : Map<String, dynamic>.from(meta),
  );
}
