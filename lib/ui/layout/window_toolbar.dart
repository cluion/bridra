import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import '../window/desktop_window.dart';

class WindowToolbar extends StatelessWidget {
  const WindowToolbar({
    super.key,
    required this.pageTitle,
    required this.onBack,
    required this.onForward,
    required this.sidebarExpanded,
    required this.sidebarWidth,
    required this.animationDuration,
    required this.onSidebarToggled,
    this.desktopWindowControls = false,
  });

  final String pageTitle;
  final VoidCallback? onBack;
  final VoidCallback? onForward;
  final bool sidebarExpanded;
  final double sidebarWidth;
  final Duration animationDuration;
  final VoidCallback onSidebarToggled;
  final bool desktopWindowControls;

  @override
  Widget build(BuildContext context) {
    final mac = !kIsWeb && defaultTargetPlatform == TargetPlatform.macOS;
    final custom = desktopWindowControls && supportsCustomWindowChrome;
    return ColoredBox(
      key: const Key('window-toolbar'),
      color:
          Theme.of(context).navigationRailTheme.backgroundColor ??
          Theme.of(context).colorScheme.surface,
      child: LayoutBuilder(
        builder: (context, constraints) {
          final compact = custom && constraints.maxWidth < 480;
          final leading = mac
              ? 88.0
              : compact
              ? 12.0
              : 24.0;
          final buttonSize = mac || compact ? 36.0 : 44.0;
          final trailing = custom ? 8.0 : 12.0;
          final pageGap = compact ? 8.0 : 16.0;
          final pageInset = sidebarExpanded && sidebarWidth > 0
              ? (sidebarWidth - leading - 3 * buttonSize)
                    .clamp(pageGap, double.infinity)
                    .toDouble()
              : pageGap;
          return SizedBox(
            key: const Key('toolbar-row'),
            height: mac ? 56 : 60,
            child: Padding(
              padding: EdgeInsets.only(left: leading, right: trailing),
              child: Row(
                children: [
                  _ToolbarButton(
                    key: const Key('navigation-back'),
                    size: buttonSize,
                    label: 'Back',
                    icon: Icons.chevron_left_rounded,
                    onPressed: onBack,
                  ),
                  _ToolbarButton(
                    key: const Key('navigation-forward'),
                    size: buttonSize,
                    label: 'Forward',
                    icon: Icons.chevron_right_rounded,
                    onPressed: onForward,
                  ),
                  _ToolbarButton(
                    key: const Key('sidebar-toggle'),
                    size: buttonSize,
                    label: sidebarExpanded
                        ? 'Collapse sidebar'
                        : 'Expand sidebar',
                    icon: Icons.view_sidebar_outlined,
                    onPressed: onSidebarToggled,
                  ),
                  Expanded(
                    child: AnimatedPadding(
                      // Reset the title inset when navigation becomes an overlay.
                      key: ValueKey(sidebarWidth > 0),
                      duration: animationDuration,
                      curve: Curves.easeInOutCubic,
                      padding: EdgeInsets.only(left: pageInset),
                      child: WindowDragRegion(
                        key: const Key('toolbar-drag-region'),
                        enabled: custom,
                        child: SizedBox(
                          height: double.infinity,
                          child: Row(
                            children: [
                              SizedBox(
                                key: const Key('toolbar-page-divider'),
                                width: 1,
                                height: 20,
                                child: ColoredBox(
                                  color: Theme.of(context).dividerColor,
                                ),
                              ),
                              SizedBox(width: pageGap),
                              Expanded(
                                child: Tooltip(
                                  message: pageTitle,
                                  child: Text(
                                    pageTitle,
                                    key: const Key('toolbar-page-title'),
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: Theme.of(context)
                                        .textTheme
                                        .titleMedium
                                        ?.copyWith(fontWeight: FontWeight.w600),
                                  ),
                                ),
                              ),
                            ],
                          ),
                        ),
                      ),
                    ),
                  ),
                  if (custom) ...[
                    const SizedBox(width: 8),
                    DesktopWindowButtons(buttonSize: buttonSize),
                  ],
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}

class _ToolbarButton extends StatelessWidget {
  const _ToolbarButton({
    super.key,
    required this.label,
    required this.icon,
    required this.onPressed,
    required this.size,
  });

  final String label;
  final IconData icon;
  final VoidCallback? onPressed;
  final double size;

  @override
  Widget build(BuildContext context) => IconButton(
    tooltip: label,
    onPressed: onPressed,
    padding: const EdgeInsets.all(8),
    constraints: BoxConstraints.tightFor(width: size, height: size),
    style: IconButton.styleFrom(
      minimumSize: Size(size, size),
      maximumSize: Size(size, size),
      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
    ),
    icon: Icon(icon, size: 20, semanticLabel: label),
  );
}
