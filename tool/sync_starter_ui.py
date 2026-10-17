#!/usr/bin/env python3
"""Sync reference UI into embedded templates without touching consumer projects."""

import argparse
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
TEMPLATES = ROOT / "backend/projecttemplate/templates"
UI_FILES = {
    "app/starter_app.dart": "starter_app.dart.tmpl",
    "ui/theme/app_theme.dart": "app_theme.dart.tmpl",
    "ui/layout/app_shell.dart": "app_shell.dart.tmpl",
    "ui/layout/window_toolbar.dart": "window_toolbar.dart.tmpl",
    "ui/window/desktop_window.dart": "desktop_window.dart.tmpl",
    "ui/widgets/section.dart": "section.dart.tmpl",
    "ui/widgets/app_toast.dart": "app_toast.dart.tmpl",
    "features/workspace/workspace_view_model.dart": "workspace_view_model.dart.tmpl",
    "features/workspace/workspace_view.dart": "workspace_view.dart.tmpl",
    "features/runtime/runtime_view.dart": "runtime_view.dart.tmpl",
    "features/settings/settings_view.dart": "settings_view.dart.tmpl",
    "features/guide/guide_view.dart": "guide_view.dart.tmpl",
}
TEST_FILES = {
    "integration_test/http_smoke_test.dart": "http_smoke_test.dart.tmpl",
    "test/bridra_app_test.dart": "widget_test.dart.tmpl",
    "test/workspace_view_model_test.dart": "workspace_view_model_test.dart.tmpl",
    "test/app_theme_test.dart": "app_theme_test.dart.tmpl",
    "test/app_toast_test.dart": "app_toast_test.dart.tmpl",
    "test/desktop_window_test.dart": "desktop_window_test.dart.tmpl",
    "test/support/fake_backend.dart": "fake_backend.dart.tmpl",
}

DESKTOP_WINDOW_STUB = """import 'package:flutter/widgets.dart';

bool get supportsCustomWindowChrome => false;

Future<bool> initializeDesktopWindow() async => false;

class DesktopWindowFrame extends StatelessWidget {
  const DesktopWindowFrame({super.key, required this.enabled, required this.child});

  final bool enabled;
  final Widget child;

  @override
  Widget build(BuildContext context) => child;
}

class WindowDragRegion extends StatelessWidget {
  const WindowDragRegion({super.key, required this.enabled, required this.child});

  final bool enabled;
  final Widget child;

  @override
  Widget build(BuildContext context) => child;
}

class DesktopWindowButtons extends StatelessWidget {
  const DesktopWindowButtons({super.key, required this.buttonSize});

  final double buttonSize;

  @override
  Widget build(BuildContext context) => const SizedBox.shrink();
}
"""


def runtime_import(source):
    return source.replace(
        "import 'package:bridra_flutter/bridra_flutter.dart';",
        "import {{quote .BridraDartImport}};",
    )


def ui_template(relative):
    source = runtime_import((ROOT / "lib" / relative).read_text(encoding="utf-8"))
    if relative == "app/starter_app.dart":
        product = "this.productName = 'Bridra'"
        platforms = "'One interface · six platforms'"
        if product not in source or platforms not in source:
            raise ValueError("Update the app configuration mappings after changing defaults")
        source = source.replace(product, "this.productName = {{dartString .DisplayName}}")
        source = source.replace(platforms, "'One interface · {{.PlatformSummary}}'")
    if relative == "ui/window/desktop_window.dart":
        # Non-desktop consumers keep the same UI interface without a desktop plugin.
        source = (
            '{{if or (hasPlatform "windows") (hasPlatform "linux")}}'
            + source + "{{else}}" + DESKTOP_WINDOW_STUB + "{{end}}"
        )
    return source


def test_template(relative):
    source = runtime_import((ROOT / relative).read_text(encoding="utf-8"))
    source = source.replace("package:bridra/", "package:{{.ProjectName}}/")
    source = source.replace(
        "import 'package:{{.ProjectName}}/bridra_app.dart';",
        "import 'package:{{.ProjectName}}/main.dart';",
    )
    source = source.replace("BridraApp(", "StarterApp(")
    source = source.replace("find.text('Bridra')", "find.text({{dartString .DisplayName}})")
    source = source.replace("import 'repository_version.dart';\n", "")
    source = source.replace("import '../repository_version.dart';\n", "")
    source = source.replace("repositoryFrameworkVersion", "'{{.FrameworkVersion}}'")
    # Generated tests exercise transport loss through the public, portable API.
    source = source.replace(
        "import 'package:bridra_flutter/bridra_flutter_sidecar.dart';\n", ""
    )
    source = source.replace("const SidecarExitedException(9)", "const BackendClosedException()")
    if relative == "test/support/fake_backend.dart":
        source = source.replace("protocolVersion: 1,", "protocolVersion: {{.ProtocolVersion}},")
    return source


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="Report drift without writing files")
    arguments = parser.parse_args()
    outputs = {name: ui_template(path) for path, name in UI_FILES.items()}
    outputs.update({name: test_template(path) for path, name in TEST_FILES.items()})
    stale = [
        name for name, contents in outputs.items()
        if not (TEMPLATES / name).exists() or (TEMPLATES / name).read_text(encoding="utf-8") != contents
    ]
    if arguments.check:
        if stale:
            print("Starter UI templates differ: " + ", ".join(stale))
            print("Run: python3 tool/sync_starter_ui.py")
            return 1
        print("Starter UI templates match the reference application and tests.")
        return 0
    for name in stale:
        (TEMPLATES / name).write_text(outputs[name], encoding="utf-8")
    print(f"Updated {len(stale)} starter UI templates.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
