import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

abstract final class AppTheme {
  static ThemeData light() => _build(Brightness.light);
  static ThemeData dark() => _build(Brightness.dark);

  static ThemeData _build(Brightness brightness) {
    final dark = brightness == Brightness.dark;
    final shell = dark ? const Color(0xFF101012) : const Color(0xFFFAFAFA);
    final canvas = dark ? const Color(0xFF09090B) : Colors.white;
    final muted = dark ? const Color(0xFF18181B) : const Color(0xFFF4F4F5);
    final border = dark ? const Color(0xFF27272A) : const Color(0xFFE4E4E7);
    final foreground = dark ? const Color(0xFFFAFAFA) : const Color(0xFF18181B);
    final colors =
        ColorScheme.fromSeed(
          seedColor: const Color(0xFF71717A),
          brightness: brightness,
        ).copyWith(
          primary: foreground,
          onPrimary: canvas,
          onSurface: foreground,
          onSurfaceVariant: dark
              ? const Color(0xFFA1A1AA)
              : const Color(0xFF71717A),
          surface: canvas,
          surfaceDim: canvas,
          surfaceBright: dark ? const Color(0xFF27272A) : Colors.white,
          surfaceContainerLowest: canvas,
          surfaceContainerLow: shell,
          surfaceContainer: shell,
          surfaceContainerHigh: muted,
          surfaceContainerHighest: dark
              ? const Color(0xFF27272A)
              : const Color(0xFFE4E4E7),
          tertiary: dark ? const Color(0xFF4ADE80) : const Color(0xFF15803D),
          onTertiary: dark ? const Color(0xFF052E16) : Colors.white,
          outline: dark ? const Color(0xFF71717A) : const Color(0xFFA1A1AA),
          outlineVariant: border,
        );
    final base = ThemeData(
      useMaterial3: true,
      colorScheme: colors,
      scaffoldBackgroundColor: canvas,
      splashFactory: NoSplash.splashFactory,
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
    );
    final text = base.textTheme.copyWith(
      headlineMedium: base.textTheme.headlineMedium?.copyWith(
        fontSize: 28,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.8,
        height: 1.25,
      ),
      titleLarge: base.textTheme.titleLarge?.copyWith(
        fontSize: 18,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.4,
      ),
      titleMedium: base.textTheme.titleMedium?.copyWith(
        fontSize: 14,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.1,
      ),
      bodyLarge: base.textTheme.bodyLarge?.copyWith(fontSize: 14, height: 1.5),
      bodyMedium: base.textTheme.bodyMedium?.copyWith(
        fontSize: 14,
        height: 1.5,
      ),
      bodySmall: base.textTheme.bodySmall?.copyWith(fontSize: 12, height: 1.5),
      labelLarge: base.textTheme.labelLarge?.copyWith(
        fontSize: 13,
        fontWeight: FontWeight.w500,
        letterSpacing: 0,
      ),
      labelMedium: base.textTheme.labelMedium?.copyWith(
        fontSize: 12,
        fontWeight: FontWeight.w500,
        letterSpacing: 0,
      ),
      labelSmall: base.textTheme.labelSmall?.copyWith(
        fontSize: 11,
        fontWeight: FontWeight.w500,
        letterSpacing: 0.3,
      ),
    );
    final shape = RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(6),
    );
    final inputs = InputDecorationThemeData(
      filled: true,
      fillColor: canvas,
      isDense: true,
      visualDensity: VisualDensity.standard,
      constraints: const BoxConstraints(minHeight: 44),
      hintStyle: text.bodyMedium?.copyWith(color: colors.onSurfaceVariant),
      contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 12),
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(6)),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(6),
        borderSide: BorderSide(color: border),
      ),
      disabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(6),
        borderSide: BorderSide(color: border),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(6),
        borderSide: BorderSide(color: colors.outline, width: 2),
      ),
    );
    return base.copyWith(
      textTheme: text,
      dividerColor: border,
      dividerTheme: DividerThemeData(color: border, thickness: 1, space: 1),
      inputDecorationTheme: inputs,
      dropdownMenuTheme: DropdownMenuThemeData(
        textStyle: text.bodyMedium,
        inputDecorationTheme: inputs.copyWith(
          suffixIconConstraints: const BoxConstraints(
            minWidth: 32,
            minHeight: 32,
          ),
        ),
        menuStyle: MenuStyle(
          backgroundColor: WidgetStatePropertyAll(canvas),
          surfaceTintColor: const WidgetStatePropertyAll(Colors.transparent),
          shadowColor: WidgetStatePropertyAll(
            Colors.black.withValues(alpha: dark ? 0.3 : 0.12),
          ),
          elevation: const WidgetStatePropertyAll(6),
          padding: const WidgetStatePropertyAll(EdgeInsets.all(4)),
          side: WidgetStatePropertyAll(BorderSide(color: border)),
          shape: WidgetStatePropertyAll(
            RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
          ),
        ),
      ),
      menuButtonTheme: MenuButtonThemeData(
        style: ButtonStyle(
          foregroundColor: WidgetStatePropertyAll(foreground),
          textStyle: WidgetStatePropertyAll(text.bodyMedium),
          minimumSize: const WidgetStatePropertyAll(Size(0, 44)),
          padding: const WidgetStatePropertyAll(
            EdgeInsets.symmetric(horizontal: 10, vertical: 10),
          ),
          backgroundColor: WidgetStateProperty.resolveWith(
            (states) =>
                states.contains(WidgetState.hovered) ||
                    states.contains(WidgetState.focused)
                ? muted
                : Colors.transparent,
          ),
          shape: WidgetStatePropertyAll(
            RoundedRectangleBorder(borderRadius: BorderRadius.circular(4)),
          ),
        ),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          backgroundColor: colors.primary,
          foregroundColor: colors.onPrimary,
          disabledBackgroundColor: muted,
          disabledForegroundColor: colors.onSurfaceVariant,
          minimumSize: const Size(0, 44),
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          textStyle: text.labelLarge,
          shape: shape,
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(
          foregroundColor: foreground,
          side: BorderSide(color: border),
          minimumSize: const Size(0, 44),
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          textStyle: text.labelLarge,
          shape: shape,
        ),
      ),
      iconButtonTheme: IconButtonThemeData(
        style: IconButton.styleFrom(
          foregroundColor: colors.onSurfaceVariant,
          shape: shape,
        ),
      ),
      navigationRailTheme: NavigationRailThemeData(
        backgroundColor: shell,
        indicatorColor: dark ? const Color(0xFF202023) : Colors.white,
      ),
      navigationBarTheme: NavigationBarThemeData(
        backgroundColor: shell,
        indicatorColor: muted,
        elevation: 0,
      ),
    );
  }
}
