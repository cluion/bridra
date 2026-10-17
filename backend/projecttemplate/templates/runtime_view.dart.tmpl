import 'package:flutter/material.dart';

import '../../ui/widgets/section.dart';
import '../workspace/workspace_view_model.dart';

class RuntimeView extends StatelessWidget {
  const RuntimeView({super.key, required this.model});

  final WorkspaceViewModel model;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final health = model.health;
    final details = <String, String>{
      'Status': switch (model.connection) {
        BackendStatus.connecting => 'Connecting',
        BackendStatus.online => 'Ready',
        BackendStatus.offline => 'Not connected',
      },
      'Transport': health?.runtime ?? 'Waiting for backend',
      'Framework': health?.frameworkVersion ?? '—',
      'RPC protocol': health?.protocolVersion.toString() ?? '—',
      'Application pipeline': health?.architecture ?? '—',
    };
    return SingleChildScrollView(
      key: const PageStorageKey('runtime-scroll'),
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            'Inspect the live backend behind this workspace.',
            style: theme.textTheme.bodyLarge?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: 28),
          Section(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (final entry in details.entries) ...[
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 14),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          entry.key,
                          style: theme.textTheme.labelMedium?.copyWith(
                            color: theme.colorScheme.onSurfaceVariant,
                          ),
                        ),
                        const SizedBox(height: 6),
                        SelectableText(
                          entry.value,
                          style: theme.textTheme.bodyLarge,
                        ),
                      ],
                    ),
                  ),
                  if (entry.key != details.keys.last) const Divider(),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}
