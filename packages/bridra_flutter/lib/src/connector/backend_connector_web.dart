import '../rpc/http_rpc_client.dart';
import '../rpc/rpc_client.dart';

const _configuredURL = String.fromEnvironment(
  'BRIDRA_BACKEND_URL',
  defaultValue: 'http://127.0.0.1:8080/rpc',
);
const _configuredToken = String.fromEnvironment(
  'BRIDRA_BACKEND_TOKEN',
  defaultValue: 'dev-token',
);

Future<RpcClient> connectDefaultRpcClient({
  void Function(String line)? onLog,
}) async {
  return HttpRpcClient(
    endpoint: Uri.parse(_configuredURL),
    token: _configuredToken,
  );
}
