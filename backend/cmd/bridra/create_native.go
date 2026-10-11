package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cluion/bridra/backend/internal/projectplatform"
)

func applyNativeDisplayName(root string, options createOptions) error {
	cppName := strconv.Quote(options.displayName)
	// MSVC may decode UTF-8 source using the Windows ANSI codepage.
	windowsName := strconv.QuoteToASCII(options.displayName)
	rcName := "\"" + strings.NewReplacer(`\`, `\\`, `"`, `""`).Replace(options.displayName) + "\""
	xcodeName := strings.NewReplacer("/", "$(BRIDRA_NAME_SLASH)", "$", "$(BRIDRA_NAME_DOLLAR)").Replace(options.displayName)
	patches := []struct {
		platform     string
		path         string
		replacements [][2]string
	}{
		{"windows", "windows/runner/main.cpp", [][2]string{
			{`window.Create(L"` + options.projectName + `"`, "window.Create(L" + windowsName},
		}},
		{"windows", "windows/runner/Runner.rc", [][2]string{
			{`VALUE "FileDescription", "` + options.projectName + `"`, `VALUE "FileDescription", ` + rcName},
			{`VALUE "ProductName", "` + options.projectName + `"`, `VALUE "ProductName", ` + rcName},
		}},
		{"linux", "linux/runner/my_application.cc", [][2]string{
			{`gtk_header_bar_set_title(header_bar, "` + options.projectName + `")`, "gtk_header_bar_set_title(header_bar, " + cppName + ")"},
			{`gtk_window_set_title(window, "` + options.projectName + `")`, "gtk_window_set_title(window, " + cppName + ")"},
		}},
		{"macos", "macos/Runner/Configs/AppInfo.xcconfig", [][2]string{
			{"PRODUCT_NAME = " + options.projectName + "\n",
				// Flutter derives the .app path from PRODUCT_NAME, so only the
				// bundle's user-facing names may use the display name.
				"PRODUCT_NAME = " + options.projectName + "\nBRIDRA_NAME_SLASH = /\nBRIDRA_NAME_DOLLAR = $\nBRIDRA_DISPLAY_NAME = " + xcodeName + "\n"},
		}},
		{"macos", "macos/Runner/Info.plist", [][2]string{
			{"\t<key>CFBundleName</key>\n\t<string>$(PRODUCT_NAME)</string>",
				"\t<key>CFBundleName</key>\n\t<string>$(BRIDRA_DISPLAY_NAME)</string>\n" +
					"\t<key>CFBundleDisplayName</key>\n\t<string>$(BRIDRA_DISPLAY_NAME)</string>"},
		}},
	}
	for _, patch := range patches {
		if !projectplatform.Contains(options.selectedPlatforms, patch.platform) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(patch.path))
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", patch.path, err)
		}
		source := strings.ReplaceAll(string(contents), "\r\n", "\n")
		for _, replacement := range patch.replacements {
			if strings.Count(source, replacement[0]) != 1 {
				return fmt.Errorf("%s does not contain the expected Flutter runner field", patch.path)
			}
			source = strings.Replace(source, replacement[0], replacement[1], 1)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", patch.path, err)
		}
	}
	return nil
}

func applyMacOSLoopbackEntitlements(root string, options createOptions) error {
	if !projectplatform.Contains(options.selectedPlatforms, "macos") {
		return nil
	}
	const sandbox = "\t<key>com.apple.security.app-sandbox</key>\n\t<true/>"
	for _, name := range []string{"DebugProfile", "Release"} {
		path := filepath.Join(root, "macos", "Runner", name+".entitlements")
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		source := strings.ReplaceAll(string(contents), "\r\n", "\n")
		if strings.Count(source, sandbox) != 1 {
			return fmt.Errorf("%s does not contain the expected Flutter sandbox entitlement", path)
		}
		additional := ""
		for _, capability := range []string{"client", "server"} {
			key := "<key>com.apple.security.network." + capability + "</key>"
			if strings.Contains(source, key) {
				if strings.Count(source, key) != 1 || strings.Count(source, key+"\n\t<true/>") != 1 {
					return fmt.Errorf("%s has an unexpected %s entitlement", path, capability)
				}
				continue
			}
			additional += "\n\t" + key + "\n\t<true/>"
		}
		// The sandbox requires both socket capabilities even though the
		// single-instance listener binds only to 127.0.0.1.
		source = strings.Replace(source, sandbox, sandbox+additional, 1)
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}
