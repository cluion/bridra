import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';

import '../../ui/widgets/section.dart';
import 'workspace_view_model.dart';

enum _GreetingExample {
  flutter('Flutter greeting', 'Flutter'),
  bridra('Bridra greeting', 'Bridra'),
  custom('Custom input', null);

  const _GreetingExample(this.label, this.input);

  final String label;
  final String? input;
}

class WorkspaceView extends StatefulWidget {
  const WorkspaceView({super.key, required this.model});

  final WorkspaceViewModel model;

  @override
  State<WorkspaceView> createState() => _WorkspaceViewState();
}

class _WorkspaceViewState extends State<WorkspaceView> {
  final _name = TextEditingController(text: 'Flutter');
  var _example = _GreetingExample.flutter;

  void _submit() => unawaited(widget.model.greet(_name.text));

  void _selectExample(_GreetingExample? example) {
    if (example == null) return;
    setState(() => _example = example);
    if (example.input case final input?) {
      _name.value = TextEditingValue(
        text: input,
        selection: TextSelection.collapsed(offset: input.length),
      );
    }
  }

  void _editName(String value) {
    final example = _GreetingExample.values.firstWhere(
      (example) => example.input == value,
      orElse: () => _GreetingExample.custom,
    );
    if (example != _example) setState(() => _example = example);
  }

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return LayoutBuilder(
      builder: (context, constraints) {
        final compact = constraints.maxWidth < 600;
        final heading = Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Request playground', style: theme.textTheme.headlineMedium),
            const SizedBox(height: 8),
            Text(
              'Send a typed request. See your backend in action.',
              style: theme.textTheme.bodyMedium?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ],
        );
        return SingleChildScrollView(
          key: const PageStorageKey('workspace-scroll'),
          padding: EdgeInsets.all(compact ? 20 : 32),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (compact) ...[
                heading,
                const SizedBox(height: 16),
                Align(
                  alignment: Alignment.centerLeft,
                  child: _ConnectionBadge(connection: widget.model.connection),
                ),
              ] else
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(child: heading),
                    const SizedBox(width: 20),
                    _ConnectionBadge(connection: widget.model.connection),
                  ],
                ),
              const SizedBox(height: 28),
              LayoutBuilder(
                builder: (context, constraints) {
                  final form = _Request(
                    model: widget.model,
                    name: _name,
                    example: _example,
                    onExampleSelected: _selectExample,
                    onNameChanged: _editName,
                    onSubmit: _submit,
                  );
                  final response = _Response(model: widget.model);
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
      },
    );
  }
}

class _ConnectionBadge extends StatelessWidget {
  const _ConnectionBadge({required this.connection});

  final BackendStatus connection;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final color = switch (connection) {
      BackendStatus.online => theme.colorScheme.tertiary,
      BackendStatus.offline => theme.colorScheme.error,
      BackendStatus.connecting => theme.colorScheme.onSurfaceVariant,
    };
    final label = switch (connection) {
      BackendStatus.online => 'Connected',
      BackendStatus.offline => 'Offline',
      BackendStatus.connecting => 'Connecting…',
    };
    return DecoratedBox(
      decoration: BoxDecoration(
        border: Border.all(color: theme.colorScheme.outlineVariant),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.circle, size: 7, color: color),
            const SizedBox(width: 7),
            Flexible(child: Text(label, style: theme.textTheme.labelMedium)),
          ],
        ),
      ),
    );
  }
}

class _RequestFields extends StatelessWidget {
  const _RequestFields({
    required this.enabled,
    required this.name,
    required this.example,
    required this.onExampleSelected,
    required this.onNameChanged,
    required this.onSubmit,
  });

  final bool enabled;
  final TextEditingController name;
  final _GreetingExample example;
  final ValueChanged<_GreetingExample?> onExampleSelected;
  final ValueChanged<String> onNameChanged;
  final VoidCallback onSubmit;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final exampleField = Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('Example request', style: theme.textTheme.labelLarge),
        const SizedBox(height: 8),
        Semantics(
          label: 'Example request',
          child: DropdownMenu<_GreetingExample>(
            key: const Key('request-example'),
            enabled: enabled,
            expandedInsets: EdgeInsets.zero,
            initialSelection: example,
            inputDecorationTheme: theme.dropdownMenuTheme.inputDecorationTheme,
            selectOnly: true,
            enableSearch: false,
            alignmentOffset: const Offset(0, 6),
            trailingIcon: const Icon(
              Icons.keyboard_arrow_down_rounded,
              size: 18,
            ),
            selectedTrailingIcon: const Icon(
              Icons.keyboard_arrow_up_rounded,
              size: 18,
            ),
            onSelected: onExampleSelected,
            dropdownMenuEntries: [
              for (final item in _GreetingExample.values)
                DropdownMenuEntry(
                  value: item,
                  label: item.label,
                  labelWidget: Text(
                    item.label,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  trailingIcon: item == example
                      ? const Icon(Icons.check_rounded, size: 16)
                      : const SizedBox.square(dimension: 16),
                ),
            ],
          ),
        ),
      ],
    );
    final nameField = Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('Your name', style: theme.textTheme.labelLarge),
        const SizedBox(height: 8),
        Semantics(
          label: 'Your name',
          child: TextField(
            key: const Key('name-field'),
            controller: name,
            enabled: enabled,
            onChanged: onNameChanged,
            textInputAction: TextInputAction.go,
            onSubmitted: (_) => onSubmit(),
            decoration: const InputDecoration(hintText: 'Enter a name'),
          ),
        ),
      ],
    );
    return LayoutBuilder(
      builder: (context, constraints) {
        final minFieldWidth =
            200 * MediaQuery.textScalerOf(context).scale(14) / 14;
        if (constraints.maxWidth >= minFieldWidth * 2 + 16) {
          return Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(child: exampleField),
              const SizedBox(width: 16),
              Expanded(child: nameField),
            ],
          );
        }
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [exampleField, const SizedBox(height: 20), nameField],
        );
      },
    );
  }
}

class _Request extends StatelessWidget {
  const _Request({
    required this.model,
    required this.name,
    required this.example,
    required this.onExampleSelected,
    required this.onNameChanged,
    required this.onSubmit,
  });

  final WorkspaceViewModel model;
  final TextEditingController name;
  final _GreetingExample example;
  final ValueChanged<_GreetingExample?> onExampleSelected;
  final ValueChanged<String> onNameChanged;
  final VoidCallback onSubmit;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Section(
      padding: 0,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const SectionHeading(
            icon: Icons.north_east_rounded,
            title: 'Request',
            description: 'A greeting, through your typed Go backend.',
          ),
          const Divider(),
          Padding(
            padding: const EdgeInsets.all(20),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                _RequestFields(
                  enabled: model.canRequest,
                  name: name,
                  example: example,
                  onExampleSelected: onExampleSelected,
                  onNameChanged: onNameChanged,
                  onSubmit: onSubmit,
                ),
                const SizedBox(height: 20),
                DecoratedBox(
                  decoration: BoxDecoration(
                    color: theme.colorScheme.surfaceContainerLow,
                    borderRadius: BorderRadius.circular(6),
                    border: Border.all(color: theme.colorScheme.outlineVariant),
                  ),
                  child: Padding(
                    padding: const EdgeInsets.all(14),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        Text(
                          'PAYLOAD',
                          style: theme.textTheme.labelSmall?.copyWith(
                            color: theme.colorScheme.onSurfaceVariant,
                          ),
                        ),
                        const SizedBox(height: 10),
                        ValueListenableBuilder<TextEditingValue>(
                          valueListenable: name,
                          builder: (context, value, _) => SingleChildScrollView(
                            scrollDirection: Axis.horizontal,
                            child: SelectableText(
                              const JsonEncoder.withIndent(
                                '  ',
                              ).convert({'name': value.text}),
                              key: const Key('request-payload'),
                              style: theme.textTheme.bodySmall?.copyWith(
                                fontFamily: 'monospace',
                                height: 1.7,
                              ),
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
                const SizedBox(height: 20),
                FilledButton.icon(
                  key: const Key('call-button'),
                  onPressed: model.canRequest ? onSubmit : null,
                  icon: model.requestRunning
                      ? SizedBox.square(
                          dimension: 14,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: theme.colorScheme.onPrimary,
                          ),
                        )
                      : const Icon(Icons.arrow_forward_rounded, size: 16),
                  label: Text(
                    model.requestRunning ? 'Sending…' : 'Send greeting',
                  ),
                ),
                if (model.connection == BackendStatus.offline) ...[
                  const SizedBox(height: 10),
                  OutlinedButton.icon(
                    key: const Key('retry-button'),
                    onPressed: model.requestRunning
                        ? null
                        : () => unawaited(model.connect()),
                    icon: const Icon(Icons.refresh_rounded, size: 16),
                    label: const Text('Reconnect'),
                  ),
                ],
              ],
            ),
          ),
          const Divider(),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 12),
            child: Text(
              'RPC  /  greeting.hello',
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
                fontFamily: 'monospace',
              ),
            ),
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
      padding: 0,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const SectionHeading(
            icon: Icons.south_west_rounded,
            title: 'Response',
            description: 'The result, directly from your backend.',
          ),
          const Divider(),
          Padding(
            padding: const EdgeInsets.all(20),
            child: ConstrainedBox(
              constraints: const BoxConstraints(minHeight: 270),
              child: Semantics(
                liveRegion: true,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Text(
                      label,
                      style: theme.textTheme.labelSmall?.copyWith(color: color),
                    ),
                    const SizedBox(height: 24),
                    if (result == null && error == null)
                      Padding(
                        padding: const EdgeInsets.symmetric(vertical: 24),
                        child: Column(
                          children: [
                            DecoratedBox(
                              decoration: BoxDecoration(
                                color: theme.colorScheme.surfaceContainerHigh,
                                borderRadius: BorderRadius.circular(10),
                              ),
                              child: Padding(
                                padding: const EdgeInsets.all(14),
                                child: Icon(
                                  Icons.data_object_rounded,
                                  size: 24,
                                  color: theme.colorScheme.onSurfaceVariant,
                                ),
                              ),
                            ),
                            const SizedBox(height: 16),
                            Text(
                              model.requestRunning
                                  ? 'Waiting for your backend…'
                                  : 'Your response will appear here.',
                              textAlign: TextAlign.center,
                              style: theme.textTheme.titleMedium,
                            ),
                            const SizedBox(height: 6),
                            Text(
                              'Inspect the result and middleware pipeline after sending a request.',
                              textAlign: TextAlign.center,
                              style: theme.textTheme.bodySmall?.copyWith(
                                color: theme.colorScheme.onSurfaceVariant,
                              ),
                            ),
                          ],
                        ),
                      )
                    else ...[
                      SelectableText(
                        error?.toString() ?? result!.message,
                        style: theme.textTheme.titleLarge?.copyWith(
                          color: error != null
                              ? color
                              : theme.colorScheme.onSurface,
                          height: 1.4,
                        ),
                      ),
                      if (result != null) ...[
                        const SizedBox(height: 24),
                        Text(
                          result.servedBy,
                          style: theme.textTheme.bodyMedium,
                        ),
                        const SizedBox(height: 4),
                        Text(
                          'Completed at ${TimeOfDay.fromDateTime(result.timestamp.toLocal()).format(context)}',
                          style: theme.textTheme.bodySmall?.copyWith(
                            color: theme.colorScheme.onSurfaceVariant,
                          ),
                        ),
                        const SizedBox(height: 24),
                        const Divider(),
                        const SizedBox(height: 20),
                        Text('PIPELINE', style: theme.textTheme.labelSmall),
                        const SizedBox(height: 10),
                        Wrap(
                          spacing: 6,
                          runSpacing: 6,
                          children: [
                            for (final step in result.pipeline)
                              DecoratedBox(
                                decoration: BoxDecoration(
                                  color: theme.colorScheme.surfaceContainerHigh,
                                  borderRadius: BorderRadius.circular(5),
                                ),
                                child: Padding(
                                  padding: const EdgeInsets.symmetric(
                                    horizontal: 8,
                                    vertical: 4,
                                  ),
                                  child: Text(
                                    step,
                                    style: theme.textTheme.bodySmall,
                                  ),
                                ),
                              ),
                          ],
                        ),
                      ],
                    ],
                  ],
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
