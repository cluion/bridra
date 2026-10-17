import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

abstract final class AppTheme {
  static const _lightShell = Color(0xFFEAEAEB);
  static const _lightContent = Colors.white;
  static const _darkShell = Color(0xFF27292B);
  static const _darkContent = Color(0xFF181818);

  static ThemeData light() => _build(Brightness.light);
  static ThemeData dark() => _build(Brightness.dark);

  static ThemeData _build(Brightness brightness) {
    final dark = brightness == Brightness.dark;
    final colors =
        ColorScheme.fromSeed(
          seedColor: const Color(0xFF6575EF),
          brightness: brightness,
        ).copyWith(
          primary: dark ? const Color(0xFFADB8FF) : const Color(0xFF4C5BD5),
          onSurface: dark ? const Color(0xFFEFEFEF) : const Color(0xFF242424),
          onSurfaceVariant: dark
              ? const Color(0xFFB4B6B8)
              : const Color(0xFF626262),
          surface: dark ? const Color(0xFF202122) : const Color(0xFFF7F7F7),
          surfaceDim: dark ? _darkContent : _lightShell,
          surfaceBright: dark ? const Color(0xFF3A3C3E) : _lightContent,
          surfaceContainerLowest: dark
              ? const Color(0xFF141414)
              : _lightContent,
          surfaceContainer: dark ? _darkShell : _lightShell,
          surfaceContainerHigh: dark
              ? const Color(0xFF303234)
              : const Color(0xFFE5E5E5),
          surfaceContainerLow: dark
              ? const Color(0xFF222426)
              : const Color(0xFFF8F8F8),
          surfaceContainerHighest: dark
              ? const Color(0xFF3A3C3E)
              : const Color(0xFFDEDEDE),
          tertiary: dark ? const Color(0xFF75D7AF) : const Color(0xFF197653),
          onTertiary: dark ? const Color(0xFF072D20) : Colors.white,
          outline: dark ? const Color(0xFF5C5E60) : const Color(0xFF8A8A8A),
          outlineVariant: dark
              ? const Color(0xFF3A3C3E)
              : const Color(0xFFDEDEDE),
        );
    final shape = RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(10),
    );
    return ThemeData(
      useMaterial3: true,
      colorScheme: colors,
      scaffoldBackgroundColor: dark ? _darkContent : _lightContent,
      // Linux engines without Fontconfig need explicit CJK fallback families.
      fontFamilyFallback:
          !kIsWeb && defaultTargetPlatform == TargetPlatform.linux
          ? const [
              'Noto Sans CJK TC',
              'Noto Sans CJK SC',
              'Noto Sans CJK JP',
              'Noto Sans CJK KR',
              'Droid Sans Fallback',
            ]
          : null,
      dividerTheme: DividerThemeData(
        color: colors.outlineVariant,
        thickness: 1,
        space: 1,
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: dark ? _darkContent : _lightContent,
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 16,
          vertical: 16,
        ),
        border: OutlineInputBorder(borderRadius: BorderRadius.circular(10)),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(10),
          borderSide: BorderSide(color: colors.outlineVariant),
        ),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          backgroundColor: const Color(0xFF5C6BE5),
          foregroundColor: Colors.white,
          padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 15),
          shape: shape,
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(
          padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 18),
          shape: shape,
        ),
      ),
      navigationRailTheme: NavigationRailThemeData(
        backgroundColor: dark ? _darkShell : _lightShell,
        indicatorColor: dark
            ? const Color(0xFF3A3C3E)
            : const Color(0xFFDADADA),
      ),
      navigationBarTheme: NavigationBarThemeData(
        backgroundColor: dark ? _darkShell : _lightShell,
        indicatorColor: dark
            ? const Color(0xFF3A3C3E)
            : const Color(0xFFDADADA),
        elevation: 0,
      ),
    );
  }
}
