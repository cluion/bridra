import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

class WindowToolbar extends StatelessWidget {
  const WindowToolbar({
    super.key,
    required this.productName,
    required this.pageTitle,
    required this.onBack,
    required this.onForward,
    required this.sidebarExpanded,
    required this.onSidebarToggled,
  });

  final String productName;
  final String pageTitle;
  final VoidCallback? onBack;
  final VoidCallback? onForward;
  final bool sidebarExpanded;
  final VoidCallback onSidebarToggled;

  @override
  Widget build(BuildContext context) {
    final mac = !kIsWeb && defaultTargetPlatform == TargetPlatform.macOS;
    final leading = mac ? 88.0 : 24.0;
    final buttonSize = mac ? 36.0 : 44.0;
    return ColoredBox(
      key: const Key('window-toolbar'),
      color:
          Theme.of(context).navigationRailTheme.backgroundColor ??
          Theme.of(context).colorScheme.surface,
      child: LayoutBuilder(
        builder: (context, constraints) {
          final titleWidth =
              ((constraints.maxWidth - leading - 40 - 3 * buttonSize) * 0.4)
                  .clamp(0.0, 160.0)
                  .toDouble();
          return SizedBox(
            key: const Key('toolbar-row'),
            height: mac ? 56 : 60,
            child: Padding(
              padding: EdgeInsets.only(left: leading, right: 12),
              child: Row(
                children: [
                  ConstrainedBox(
                    constraints: BoxConstraints(maxWidth: titleWidth),
                    child: Tooltip(
                      message: productName,
                      child: Text(
                        productName,
                        key: const Key('toolbar-product-name'),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.titleSmall?.copyWith(
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(width: 12),
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
                  const SizedBox(width: 16),
                  Expanded(
                    child: Tooltip(
                      message: pageTitle,
                      child: Text(
                        pageTitle,
                        key: const Key('toolbar-page-title'),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.titleMedium
                            ?.copyWith(fontWeight: FontWeight.w600),
                      ),
                    ),
                  ),
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
