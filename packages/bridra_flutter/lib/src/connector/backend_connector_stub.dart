import '../rpc/rpc_client.dart';

Future<RpcClient> connectDefaultRpcClient({void Function(String line)? onLog}) {
  throw UnsupportedError('No backend transport is available on this platform.');
}
