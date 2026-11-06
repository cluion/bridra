package webdesktop

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func buildMacNative(sourceDir, output string) error {
	architecture := runtime.GOARCH
	if architecture == "amd64" {
		architecture = "x86_64"
	}
	cmd := exec.Command("xcrun", "--sdk", "macosx", "swiftc", "-swift-version", "5", "-O",
		"-target", architecture+"-apple-macos13.0", "-module-cache-path", filepath.Join(sourceDir, ".module-cache"),
		filepath.Join(sourceDir, "main.swift"), "-framework", "AppKit", "-framework", "WebKit", "-o", output)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compile WKWebView host with xcrun swiftc: %w: %s", err, output)
	}
	return nil
}

func finishMacBundle(bundle, projectName string) error {
	var name bytes.Buffer
	if err := xml.EscapeText(&name, []byte(projectName)); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key><string>%[1]s</string>
  <key>CFBundleIdentifier</key><string>app.bridra.%[2]s</string>
  <key>CFBundleName</key><string>%[1]s</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict>
</plist>
`, name.String(), strings.ReplaceAll(name.String(), "_", "-"))
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
		return err
	}
	paths := []string{filepath.Join(bundle, "Contents", "MacOS", "bridra-webview"), bundle}
	for _, path := range paths {
		if output, err := exec.Command("codesign", "--force", "--sign", "-", path).CombinedOutput(); err != nil {
			return fmt.Errorf("ad-hoc sign %s: %w: %s", path, err, output)
		}
	}
	return nil
}
