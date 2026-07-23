import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'public default connector selects the Web HTTP client',
    () async {
      final client = await connectDefaultRpcClient();
      addTearDown(client.close);

      expect(client, isA<HttpRpcClient>());
      expect(
        (client as HttpRpcClient).endpoint,
        Uri.parse('http://127.0.0.1:8080/rpc'),
      );
    },
    skip: kIsWeb ? false : 'Browser connector test.',
  );
}
