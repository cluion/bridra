import 'dart:async';

import 'package:flutter/material.dart';

import '../api/backend_gateway.dart';
import '../features/guide/guide_view.dart';
import '../features/runtime/runtime_view.dart';
import '../features/settings/settings_view.dart';
import '../features/workspace/workspace_view.dart';
import '../features/workspace/workspace_view_model.dart';
import '../ui/layout/app_shell.dart';
import '../ui/theme/app_theme.dart';
import '../ui/window/desktop_window.dart';

class StarterApp extends StatefulWidget {
  const StarterApp({
    super.key,
    this.connector = RpcBackend.connect,
    this.productName = 'Bridra',
    this.platformSummary = 'One interface · six platforms',
    this.desktopWindowControls = false,
  });

  final BackendConnector connector;
  final String productName;
  final String platformSummary;
  final bool desktopWindowControls;

  @override
  State<StarterApp> createState() => _StarterAppState();
}

class _StarterAppState extends State<StarterApp> {
  late WorkspaceViewModel _workspace;
  final _history = <AppDestination>[AppDestination.workspace];
  var _historyIndex = 0;
  AppDestination get _destination => _history[_historyIndex];
  var _themeMode = ThemeMode.system;
  bool? _sidebarCollapsed;

  void _navigate(AppDestination destination) {
    if (destination == _destination) return;
    setState(() {
      _history.removeRange(_historyIndex + 1, _history.length);
      _history.add(destination);
      if (_history.length > 100) _history.removeAt(0);
      _historyIndex = _history.length - 1;
    });
  }

  void _goBack() {
    if (_historyIndex == 0) return;
    setState(() => _historyIndex--);
  }

  void _goForward() {
    if (_historyIndex >= _history.length - 1) return;
    setState(() => _historyIndex++);
  }

  @override
  void initState() {
    super.initState();
    _workspace = WorkspaceViewModel(connector: widget.connector);
    unawaited(_workspace.connect());
  }

  @override
  void didUpdateWidget(covariant StarterApp oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.connector != widget.connector) {
      _workspace.dispose();
      _workspace = WorkspaceViewModel(connector: widget.connector);
      unawaited(_workspace.connect());
    }
  }

  @override
  void dispose() {
    _workspace.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: widget.productName,
      debugShowCheckedModeBanner: false,
      theme: AppTheme.light(),
      darkTheme: AppTheme.dark(),
      themeMode: _themeMode,
      builder: (context, child) => DesktopWindowFrame(
        enabled: widget.desktopWindowControls,
        child: child!,
      ),
      home: ListenableBuilder(
        listenable: _workspace,
        builder: (context, _) => AppShell(
          desktopWindowControls: widget.desktopWindowControls,
          productName: widget.productName,
          destination: _destination,
          onDestinationChanged: _navigate,
          onBack: _historyIndex > 0 ? _goBack : null,
          onForward: _historyIndex < _history.length - 1 ? _goForward : null,
          sidebarCollapsed: _sidebarCollapsed,
          onSidebarCollapsedChanged: (value) =>
              setState(() => _sidebarCollapsed = value),
          child: IndexedStack(
            index: _destination.index,
            children: [
              WorkspaceView(model: _workspace),
              RuntimeView(model: _workspace),
              GuideView(platformSummary: widget.platformSummary),
              SettingsView(
                themeMode: _themeMode,
                onThemeChanged: (value) => setState(() => _themeMode = value),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
