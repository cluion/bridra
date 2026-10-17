import 'package:flutter_test/flutter_test.dart';
import 'ios_xctest_smoke.dart' show prepareXCTest;

void main() {
  prepareXCTest();
  testWidgets('intentional Dart assertion failure', (tester) async {
    fail('Bridra intentional Dart assertion failure');
  });
}
