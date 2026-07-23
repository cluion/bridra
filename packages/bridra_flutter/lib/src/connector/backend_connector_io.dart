import 'dart:io';

import '../sidecar/sidecar_client.dart';
import '../rpc/http_rpc_client.dart';
import '../rpc/rpc_client.dart';

const _configuredURL = String.fromEnvironment('BRIDRA_BACKEND_URL');
const _configuredToken = String.fromEnvironment(
  'BRIDRA_BACKEND_TOKEN',
  defaultValue: 'dev-token',
);

Future<RpcClient> connectDefaultRpcClient({
  void Function(String line)? onLog,
}) async {
  if (_configuredURL.isNotEmpty) {
    return HttpRpcClient(
      endpoint: Uri.parse(_configuredURL),
      token: _configuredToken,
    );
  }

  if (Platform.isWindows || Platform.isMacOS || Platform.isLinux) {
    final executable = await SidecarClient.resolveExecutable();
    return SidecarClient.start(
      executablePath: executable,
      token: SidecarClient.createToken(),
      onLog: onLog,
    );
  }

  final host = Platform.isAndroid ? '10.0.2.2' : '127.0.0.1';
  return HttpRpcClient(
    endpoint: Uri.parse('http://$host:8080/rpc'),
    token: _configuredToken,
  );
}
