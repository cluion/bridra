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
  }) async {
    if (_closed) throw const BackendClosedException();

    final id = '${++_nextID}';
    http.Response response;
    try {
      response = await _client
          .post(
            endpoint,
            headers: const {
              'accept': 'application/json',
              'content-type': 'application/json',
            },
            body: encodeRpcRequest(
              id: id,
              method: method,
              params: params,
              token: _token,
            ),
          )
          .timeout(timeout);
    } on TimeoutException {
      rethrow;
    } on Object catch (error) {
      throw BackendTransportException(
        'Could not reach the Go HTTP backend at $endpoint.',
        cause: error,
      );
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
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _client.close();
  }
}
