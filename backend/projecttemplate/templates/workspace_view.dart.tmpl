import 'dart:async';

import 'package:flutter/material.dart';

import '../../ui/widgets/section.dart';
import 'workspace_view_model.dart';

class WorkspaceView extends StatefulWidget {
  const WorkspaceView({super.key, required this.model});

  final WorkspaceViewModel model;

  @override
  State<WorkspaceView> createState() => _WorkspaceViewState();
}

class _WorkspaceViewState extends State<WorkspaceView> {
  final _name = TextEditingController(text: 'Flutter');

  void _submit() => unawaited(widget.model.greet(_name.text));

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final model = widget.model;
    final theme = Theme.of(context);
    return SingleChildScrollView(
      key: const PageStorageKey('workspace-scroll'),
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          LayoutBuilder(
            builder: (context, constraints) {
              final form = Section(
                child: ConstrainedBox(
                  constraints: const BoxConstraints(minHeight: 280),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Row(
                        children: [
                          Expanded(
                            child: Text(
                              'Request playground',
                              style: theme.textTheme.titleLarge,
                            ),
                          ),
                          const SizedBox(width: 12),
                          Icon(
                            Icons.swap_horiz_rounded,
                            size: 22,
                            color: theme.colorScheme.primary,
                          ),
                        ],
                      ),
                      const SizedBox(height: 8),
                      Text(
                        'Send a greeting through your typed Go backend.',
                        style: theme.textTheme.bodyMedium?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                          height: 1.5,
                        ),
                      ),
                      const SizedBox(height: 28),
                      TextField(
                        key: const Key('name-field'),
                        controller: _name,
                        enabled: model.canRequest,
                        textInputAction: TextInputAction.go,
                        onSubmitted: (_) => _submit(),
                        decoration: const InputDecoration(
                          labelText: 'Your name',
                        ),
                      ),
                      const SizedBox(height: 16),
                      FilledButton.icon(
                        key: const Key('call-button'),
                        onPressed: model.canRequest ? _submit : null,
                        icon: model.requestRunning
                            ? const SizedBox.square(
                                dimension: 16,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                ),
                              )
                            : const Icon(Icons.arrow_forward_rounded, size: 18),
                        label: Text(
                          model.requestRunning ? 'Sending…' : 'Send greeting',
                        ),
                      ),
                      if (model.connection == BackendStatus.offline) ...[
                        const SizedBox(height: 12),
                        OutlinedButton.icon(
                          key: const Key('retry-button'),
                          onPressed: model.requestRunning
                              ? null
                              : () => unawaited(model.connect()),
                          icon: const Icon(Icons.refresh_rounded, size: 18),
                          label: const Text('Reconnect'),
                        ),
                      ],
                      const SizedBox(height: 20),
                      Text(
                        'greeting.hello',
                        style: theme.textTheme.labelMedium?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                    ],
                  ),
                ),
              );
              final response = _Response(model: model);
              if (constraints.maxWidth >= 720) {
                return Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(child: form),
                    const SizedBox(width: 20),
                    Expanded(child: response),
                  ],
                );
              }
              return Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [form, const SizedBox(height: 20), response],
              );
            },
          ),
        ],
      ),
    );
  }
}

class _Response extends StatelessWidget {
  const _Response({required this.model});

  final WorkspaceViewModel model;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final result = model.greeting;
    final error = model.error;
    final color = error != null
        ? theme.colorScheme.error
        : result != null
        ? theme.colorScheme.tertiary
        : theme.colorScheme.onSurfaceVariant;
    final label = error != null
        ? model.connection == BackendStatus.offline
              ? 'CONNECTION UNAVAILABLE'
              : 'REQUEST FAILED'
        : model.requestRunning
        ? 'SENDING REQUEST'
        : result != null
        ? '200 · CONTROLLER RESPONSE'
        : 'READY · AWAITING REQUEST';
    return Section(
      key: const Key('request-result'),
      child: ConstrainedBox(
        constraints: const BoxConstraints(minHeight: 280),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text('Response', style: theme.textTheme.titleLarge),
            const SizedBox(height: 24),
            Text(
              label,
              style: theme.textTheme.labelMedium?.copyWith(color: color),
            ),
            const SizedBox(height: 20),
            Semantics(
              liveRegion: true,
              child: SelectableText(
                error?.toString() ??
                    result?.message ??
                    'Your response will appear here.',
                style: theme.textTheme.titleLarge?.copyWith(
                  color: error != null ? color : theme.colorScheme.onSurface,
                  height: 1.4,
                ),
              ),
            ),
            const SizedBox(height: 16),
            if (result != null) ...[
              Text(result.servedBy, style: theme.textTheme.bodyMedium),
              const SizedBox(height: 4),
              Text(
                'Completed at ${TimeOfDay.fromDateTime(result.timestamp.toLocal()).format(context)}',
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
              const SizedBox(height: 24),
              LayoutBuilder(
                builder: (context, constraints) => Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    for (final step in result.pipeline)
                      ConstrainedBox(
                        constraints: BoxConstraints(
                          maxWidth: constraints.maxWidth,
                        ),
                        child: Chip(
                          label: Text(
                            step,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                          ),
                          side: BorderSide.none,
                          backgroundColor:
                              theme.colorScheme.surfaceContainerLow,
                        ),
                      ),
                  ],
                ),
              ),
            ] else if (error == null) ...[
              Text(
                'The live result includes the service, completion time and middleware pipeline.',
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                  height: 1.5,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}
