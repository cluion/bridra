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
    final status = switch (model.connection) {
      BackendStatus.connecting => 'Connecting',
      BackendStatus.online => 'Ready',
      BackendStatus.offline => 'Not connected',
    };
    final statusColor = switch (model.connection) {
      BackendStatus.connecting => theme.colorScheme.onSurfaceVariant,
      BackendStatus.online => theme.colorScheme.tertiary,
      BackendStatus.offline => theme.colorScheme.error,
    };
    final metrics = [
      _RuntimeMetric(
        icon: Icons.hub_outlined,
        label: 'Transport',
        value: health?.runtime ?? 'Waiting for backend',
        description: 'Current backend runtime',
      ),
      _RuntimeMetric(
        icon: Icons.widgets_outlined,
        label: 'Framework',
        value: health?.frameworkVersion ?? '—',
        description: 'Framework version',
      ),
      _RuntimeMetric(
        icon: Icons.code_rounded,
        label: 'RPC protocol',
        value: health?.protocolVersion.toString() ?? '—',
        description: 'Application contract',
      ),
    ];
    return FeaturePage(
      storageKey: 'runtime-scroll',
      title: 'Runtime overview',
      description: 'Inspect the live backend behind this workspace.',
      trailing: AppBadge(label: status, statusColor: statusColor),
      children: [
        LayoutBuilder(
          builder: (context, constraints) {
            if (constraints.maxWidth >= 700 &&
                MediaQuery.textScalerOf(context).scale(14) <= 18) {
              return Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  for (var i = 0; i < metrics.length; i++) ...[
                    if (i > 0) const SizedBox(width: 16),
                    Expanded(child: metrics[i]),
                  ],
                ],
              );
            }
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (var i = 0; i < metrics.length; i++) ...[
                  if (i > 0) const SizedBox(height: 12),
                  metrics[i],
                ],
              ],
            );
          },
        ),
        const SizedBox(height: 20),
        Section(
          padding: 0,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const SectionHeading(
                icon: Icons.route_outlined,
                title: 'Application pipeline',
                description: 'The architecture reported by your backend.',
              ),
              const Divider(),
              Padding(
                padding: const EdgeInsets.all(20),
                child: DecoratedBox(
                  decoration: BoxDecoration(
                    color: theme.colorScheme.surfaceContainerLow,
                    border: Border.all(color: theme.colorScheme.outlineVariant),
                    borderRadius: BorderRadius.circular(6),
                  ),
                  child: Padding(
                    padding: const EdgeInsets.all(16),
                    child: SelectableText(
                      health?.architecture ?? 'Waiting for backend',
                      style: theme.textTheme.bodyMedium?.copyWith(
                        fontFamily: 'monospace',
                        height: 1.7,
                      ),
                    ),
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
                      model.connection == BackendStatus.offline
                          ? Icons.cloud_off_outlined
                          : Icons.info_outline_rounded,
                      size: 16,
                      color: statusColor,
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Text(
                        model.connection == BackendStatus.offline
                            ? 'Reconnect from Workspace to refresh these details.'
                            : 'Runtime uses the current Workspace connection.',
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

class _RuntimeMetric extends StatelessWidget {
  const _RuntimeMetric({
    required this.icon,
    required this.label,
    required this.value,
    required this.description,
  });

  final IconData icon;
  final String label;
  final String value;
  final String description;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Section(
      padding: 20,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  label,
                  style: theme.textTheme.labelMedium?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Icon(icon, size: 16, color: theme.colorScheme.onSurfaceVariant),
            ],
          ),
          const SizedBox(height: 16),
          SelectableText(value, style: theme.textTheme.titleLarge),
          const SizedBox(height: 6),
          Text(
            description,
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
    );
  }
}
