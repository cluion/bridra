import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../widgets/app_toast.dart';
import 'window_toolbar.dart';

const _expandedSidebarWidth = 240.0;
const _collapsedSidebarWidth = 64.0;
const _sidebarHorizontalPadding = 8.0;

enum AppDestination {
  workspace('Workspace', Icons.dashboard_outlined, Icons.dashboard_rounded),
  runtime('Runtime', Icons.dns_outlined, Icons.dns_rounded),
  guide('Guide', Icons.menu_book_outlined, Icons.menu_book_rounded),
  settings('Settings', Icons.settings_outlined, Icons.settings_rounded);

  static const primary = [workspace, runtime, guide];

  const AppDestination(this.label, this.icon, this.selectedIcon);

  final String label;
  final IconData icon;
  final IconData selectedIcon;
}

class AppShell extends StatefulWidget {
  const AppShell({
    super.key,
    required this.productName,
    required this.destination,
    required this.onDestinationChanged,
    required this.onBack,
    required this.onForward,
    required this.sidebarCollapsed,
    required this.onSidebarCollapsedChanged,
    required this.child,
  });

  final String productName;
  final AppDestination destination;
  final ValueChanged<AppDestination> onDestinationChanged;
  final VoidCallback? onBack;
  final VoidCallback? onForward;
  final bool? sidebarCollapsed;
  final ValueChanged<bool> onSidebarCollapsedChanged;
  final Widget child;

  @override
  State<AppShell> createState() => _AppShellState();
}

class _AppShellState extends State<AppShell> {
  var _overlayOpen = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final size = MediaQuery.sizeOf(context);
    if (size.width >= 760 && size.height >= 480) _overlayOpen = false;
  }

  void _closeOverlay() {
    if (_overlayOpen) setState(() => _overlayOpen = false);
  }

  void _navigate(AppDestination destination) {
    _closeOverlay();
    widget.onDestinationChanged(destination);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return LayoutBuilder(
      builder: (context, constraints) {
        final sidebar =
            constraints.maxWidth >= 760 && constraints.maxHeight >= 480;
        final extended =
            sidebar &&
            !(widget.sidebarCollapsed ?? constraints.maxWidth < 1100);
        final overlayShown = !sidebar && _overlayOpen;
        final animationDuration = MediaQuery.disableAnimationsOf(context)
            ? Duration.zero
            : const Duration(milliseconds: 220);
        Widget navigation({required bool expanded}) => _Sidebar(
          extended: expanded,
          destination: widget.destination,
          onDestinationChanged: _navigate,
          animationDuration: animationDuration,
        );
        return CallbackShortcuts(
          bindings: {
            if (overlayShown)
              const SingleActivator(LogicalKeyboardKey.escape): _closeOverlay,
          },
          child: Scaffold(
            body: ColoredBox(
              color:
                  theme.navigationRailTheme.backgroundColor ??
                  theme.scaffoldBackgroundColor,
              child: SafeArea(
                child: Column(
                  children: [
                    WindowToolbar(
                      productName: widget.productName,
                      pageTitle: widget.destination.label,
                      onBack: widget.onBack == null
                          ? null
                          : () {
                              _closeOverlay();
                              widget.onBack!();
                            },
                      onForward: widget.onForward == null
                          ? null
                          : () {
                              _closeOverlay();
                              widget.onForward!();
                            },
                      sidebarExpanded: sidebar ? extended : overlayShown,
                      onSidebarToggled: () {
                        if (sidebar) {
                          widget.onSidebarCollapsedChanged(extended);
                        } else {
                          setState(() => _overlayOpen = !_overlayOpen);
                        }
                      },
                    ),
                    Expanded(
                      child: AppToastHost(
                        child: Stack(
                          children: [
                            ExcludeFocus(
                              excluding: overlayShown,
                              child: ExcludeSemantics(
                                excluding: overlayShown,
                                child: IgnorePointer(
                                  ignoring: overlayShown,
                                  child: Row(
                                    children: [
                                      if (sidebar)
                                        AnimatedContainer(
                                          key: const Key('desktop-navigation'),
                                          width: extended
                                              ? _expandedSidebarWidth
                                              : _collapsedSidebarWidth,
                                          duration: animationDuration,
                                          curve: Curves.easeInOutCubic,
                                          child: ClipRect(
                                            child: navigation(
                                              expanded: extended,
                                            ),
                                          ),
                                        ),
                                      Expanded(
                                        key: const Key('app-content'),
                                        child: Card(
                                          key: const Key('main-content-card'),
                                          elevation: 0,
                                          color: theme.scaffoldBackgroundColor,
                                          surfaceTintColor: Colors.transparent,
                                          margin: EdgeInsets.fromLTRB(
                                            sidebar ? 0 : 12,
                                            0,
                                            12,
                                            12,
                                          ),
                                          shape: RoundedRectangleBorder(
                                            borderRadius: BorderRadius.circular(
                                              20,
                                            ),
                                          ),
                                          clipBehavior: Clip.antiAlias,
                                          child: Align(
                                            alignment: Alignment.topCenter,
                                            child: ConstrainedBox(
                                              constraints: const BoxConstraints(
                                                maxWidth: 1200,
                                              ),
                                              child: widget.child,
                                            ),
                                          ),
                                        ),
                                      ),
                                    ],
                                  ),
                                ),
                              ),
                            ),
                            AnimatedSwitcher(
                              duration: animationDuration,
                              layoutBuilder: (current, previous) => Stack(
                                children: [
                                  for (final child in previous)
                                    ExcludeFocus(
                                      child: IgnorePointer(
                                        child: ExcludeSemantics(child: child),
                                      ),
                                    ),
                                  ?current,
                                ],
                              ),
                              child: overlayShown
                                  ? Stack(
                                      key: const Key('navigation-overlay'),
                                      children: [
                                        Semantics(
                                          container: true,
                                          label: 'Close navigation',
                                          button: true,
                                          onTap: _closeOverlay,
                                          child: GestureDetector(
                                            onTap: _closeOverlay,
                                            excludeFromSemantics: true,
                                            child: const ColoredBox(
                                              color: Colors.black38,
                                              child: SizedBox.expand(),
                                            ),
                                          ),
                                        ),
                                        Align(
                                          alignment: Alignment.centerLeft,
                                          child: SizedBox(
                                            width: (constraints.maxWidth - 40)
                                                .clamp(
                                                  0.0,
                                                  _expandedSidebarWidth,
                                                ),
                                            child: FocusScope(
                                              autofocus: true,
                                              child: navigation(expanded: true),
                                            ),
                                          ),
                                        ),
                                      ],
                                    )
                                  : const SizedBox.shrink(),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ),
            bottomNavigationBar: sidebar
                ? null
                : NavigationBar(
                    key: const Key('mobile-navigation'),
                    selectedIndex: widget.destination.index,
                    onDestinationSelected: (index) =>
                        _navigate(AppDestination.values[index]),
                    destinations: [
                      for (final item in AppDestination.values)
                        NavigationDestination(
                          icon: Icon(item.icon),
                          selectedIcon: Icon(item.selectedIcon),
                          label: item.label,
                        ),
                    ],
                  ),
          ),
        );
      },
    );
  }
}

class _Sidebar extends StatelessWidget {
  const _Sidebar({
    required this.extended,
    required this.destination,
    required this.onDestinationChanged,
    required this.animationDuration,
  });

  final bool extended;
  final AppDestination destination;
  final ValueChanged<AppDestination> onDestinationChanged;
  final Duration animationDuration;

  @override
  Widget build(BuildContext context) {
    Widget destinationButton(AppDestination item) => _SidebarDestination(
      destination: item,
      selected: item == destination,
      extended: extended,
      animationDuration: animationDuration,
      onPressed: () => onDestinationChanged(item),
    );
    return DecoratedBox(
      key: const Key('sidebar-surface'),
      decoration: BoxDecoration(
        color: Theme.of(context).navigationRailTheme.backgroundColor,
      ),
      child: LayoutBuilder(
        builder: (context, constraints) {
          final short = constraints.maxHeight < 240;
          return Column(
            children: [
              Expanded(
                child: SingleChildScrollView(
                  padding: const EdgeInsets.fromLTRB(
                    _sidebarHorizontalPadding,
                    8,
                    _sidebarHorizontalPadding,
                    0,
                  ),
                  child: Column(
                    children: [
                      for (final item in AppDestination.primary) ...[
                        destinationButton(item),
                        const SizedBox(height: 8),
                      ],
                      if (short) destinationButton(AppDestination.settings),
                    ],
                  ),
                ),
              ),
              if (!short)
                Padding(
                  padding: const EdgeInsets.fromLTRB(
                    _sidebarHorizontalPadding,
                    0,
                    _sidebarHorizontalPadding,
                    8,
                  ),
                  child: destinationButton(AppDestination.settings),
                ),
            ],
          );
        },
      ),
    );
  }
}

class _SidebarDestination extends StatelessWidget {
  const _SidebarDestination({
    required this.destination,
    required this.selected,
    required this.extended,
    required this.animationDuration,
    required this.onPressed,
  });

  final AppDestination destination;
  final bool selected;
  final bool extended;
  final Duration animationDuration;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    final button = Semantics(
      selected: selected,
      child: TextButton(
        key: Key('destination-${destination.name}'),
        onPressed: onPressed,
        style: ButtonStyle(
          minimumSize: const WidgetStatePropertyAll(Size(0, 52)),
          padding: const WidgetStatePropertyAll(
            EdgeInsets.symmetric(vertical: 14),
          ),
          shape: WidgetStatePropertyAll(
            RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
          ),
          foregroundColor: WidgetStatePropertyAll(
            selected ? colors.onSurface : colors.onSurfaceVariant,
          ),
          backgroundColor: WidgetStatePropertyAll(
            selected
                ? theme.navigationRailTheme.indicatorColor
                : Colors.transparent,
          ),
          side: WidgetStateProperty.resolveWith(
            (states) => states.contains(WidgetState.focused)
                ? BorderSide(color: colors.primary)
                : BorderSide.none,
          ),
        ),
        child: SizedBox(
          height: MediaQuery.textScalerOf(
            context,
          ).scale(20).clamp(24.0, 80.0).toDouble(),
          child: ClipRect(
            child: Row(
              children: [
                SizedBox(
                  width: _collapsedSidebarWidth - 2 * _sidebarHorizontalPadding,
                  child: Icon(
                    destination.icon,
                    size: 22,
                    semanticLabel: destination.label,
                  ),
                ),
                Expanded(
                  child: ClipRect(
                    child: OverflowBox(
                      alignment: Alignment.centerLeft,
                      minWidth: 160,
                      maxWidth: 160,
                      child: ExcludeSemantics(
                        child: AnimatedOpacity(
                          opacity: extended ? 1 : 0,
                          duration: animationDuration,
                          child: Padding(
                            padding: const EdgeInsets.only(left: 6),
                            child: Text(
                              destination.label,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: const TextStyle(
                                fontWeight: FontWeight.w500,
                              ),
                            ),
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
    return extended
        ? button
        : Tooltip(message: destination.label, child: button);
  }
}
