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
	rcName := "\"" + strings.NewReplacer(`\`, `\\`, `"`, `""`).Replace(options.displayName) + "\""
	xcodeName := strings.NewReplacer("/", "$(BRIDRA_NAME_SLASH)", "$", "$(BRIDRA_NAME_DOLLAR)").Replace(options.displayName)
	patches := []struct {
		platform     string
		path         string
		replacements [][2]string
	}{
		{"windows", "windows/runner/main.cpp", [][2]string{
			{`window.Create(L"` + options.projectName + `"`, "window.Create(L" + cppName},
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
				"BRIDRA_NAME_SLASH = /\nBRIDRA_NAME_DOLLAR = $\nPRODUCT_NAME = " + xcodeName + "\n" +
					"EXECUTABLE_NAME = " + options.projectName + "\nWRAPPER_NAME = " + options.projectName + ".app\n"},
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
