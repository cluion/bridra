import 'dart:async';
import 'dart:convert';

import 'package:crypto/crypto.dart';

class RpcFileReference {
  RpcFileReference._({
    required this.id,
    required this.name,
    required this.mediaType,
    required this.size,
    required this.sha256,
    required this.expiresAt,
    required this.localPath,
  });

  factory RpcFileReference.fromJson(Map<String, dynamic> json) {
    final id = json['id'];
    final name = json['name'];
    final mediaType = json['mediaType'];
    final size = json['size'];
    final checksum = json['sha256'];
    final expiresAt = json['expiresAt'];
    final localPath = json['localPath'];
    if (id is! String ||
        !RegExp(r'^[0-9a-f]{64}$').hasMatch(id) ||
        name is! String ||
        name.isEmpty ||
        name == '.' ||
        name == '..' ||
        RegExp(r'[/\\\x00-\x1f\x7f]').hasMatch(name) ||
        mediaType is! String ||
        mediaType.isEmpty ||
        mediaType.contains('\r') ||
        mediaType.contains('\n') ||
        size is! int ||
        size < 0 ||
        checksum is! String ||
        !RegExp(r'^[0-9a-f]{64}$').hasMatch(checksum) ||
        expiresAt is! String ||
        (localPath != null && (localPath is! String || localPath.isEmpty))) {
      throw const BackendProtocolException('The file reference is invalid.');
    }
    DateTime expiry;
    try {
      expiry = DateTime.parse(expiresAt).toUtc();
    } on FormatException catch (error) {
      throw BackendProtocolException(
        'The file reference expiry is invalid.',
        cause: error,
      );
    }
    return RpcFileReference._(
      id: id,
      name: name,
      mediaType: mediaType,
      size: size,
      sha256: checksum,
      expiresAt: expiry,
      localPath: localPath as String?,
    );
  }

  final String id;
  final String name;
  final String mediaType;
  final int size;
  final String sha256;
  final DateTime expiresAt;
  final String? localPath;

  bool get isExpired => !DateTime.now().toUtc().isBefore(expiresAt);

  Map<String, Object?> toJson() => {
    'id': id,
    'name': name,
    'mediaType': mediaType,
    'size': size,
    'sha256': sha256,
    'expiresAt': expiresAt.toUtc().toIso8601String(),
  };
}

typedef RpcFileRangeReader = Stream<List<int>> Function(int offset);

class RpcFileUpload {
  factory RpcFileUpload({
    required String name,
    required String mediaType,
    required int size,
    required String sha256,
    required RpcFileRangeReader openRead,
  }) {
    if (name.isEmpty ||
        name == '.' ||
        name == '..' ||
        RegExp(r'[/\\\x00-\x1f\x7f]').hasMatch(name)) {
      throw ArgumentError.value(name, 'name', 'Use a safe base file name.');
    }
    if (mediaType.isEmpty ||
        mediaType.contains('\r') ||
        mediaType.contains('\n')) {
      throw ArgumentError.value(
        mediaType,
        'mediaType',
        'Use a valid media type.',
      );
    }
    if (size < 0) {
      throw ArgumentError.value(size, 'size', 'The size cannot be negative.');
    }
    if (!RegExp(r'^[0-9a-f]{64}$').hasMatch(sha256)) {
      throw ArgumentError.value(
        sha256,
        'sha256',
        'Use a lowercase SHA-256 digest.',
      );
    }
    return RpcFileUpload._(
      name: name,
      mediaType: mediaType,
      size: size,
      sha256: sha256,
      openRead: openRead,
    );
  }

  const RpcFileUpload._({
    required this.name,
    required this.mediaType,
    required this.size,
    required this.sha256,
    required this.openRead,
  });

  final String name;
  final String mediaType;
  final int size;
  final String sha256;
  final RpcFileRangeReader openRead;
}

class RpcReply {
  const RpcReply({required this.result, required this.meta});

  final Object? result;
  final Map<String, dynamic> meta;
}

class RpcProgress {
  const RpcProgress({
    required this.completed,
    required this.total,
    this.message,
    this.unit,
  });

  final int completed;
  final int total;
  final String? message;
  final String? unit;

  double get fraction => completed / total;
}

sealed class RpcStreamEvent<T> {
  const RpcStreamEvent({required this.sequence, required this.meta});

  final int sequence;
  final Map<String, dynamic> meta;
}

final class RpcStreamData<T> extends RpcStreamEvent<T> {
  const RpcStreamData({
    required super.sequence,
    required super.meta,
    required this.value,
  });

  final T value;
}

final class RpcStreamProgress<T> extends RpcStreamEvent<T> {
  const RpcStreamProgress({
    required super.sequence,
    required super.meta,
    required this.progress,
  });

  final RpcProgress progress;
}

class RpcStreamFrame {
  const RpcStreamFrame._({
    required this.sequence,
    required this.done,
    this.event,
  });

  final int sequence;
  final bool done;
  final RpcStreamEvent<RpcReply>? event;
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

class RpcRateLimitedException extends BackendConnectionException {
  const RpcRateLimitedException({this.retryAfter})
    : super('The backend rate limit was exceeded.');

  final Duration? retryAfter;
}

class BackendProtocolException extends BackendConnectionException {
  const BackendProtocolException(super.message, {this.cause});

  final Object? cause;
}

class RpcFileExpiredException extends BackendConnectionException {
  const RpcFileExpiredException() : super('The file transfer has expired.');
}

class RpcFileUnavailableException extends BackendConnectionException {
  const RpcFileUnavailableException()
    : super('The file transfer is unavailable.');
}

abstract interface class RpcClient {
  Future<RpcReply> call(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(seconds: 5),
    RpcCancellationToken? cancellationToken,
  });

  Stream<RpcStreamEvent<RpcReply>> stream(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(minutes: 5),
    RpcCancellationToken? cancellationToken,
  });

  Stream<List<int>> download(
    RpcFileReference file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
    int maxAttempts = 3,
  });

  Future<RpcFileReference> upload(
    RpcFileUpload file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
    int maxAttempts = 3,
  });

  Future<void> close();
}

Stream<List<int>> verifyRpcFileDownload(
  Stream<List<int>> source,
  RpcFileReference file,
) async* {
  final digest = _DigestSink();
  final hashInput = sha256.startChunkedConversion(digest);
  var received = 0;
  try {
    await for (final chunk in source) {
      received += chunk.length;
      if (received > file.size) {
        throw BackendProtocolException(
          'File ${file.name} exceeds its declared size.',
        );
      }
      hashInput.add(chunk);
      yield chunk;
    }
  } finally {
    hashInput.close();
  }
  if (received != file.size) {
    throw BackendProtocolException(
      'File ${file.name} has $received bytes; expected ${file.size}.',
    );
  }
  if (digest.value?.toString() != file.sha256) {
    throw BackendProtocolException(
      'File ${file.name} failed SHA-256 verification.',
    );
  }
}

class _DigestSink implements Sink<Digest> {
  Digest? value;

  @override
  void add(Digest data) {
    if (value != null) {
      throw StateError('The SHA-256 digest was emitted more than once.');
    }
    value = data;
  }

  @override
  void close() {}
}

String encodeRpcRequest({
  required String id,
  required String method,
  required Map<String, Object?> params,
  required String token,
  bool stream = false,
  int? streamWindow,
}) {
  final meta = <String, String>{'token': token};
  if (stream) {
    meta['stream'] = '1';
  }
  if (streamWindow != null) {
    meta['stream_window'] = '$streamWindow';
  }
  return jsonEncode({
    'id': id,
    'method': method,
    'params': params,
    'meta': meta,
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

String encodeRpcStreamAcknowledgement({
  required String id,
  required int sequence,
  required String token,
}) {
  return encodeRpcRequest(
    id: id,
    method: 'rpc.stream_ack',
    params: {'sequence': sequence},
    token: token,
  );
}

RpcReply decodeRpcReply(Object? decoded, {required String expectedID}) {
  final message = _decodeEnvelope(decoded, expectedID: expectedID);
  _throwRpcError(message);

  if (!message.containsKey('result')) {
    throw const FormatException('Response is missing a result.');
  }
  return RpcReply(result: message['result'], meta: _decodeMeta(message));
}

RpcStreamFrame decodeRpcStreamFrame(
  Object? decoded, {
  required String expectedID,
}) {
  final message = _decodeEnvelope(decoded, expectedID: expectedID);
  final stream = message['stream'];
  if (stream is! Map) {
    throw const FormatException(
      'Streaming response is missing stream metadata.',
    );
  }
  final metadata = Map<String, dynamic>.from(stream);
  final sequence = metadata['sequence'];
  final kind = metadata['kind'];
  if (sequence is! int || sequence < 1) {
    throw const FormatException(
      'Streaming response sequence must be a positive integer.',
    );
  }
  if (kind is! String) {
    throw const FormatException('Streaming response kind must be a string.');
  }

  if (kind == 'complete') {
    _throwRpcError(message);
    return RpcStreamFrame._(sequence: sequence, done: true);
  }
  _throwRpcError(message);
  final meta = _decodeMeta(message);
  if (kind == 'data') {
    if (!message.containsKey('result')) {
      throw const FormatException('Streaming data is missing a result.');
    }
    return RpcStreamFrame._(
      sequence: sequence,
      done: false,
      event: RpcStreamData<RpcReply>(
        sequence: sequence,
        meta: meta,
        value: RpcReply(result: message['result'], meta: meta),
      ),
    );
  }
  if (kind == 'progress') {
    final progressValue = metadata['progress'];
    if (progressValue is! Map) {
      throw const FormatException(
        'Streaming progress is missing progress metadata.',
      );
    }
    final progress = Map<String, dynamic>.from(progressValue);
    final completed = progress['completed'];
    final total = progress['total'];
    final messageText = progress['message'];
    final unit = progress['unit'];
    if (completed is! int ||
        total is! int ||
        completed < 0 ||
        total <= 0 ||
        completed > total) {
      throw const FormatException(
        'Streaming progress requires 0 <= completed <= total and total > 0.',
      );
    }
    if (messageText != null && messageText is! String) {
      throw const FormatException('Progress message must be a string.');
    }
    if (unit != null && unit is! String) {
      throw const FormatException('Progress unit must be a string.');
    }
    return RpcStreamFrame._(
      sequence: sequence,
      done: false,
      event: RpcStreamProgress<RpcReply>(
        sequence: sequence,
        meta: meta,
        progress: RpcProgress(
          completed: completed,
          total: total,
          message: messageText as String?,
          unit: unit as String?,
        ),
      ),
    );
  }
  throw FormatException('Unknown streaming response kind $kind.');
}

Map<String, dynamic> _decodeEnvelope(
  Object? decoded, {
  required String expectedID,
}) {
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
  return message;
}

Map<String, dynamic> _decodeMeta(Map<String, dynamic> message) {
  final meta = message['meta'];
  if (meta != null && meta is! Map) {
    throw const FormatException('Response meta must be an object.');
  }
  return meta == null ? const {} : Map<String, dynamic>.from(meta);
}

void _throwRpcError(Map<String, dynamic> message) {
  final error = message['error'];
  if (error == null) return;
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
