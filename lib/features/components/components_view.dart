import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../ui/widgets/app_toast.dart';
import '../../ui/widgets/section.dart';

class ComponentsView extends StatefulWidget {
  const ComponentsView({super.key});

  @override
  State<ComponentsView> createState() => _ComponentsViewState();
}

class _ComponentsViewState extends State<ComponentsView> {
  final _name = TextEditingController(text: 'Alex Morgan');
  final _email = TextEditingController(text: 'not-an-email');
  final _popoverController = MenuController();
  var _tab = 0;
  var _role = 'Designer';
  var _checked = false;
  bool? _mixed;
  var _enabled = true;
  var _density = 'Comfortable';
  var _progress = 0.65;
  var _hasItem = false;
  var _showDetails = true;
  var _lastAction = 'No action selected';
  DateTime? _date;
  var _emptyTable = false;
  var _ascending = true;
  final _selectedMembers = <String>{};

  static const _members = [
    (name: 'Alex Morgan', role: 'Designer', status: 'Active'),
    (name: 'Sam Chen', role: 'Developer', status: 'Invited'),
    (name: 'Taylor Reed', role: 'Viewer', status: 'Active'),
  ];

  @override
  void dispose() {
    _name.dispose();
    _email.dispose();
    super.dispose();
  }

  void _notify(String message) => AppToastHost.show(context, message);

  Future<void> _confirm() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        scrollable: true,
        title: const Text('Confirm demo action'),
        content: const Text(
          'Try the confirmation flow with this sample action.',
        ),
        actions: [
          TextButton(
            key: const Key('gallery-dialog-cancel'),
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            key: const Key('gallery-dialog-confirm'),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Confirm'),
          ),
        ],
      ),
    );
    if (confirmed == true && mounted) _notify('Demo action confirmed');
  }

  Future<void> _pickDate() async {
    final today = DateUtils.dateOnly(DateTime.now());
    final date = await showDatePicker(
      context: context,
      initialDate: _date ?? today,
      firstDate: DateTime(today.year - 5),
      lastDate: DateTime(today.year + 5, 12, 31),
      helpText: 'Select date',
    );
    if (date != null && mounted) setState(() => _date = date);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return DefaultTabController(
      length: 4,
      animationDuration: MediaQuery.disableAnimationsOf(context)
          ? Duration.zero
          : const Duration(milliseconds: 180),
      child: FeaturePage(
        storageKey: 'components-scroll',
        title: 'Component gallery',
        description: 'Interactive examples. Changes stay in this gallery.',
        trailing: const AppBadge(
          label: 'Sample data only',
          icon: Icons.tune_rounded,
        ),
        children: [
          Material(
            color: theme.colorScheme.surfaceContainerHigh,
            borderRadius: BorderRadius.circular(8),
            clipBehavior: Clip.antiAlias,
            child: Padding(
              padding: const EdgeInsets.all(4),
              child: TabBar(
                isScrollable: true,
                tabAlignment: TabAlignment.start,
                onTap: (index) => setState(() => _tab = index),
                tabs: [
                  for (final tab in const [
                    'Controls',
                    'Feedback',
                    'Overlays',
                    'Data',
                  ])
                    Tab(
                      key: Key('components-tab-${tab.toLowerCase()}'),
                      text: tab,
                      height: 40,
                    ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 20),
          ...switch (_tab) {
            0 => [
              _buttons(),
              const SizedBox(height: 20),
              _inputs(),
              const SizedBox(height: 20),
              _selection(),
            ],
            1 => [_feedback()],
            2 => [_overlays()],
            _ => [_data()],
          },
        ],
      ),
    );
  }

  Widget _buttons() {
    final colors = Theme.of(context).colorScheme;
    return _GallerySection(
      icon: Icons.smart_button_outlined,
      title: 'Buttons',
      description:
          'Primary, secondary, ghost, destructive and unavailable actions.',
      child: Wrap(
        spacing: 12,
        runSpacing: 12,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          FilledButton(
            onPressed: () => _notify('Primary action clicked'),
            child: const Text('Primary'),
          ),
          OutlinedButton(
            onPressed: () => _notify('Secondary action clicked'),
            child: const Text('Secondary'),
          ),
          TextButton(
            onPressed: () => _notify('Ghost action clicked'),
            child: const Text('Ghost'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: colors.error,
              foregroundColor: colors.onError,
            ),
            onPressed: _confirm,
            child: const Text('Destructive'),
          ),
          const FilledButton(onPressed: null, child: Text('Disabled')),
          FilledButton.icon(
            key: const Key('gallery-loading-button'),
            style: FilledButton.styleFrom(
              disabledBackgroundColor: colors.primary,
              disabledForegroundColor: colors.onPrimary,
            ),
            onPressed: null,
            icon: SizedBox.square(
              dimension: 14,
              child: CircularProgressIndicator(
                value: MediaQuery.disableAnimationsOf(context) ? 0.7 : null,
                strokeWidth: 2,
                color: colors.onPrimary,
                semanticsLabel: 'Loading preview',
              ),
            ),
            label: const Text('Loading…'),
          ),
          IconButton(
            tooltip: 'Add sample item',
            onPressed: () => _notify('Add action clicked'),
            icon: const Icon(Icons.add_rounded, size: 18),
          ),
        ],
      ),
    );
  }

  Widget _inputs() {
    final theme = Theme.of(context);
    return _GallerySection(
      icon: Icons.text_fields_rounded,
      title: 'Inputs and selects',
      description: 'Editable, invalid, read-only and disabled fields.',
      child: _DemoGrid(
        children: [
          _Example(
            label: 'Default input',
            child: TextField(
              key: const Key('gallery-name'),
              controller: _name,
              decoration: const InputDecoration(
                hintText: 'Display name',
                helperText: 'An editable preview value.',
              ),
            ),
          ),
          _Example(
            label: 'Validation error',
            child: TextFormField(
              key: const Key('gallery-email'),
              controller: _email,
              autovalidateMode: AutovalidateMode.always,
              validator: (value) =>
                  value != null &&
                      RegExp(
                        r'^[^@\s]+@[^@\s]+\.[^@\s]+$',
                      ).hasMatch(value.trim())
                  ? null
                  : 'Please enter a valid email address.',
              decoration: const InputDecoration(
                hintText: 'Email address',
                helperText: 'Enter an email to clear the error.',
                errorMaxLines: 3,
                helperMaxLines: 3,
              ),
            ),
          ),
          _Example(
            label: 'Workspace role',
            child: Semantics(
              label: 'Workspace role',
              child: DropdownMenu<String>(
                key: const Key('gallery-role'),
                expandedInsets: EdgeInsets.zero,
                inputDecorationTheme:
                    theme.dropdownMenuTheme.inputDecorationTheme,
                initialSelection: _role,
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
                onSelected: (role) {
                  if (role != null) setState(() => _role = role);
                },
                dropdownMenuEntries: [
                  for (final role in const ['Designer', 'Developer', 'Viewer'])
                    DropdownMenuEntry(
                      value: role,
                      label: role,
                      trailingIcon: role == _role
                          ? const Icon(Icons.check_rounded, size: 16)
                          : const SizedBox.square(dimension: 16),
                    ),
                ],
              ),
            ),
          ),
          const _Example(
            label: 'Read-only input',
            child: _ReadOnlyPreview(value: 'Read-only value'),
          ),
          const _Example(
            label: 'Disabled input',
            child: _ReadOnlyPreview(value: 'Unavailable', enabled: false),
          ),
          _Example(
            label: 'Disabled select',
            child: DropdownMenu<String>(
              enabled: false,
              expandedInsets: EdgeInsets.zero,
              inputDecorationTheme:
                  theme.dropdownMenuTheme.inputDecorationTheme,
              initialSelection: 'Viewer',
              selectOnly: true,
              dropdownMenuEntries: const [
                DropdownMenuEntry(value: 'Viewer', label: 'Viewer'),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _selection() => _GallerySection(
    icon: Icons.check_box_outlined,
    title: 'Selection controls',
    description:
        'Try checked, mixed, selected, disabled and continuous values.',
    child: _DemoGrid(
      children: [
        Column(
          children: [
            CheckboxListTile(
              key: const Key('gallery-checkbox'),
              contentPadding: EdgeInsets.zero,
              controlAffinity: ListTileControlAffinity.leading,
              title: const Text('Email notifications'),
              value: _checked,
              onChanged: (value) => setState(() => _checked = value ?? false),
            ),
            CheckboxListTile(
              contentPadding: EdgeInsets.zero,
              controlAffinity: ListTileControlAffinity.leading,
              title: const Text('Mixed selection'),
              subtitle: const Text('Click to cycle through three states.'),
              tristate: true,
              value: _mixed,
              onChanged: (value) => setState(() => _mixed = value),
            ),
            const CheckboxListTile(
              contentPadding: EdgeInsets.zero,
              controlAffinity: ListTileControlAffinity.leading,
              title: Text('Disabled checkbox'),
              value: true,
              onChanged: null,
            ),
            SwitchListTile(
              key: const Key('gallery-switch'),
              contentPadding: EdgeInsets.zero,
              title: const Text('Live preview'),
              value: _enabled,
              onChanged: (value) => setState(() => _enabled = value),
            ),
            const SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: Text('Disabled switch'),
              value: false,
              onChanged: null,
            ),
          ],
        ),
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            RadioGroup<String>(
              groupValue: _density,
              onChanged: (value) {
                if (value != null) setState(() => _density = value);
              },
              child: const Column(
                children: [
                  RadioListTile<String>(
                    contentPadding: EdgeInsets.zero,
                    title: Text('Comfortable'),
                    value: 'Comfortable',
                  ),
                  RadioListTile<String>(
                    contentPadding: EdgeInsets.zero,
                    title: Text('Compact'),
                    value: 'Compact',
                  ),
                  RadioListTile<String>(
                    contentPadding: EdgeInsets.zero,
                    title: Text('Disabled radio'),
                    value: 'Disabled',
                    enabled: false,
                  ),
                ],
              ),
            ),
            const SizedBox(height: 16),
            Text('Intensity: ${(_progress * 100).round()}%'),
            Overlay.wrap(
              alwaysSizeToContent: true,
              clipBehavior: Clip.none,
              child: Slider(
                key: const Key('gallery-slider'),
                value: _progress,
                divisions: 20,
                label: '${(_progress * 100).round()}%',
                onChanged: (value) => setState(() => _progress = value),
              ),
            ),
            Overlay.wrap(
              alwaysSizeToContent: true,
              clipBehavior: Clip.none,
              child: const Slider(value: 0.35, onChanged: null),
            ),
          ],
        ),
      ],
    ),
  );

  Widget _feedback() {
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    final warning = theme.brightness == Brightness.dark
        ? const Color(0xFFFACC15)
        : const Color(0xFFA16207);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _GallerySection(
          icon: Icons.notifications_none_rounded,
          title: 'Badges and alerts',
          description: 'Neutral, success, warning and error feedback.',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  const AppBadge(label: 'Default'),
                  AppBadge(label: 'Success', statusColor: colors.tertiary),
                  AppBadge(label: 'Warning', statusColor: warning),
                  AppBadge(label: 'Error', statusColor: colors.error),
                ],
              ),
              const SizedBox(height: 20),
              _DemoGrid(
                children: [
                  _PreviewAlert(
                    icon: Icons.info_outline_rounded,
                    title: 'Information',
                    message: 'Use this state to offer helpful context.',
                    color: colors.onSurfaceVariant,
                  ),
                  _PreviewAlert(
                    icon: Icons.check_circle_outline_rounded,
                    title: 'Success',
                    message: 'Confirm a completed action with clear feedback.',
                    color: colors.tertiary,
                  ),
                  _PreviewAlert(
                    icon: Icons.warning_amber_rounded,
                    title: 'Warning',
                    message: 'Explain what needs attention before continuing.',
                    color: warning,
                  ),
                  _PreviewAlert(
                    icon: Icons.error_outline_rounded,
                    title: 'Error',
                    message: 'Describe the problem and a useful next step.',
                    color: colors.error,
                  ),
                ],
              ),
            ],
          ),
        ),
        const SizedBox(height: 20),
        _GallerySection(
          icon: Icons.hourglass_bottom_rounded,
          title: 'Progress and notifications',
          description:
              'Determinate progress, loading and temporary confirmation.',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text('${(_progress * 100).round()}% complete'),
              const SizedBox(height: 12),
              LinearProgressIndicator(
                value: _progress,
                minHeight: 5,
                borderRadius: BorderRadius.circular(4),
              ),
              const SizedBox(height: 20),
              LinearProgressIndicator(
                value: MediaQuery.disableAnimationsOf(context) ? 0.7 : null,
                minHeight: 5,
                borderRadius: BorderRadius.circular(4),
                semanticsLabel: 'Loading preview',
              ),
              const SizedBox(height: 20),
              Align(
                alignment: Alignment.centerLeft,
                child: OutlinedButton.icon(
                  key: const Key('gallery-toast'),
                  onPressed: () => _notify('This is a preview notification.'),
                  icon: const Icon(Icons.notifications_none_rounded, size: 16),
                  label: const Text('Show notification'),
                ),
              ),
            ],
          ),
        ),
        const SizedBox(height: 20),
        _GallerySection(
          icon: Icons.inbox_outlined,
          title: 'Empty and populated states',
          description: 'Add and clear a sample item to compare both states.',
          child: Column(
            children: [
              Icon(
                _hasItem ? Icons.task_alt_rounded : Icons.inbox_outlined,
                size: 32,
                color: colors.onSurfaceVariant,
              ),
              const SizedBox(height: 12),
              Text(
                _hasItem ? 'Sample item added' : 'No items yet',
                style: theme.textTheme.titleMedium,
              ),
              const SizedBox(height: 16),
              OutlinedButton(
                key: const Key('gallery-empty-action'),
                onPressed: () => setState(() => _hasItem = !_hasItem),
                child: Text(_hasItem ? 'Clear sample item' : 'Add sample item'),
              ),
            ],
          ),
        ),
      ],
    );
  }

  Widget _overlays() => _GallerySection(
    icon: Icons.open_in_new_rounded,
    title: 'Dialogs, menus and dates',
    description: 'Open an overlay, choose an action or select a sample date.',
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Wrap(
          spacing: 12,
          runSpacing: 12,
          children: [
            OutlinedButton(
              key: const Key('gallery-dialog'),
              onPressed: _confirm,
              child: const Text('Open dialog'),
            ),
            MenuAnchor(
              menuChildren: [
                for (final action in const ['Edit draft', 'Duplicate sample'])
                  MenuItemButton(
                    onPressed: () => setState(() => _lastAction = action),
                    child: Text(action),
                  ),
                const Divider(),
                const MenuItemButton(
                  onPressed: null,
                  child: Text('Unavailable action'),
                ),
              ],
              builder: (context, controller, _) => OutlinedButton.icon(
                key: const Key('gallery-menu'),
                onPressed: () =>
                    controller.isOpen ? controller.close() : controller.open(),
                icon: const Icon(Icons.more_horiz_rounded, size: 16),
                label: const Text('Action menu'),
              ),
            ),
            MenuAnchor(
              controller: _popoverController,
              menuChildren: [
                CallbackShortcuts(
                  bindings: {
                    const SingleActivator(LogicalKeyboardKey.escape):
                        _popoverController.close,
                  },
                  child: Focus(
                    autofocus: true,
                    child: SizedBox(
                      width: 250,
                      child: Padding(
                        padding: const EdgeInsets.all(12),
                        child: StatefulBuilder(
                          builder: (context, updatePopover) => Column(
                            crossAxisAlignment: CrossAxisAlignment.stretch,
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              Text(
                                'Display options',
                                style: Theme.of(context).textTheme.titleMedium,
                              ),
                              const SizedBox(height: 8),
                              CheckboxListTile(
                                contentPadding: EdgeInsets.zero,
                                title: const Text('Show details'),
                                value: _showDetails,
                                onChanged: (value) {
                                  setState(() => _showDetails = value ?? false);
                                  updatePopover(() {});
                                },
                              ),
                            ],
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
              ],
              builder: (context, controller, _) => OutlinedButton(
                key: const Key('gallery-popover'),
                onPressed: () =>
                    controller.isOpen ? controller.close() : controller.open(),
                child: const Text('Open popover'),
              ),
            ),
            OutlinedButton.icon(
              key: const Key('gallery-date'),
              onPressed: _pickDate,
              icon: const Icon(Icons.calendar_today_outlined, size: 16),
              label: const Text('Choose date'),
            ),
            IconButton(
              tooltip: 'Helpful context for this preview',
              onPressed: () => _notify('Tooltip action clicked'),
              icon: const Icon(Icons.help_outline_rounded, size: 20),
            ),
          ],
        ),
        const SizedBox(height: 24),
        Text(_lastAction, key: const Key('gallery-menu-result')),
        const SizedBox(height: 8),
        Text(
          _date == null
              ? 'No date selected'
              : 'Selected date: ${MaterialLocalizations.of(context).formatMediumDate(_date!)}',
          key: const Key('gallery-date-result'),
        ),
      ],
    ),
  );

  Widget _data() {
    final rows = [..._members]
      ..sort(
        (a, b) =>
            _ascending ? a.name.compareTo(b.name) : b.name.compareTo(a.name),
      );
    return _GallerySection(
      icon: Icons.table_chart_outlined,
      title: 'Team members',
      description:
          'Sample rows with selection, sorting, badges and an empty state.',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Wrap(
            spacing: 12,
            runSpacing: 12,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              AppBadge(label: '${_selectedMembers.length} selected'),
              OutlinedButton(
                key: const Key('gallery-table-state'),
                onPressed: () => setState(() {
                  _emptyTable = !_emptyTable;
                  _selectedMembers.clear();
                }),
                child: Text(
                  _emptyTable ? 'Restore sample rows' : 'Show empty state',
                ),
              ),
            ],
          ),
          const SizedBox(height: 20),
          if (_emptyTable)
            const Padding(
              padding: EdgeInsets.symmetric(vertical: 32),
              child: Center(child: Text('No members to show')),
            )
          else
            LayoutBuilder(
              builder: (context, constraints) => SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: ConstrainedBox(
                  constraints: BoxConstraints(minWidth: constraints.maxWidth),
                  child: DataTable(
                    key: const Key('gallery-table'),
                    sortColumnIndex: 0,
                    sortAscending: _ascending,
                    onSelectAll: (selected) => setState(() {
                      _selectedMembers.clear();
                      if (selected == true) {
                        _selectedMembers.addAll(rows.map((row) => row.name));
                      }
                    }),
                    columns: [
                      DataColumn(
                        label: const Text('Member'),
                        onSort: (_, ascending) =>
                            setState(() => _ascending = ascending),
                      ),
                      const DataColumn(label: Text('Role')),
                      const DataColumn(label: Text('Status')),
                    ],
                    rows: [
                      for (final row in rows)
                        DataRow(
                          selected: _selectedMembers.contains(row.name),
                          onSelectChanged: (selected) => setState(() {
                            if (selected == true) {
                              _selectedMembers.add(row.name);
                            } else {
                              _selectedMembers.remove(row.name);
                            }
                          }),
                          cells: [
                            DataCell(Text(row.name)),
                            DataCell(Text(row.role)),
                            DataCell(
                              AppBadge(
                                label: row.status,
                                statusColor: row.status == 'Active'
                                    ? Theme.of(context).colorScheme.tertiary
                                    : null,
                              ),
                            ),
                          ],
                        ),
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

class _ReadOnlyPreview extends StatelessWidget {
  const _ReadOnlyPreview({required this.value, this.enabled = true});

  final String value;
  final bool enabled;

  @override
  Widget build(BuildContext context) =>
      TextFormField(initialValue: value, enabled: enabled, readOnly: true);
}

class _GallerySection extends StatelessWidget {
  const _GallerySection({
    required this.icon,
    required this.title,
    required this.description,
    required this.child,
  });

  final IconData icon;
  final String title;
  final String description;
  final Widget child;

  @override
  Widget build(BuildContext context) => Section(
    padding: 0,
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        SectionHeading(icon: icon, title: title, description: description),
        const Divider(),
        Material(
          type: MaterialType.transparency,
          child: Padding(padding: const EdgeInsets.all(20), child: child),
        ),
      ],
    ),
  );
}

class _Example extends StatelessWidget {
  const _Example({required this.label, required this.child});

  final String label;
  final Widget child;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      Text(label, style: Theme.of(context).textTheme.labelLarge),
      const SizedBox(height: 8),
      child,
    ],
  );
}

class _DemoGrid extends StatelessWidget {
  const _DemoGrid({required this.children});

  final List<Widget> children;

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final minWidth = 240 * MediaQuery.textScalerOf(context).scale(14) / 14;
      final width = constraints.maxWidth >= minWidth * 2 + 20
          ? (constraints.maxWidth - 20) / 2
          : constraints.maxWidth;
      return Wrap(
        spacing: 20,
        runSpacing: 20,
        children: [
          for (final child in children) SizedBox(width: width, child: child),
        ],
      );
    },
  );
}

class _PreviewAlert extends StatelessWidget {
  const _PreviewAlert({
    required this.icon,
    required this.title,
    required this.message,
    required this.color,
  });

  final IconData icon;
  final String title;
  final String message;
  final Color color;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return DecoratedBox(
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.06),
        border: Border.all(color: color.withValues(alpha: 0.3)),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(icon, color: color, size: 18),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title, style: theme.textTheme.titleMedium),
                  const SizedBox(height: 4),
                  Text(message, style: theme.textTheme.bodySmall),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
