//go:build darwin && cgo && bridra_macos_native

package framework

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/framework/internal/macosbookmarktest"
)

const (
	macOSSandboxSmokeEnabled = "BRIDRA_MACOS_SANDBOX_SMOKE"
	macOSSandboxSmokeChild   = "BRIDRA_MACOS_SANDBOX_SMOKE_CHILD"
	macOSSandboxSmokeCreator = "BRIDRA_MACOS_SANDBOX_SMOKE_CREATOR"
	macOSSandboxSmokeMarker  = "BRIDRA_MACOS_SANDBOX_SMOKE_OK"
	macOSSandboxBookmarkMark = "BRIDRA_MACOS_SANDBOX_BOOKMARK:"
	macOSSandboxSmokeContent = "bridra sandbox bookmark smoke\n"
)

type macOSSandboxSmokePayload struct {
	Bookmark     string `json:"bookmark"`
	RawPath      string `json:"rawPath"`
	RelativeName string `json:"relativeName"`
}

func TestMacOSSandboxBookmarkHandoff(t *testing.T) {
	if os.Getenv(macOSSandboxSmokeCreator) == "1" {
		runMacOSSandboxSmokeCreator(t)
		return
	}
	if os.Getenv(macOSSandboxSmokeChild) == "1" {
		runMacOSSandboxSmokeChild(t)
		return
	}
	if os.Getenv(macOSSandboxSmokeEnabled) != "1" {
		t.Skip("set BRIDRA_MACOS_SANDBOX_SMOKE=1 to run the signed App Sandbox smoke")
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Fatal("resolve home directory for sandbox smoke")
	}
	outside, err := os.MkdirTemp(home, ".bridra-sandbox-smoke-")
	if err != nil {
		t.Fatal("create outside-container sandbox fixture")
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(outside); err != nil {
			t.Errorf("remove outside-container sandbox fixture: %v", err)
		}
	})

	const relativeName = "selected.txt"
	rawPath := filepath.Join(outside, relativeName)
	if err := os.WriteFile(rawPath, []byte(macOSSandboxSmokeContent), 0o600); err != nil {
		t.Fatal("write outside-container sandbox fixture")
	}
	bookmark := createAuthorizedMacOSSandboxBookmark(t, outside, rawPath)
	payload, err := json.Marshal(macOSSandboxSmokePayload{
		Bookmark:     base64.StdEncoding.EncodeToString(bookmark),
		RawPath:      rawPath,
		RelativeName: relativeName,
	})
	if err != nil {
		t.Fatal("encode sandbox smoke payload")
	}

	signedExecutable := prepareSignedMacOSSandboxSmokeExecutable(t, "receiver", "")
	command := exec.Command(
		signedExecutable,
		"-test.run=^TestMacOSSandboxBookmarkHandoff$",
		"-test.v",
	)
	command.Env = append(os.Environ(), macOSSandboxSmokeChild+"=1")
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"signed App Sandbox smoke failed (%v):\n%s",
			err,
			redactSandboxSmokeOutput(output, outside),
		)
	}
	if !bytes.Contains(output, []byte(macOSSandboxSmokeMarker)) {
		t.Fatalf("signed App Sandbox smoke omitted success marker:\n%s", redactSandboxSmokeOutput(output, outside))
	}
	if bytes.Contains(output, []byte(outside)) {
		t.Fatal("signed App Sandbox smoke output exposed the outside-container path")
	}
}

func createAuthorizedMacOSSandboxBookmark(t *testing.T, directory, rawPath string) []byte {
	t.Helper()
	executable := prepareSignedMacOSSandboxSmokeExecutable(t, "creator", directory)
	payload, err := json.Marshal(struct {
		Directory string `json:"directory"`
		RawPath   string `json:"rawPath"`
	}{Directory: directory, RawPath: rawPath})
	if err != nil {
		t.Fatal("encode sandbox bookmark creator payload")
	}
	command := exec.Command(
		executable,
		"-test.run=^TestMacOSSandboxBookmarkHandoff$",
		"-test.v",
	)
	command.Env = append(os.Environ(), macOSSandboxSmokeCreator+"=1")
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"signed App Sandbox bookmark creator failed (%v):\n%s",
			err,
			redactSandboxSmokeOutput(output, directory),
		)
	}
	if bytes.Contains(output, []byte(directory)) {
		t.Fatal("signed App Sandbox bookmark creator exposed the outside-container path")
	}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, macOSSandboxBookmarkMark) {
			continue
		}
		bookmark, err := base64.StdEncoding.DecodeString(
			strings.TrimPrefix(line, macOSSandboxBookmarkMark),
		)
		if err != nil || len(bookmark) == 0 {
			t.Fatal("signed App Sandbox bookmark creator returned invalid bookmark data")
		}
		return bookmark
	}
	t.Fatal("signed App Sandbox bookmark creator omitted bookmark data")
	return nil
}

func runMacOSSandboxSmokeCreator(t *testing.T) {
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 16*1024))
	decoder.DisallowUnknownFields()
	var payload struct {
		Directory string `json:"directory"`
		RawPath   string `json:"rawPath"`
	}
	if err := decoder.Decode(&payload); err != nil ||
		payload.Directory == "" || payload.RawPath == "" {
		t.Fatal("sandbox bookmark creator received an invalid payload")
	}
	contents, err := os.ReadFile(payload.RawPath)
	if err != nil || string(contents) != macOSSandboxSmokeContent {
		t.Fatal("sandbox bookmark creator lacks its test-only read authority")
	}
	bookmark, err := macosbookmarktest.CreateEphemeral(payload.Directory)
	if err != nil {
		t.Fatal("sandbox bookmark creator could not create bookmark data")
	}
	fmt.Fprintln(os.Stdout, macOSSandboxBookmarkMark+base64.StdEncoding.EncodeToString(bookmark))
}

func runMacOSSandboxSmokeChild(t *testing.T) {
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, defaultResourceBookmarkMaxBytes*2))
	decoder.DisallowUnknownFields()
	var payload macOSSandboxSmokePayload
	if err := decoder.Decode(&payload); err != nil ||
		payload.Bookmark == "" || payload.RawPath == "" || payload.RelativeName == "" ||
		filepath.Base(payload.RelativeName) != payload.RelativeName {
		t.Fatal("sandbox smoke received an invalid payload")
	}
	bookmark, err := base64.StdEncoding.DecodeString(payload.Bookmark)
	if err != nil {
		t.Fatal("sandbox smoke received invalid bookmark data")
	}
	assertMacOSSandboxReadDenied(t, payload.RawPath, "before grant")

	broker, err := NewResourceBroker(
		NewMacOSResourceBookmarkResolver(),
		DefaultResourceBrokerOptions(),
	)
	if err != nil {
		t.Fatal("sandbox smoke could not create the resource broker")
	}
	defer func() {
		if err := broker.Close(); err != nil {
			t.Errorf("sandbox smoke broker cleanup failed")
		}
	}()

	capability, err := broker.Grant(bookmark, ResourceBookmarkEphemeral)
	if err != nil {
		t.Fatalf("sandbox smoke bookmark grant failed: %v", err)
	}
	root, err := broker.ResolvePath(capability)
	if err != nil {
		t.Fatal("sandbox smoke capability resolution failed")
	}
	grantedPath := filepath.Join(root, payload.RelativeName)
	contents, err := os.ReadFile(grantedPath)
	if err != nil || string(contents) != macOSSandboxSmokeContent {
		t.Fatal("sandbox smoke could not read through the granted capability")
	}
	if err := broker.Release(capability); err != nil {
		t.Fatal("sandbox smoke capability release failed")
	}
	assertMacOSSandboxReadDenied(t, payload.RawPath, "after release")
	fmt.Fprintln(os.Stdout, macOSSandboxSmokeMarker)
}

func assertMacOSSandboxReadDenied(t *testing.T, path, phase string) {
	t.Helper()
	_, err := os.ReadFile(path)
	if err == nil {
		t.Fatalf("sandbox smoke raw path was readable %s", phase)
	}
	if !os.IsPermission(err) {
		t.Fatalf("sandbox smoke raw path returned an unexpected error %s", phase)
	}
}

func prepareSignedMacOSSandboxSmokeExecutable(t *testing.T, role, readOnlyPath string) string {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal("resolve sandbox smoke test executable")
	}
	directory := t.TempDir()
	bundleIdentifier := "dev.cluion.bridra.sandbox-smoke." + role
	bundle := filepath.Join(directory, "BridraSandboxSmoke-"+role+".app")
	contentsDirectory := filepath.Join(bundle, "Contents")
	executableDirectory := filepath.Join(contentsDirectory, "MacOS")
	if err := os.MkdirAll(executableDirectory, 0o755); err != nil {
		t.Fatal("create sandbox smoke application bundle")
	}
	target := filepath.Join(executableDirectory, "bridra-macos-sandbox-smoke")
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal("read sandbox smoke test executable")
	}
	if err := os.WriteFile(target, contents, 0o755); err != nil {
		t.Fatal("copy sandbox smoke test executable")
	}
	infoPlist := filepath.Join(contentsDirectory, "Info.plist")
	if err := os.WriteFile(infoPlist, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key>
  <string>bridra-macos-sandbox-smoke</string>
  <key>CFBundleIdentifier</key>
  <string>`+bundleIdentifier+`</string>
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleName</key>
  <string>Bridra Sandbox Smoke</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>1.0</string>
  <key>CFBundleVersion</key>
  <string>1</string>
</dict>
</plist>
`), 0o600); err != nil {
		t.Fatal("write sandbox smoke application metadata")
	}
	extraEntitlements := ""
	if readOnlyPath != "" {
		extraEntitlements = `
  <key>com.apple.security.temporary-exception.files.absolute-path.read-only</key>
  <array>
    <string>` + xmlEscape(readOnlyPath+string(os.PathSeparator)) + `</string>
  </array>`
	}
	entitlements := filepath.Join(directory, "sandbox.entitlements")
	if err := os.WriteFile(entitlements, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>com.apple.security.app-sandbox</key>
  <true/>
`+extraEntitlements+`
</dict>
</plist>
`), 0o600); err != nil {
		t.Fatal("write sandbox smoke entitlements")
	}
	runMacOSSandboxSmokeCommand(t, "/usr/bin/codesign",
		"--force", "--sign", "-",
		"--identifier", bundleIdentifier,
		"--entitlements", entitlements,
		bundle,
	)
	runMacOSSandboxSmokeCommand(t, "/usr/bin/codesign", "--verify", "--deep", "--strict", bundle)
	return target
}

func xmlEscape(value string) string {
	var output bytes.Buffer
	if err := xml.EscapeText(&output, []byte(value)); err != nil {
		return ""
	}
	return output.String()
}

func runMacOSSandboxSmokeCommand(t *testing.T, name string, arguments ...string) {
	t.Helper()
	command := exec.Command(name, arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("sandbox smoke signing command failed: %s", strings.TrimSpace(string(output)))
	}
}

func redactSandboxSmokeOutput(output []byte, secret string) string {
	return strings.ReplaceAll(string(output), secret, "[REDACTED]")
}
