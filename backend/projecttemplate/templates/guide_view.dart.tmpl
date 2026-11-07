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
    final commands = Section(
      padding: 0,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const SectionHeading(
            icon: Icons.terminal_rounded,
            title: 'Project commands',
            description: 'Your everyday development workflow.',
          ),
          const Divider(),
          Padding(
            padding: const EdgeInsets.all(20),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (final entry in const {
                  'make doctor': 'Check your development environment',
                  'make verify': 'Run the project verification checks',
                  'make run': 'Start your application locally',
                }.entries) ...[
                  Text(entry.value, style: theme.textTheme.labelLarge),
                  const SizedBox(height: 8),
                  DecoratedBox(
                    decoration: BoxDecoration(
                      color: theme.colorScheme.surfaceContainerLow,
                      border: Border.all(
                        color: theme.colorScheme.outlineVariant,
                      ),
                      borderRadius: BorderRadius.circular(6),
                    ),
                    child: Padding(
                      padding: const EdgeInsets.only(left: 12, right: 4),
                      child: Row(
                        children: [
                          Expanded(
                            child: SelectableText(
                              entry.key,
                              style: theme.textTheme.bodyMedium?.copyWith(
                                fontFamily: 'monospace',
                              ),
                            ),
                          ),
                          const SizedBox(width: 8),
                          IconButton(
                            tooltip: 'Copy ${entry.key}',
                            onPressed: () => _copy(context, entry.key),
                            icon: Icon(
                              Icons.copy_outlined,
                              size: 16,
                              semanticLabel: 'Copy ${entry.key}',
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                  if (entry.key != 'make run') const SizedBox(height: 20),
                ],
              ],
            ),
          ),
        ],
      ),
    );
    final structure = Section(
      padding: 0,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const SectionHeading(
            icon: Icons.account_tree_outlined,
            title: 'Where things live',
            description: 'A clear home for each part of your application.',
          ),
          const Divider(),
          Padding(
            padding: const EdgeInsets.all(20),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (final entry in const {
                  'lib/app/': 'Application composition and local preferences',
                  'lib/ui/': 'Theme, adaptive layout and shared widgets',
                  'lib/features/': 'Feature views and presentation state',
                  'lib/api/': 'Typed gateway and generated RPC contracts',
                }.entries) ...[
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Padding(
                        padding: const EdgeInsets.only(top: 2),
                        child: Icon(
                          Icons.folder_outlined,
                          size: 16,
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                      const SizedBox(width: 10),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            Text(
                              entry.key,
                              style: theme.textTheme.labelLarge?.copyWith(
                                fontFamily: 'monospace',
                              ),
                            ),
                            const SizedBox(height: 4),
                            Text(
                              entry.value,
                              style: theme.textTheme.bodySmall?.copyWith(
                                color: theme.colorScheme.onSurfaceVariant,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ],
                  ),
                  if (entry.key != 'lib/api/') const SizedBox(height: 20),
                ],
              ],
            ),
          ),
        ],
      ),
    );
    return FeaturePage(
      storageKey: 'guide-scroll',
      title: 'Project guide',
      description: 'A small foundation with room for your application.',
      trailing: AppBadge(label: platformSummary, icon: Icons.devices_outlined),
      children: [
        LayoutBuilder(
          builder: (context, constraints) {
            if (constraints.maxWidth >= 800 &&
                MediaQuery.textScalerOf(context).scale(14) <= 18) {
              return Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Expanded(flex: 5, child: commands),
                  const SizedBox(width: 20),
                  Expanded(flex: 4, child: structure),
                ],
              );
            }
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [commands, const SizedBox(height: 20), structure],
            );
          },
        ),
      ],
    );
  }
}
