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
    return SingleChildScrollView(
      key: const PageStorageKey('settings-scroll'),
      padding: const EdgeInsets.all(24),
      child: Section(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text('Appearance', style: Theme.of(context).textTheme.titleLarge),
            const SizedBox(height: 16),
            Material(
              type: MaterialType.transparency,
              child: RadioGroup<ThemeMode>(
                groupValue: themeMode,
                onChanged: (value) {
                  if (value != null) onThemeChanged(value);
                },
                child: Column(
                  children: [
                    for (final item in const {
                      ThemeMode.system: 'System',
                      ThemeMode.light: 'Light',
                      ThemeMode.dark: 'Dark',
                    }.entries)
                      RadioListTile<ThemeMode>(
                        key: Key('appearance-${item.key.name}'),
                        value: item.key,
                        title: Text(item.value),
                        subtitle: item.key == ThemeMode.system
                            ? const Text('Follow your device appearance')
                            : null,
                        controlAffinity: ListTileControlAffinity.trailing,
                        contentPadding: const EdgeInsets.symmetric(
                          horizontal: 8,
                        ),
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(10),
                        ),
                      ),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
