package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/internal/projectplatform"
)

func writeNativeRunnerFixtures(root, projectName string, platforms []string) error {
	for _, file := range []struct{ platform, path, contents string }{
		{"windows", "windows/runner/main.cpp", `window.Create(L"` + projectName + `", origin, size);`},
		{"windows", "windows/runner/Runner.rc", `VALUE "FileDescription", "` + projectName + `" "\0"` + "\n" + `VALUE "ProductName", "` + projectName + `" "\0"` + "\n" + `VALUE "OriginalFilename", "` + projectName + `.exe" "\0"`},
		{"linux", "linux/runner/my_application.cc", `gtk_header_bar_set_title(header_bar, "` + projectName + `");` + "\n" + `gtk_window_set_title(window, "` + projectName + `");`},
		{"macos", "macos/Runner/Configs/AppInfo.xcconfig", "PRODUCT_NAME = " + projectName + "\nPRODUCT_BUNDLE_IDENTIFIER = com.example." + projectName + "\n"},
	} {
		if !projectplatform.Contains(platforms, file.platform) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(file.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(file.contents), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func TestCreateNativeDisplayNamePreservesBinaryNamesAndIdentifiers(t *testing.T) {
	root := t.TempDir()
	options := createOptions{projectName: "live_sentry", displayName: "Live Sentry", selectedPlatforms: []string{"linux", "macos", "windows"}}
	if err := writeNativeRunnerFixtures(root, options.projectName, options.selectedPlatforms); err != nil {
		t.Fatal(err)
	}
	if err := applyNativeDisplayName(root, options); err != nil {
		t.Fatal(err)
	}
	for path, fragments := range map[string][]string{
		"windows/runner/main.cpp":               {`window.Create(L"Live Sentry"`},
		"windows/runner/Runner.rc":              {`VALUE "FileDescription", "Live Sentry"`, `VALUE "ProductName", "Live Sentry"`, `"live_sentry.exe"`},
		"linux/runner/my_application.cc":        {`gtk_header_bar_set_title(header_bar, "Live Sentry")`, `gtk_window_set_title(window, "Live Sentry")`},
		"macos/Runner/Configs/AppInfo.xcconfig": {"PRODUCT_NAME = Live Sentry", "PRODUCT_BUNDLE_IDENTIFIER = com.example.live_sentry", "EXECUTABLE_NAME = live_sentry", "WRAPPER_NAME = live_sentry.app"},
	} {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		for _, fragment := range fragments {
			if !strings.Contains(string(contents), fragment) {
				t.Errorf("%s missing %s", path, fragment)
			}
		}
	}
}

func TestCreateNativeDisplayNameEscapesNativeFields(t *testing.T) {
	root := t.TempDir()
	options := createOptions{projectName: "app", displayName: `Sentry "Pro" $HOME //`, selectedPlatforms: []string{"linux", "macos", "windows"}}
	if err := writeNativeRunnerFixtures(root, options.projectName, options.selectedPlatforms); err != nil {
		t.Fatal(err)
	}
	if err := applyNativeDisplayName(root, options); err != nil {
		t.Fatal(err)
	}
	for path, fragment := range map[string]string{
		"windows/runner/main.cpp":               `L"Sentry \"Pro\" $HOME //"`,
		"windows/runner/Runner.rc":              `"Sentry ""Pro"" $HOME //"`,
		"macos/Runner/Configs/AppInfo.xcconfig": `PRODUCT_NAME = Sentry "Pro" $(BRIDRA_NAME_DOLLAR)HOME $(BRIDRA_NAME_SLASH)$(BRIDRA_NAME_SLASH)`,
	} {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil || !strings.Contains(string(contents), fragment) {
			t.Errorf("%s: %v\n%s", path, err, contents)
		}
	}
}

func TestCreateNativeDisplayNameRejectsUnexpectedRunner(t *testing.T) {
	root := t.TempDir()
	options := createOptions{projectName: "app", displayName: "App", selectedPlatforms: []string{"windows"}}
	if err := writeNativeRunnerFixtures(root, "custom", options.selectedPlatforms); err != nil {
		t.Fatal(err)
	}
	if err := applyNativeDisplayName(root, options); err == nil {
		t.Fatal("unexpected Flutter runner must fail creation")
	}
}
