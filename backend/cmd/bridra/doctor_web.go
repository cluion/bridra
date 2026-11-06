package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cluion/bridra/backend/internal/projectplatform"
	"github.com/cluion/bridra/backend/internal/webdesktop"
)

func (item doctorCommand) checkWebHost(platforms []string, report *doctorReport) {
	target, err := hostDesktopDevice(item.system.goos)
	if err != nil || !projectplatform.Contains(platforms, target) {
		return
	}
	switch target {
	case projectplatform.MacOS:
		item.checkWebTool(report, "codesign")
		if item.checkWebTool(report, "xcrun") {
			item.checkWebCommand(report, "Swift compiler", "xcrun", "--sdk", "macosx", "--find", "swiftc")
			item.checkWebCommand(report, "macOS SDK", "xcrun", "--sdk", "macosx", "--show-sdk-path")
		}
	case projectplatform.Linux:
		item.checkWebTool(report, "cc")
		if item.checkWebTool(report, "pkg-config") {
			item.checkWebCommand(report, "GTK/WebKit development packages", "pkg-config", "--print-errors", "--exists", "gtk4", "webkitgtk-6.0", "libsoup-3.0")
		}
	case projectplatform.Windows:
		cl := item.checkWebTool(report, "cl.exe")
		sdk, err := webdesktop.ResolveWindowsSDK(item.system.goarch)
		if err != nil {
			report.add(doctorFailure, "WebView2 SDK", err.Error())
			return
		}
		report.add(doctorOK, "WebView2 SDK", sdk.Loader)
		if cl {
			item.checkWebMSVC(report, sdk)
		}
	}
}

func (item doctorCommand) checkWebTool(report *doctorReport, tool string) bool {
	path, err := item.system.lookPath(tool)
	if err != nil {
		report.add(doctorFailure, tool, "not found in PATH; required to package the selected desktop Web UI")
		return false
	}
	report.add(doctorOK, tool, path)
	return true
}

func (item doctorCommand) checkWebCommand(report *doctorReport, label, name string, args ...string) {
	output, err := item.execute(name, args...)
	if err != nil {
		report.add(doctorFailure, label, commandFailureDetail(output, err))
		return
	}
	report.add(doctorOK, label, "available")
}

func (item doctorCommand) checkWebMSVC(report *doctorReport, sdk webdesktop.WindowsSDK) {
	// Ask the actual compiler so manually configured MSVC environments work too.
	macro := map[string]string{"x64": "_M_X64", "arm64": "_M_ARM64", "x86": "_M_IX86"}[sdk.Architecture]
	source := fmt.Sprintf("#ifndef %s\n#error Use the %s MSVC Native Tools prompt\n#endif\n", macro, sdk.Architecture) +
		"#include <windows.h>\n#include <WebView2.h>\n#include <winrt/Windows.Data.Json.h>\n"
	tmp, err := os.MkdirTemp("", "bridra-doctor-")
	if err != nil {
		report.add(doctorFailure, "MSVC", err.Error())
		return
	}
	defer os.RemoveAll(tmp)
	path := filepath.Join(tmp, "check.cpp")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		report.add(doctorFailure, "MSVC", err.Error())
		return
	}
	output, err := item.execute("cl.exe", "/nologo", "/std:c++20", "/EHsc", "/Zs", "/I"+sdk.Include, path)
	if err != nil {
		report.add(doctorFailure, "MSVC", fmt.Sprintf("use the %s Native Tools prompt with the Windows SDK and C++/WinRT headers; %s",
			sdk.Architecture, commandFailureDetail(output, err)))
		return
	}
	report.add(doctorOK, "MSVC", strings.ToUpper(sdk.Architecture)+" compiler and Windows SDK headers")
}
