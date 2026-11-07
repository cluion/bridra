import 'package:flutter/material.dart';

import '../../ui/widgets/section.dart';

class SettingsView extends StatelessWidget {
  const SettingsView({
    super.key,
    required this.themeMode,
    required this.onThemeChanged,
  });

  final ThemeMode themeMode;
  final ValueChanged<ThemeMode> onThemeChanged;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final choices = [
      for (final mode in ThemeMode.values)
        _AppearanceChoice(
          mode: mode,
          selected: mode == themeMode,
          onSelected: () => onThemeChanged(mode),
        ),
    ];
    return FeaturePage(
      storageKey: 'settings-scroll',
      title: 'Preferences',
      description: 'Make this workspace feel like yours.',
      children: [
        Section(
          padding: 0,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const SectionHeading(
                icon: Icons.palette_outlined,
                title: 'Appearance',
                description: 'Choose a theme or follow your device.',
              ),
              const Divider(),
              Padding(
                padding: const EdgeInsets.all(20),
                child: RadioGroup<ThemeMode>(
                  groupValue: themeMode,
                  onChanged: (value) {
                    if (value != null) onThemeChanged(value);
                  },
                  child: LayoutBuilder(
                    builder: (context, constraints) {
                      final minChoiceWidth =
                          180 * MediaQuery.textScalerOf(context).scale(14) / 14;
                      if (constraints.maxWidth >= minChoiceWidth * 3 + 24) {
                        return Row(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            for (var i = 0; i < choices.length; i++) ...[
                              if (i > 0) const SizedBox(width: 12),
                              Expanded(child: choices[i]),
                            ],
                          ],
                        );
                      }
                      return Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          for (var i = 0; i < choices.length; i++) ...[
                            if (i > 0) const SizedBox(height: 12),
                            choices[i],
                          ],
                        ],
                      );
                    },
                  ),
                ),
              ),
              const Divider(),
              Padding(
                padding: const EdgeInsets.all(16),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Icon(
                      Icons.info_outline_rounded,
                      size: 16,
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Text(
                        'Changes apply immediately to every page. Your choice is kept for the current session.',
                        style: theme.textTheme.bodySmall?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }
}

class _AppearanceChoice extends StatelessWidget {
  const _AppearanceChoice({
    required this.mode,
    required this.selected,
    required this.onSelected,
  });

  final ThemeMode mode;
  final bool selected;
  final VoidCallback onSelected;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final label = switch (mode) {
      ThemeMode.system => 'System',
      ThemeMode.light => 'Light',
      ThemeMode.dark => 'Dark',
    };
    final description = switch (mode) {
      ThemeMode.system => 'Follow your device appearance',
      ThemeMode.light => 'A bright, neutral workspace',
      ThemeMode.dark => 'A quiet, low-light workspace',
    };
    return Material(
      color: theme.colorScheme.surface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(8),
        side: BorderSide(
          color: selected
              ? theme.colorScheme.primary
              : theme.colorScheme.outlineVariant,
        ),
      ),
      clipBehavior: Clip.antiAlias,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.all(12),
            child: GestureDetector(
              key: Key('appearance-preview-${mode.name}'),
              onTap: onSelected,
              excludeFromSemantics: true,
              child: ExcludeSemantics(child: _ThemePreview(mode: mode)),
            ),
          ),
          RadioListTile<ThemeMode>(
            key: Key('appearance-${mode.name}'),
            value: mode,
            selected: selected,
            title: Text(label, style: theme.textTheme.labelLarge),
            subtitle: Text(
              description,
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            controlAffinity: ListTileControlAffinity.trailing,
            contentPadding: const EdgeInsets.fromLTRB(12, 0, 12, 10),
            dense: true,
          ),
        ],
      ),
    );
  }
}

class _ThemePreview extends StatelessWidget {
  const _ThemePreview({required this.mode});

  final ThemeMode mode;

  @override
  Widget build(BuildContext context) => Container(
    height: 88,
    decoration: BoxDecoration(
      border: Border.all(color: Theme.of(context).colorScheme.outlineVariant),
      borderRadius: BorderRadius.circular(6),
    ),
    clipBehavior: Clip.antiAlias,
    child: mode == ThemeMode.system
        ? const Row(
            children: [
              Expanded(child: _MiniWorkspace(dark: false)),
              Expanded(child: _MiniWorkspace(dark: true)),
            ],
          )
        : _MiniWorkspace(dark: mode == ThemeMode.dark),
  );
}

class _MiniWorkspace extends StatelessWidget {
  const _MiniWorkspace({required this.dark});

  final bool dark;

  @override
  Widget build(BuildContext context) {
    final canvas = dark ? const Color(0xFF09090B) : Colors.white;
    final muted = dark ? const Color(0xFF27272A) : const Color(0xFFE4E4E7);
    final shell = dark ? const Color(0xFF18181B) : const Color(0xFFF4F4F5);
    return ColoredBox(
      color: canvas,
      child: Column(
        children: [
          Container(
            height: 14,
            color: shell,
            padding: const EdgeInsets.only(left: 6),
            child: Row(
              children: [
                for (var i = 0; i < 3; i++) ...[
                  DecoratedBox(
                    decoration: BoxDecoration(
                      color: muted,
                      shape: BoxShape.circle,
                    ),
                    child: const SizedBox.square(dimension: 3),
                  ),
                  const SizedBox(width: 3),
                ],
              ],
            ),
          ),
          Expanded(
            child: Row(
              children: [
                Container(width: 24, color: shell),
                Expanded(
                  child: Padding(
                    padding: const EdgeInsets.all(8),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        Align(
                          alignment: Alignment.centerLeft,
                          child: Container(width: 24, height: 4, color: muted),
                        ),
                        const SizedBox(height: 6),
                        Expanded(
                          child: Row(
                            children: [
                              Expanded(
                                child: ColoredBox(
                                  color: shell,
                                  child: const SizedBox.expand(),
                                ),
                              ),
                              const SizedBox(width: 5),
                              Expanded(
                                child: ColoredBox(
                                  color: shell,
                                  child: const SizedBox.expand(),
                                ),
                              ),
                            ],
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
