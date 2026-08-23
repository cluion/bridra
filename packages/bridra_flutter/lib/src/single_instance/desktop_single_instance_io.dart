import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'desktop_application_lifecycle.dart' as application_lifecycle;
import 'desktop_single_instance_types.dart';

const _protocolVersion = 1;
const _maximumFrameBytes = 1024 * 1024;
const _maximumMetadataBytes = 16 * 1024;
const _requestTimeout = Duration(seconds: 2);
const _retryDelay = Duration(milliseconds: 25);

final _applicationIdPattern = RegExp(
  r'^[a-z][a-z0-9_-]*(?:\.[a-z][a-z0-9_-]*)+$',
);
final _activeApplicationIds = <String>{};

bool get isSupported =>
    Platform.isWindows || Platform.isMacOS || Platform.isLinux;

String defaultDesktopRuntimeDirectoryForTesting() {
  return _defaultRuntimeDirectory();
}

Future<bool> forwardDesktopActivationForTesting({
  required String runtimeDirectory,
  required String applicationId,
  required Iterable<String> arguments,
  required String workingDirectory,
}) {
  final normalizedId = applicationId.trim().toLowerCase();
  return _tryForwardActivation(
    metadataFile: File(
      _joinPath(
        Directory(runtimeDirectory).absolute.path,
        '$normalizedId.json',
      ),
    ),
    applicationId: normalizedId,
    activation: DesktopActivation(
      arguments: arguments,
      workingDirectory: workingDirectory,
    ),
    deadline: DateTime.now().add(const Duration(seconds: 5)),
  );
}

Future<void> terminateSecondary(
  DesktopSingleInstanceSession session, {
  bool? requiresNativeTermination,
}) async {
  if (session.isPrimary) {
    throw StateError('The primary desktop instance must remain running.');
  }

  await session.close();
  if (!(requiresNativeTermination ?? Platform.isMacOS)) return;

  try {
    await application_lifecycle.terminateSecondary();
  } on Object catch (error) {
    throw DesktopSingleInstanceException(
      'Could not terminate the secondary macOS application instance.',
      error,
    );
  }
}

Future<DesktopSingleInstanceSession> acquire({
  required String applicationId,
  Iterable<String> arguments = const [],
  String? workingDirectory,
  String? runtimeDirectory,
  Duration startupTimeout = const Duration(seconds: 5),
}) async {
  if (!isSupported) {
    throw UnsupportedError(
      'Desktop single-instance coordination is only available on '
      'Windows, macOS, and Linux.',
    );
  }
  if (startupTimeout <= Duration.zero) {
    throw ArgumentError.value(
      startupTimeout,
      'startupTimeout',
      'must be positive',
    );
  }

  final normalizedId = applicationId.trim().toLowerCase();
  if (normalizedId.length > 200 ||
      !_applicationIdPattern.hasMatch(normalizedId)) {
    throw ArgumentError.value(
      applicationId,
      'applicationId',
      'must be a safe reverse-domain identifier',
    );
  }
  if (!_activeApplicationIds.add(normalizedId)) {
    throw StateError(
      'Desktop single-instance coordination is already active for '
      '$normalizedId in this isolate.',
    );
  }

  var keepReservation = false;
  try {
    final activation = DesktopActivation(
      arguments: arguments,
      workingDirectory: workingDirectory ?? Directory.current.path,
    );
    if (_encodeActivationRequest(
          applicationId: normalizedId,
          token: '0'.padRight(64, '0'),
          activation: activation,
        ).length >
        _maximumFrameBytes) {
      throw ArgumentError.value(
        activation.arguments,
        'arguments',
        'desktop activation exceeds the 1 MiB frame limit',
      );
    }
    final directory = Directory(
      runtimeDirectory == null
          ? _defaultRuntimeDirectory()
          : Directory(runtimeDirectory).absolute.path,
    );
    await directory.create(recursive: true);
    final lockFile = File(_joinPath(directory.path, '$normalizedId.lock'));
    final metadataFile = File(_joinPath(directory.path, '$normalizedId.json'));
    final deadline = DateTime.now().add(startupTimeout);
    Object? lastError;

    while (true) {
      RandomAccessFile? owner;
      try {
        owner = await lockFile.open(mode: FileMode.append);
        await owner.lock(FileLock.exclusive);
      } on FileSystemException catch (error) {
        lastError = error;
        await owner?.close();
        if (await _tryForwardActivation(
          metadataFile: metadataFile,
          applicationId: normalizedId,
          activation: activation,
          deadline: deadline,
        )) {
          return _SecondarySession(activation);
        }
        if (!DateTime.now().isBefore(deadline)) {
          throw DesktopSingleInstanceException(
            'The primary instance did not accept activation before the '
            'startup timeout.',
            lastError,
          );
        }
        await Future<void>.delayed(_remainingDelay(deadline));
        continue;
      }

      try {
        final session = await _startPrimary(
          applicationId: normalizedId,
          activation: activation,
          owner: owner,
          metadataFile: metadataFile,
          onClose: () => _activeApplicationIds.remove(normalizedId),
        );
        keepReservation = true;
        return session;
      } on Object catch (error) {
        await _releaseOwner(owner);
        throw DesktopSingleInstanceException(
          'Could not start the primary desktop instance.',
          error,
        );
      }
    }
  } finally {
    if (!keepReservation) {
      _activeApplicationIds.remove(normalizedId);
    }
  }
}

Future<_PrimarySession> _startPrimary({
  required String applicationId,
  required DesktopActivation activation,
  required RandomAccessFile owner,
  required File metadataFile,
  required void Function() onClose,
}) async {
  ServerSocket? server;
  try {
    server = await ServerSocket.bind(
      InternetAddress.loopbackIPv4,
      0,
      shared: false,
    );
    final token = _createToken();
    final metadata = utf8.encode(
      '${jsonEncode({'protocolVersion': _protocolVersion, 'applicationId': applicationId, 'port': server.port, 'token': token, 'pid': pid})}\n',
    );
    await metadataFile.writeAsBytes(metadata, flush: true);
    return _PrimarySession(
      applicationId: applicationId,
      initialActivation: activation,
      token: token,
      owner: owner,
      server: server,
      onClose: onClose,
    );
  } on Object {
    await server?.close();
    rethrow;
  }
}

Future<bool> _tryForwardActivation({
  required File metadataFile,
  required String applicationId,
  required DesktopActivation activation,
  required DateTime deadline,
}) async {
  final remaining = deadline.difference(DateTime.now());
  if (remaining <= Duration.zero) return false;

  _InstanceMetadata? metadata;
  try {
    metadata = await _readMetadata(metadataFile);
  } on Object {
    return false;
  }
  if (metadata == null || metadata.applicationId != applicationId) {
    return false;
  }

  Socket? socket;
  try {
    final timeout = _boundedAttemptTimeout(deadline);
    socket = await Socket.connect(
      InternetAddress.loopbackIPv4,
      metadata.port,
      timeout: timeout,
    );
    socket.add(
      _encodeActivationRequest(
        applicationId: applicationId,
        token: metadata.token,
        activation: activation,
      ),
    );
    await socket.flush();
    final response = jsonDecode(
      await _readLine(socket, timeout: _boundedAttemptTimeout(deadline)),
    );
    return response is Map<String, dynamic> &&
        response['accepted'] == true &&
        response['applicationId'] == applicationId;
  } on Object {
    return false;
  } finally {
    socket?.destroy();
  }
}

Future<_InstanceMetadata?> _readMetadata(File file) async {
  RandomAccessFile? reader;
  try {
    reader = await file.open();
    final length = await reader.length();
    if (length <= 0 || length > _maximumMetadataBytes) return null;
    await reader.setPosition(0);
    final contents = await reader.read(length);
    final decoded = jsonDecode(utf8.decode(contents).trim());
    if (decoded is! Map<String, dynamic> ||
        decoded['protocolVersion'] != _protocolVersion ||
        decoded['applicationId'] is! String ||
        decoded['port'] is! int ||
        decoded['token'] is! String) {
      return null;
    }
    final port = decoded['port'] as int;
    final token = decoded['token'] as String;
    if (port < 1 || port > 65535 || token.length != 64) return null;
    return _InstanceMetadata(
      applicationId: decoded['applicationId'] as String,
      port: port,
      token: token,
    );
  } finally {
    await reader?.close();
  }
}

final class _InstanceMetadata {
  const _InstanceMetadata({
    required this.applicationId,
    required this.port,
    required this.token,
  });

  final String applicationId;
  final int port;
  final String token;
}

final class _PrimarySession implements DesktopSingleInstanceSession {
  _PrimarySession({
    required this.applicationId,
    required this.initialActivation,
    required this._token,
    required this._owner,
    required this._server,
    required this._onClose,
  }) {
    _serverSubscription = _server.listen(
      _accept,
      onError: _activations.addError,
    );
  }

  final String applicationId;
  final String _token;
  final RandomAccessFile _owner;
  final ServerSocket _server;
  final void Function() _onClose;
  final _activations = StreamController<DesktopActivation>();
  final _sockets = <Socket>{};
  final _clientTasks = <Future<void>>{};
  late final StreamSubscription<Socket> _serverSubscription;
  Future<void>? _closeFuture;
  var _closed = false;

  @override
  bool get isPrimary => true;

  @override
  final DesktopActivation initialActivation;

  @override
  Stream<DesktopActivation> get activations => _activations.stream;

  void _accept(Socket socket) {
    if (_closed) {
      socket.destroy();
      return;
    }
    _sockets.add(socket);
    late final Future<void> task;
    task = _handleSocket(socket).whenComplete(() {
      _sockets.remove(socket);
      _clientTasks.remove(task);
    });
    _clientTasks.add(task);
  }

  Future<void> _handleSocket(Socket socket) async {
    try {
      final request = jsonDecode(
        await _readLine(socket, timeout: _requestTimeout),
      );
      if (request is! Map<String, dynamic> ||
          request['protocolVersion'] != _protocolVersion ||
          request['applicationId'] != applicationId ||
          request['token'] is! String ||
          !_constantTimeEquals(request['token'] as String, _token) ||
          request['activation'] is! Map<String, dynamic> ||
          _closed) {
        await _writeResponse(socket, accepted: false);
        return;
      }
      final activation = _decodeActivation(
        request['activation'] as Map<String, dynamic>,
      );
      _activations.add(activation);
      await _writeResponse(socket, accepted: true);
    } on Object {
      await _writeResponse(socket, accepted: false);
    } finally {
      socket.destroy();
    }
  }

  Future<void> _writeResponse(Socket socket, {required bool accepted}) async {
    try {
      socket.add(
        utf8.encode(
          '${jsonEncode({'accepted': accepted, 'applicationId': applicationId})}\n',
        ),
      );
      await socket.flush();
    } on Object {
      // The caller can retry or become primary after the ownership lock drops.
    }
  }

  @override
  Future<void> close() {
    return _closeFuture ??= _close();
  }

  Future<void> _close() async {
    _closed = true;
    Object? firstError;

    Future<void> capture(Future<void> Function() operation) async {
      try {
        await operation();
      } on Object catch (error) {
        firstError ??= error;
      }
    }

    await capture(() async {
      await _server.close();
    });
    await capture(_serverSubscription.cancel);
    for (final socket in _sockets.toList(growable: false)) {
      socket.destroy();
    }
    await capture(() async {
      await Future.wait(_clientTasks.toList(growable: false));
    });
    unawaited(_activations.close());
    await capture(_owner.unlock);
    await capture(_owner.close);
    _onClose();

    if (firstError case final error?) {
      throw DesktopSingleInstanceException(
        'Could not close desktop single-instance coordination cleanly.',
        error,
      );
    }
  }
}

final class _SecondarySession implements DesktopSingleInstanceSession {
  const _SecondarySession(this.initialActivation);

  @override
  bool get isPrimary => false;

  @override
  final DesktopActivation initialActivation;

  @override
  Stream<DesktopActivation> get activations =>
      const Stream<DesktopActivation>.empty();

  @override
  Future<void> close() async {}
}

DesktopActivation _decodeActivation(Map<String, dynamic> value) {
  final arguments = value['arguments'];
  final workingDirectory = value['workingDirectory'];
  if (arguments is! List<dynamic> ||
      arguments.any((argument) => argument is! String) ||
      workingDirectory is! String) {
    throw const FormatException('Invalid desktop activation payload.');
  }
  return DesktopActivation(
    arguments: arguments.cast<String>(),
    workingDirectory: workingDirectory,
  );
}

List<int> _encodeActivationRequest({
  required String applicationId,
  required String token,
  required DesktopActivation activation,
}) {
  return utf8.encode(
    '${jsonEncode({
      'protocolVersion': _protocolVersion,
      'applicationId': applicationId,
      'token': token,
      'activation': {'arguments': activation.arguments, 'workingDirectory': activation.workingDirectory},
    })}\n',
  );
}

Future<String> _readLine(Socket socket, {required Duration timeout}) async {
  final bytes = <int>[];
  await for (final chunk in socket.timeout(timeout)) {
    for (final byte in chunk) {
      if (byte == 0x0A) {
        if (bytes.isNotEmpty && bytes.last == 0x0D) {
          bytes.removeLast();
        }
        return utf8.decode(bytes);
      }
      bytes.add(byte);
      if (bytes.length > _maximumFrameBytes) {
        throw const FormatException(
          'Desktop activation frame exceeds the size limit.',
        );
      }
    }
  }
  throw const FormatException('Desktop activation frame ended unexpectedly.');
}

Future<void> _releaseOwner(RandomAccessFile owner) async {
  try {
    await owner.unlock();
  } on Object {
    // Closing the handle still releases the operating-system lock.
  } finally {
    await owner.close();
  }
}

bool _constantTimeEquals(String left, String right) {
  final leftUnits = left.codeUnits;
  final rightUnits = right.codeUnits;
  var difference = leftUnits.length ^ rightUnits.length;
  final length = min(leftUnits.length, rightUnits.length);
  for (var index = 0; index < length; index++) {
    difference |= leftUnits[index] ^ rightUnits[index];
  }
  return difference == 0;
}

String _createToken() {
  final random = Random.secure();
  return List<String>.generate(
    32,
    (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
    growable: false,
  ).join();
}

Duration _boundedAttemptTimeout(DateTime deadline) {
  final remaining = deadline.difference(DateTime.now());
  if (remaining <= Duration.zero) return const Duration(milliseconds: 1);
  return remaining < _requestTimeout ? remaining : _requestTimeout;
}

Duration _remainingDelay(DateTime deadline) {
  final remaining = deadline.difference(DateTime.now());
  if (remaining <= Duration.zero) return Duration.zero;
  return remaining < _retryDelay ? remaining : _retryDelay;
}

String _defaultRuntimeDirectory() {
  final environment = Platform.environment;
  String? base;
  if (Platform.isWindows) {
    base = environment['LOCALAPPDATA'] ?? environment['TEMP'];
  } else if (Platform.isMacOS) {
    final home = environment['HOME'];
    if (home != null && home.isNotEmpty) {
      base = _joinPath(_joinPath(home, 'Library'), 'Caches');
    }
  } else {
    base = environment['XDG_RUNTIME_DIR'] ?? environment['XDG_CACHE_HOME'];
    if (base == null || base.isEmpty) {
      final home = environment['HOME'];
      if (home != null && home.isNotEmpty) {
        base = _joinPath(home, '.cache');
      }
    }
  }
  if (base == null || base.isEmpty) {
    base = Directory.systemTemp.path;
  }
  return _joinPath(_joinPath(base, 'bridra'), 'single-instance');
}

String _joinPath(String parent, String child) {
  if (parent.endsWith(Platform.pathSeparator)) return '$parent$child';
  return '$parent${Platform.pathSeparator}$child';
}
