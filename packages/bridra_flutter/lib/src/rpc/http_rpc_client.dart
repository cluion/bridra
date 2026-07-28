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
  }) async* {
    if (_closed) throw const BackendClosedException();
    if (cancellationToken?.isCancelled ?? false) {
      throw const RpcCancelledException('file.download');
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
    final request = http.AbortableRequest(
      'GET',
      _fileDownloadEndpoint(file),
      abortTrigger: abort.future,
    )..headers['accept'] = file.mediaType;

    try {
      final responseFuture = _client.send(request);
      final abortFuture = abort.future.then<http.StreamedResponse>(
        (_) => throw abortError!,
      );
      final response = await Future.any([responseFuture, abortFuture]);
      if (response.statusCode == 404 || response.statusCode == 410) {
        throw const RpcFileUnavailableException();
      }
      if (response.statusCode != 200) {
        throw BackendTransportException(
          'The Go HTTP backend returned status ${response.statusCode} '
          'for the file download.',
        );
      }
      await for (final chunk in verifyRpcFileDownload(response.stream, file)) {
        yield chunk;
      }
    } on TimeoutException {
      rethrow;
    } on RpcCancelledException {
      rethrow;
    } on BackendConnectionException {
      rethrow;
    } on http.RequestAbortedException {
      final error = abortError;
      if (error != null) throw error;
      throw const RpcCancelledException('file.download');
    } on Object catch (error) {
      final requestedError = abortError;
      if (requestedError != null) throw requestedError;
      throw BackendTransportException(
        'Could not download ${file.name} from the Go HTTP backend.',
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
