import '../rpc/rpc_client.dart';
import 'backend_connector_stub.dart'
    if (dart.library.io) 'backend_connector_io.dart'
    if (dart.library.js_interop) 'backend_connector_web.dart'
    as implementation;

Future<RpcClient> connectDefaultRpcClient({void Function(String line)? onLog}) {
  return implementation.connectDefaultRpcClient(onLog: onLog);
}
