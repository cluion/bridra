import 'package:flutter/material.dart';

class Section extends StatelessWidget {
  const Section({super.key, required this.child, this.padding = 24});

  final Widget child;
  final double padding;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return DecoratedBox(
      decoration: BoxDecoration(
        color: colors.surface.withValues(alpha: 0.7),
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: colors.outlineVariant),
      ),
      child: Padding(padding: EdgeInsets.all(padding), child: child),
    );
  }
}
