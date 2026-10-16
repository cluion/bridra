import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../ui/widgets/app_toast.dart';
import '../../ui/widgets/section.dart';

class GuideView extends StatelessWidget {
  const GuideView({super.key, required this.platformSummary});

  final String platformSummary;

  Future<void> _copy(BuildContext context, String command) async {
    await Clipboard.setData(ClipboardData(text: command));
    if (!context.mounted) return;
    AppToastHost.show(context, 'Copied $command');
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return SingleChildScrollView(
      key: const PageStorageKey('guide-scroll'),
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            'A small foundation with room for your application.',
            style: theme.textTheme.bodyLarge?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: 16),
          Text(platformSummary, style: theme.textTheme.bodyMedium),
          const SizedBox(height: 28),
          Section(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text('Project commands', style: theme.textTheme.titleLarge),
                const SizedBox(height: 12),
                for (final command in const [
                  'make doctor',
                  'make verify',
                  'make run',
                ])
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 4),
                    child: Row(
                      children: [
                        Expanded(child: SelectableText(command)),
                        IconButton(
                          tooltip: 'Copy $command',
                          onPressed: () => _copy(context, command),
                          icon: Icon(
                            Icons.copy_outlined,
                            size: 18,
                            semanticLabel: 'Copy $command',
                          ),
                        ),
                      ],
                    ),
                  ),
              ],
            ),
          ),
          const SizedBox(height: 20),
          Section(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text('Where things live', style: theme.textTheme.titleLarge),
                const SizedBox(height: 20),
                for (final entry in const {
                  'app/': 'Application composition and local preferences',
                  'ui/': 'Theme, adaptive layout and shared widgets',
                  'features/': 'Feature views and presentation state',
                  'api/': 'Typed gateway and generated RPC contracts',
                }.entries) ...[
                  Text(entry.key, style: theme.textTheme.titleSmall),
                  const SizedBox(height: 4),
                  Text(
                    entry.value,
                    style: theme.textTheme.bodyMedium?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                  const SizedBox(height: 16),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}
