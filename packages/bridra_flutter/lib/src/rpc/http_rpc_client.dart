import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import 'rpc_client.dart';

class HttpRpcClient implements RpcClient {
  factory HttpRpcClient({
    required Uri endpoint,
    required String token,
    http.Client? client,
  }) {
    if (!endpoint.hasScheme ||
        !endpoint.hasAuthority ||
        (endpoint.scheme != 'http' && endpoint.scheme != 'https')) {
      throw ArgumentError.value(endpoint, 'endpoint', 'Use an HTTP(S) URL.');
    }
    if (token.isEmpty) {
      throw ArgumentError.value(token, 'token', 'The token cannot be empty.');
    }
    return HttpRpcClient._(endpoint, token, client ?? http.Client());
  }

  HttpRpcClient._(this.endpoint, this._token, this._client);

  final Uri endpoint;
  final String _token;
  final http.Client _client;
  var _nextID = 0;
  var _closed = false;

  @override
  Future<RpcReply> call(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(seconds: 5),
    RpcCancellationToken? cancellationToken,
  }) async {
    if (_closed) throw const BackendClosedException();
    if (cancellationToken?.isCancelled ?? false) {
      throw RpcCancelledException(method);
    }

    final id = '${++_nextID}';
    final abort = Completer<void>();
    Object? abortError;
    void requestAbort(Object error) {
      if (abort.isCompleted) return;
      abortError = error;
      abort.complete();
    }

    final timeoutTimer = Timer(
      timeout,
      () => requestAbort(
        TimeoutException('HTTP method $method timed out.', timeout),
      ),
    );
    final cancellationSubscription = cancellationToken?.onCancel.listen(
      (_) => requestAbort(RpcCancelledException(method)),
    );
    final request =
        http.AbortableRequest('POST', endpoint, abortTrigger: abort.future)
          ..headers.addAll(const {
            'accept': 'application/json',
            'content-type': 'application/json',
          })
          ..body = encodeRpcRequest(
            id: id,
            method: method,
            params: params,
            token: _token,
          );

    http.Response response;
    try {
      final responseFuture = _client
          .send(request)
          .then(http.Response.fromStream);
      final abortFuture = abort.future.then<http.Response>(
        (_) => throw abortError!,
      );
      response = await Future.any([responseFuture, abortFuture]);
    } on TimeoutException {
      rethrow;
    } on RpcCancelledException {
      rethrow;
    } on http.RequestAbortedException {
      final error = abortError;
      if (error != null) throw error;
      throw RpcCancelledException(method);
    } on Object catch (error) {
      throw BackendTransportException(
        'Could not reach the Go HTTP backend at $endpoint.',
        cause: error,
      );
    } finally {
      timeoutTimer.cancel();
      if (cancellationSubscription != null) {
        unawaited(cancellationSubscription.cancel());
      }
    }

    if (response.statusCode != 200) {
      throw BackendTransportException(
        'The Go HTTP backend returned status ${response.statusCode}.',
      );
    }

    try {
      return decodeRpcReply(jsonDecode(response.body), expectedID: id);
    } on RpcException {
      rethrow;
    } on Object catch (error) {
      throw BackendProtocolException(
        'The Go HTTP backend returned an invalid response.',
        cause: error,
      );
    }
  }

  @override
  Stream<RpcStreamEvent<RpcReply>> stream(
    String method, {
    Map<String, Object?> params = const {},
    Duration timeout = const Duration(minutes: 5),
    RpcCancellationToken? cancellationToken,
  }) async* {
    if (_closed) throw const BackendClosedException();
    if (cancellationToken?.isCancelled ?? false) {
      throw RpcCancelledException(method);
    }

    final id = '${++_nextID}';
    final abort = Completer<void>();
    Object? abortError;
    void requestAbort(Object error) {
      if (abort.isCompleted) return;
      abortError = error;
      abort.complete();
    }

    final timeoutTimer = Timer(
      timeout,
      () => requestAbort(
        TimeoutException('HTTP stream $method timed out.', timeout),
      ),
    );
    final cancellationSubscription = cancellationToken?.onCancel.listen(
      (_) => requestAbort(RpcCancelledException(method)),
    );
    final request =
        http.AbortableRequest('POST', endpoint, abortTrigger: abort.future)
          ..headers.addAll(const {
            'accept': 'application/x-ndjson',
            'content-type': 'application/json',
          })
          ..body = encodeRpcRequest(
            id: id,
            method: method,
            params: params,
            token: _token,
            stream: true,
          );

    try {
      final responseFuture = _client.send(request);
      final abortFuture = abort.future.then<http.StreamedResponse>(
        (_) => throw abortError!,
      );
      final response = await Future.any([responseFuture, abortFuture]);
      if (response.statusCode != 200) {
        throw BackendTransportException(
          'The Go HTTP backend returned status ${response.statusCode}.',
        );
      }

      var expectedSequence = 1;
      var completed = false;
      await for (final line
          in response.stream
              .transform(utf8.decoder)
              .transform(const LineSplitter())) {
        if (line.isEmpty) continue;
        final frame = decodeRpcStreamFrame(jsonDecode(line), expectedID: id);
        if (frame.sequence != expectedSequence) {
          throw FormatException(
            'Expected stream sequence $expectedSequence but received '
            '${frame.sequence}.',
          );
        }
        expectedSequence++;
        if (frame.done) {
          completed = true;
          break;
        }
        yield frame.event!;
      }
      if (!completed) {
        final error = abortError;
        if (error != null) throw error;
        throw const FormatException(
          'The HTTP stream ended without a completion frame.',
        );
      }
    } on TimeoutException {
      rethrow;
    } on RpcCancelledException {
      rethrow;
    } on RpcException {
      rethrow;
    } on BackendConnectionException {
      rethrow;
    } on http.RequestAbortedException {
      final error = abortError;
      if (error != null) throw error;
      throw RpcCancelledException(method);
    } on FormatException catch (error) {
      throw BackendProtocolException(
        'The Go HTTP backend returned an invalid stream.',
        cause: error,
      );
    } on Object catch (error) {
      final requestedError = abortError;
      if (requestedError != null) throw requestedError;
      throw BackendTransportException(
        'Could not stream from the Go HTTP backend at $endpoint.',
        cause: error,
      );
    } finally {
      timeoutTimer.cancel();
      if (cancellationSubscription != null) {
        unawaited(cancellationSubscription.cancel());
      }
      if (!abort.isCompleted) {
        abort.complete();
      }
    }
  }

  @override
  Stream<List<int>> download(
    RpcFileReference file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
    int maxAttempts = 3,
  }) async* {
    if (maxAttempts < 1) {
      throw ArgumentError.value(
        maxAttempts,
        'maxAttempts',
        'Use at least one attempt.',
      );
    }
    await for (final chunk in verifyRpcFileDownload(
      _downloadWithRetries(
        file,
        timeout: timeout,
        cancellationToken: cancellationToken,
        maxAttempts: maxAttempts,
      ),
      file,
    )) {
      yield chunk;
    }
  }

  Stream<List<int>> _downloadWithRetries(
    RpcFileReference file, {
    required Duration timeout,
    required RpcCancellationToken? cancellationToken,
    required int maxAttempts,
  }) async* {
    if (_closed) throw const BackendClosedException();
    if (cancellationToken?.isCancelled ?? false) {
      throw const RpcCancelledException('file.download');
    }
    if (file.isExpired) {
      throw const RpcFileExpiredException();
    }
    final abort = Completer<void>();
    Object? abortError;
    void requestAbort(Object error) {
      if (abort.isCompleted) return;
      abortError = error;
      abort.complete();
    }

    final timeoutTimer = Timer(
      timeout,
      () => requestAbort(
        TimeoutException('HTTP file download timed out.', timeout),
      ),
    );
    final cancellationSubscription = cancellationToken?.onCancel.listen(
      (_) => requestAbort(const RpcCancelledException('file.download')),
    );

    var attempt = 0;
    var received = 0;
    var firstRequest = true;
    try {
      while (firstRequest || received < file.size) {
        firstRequest = false;
        attempt++;
        final request = http.AbortableRequest(
          'GET',
          _fileDownloadEndpoint(file),
          abortTrigger: abort.future,
        )..headers['accept'] = file.mediaType;
        if (received > 0) {
          request.headers['range'] = 'bytes=$received-';
        }
        try {
          final responseFuture = _client.send(request);
          final abortFuture = abort.future.then<http.StreamedResponse>(
            (_) => throw abortError!,
          );
          final response = await Future.any([responseFuture, abortFuture]);
          if (response.statusCode == 404 || response.statusCode == 410) {
            throw const RpcFileUnavailableException();
          }
          if (response.statusCode == 409 && attempt < maxAttempts) {
            await response.stream.drain<void>();
            await Future<void>.delayed(const Duration(milliseconds: 25));
            continue;
          }
          final expectedStatus = received == 0 ? 200 : 206;
          if (response.statusCode != expectedStatus) {
            throw BackendTransportException(
              'The Go HTTP backend returned status ${response.statusCode} '
              'for the file download.',
            );
          }
          if (received > 0) {
            final contentRange = response.headers['content-range'];
            if (contentRange == null ||
                !contentRange.startsWith('bytes $received-') ||
                !contentRange.endsWith('/${file.size}')) {
              throw const BackendProtocolException(
                'The resumed file download returned an invalid range.',
              );
            }
          }
          await for (final chunk in response.stream) {
            received += chunk.length;
            if (received > file.size) {
              throw BackendProtocolException(
                'File ${file.name} exceeds its declared size.',
              );
            }
            yield chunk;
          }
          if (received < file.size && attempt >= maxAttempts) {
            throw BackendTransportException(
              'The file download ended before all bytes were received.',
            );
          }
        } on TimeoutException {
          rethrow;
        } on RpcCancelledException {
          rethrow;
        } on RpcFileUnavailableException {
          rethrow;
        } on BackendProtocolException {
          rethrow;
        } on http.RequestAbortedException {
          final error = abortError;
          if (error != null) throw error;
          if (attempt >= maxAttempts) {
            throw BackendTransportException(
              'Could not resume ${file.name} from the Go HTTP backend.',
            );
          }
        } on Object catch (error) {
          final requestedError = abortError;
          if (requestedError != null) throw requestedError;
          if (error is BackendConnectionException) rethrow;
          if (attempt >= maxAttempts) {
            throw BackendTransportException(
              'Could not download ${file.name} from the Go HTTP backend.',
              cause: error,
            );
          }
        }
        if (received < file.size && file.isExpired) {
          throw const RpcFileExpiredException();
        }
      }
    } finally {
      timeoutTimer.cancel();
      if (cancellationSubscription != null) {
        unawaited(cancellationSubscription.cancel());
      }
      if (!abort.isCompleted) {
        abort.complete();
      }
    }
  }

  @override
  Future<RpcFileReference> upload(
    RpcFileUpload file, {
    Duration timeout = const Duration(minutes: 15),
    RpcCancellationToken? cancellationToken,
    int maxAttempts = 3,
  }) async {
    if (_closed) throw const BackendClosedException();
    if (maxAttempts < 1) {
      throw ArgumentError.value(
        maxAttempts,
        'maxAttempts',
        'Use at least one attempt.',
      );
    }
    if (cancellationToken?.isCancelled ?? false) {
      throw const RpcCancelledException('file.upload');
    }

    final abort = Completer<void>();
    Object? abortError;
    void requestAbort(Object error) {
      if (abort.isCompleted) return;
      abortError = error;
      abort.complete();
    }

    final timeoutTimer = Timer(
      timeout,
      () => requestAbort(
        TimeoutException('HTTP file upload timed out.', timeout),
      ),
    );
    final cancellationSubscription = cancellationToken?.onCancel.listen(
      (_) => requestAbort(const RpcCancelledException('file.upload')),
    );
    try {
      final createRequest =
          http.AbortableRequest(
              'POST',
              _fileUploadEndpoint(),
              abortTrigger: abort.future,
            )
            ..headers.addAll({
              'accept': 'application/json',
              'content-type': 'application/json',
              'x-bridra-token': _token,
            })
            ..body = jsonEncode({
              'name': file.name,
              'mediaType': file.mediaType,
              'size': file.size,
              'sha256': file.sha256,
            });
      final created = await _sendFileRequest(createRequest, abort, () {
        return abortError;
      });
      if (created.statusCode != 201) {
        throw BackendTransportException(
          'The Go HTTP backend returned status ${created.statusCode} '
          'while creating the file upload.',
        );
      }
      var state = await _decodeUploadState(created);
      _validateUploadReference(state.file, file);
      var attempt = 0;
      while (!state.complete) {
        attempt++;
        final startOffset = state.offset;
        try {
          final request =
              _AbortableFileRequest(
                  'PATCH',
                  _fileDownloadEndpoint(state.file),
                  file.openRead(startOffset),
                  abortTrigger: abort.future,
                )
                ..contentLength = file.size - startOffset
                ..headers.addAll({
                  'accept': 'application/json',
                  'content-type': 'application/offset+octet-stream',
                  'upload-offset': '$startOffset',
                });
          final response = await _sendFileRequest(request, abort, () {
            return abortError;
          });
          if (response.statusCode == 404 || response.statusCode == 410) {
            throw const RpcFileUnavailableException();
          }
          if (response.statusCode == 413 || response.statusCode == 422) {
            throw const BackendProtocolException(
              'The Go HTTP backend rejected the file size or SHA-256 digest.',
            );
          }
          if (response.statusCode != 200) {
            throw BackendTransportException(
              'The Go HTTP backend returned status ${response.statusCode} '
              'for the file upload.',
            );
          }
          final next = await _decodeUploadState(response);
          _validateUploadReference(next.file, file);
          if (!next.complete && next.offset <= startOffset) {
            throw const BackendProtocolException(
              'The file upload did not advance its offset.',
            );
          }
          state = next;
        } on TimeoutException {
          rethrow;
        } on RpcCancelledException {
          rethrow;
        } on RpcFileUnavailableException {
          rethrow;
        } on BackendProtocolException {
          rethrow;
        } on Object catch (error) {
          final requestedError = abortError;
          if (requestedError != null) throw requestedError;
          if (attempt >= maxAttempts) {
            if (error is BackendConnectionException) rethrow;
            throw BackendTransportException(
              'Could not upload ${file.name} to the Go HTTP backend.',
              cause: error,
            );
          }
          state = await _readUploadState(state.file, abort, () => abortError);
          _validateUploadReference(state.file, file);
        }
      }
      return state.file;
    } finally {
      timeoutTimer.cancel();
      if (cancellationSubscription != null) {
        unawaited(cancellationSubscription.cancel());
      }
      if (!abort.isCompleted) {
        abort.complete();
      }
    }
  }

  Future<http.StreamedResponse> _sendFileRequest(
    http.BaseRequest request,
    Completer<void> abort,
    Object? Function() abortError,
  ) async {
    try {
      final responseFuture = _client.send(request);
      final abortFuture = abort.future.then<http.StreamedResponse>(
        (_) => throw abortError()!,
      );
      return await Future.any([responseFuture, abortFuture]);
    } on http.RequestAbortedException {
      final error = abortError();
      if (error != null) throw error;
      rethrow;
    }
  }

  Future<_HttpUploadState> _decodeUploadState(
    http.StreamedResponse response,
  ) async {
    try {
      final decoded = jsonDecode(await response.stream.bytesToString());
      if (decoded is! Map) {
        throw const FormatException('Upload status must be an object.');
      }
      final data = Map<String, dynamic>.from(decoded);
      final file = data['file'];
      final offset = data['offset'];
      final complete = data['complete'];
      if (file is! Map || offset is! int || offset < 0 || complete is! bool) {
        throw const FormatException('Upload status fields are invalid.');
      }
      return _HttpUploadState(
        file: RpcFileReference.fromJson(Map<String, dynamic>.from(file)),
        offset: offset,
        complete: complete,
      );
    } on BackendConnectionException {
      rethrow;
    } on Object catch (error) {
      throw BackendProtocolException(
        'The Go HTTP backend returned an invalid upload status.',
        cause: error,
      );
    }
  }

  Future<_HttpUploadState> _readUploadState(
    RpcFileReference file,
    Completer<void> abort,
    Object? Function() abortError,
  ) async {
    final request = http.AbortableRequest(
      'HEAD',
      _fileDownloadEndpoint(file),
      abortTrigger: abort.future,
    );
    final response = await _sendFileRequest(request, abort, abortError);
    if (response.statusCode == 404 || response.statusCode == 410) {
      throw const RpcFileUnavailableException();
    }
    if (response.statusCode != 204) {
      throw BackendTransportException(
        'The Go HTTP backend returned status ${response.statusCode} '
        'while resuming the file upload.',
      );
    }
    final offset = int.tryParse(response.headers['upload-offset'] ?? '');
    final length = int.tryParse(response.headers['upload-length'] ?? '');
    final complete = response.headers['upload-complete'];
    final expiresAt = response.headers['upload-expires-at'];
    if (offset == null ||
        offset < 0 ||
        length != file.size ||
        (complete != 'true' && complete != 'false') ||
        expiresAt == null) {
      throw const BackendProtocolException(
        'The resumed file upload returned invalid state.',
      );
    }
    final refreshed = RpcFileReference.fromJson({
      ...file.toJson(),
      'expiresAt': expiresAt,
    });
    return _HttpUploadState(
      file: refreshed,
      offset: offset,
      complete: complete == 'true',
    );
  }

  void _validateUploadReference(
    RpcFileReference reference,
    RpcFileUpload upload,
  ) {
    if (reference.name != upload.name ||
        reference.mediaType != upload.mediaType ||
        reference.size != upload.size ||
        reference.sha256 != upload.sha256 ||
        reference.localPath != null) {
      throw const BackendProtocolException(
        'The file upload reference does not match its source.',
      );
    }
  }

  Uri _fileUploadEndpoint() {
    final rpcPath = endpoint.path.endsWith('/')
        ? endpoint.path.substring(0, endpoint.path.length - 1)
        : endpoint.path;
    return Uri(
      scheme: endpoint.scheme,
      userInfo: endpoint.userInfo,
      host: endpoint.host,
      port: endpoint.hasPort ? endpoint.port : null,
      path: '$rpcPath/files/',
    );
  }

  Uri _fileDownloadEndpoint(RpcFileReference file) {
    final rpcPath = endpoint.path.endsWith('/')
        ? endpoint.path.substring(0, endpoint.path.length - 1)
        : endpoint.path;
    return Uri(
      scheme: endpoint.scheme,
      userInfo: endpoint.userInfo,
      host: endpoint.host,
      port: endpoint.hasPort ? endpoint.port : null,
      path: '$rpcPath/files/${file.id}',
    );
  }

  @override
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _client.close();
  }
}

class _HttpUploadState {
  const _HttpUploadState({
    required this.file,
    required this.offset,
    required this.complete,
  });

  final RpcFileReference file;
  final int offset;
  final bool complete;
}

final class _AbortableFileRequest extends http.BaseRequest with http.Abortable {
  _AbortableFileRequest(
    super.method,
    super.url,
    this.source, {
    this.abortTrigger,
  });

  final Stream<List<int>> source;

  @override
  final Future<void>? abortTrigger;

  @override
  http.ByteStream finalize() {
    super.finalize();
    return http.ByteStream(source);
  }
}
