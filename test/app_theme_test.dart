import 'package:bridra/ui/theme/app_theme.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('preserves explicit Linux CJK fallback in both appearances', () {
    debugDefaultTargetPlatformOverride = TargetPlatform.linux;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
    for (final theme in [AppTheme.light(), AppTheme.dark()]) {
      expect(
        theme.textTheme.bodyMedium?.fontFamilyFallback,
        containsAll([
          'Noto Sans CJK TC',
          'Noto Sans CJK SC',
          'Noto Sans CJK JP',
          'Noto Sans CJK KR',
          'Droid Sans Fallback',
        ]),
      );
    }
  });
}
