import 'dart:async';

import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:flutter/foundation.dart';

import '../../api/backend_gateway.dart';

typedef BackendConnector = Future<BackendGateway> Function();

enum BackendStatus { connecting, online, offline }

class WorkspaceViewModel extends ChangeNotifier {
  WorkspaceViewModel({required this.connector});

  final BackendConnector connector;
  BackendGateway? _backend;
  RpcCancellationToken? _requestCancellation;
  var _disposed = false;
  var _connecting = false;

  BackendStatus _connection = BackendStatus.connecting;
  HealthInfo? _health;
  GreetingResult? _greeting;
  Object? _error;
  var _requestRunning = false;

  BackendStatus get connection => _connection;
  HealthInfo? get health => _health;
  GreetingResult? get greeting => _greeting;
  Object? get error => _error;
  bool get requestRunning => _requestRunning;
  bool get canRequest => _backend != null && !_requestRunning;

  Future<void> connect() async {
    if (_disposed || _connecting || _backend != null) return;
    _connecting = true;
    _connection = BackendStatus.connecting;
    _health = null;
    _greeting = null;
    _error = null;
    notifyListeners();
    BackendGateway? backend;
    try {
      backend = await connector();
      if (_disposed) {
        await _close(backend);
        return;
      }
      final health = await backend.health();
      if (_disposed) {
        await _close(backend);
        return;
      }
      if (health.status != 'ok') {
        throw StateError('Go core is not ready: ${health.status}');
      }
      _backend = backend;
      _health = health;
      _connection = BackendStatus.online;
    } on Object catch (error) {
      await _close(backend);
      if (!_disposed) {
        _error = error;
        _connection = BackendStatus.offline;
      }
    } finally {
      _connecting = false;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> greet(String name) async {
    final backend = _backend;
    if (_disposed || backend == null || _requestRunning) return;
    final cancellation = RpcCancellationToken();
    _requestCancellation = cancellation;
    _requestRunning = true;
    _greeting = null;
    _error = null;
    notifyListeners();
    try {
      final result = await backend.greet(
        GreetingRequest(name: name),
        cancellationToken: cancellation,
      );
      if (!_disposed) _greeting = result;
    } on BackendConnectionException catch (error) {
      if (!_disposed) {
        _backend = null;
        _health = null;
        _connection = BackendStatus.offline;
        _error = error;
      }
      // Disposal already owns cleanup if it happened during this request.
      if (!_disposed) await _close(backend);
    } on Object catch (error) {
      if (!_disposed) _error = error;
    } finally {
      _requestRunning = false;
      _requestCancellation = null;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> _close(BackendGateway? backend) async {
    if (backend == null) return;
    try {
      await backend.close();
    } on Object catch (error) {
      debugPrint('Backend cleanup failed: $error');
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _requestCancellation?.cancel();
    final backend = _backend;
    _backend = null;
    unawaited(_close(backend));
    super.dispose();
  }
}
