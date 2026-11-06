package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/codegen"
)

func TestDoctorWebDesktopToolchains(t *testing.T) {
	for _, tc := range []struct {
		name, host, platform, missing string
		want                          []string
	}{
		{"website only", "darwin", "web", "", nil},
		{"other desktop only", "linux", "windows", "", nil},
		{"macOS ready", "darwin", "macos", "", []string{"[ok] codesign", "[ok] Swift compiler", "[ok] macOS SDK"}},
		{"macOS lacks xcrun", "darwin", "macos", "xcrun", []string{"[fail] xcrun"}},
		{"macOS lacks codesign", "darwin", "macos", "codesign", []string{"[fail] codesign"}},
		{"macOS lacks Swift", "darwin", "macos", "--find", []string{"[fail] Swift compiler"}},
		{"macOS lacks SDK", "darwin", "macos", "--show-sdk-path", []string{"[fail] macOS SDK"}},
		{"Linux ready", "linux", "linux", "", []string{"[ok] cc", "[ok] GTK/WebKit development packages"}},
		{"Linux lacks compiler", "linux", "linux", "cc", []string{"[fail] cc"}},
		{"Linux lacks pkg-config", "linux", "linux", "pkg-config", []string{"[fail] pkg-config"}},
		{"Linux lacks WebKitGTK", "linux", "linux", "webkitgtk-6.0", []string{"[fail] GTK/WebKit development packages", "webkitgtk-6.0"}},
		{"Windows ready", "windows", "windows", "", []string{"[ok] WebView2 SDK", "[ok] MSVC"}},
		{"Windows lacks compiler", "windows", "windows", "cl.exe", []string{"[fail] cl.exe"}},
		{"Windows lacks ARM64 loader", "windows", "windows", "ARM64 loader", []string{"[fail] WebView2 SDK", filepath.Join("arm64", "WebView2LoaderStatic.lib")}},
		{"Windows mismatched compiler", "windows", "windows", "/Zs", []string{"[fail] MSVC", "arm64 Native Tools prompt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := makeProjectRoot(t, fmt.Sprintf(`{"schemaVersion":4,"projectName":"portal","goModule":"example.com/portal",
"frameworkModule":"github.com/cluion/bridra/backend","frameworkVersion":"0.23.2","templateVersion":9,"protocolVersion":1,
"platforms":[%q],"ui":{"mode":"web","preset":"vue","dist":"frontend/dist"}}`, tc.platform))
			schema := readTestFile(t, "../../../schema/bridra.json")
			writeBuildTestOutput(t, filepath.Join(root, "schema", "bridra.json"), string(schema))
			writeBuildTestOutput(t, filepath.Join(root, codegen.GoProtocolPath), "const ProtocolVersion = 1\n")
			writeBuildTestOutput(t, filepath.Join(root, codegen.TypeScriptClientPath), "export const protocolVersion = 1;\n")

			if tc.host == "windows" {
				sdk := t.TempDir()
				t.Setenv("BRIDRA_WEBVIEW2_SDK", sdk)
				native := filepath.Join(sdk, "build", "native")
				writeBuildTestOutput(t, filepath.Join(native, "include", "WebView2.h"), "")
				arch := "arm64"
				if tc.missing == "ARM64 loader" {
					arch = "x64"
				}
				writeBuildTestOutput(t, filepath.Join(native, arch, "WebView2LoaderStatic.lib"), "")
			}

			system := healthyDoctorSystem()
			system.goos, system.goarch = tc.host, "arm64"
			system.lookPath = func(tool string) (string, error) {
				if !slices.Contains([]string{"go", "node", "npm"}, tool) && tc.want == nil {
					t.Fatalf("checked native tool %s for selected platform %s", tool, tc.platform)
				}
				if tool == tc.missing {
					return "", os.ErrNotExist
				}
				return tool, nil
			}
			system.run = func(_ context.Context, command string, args ...string) ([]byte, error) {
				if tc.missing != "" && slices.Contains(args, tc.missing) {
					return nil, fmt.Errorf("unavailable %s", tc.missing)
				}
				switch command {
				case "go":
					return []byte("go version go1.25.5"), nil
				case "xcrun":
					if slices.Equal(args, []string{"--sdk", "macosx", "--find", "swiftc"}) || slices.Equal(args, []string{"--sdk", "macosx", "--show-sdk-path"}) {
						return []byte("/Developer"), nil
					}
				case "pkg-config":
					if slices.Equal(args, []string{"--print-errors", "--exists", "gtk4", "webkitgtk-6.0", "libsoup-3.0"}) {
						return nil, nil
					}
				case "cl.exe":
					if slices.Contains(args, "/Zs") {
						probe := string(readTestFile(t, args[len(args)-1]))
						if !strings.Contains(probe, "#ifndef _M_ARM64") || !strings.Contains(probe, "<winrt/Windows.Data.Json.h>") {
							t.Fatal("probe must validate target architecture and Windows SDK")
						}
						return nil, nil
					}
				}
				t.Fatalf("unexpected probe %s %v", command, args)
				return nil, nil
			}
			var output bytes.Buffer
			err := (doctorCommand{system: system}).run([]string{"--root", root, "--strict"}, &output, io.Discard)
			if tc.missing == "" && err != nil || tc.missing != "" && !errors.Is(err, errDoctorFailed) {
				t.Fatalf("doctor error = %v\n%s", err, output.String())
			}
			for _, message := range tc.want {
				if !strings.Contains(output.String(), message) {
					t.Errorf("missing %q\n%s", message, output.String())
				}
			}
		})
	}
}
