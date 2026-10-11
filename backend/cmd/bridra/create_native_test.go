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
		{"macos", "macos/Runner/Info.plist", "\t<key>CFBundleName</key>\n\t<string>$(PRODUCT_NAME)</string>\n"},
		{"macos", "macos/Runner/DebugProfile.entitlements", "<dict>\n\t<key>com.apple.security.app-sandbox</key>\n\t<true/>\n\t<key>com.apple.security.network.server</key>\n\t<true/>\n</dict>\n"},
		{"macos", "macos/Runner/Release.entitlements", "<dict>\n\t<key>com.apple.security.app-sandbox</key>\n\t<true/>\n</dict>\n"},
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
		"macos/Runner/Configs/AppInfo.xcconfig": {"PRODUCT_NAME = live_sentry\n", "BRIDRA_DISPLAY_NAME = Live Sentry", "PRODUCT_BUNDLE_IDENTIFIER = com.example.live_sentry"},
		"macos/Runner/Info.plist":               {"<key>CFBundleName</key>\n\t<string>$(BRIDRA_DISPLAY_NAME)</string>", "<key>CFBundleDisplayName</key>\n\t<string>$(BRIDRA_DISPLAY_NAME)</string>"},
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
		"macos/Runner/Configs/AppInfo.xcconfig": `BRIDRA_DISPLAY_NAME = Sentry "Pro" $(BRIDRA_NAME_DOLLAR)HOME $(BRIDRA_NAME_SLASH)$(BRIDRA_NAME_SLASH)`,
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

func TestCreateNativeDisplayNameUsesASCIIWindowsUnicodeLiteral(t *testing.T) {
	root := t.TempDir()
	options := createOptions{
		projectName:       "app",
		displayName:       `Bridra 驗收 "Pro" \ é 🚀 & Co.`,
		selectedPlatforms: []string{"windows", "linux"},
	}
	if err := writeNativeRunnerFixtures(root, options.projectName, options.selectedPlatforms); err != nil {
		t.Fatal(err)
	}
	if err := applyNativeDisplayName(root, options); err != nil {
		t.Fatal(err)
	}
	windows, err := os.ReadFile(filepath.Join(root, "windows", "runner", "main.cpp"))
	if err != nil {
		t.Fatal(err)
	}
	const literal = `L"Bridra \u9a57\u6536 \"Pro\" \\ \u00e9 \U0001f680 & Co."`
	if !strings.Contains(string(windows), "window.Create("+literal+",") {
		t.Fatalf("Windows title must use codepage-independent Unicode escapes:\n%s", windows)
	}
	for _, b := range windows {
		if b > 0x7f {
			t.Fatalf("Windows title source contains non-ASCII byte: %x", b)
		}
	}
	linux, err := os.ReadFile(filepath.Join(root, "linux", "runner", "my_application.cc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(linux), `驗收 \"Pro\" \\ é 🚀`) {
		t.Fatalf("GTK titles must retain their UTF-8 encoding:\n%s", linux)
	}
}

func TestCreateMacOSLoopbackEntitlementsPreserveSandboxAndExistingKeys(t *testing.T) {
	root := t.TempDir()
	options := createOptions{selectedPlatforms: []string{"macos"}}
	if err := writeNativeRunnerFixtures(root, "app", options.selectedPlatforms); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := applyMacOSLoopbackEntitlements(root, options); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"DebugProfile", "Release"} {
		contents, err := os.ReadFile(filepath.Join(root, "macos", "Runner", name+".entitlements"))
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"app-sandbox", "network.client", "network.server"} {
			if strings.Count(string(contents), "<key>com.apple.security."+key+"</key>\n\t<true/>") != 1 {
				t.Errorf("%s must enable %s exactly once:\n%s", name, key, contents)
			}
		}
	}
}

func TestCreateMacOSLoopbackEntitlementsSkipUnselectedPlatform(t *testing.T) {
	if err := applyMacOSLoopbackEntitlements(t.TempDir(), createOptions{selectedPlatforms: []string{"linux"}}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateMacOSLoopbackEntitlementsRejectDisabledCapability(t *testing.T) {
	root := t.TempDir()
	options := createOptions{selectedPlatforms: []string{"macos"}}
	if err := writeNativeRunnerFixtures(root, "app", options.selectedPlatforms); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "macos", "Runner", "DebugProfile.entitlements")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(contents), "</dict>", "\t<key>com.apple.security.network.client</key>\n\t<false/>\n</dict>", 1)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyMacOSLoopbackEntitlements(root, options); err == nil {
		t.Fatal("explicitly disabled socket capability must not be overwritten")
	}
}
