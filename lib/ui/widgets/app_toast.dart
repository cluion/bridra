import 'dart:async';

import 'package:flutter/material.dart';

class AppToastHost extends StatefulWidget {
  const AppToastHost({super.key, required this.child});

  final Widget child;

  static void show(BuildContext context, String message) {
    final host = context.findAncestorStateOfType<_AppToastHostState>();
    if (host == null) {
      throw FlutterError('AppToastHost is missing above this context.');
    }
    host.show(message);
  }

  @override
  State<AppToastHost> createState() => _AppToastHostState();
}

class _AppToastHostState extends State<AppToastHost>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller;
  late final CurvedAnimation _animation;
  Timer? _timer;
  String? _message;
  var _sequence = 0;

  @override
  void initState() {
    super.initState();
    _controller =
        AnimationController(
          vsync: this,
          duration: const Duration(milliseconds: 220),
          reverseDuration: const Duration(milliseconds: 160),
        )..addStatusListener((status) {
          if (status == AnimationStatus.dismissed &&
              mounted &&
              _message != null) {
            setState(() => _message = null);
          }
        });
    _animation = CurvedAnimation(
      parent: _controller,
      curve: Curves.easeOutCubic,
      reverseCurve: Curves.easeInCubic,
    );
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final reducedMotion = MediaQuery.disableAnimationsOf(context);
    _controller.duration = reducedMotion
        ? Duration.zero
        : const Duration(milliseconds: 220);
    _controller.reverseDuration = reducedMotion
        ? Duration.zero
        : const Duration(milliseconds: 160);
    if (reducedMotion && _controller.isAnimating) {
      _controller.value = _controller.status == AnimationStatus.reverse ? 0 : 1;
    }
  }

  void show(String message) {
    _timer?.cancel();
    setState(() {
      _message = message;
      _sequence++;
    });
    _controller.forward();
    _timer = Timer(const Duration(seconds: 3), dismiss);
  }

  void dismiss() {
    _timer?.cancel();
    _controller.reverse();
  }

  @override
  void dispose() {
    _timer?.cancel();
    _animation.dispose();
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return Stack(
      fit: StackFit.expand,
      children: [
        widget.child,
        if (_message case final message?)
          Positioned.fill(
            child: Padding(
              padding: const EdgeInsets.all(12),
              child: Align(
                alignment: Alignment.topCenter,
                child: ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 420),
                  child: SlideTransition(
                    position: Tween<Offset>(
                      begin: const Offset(0, -0.35),
                      end: Offset.zero,
                    ).animate(_animation),
                    child: FadeTransition(
                      opacity: _animation,
                      child: Semantics(
                        key: ValueKey(_sequence),
                        container: true,
                        liveRegion: true,
                        child: Material(
                          key: const Key('app-toast'),
                          color: colors.surface,
                          surfaceTintColor: Colors.transparent,
                          elevation: 8,
                          shadowColor: Colors.black.withValues(alpha: 0.16),
                          shape: RoundedRectangleBorder(
                            borderRadius: BorderRadius.circular(14),
                            side: BorderSide(color: colors.outlineVariant),
                          ),
                          clipBehavior: Clip.antiAlias,
                          child: Padding(
                            padding: const EdgeInsets.fromLTRB(16, 8, 8, 8),
                            child: Row(
                              children: [
                                ExcludeSemantics(
                                  child: Icon(
                                    Icons.check_circle_outline_rounded,
                                    color: colors.tertiary,
                                    size: 22,
                                  ),
                                ),
                                const SizedBox(width: 12),
                                Expanded(
                                  child: SingleChildScrollView(
                                    child: Text(
                                      message,
                                      style: Theme.of(
                                        context,
                                      ).textTheme.bodyMedium,
                                    ),
                                  ),
                                ),
                                const SizedBox(width: 8),
                                IconButton(
                                  key: const Key('dismiss-toast'),
                                  tooltip: 'Dismiss notification',
                                  onPressed: dismiss,
                                  icon: const Icon(
                                    Icons.close_rounded,
                                    size: 18,
                                    semanticLabel: 'Dismiss notification',
                                  ),
                                ),
                              ],
                            ),
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ),
      ],
    );
  }
}
