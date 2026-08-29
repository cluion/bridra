package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

func TestBuildLinuxBundlesSidecarAndWritesManifest(t *testing.T) {
	root := buildProjectRoot(t)
	var specifications []buildProcessSpec
	system := buildTestSystem("linux", "amd64")
	system.run = func(specification buildProcessSpec) error {
		specifications = append(specifications, specification)
		switch specification.Name {
		case "go":
			writeBuildTestOutput(t, buildOutputArgument(t, specification.Arguments), "linux-sidecar")
		case "fvm":
			bundle := filepath.Join(root, "build", "linux", "x64", "release", "bundle")
			writeBuildTestOutput(t, filepath.Join(bundle, "example"), "flutter-app")
		default:
			t.Fatalf("unexpected command: %#v", specification)
		}
		return nil
	}
	command := buildCommand{system: system}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := command.run([]string{"linux", "--root", root}, &stdout, &stderr); err != nil {
		t.Fatalf("build: %v\n%s", err, stderr.String())
	}

	if len(specifications) != 2 || specifications[0].Name != "go" || specifications[1].Name != "fvm" {
		t.Fatalf("specifications = %#v", specifications)
	}
	if !containsString(specifications[1].Arguments, "--target-platform=linux-x64") {
		t.Fatalf("Flutter arguments = %v", specifications[1].Arguments)
	}
	for _, expected := range []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64"} {
		if !containsString(specifications[0].Environment, expected) {
			t.Fatalf("Go environment = %v, want %s", specifications[0].Environment, expected)
		}
	}
	sidecarPath := filepath.Join(
		root, "build", "linux", "x64", "release", "bundle", "libexec", "bridra_backend",
	)
	contents, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read Sidecar: %v", err)
	}
	if string(contents) != "linux-sidecar" {
		t.Fatalf("Sidecar = %q", contents)
	}
	manifest := readBuildTestManifest(t, filepath.Join(root, "build", "bridra", "linux-release.json"))
	if manifest.SchemaVersion != buildManifestSchemaVersion ||
		manifest.Target != buildTargetLinux || manifest.Mode != buildModeRelease ||
		manifest.Transport != buildTransportSidecar || manifest.Architecture != "x64" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if manifest.Sidecar != "build/linux/x64/release/bundle/libexec/bridra_backend" ||
		manifest.SidecarSHA256 == "" || manifest.ArtifactSHA256 == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
	for _, expected := range []string{
		"Bridra Build " + releaseinfo.Version,
		"Target: linux",
		"Transport: sidecar",
		"Artifact: build/linux/x64/release/bundle",
		"Manifest: build/bridra/linux-release.json",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), expected)
		}
	}
}

func TestBuildWebReleaseUsesHTTPAndDoesNotPersistToken(t *testing.T) {
	root := buildProjectRoot(t)
	var flutter buildProcessSpec
	system := buildTestSystem("darwin", "arm64")
	system.run = func(specification buildProcessSpec) error {
		if specification.Name != "fvm" {
			t.Fatalf("unexpected command: %#v", specification)
		}
		flutter = specification
		writeBuildTestOutput(t, filepath.Join(root, "build", "web", "main.dart.js"), "web-app")
		fmt.Fprintln(specification.Stdout, "Flutter output preserved")
		return nil
	}
	var stdout bytes.Buffer
	secret := "release-secret"
	err := (buildCommand{system: system}).run([]string{
		"web",
		"--root", root,
		"--backend-url", "https://api.example.test/rpc",
		"--token", secret,
	}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, expected := range []string{
		"flutter", "build", "web", "--release",
		"--dart-define=BRIDRA_BACKEND_URL=https://api.example.test/rpc",
		"--dart-define=BRIDRA_BACKEND_TOKEN=" + secret,
	} {
		if !containsString(flutter.Arguments, expected) {
			t.Fatalf("Flutter arguments = %v, want %s", flutter.Arguments, expected)
		}
	}
	manifestPath := filepath.Join(root, "build", "bridra", "web-release.json")
	manifestContents, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if bytes.Contains(manifestContents, []byte(secret)) || strings.Contains(stdout.String(), secret) {
		t.Fatal("release token leaked into Bridra output")
	}
	manifest := readBuildTestManifest(t, manifestPath)
	if manifest.Transport != buildTransportHTTP ||
		manifest.BackendURL != "https://api.example.test/rpc" ||
		manifest.Artifact != "build/web" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if !strings.Contains(stdout.String(), "Flutter output preserved") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestBuildMacOSCreatesUniversalSidecarAndResignsApp(t *testing.T) {
	root := buildProjectRoot(t)
	appEntitlements := `<?xml version="1.0"?><plist version="1.0"><dict><key>com.apple.security.app-sandbox</key><true/></dict></plist>`
	sidecarEntitlements := `<?xml version="1.0"?><plist version="1.0"><dict><key>com.apple.security.app-sandbox</key><true/><key>com.apple.security.inherit</key><true/></dict></plist>`
	sidecarEntitlementsPath := filepath.Join(root, "macos", "Runner", "Sidecar.entitlements")
	writeBuildTestOutput(t, sidecarEntitlementsPath, sidecarEntitlements)
	var specifications []buildProcessSpec
	system := buildTestSystem("darwin", "arm64")
	system.run = func(specification buildProcessSpec) error {
		specifications = append(specifications, specification)
		switch specification.Name {
		case "go":
			architecture := buildEnvironmentValue(specification.Environment, "GOARCH")
			writeBuildTestOutput(
				t,
				buildOutputArgument(t, specification.Arguments),
				"sidecar-"+architecture,
			)
		case "xcrun":
			writeBuildTestOutput(t, buildNamedArgument(t, specification.Arguments, "-output"), "universal-sidecar")
		case "fvm":
			writeBuildTestOutput(
				t,
				filepath.Join(root, "macos", "Flutter", "ephemeral", ".app_filename"),
				"example.app\n",
			)
			writeBuildTestOutput(
				t,
				filepath.Join(root, "build", "macos", "Build", "Products", "Release", "example.app", "Contents", "MacOS", "example"),
				"flutter-app",
			)
		case "codesign":
			if containsString(specification.Arguments, "--display") {
				destination := buildNamedArgument(t, specification.Arguments, "--entitlements")
				target := specification.Arguments[len(specification.Arguments)-1]
				contents := appEntitlements
				if strings.HasSuffix(target, "bridra_backend") {
					contents = sidecarEntitlements
				}
				writeBuildTestOutput(t, destination, contents)
			}
		case "plutil":
			path := specification.Arguments[len(specification.Arguments)-1]
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read entitlements for normalization: %v", err)
			}
			if bytes.Contains(contents, []byte("com.apple.security.inherit")) {
				fmt.Fprint(specification.Stdout, `{"com.apple.security.app-sandbox":true,"com.apple.security.inherit":true}`)
			} else {
				fmt.Fprint(specification.Stdout, `{"com.apple.security.app-sandbox":true}`)
			}
		default:
			t.Fatalf("unexpected command: %#v", specification)
		}
		return nil
	}
	if err := (buildCommand{system: system}).run(
		[]string{
			"macos",
			"--root", root,
			"--macos-sidecar-native",
			"--macos-sidecar-entitlements", "macos/Runner/Sidecar.entitlements",
		},
		&bytes.Buffer{},
		&bytes.Buffer{},
	); err != nil {
		t.Fatalf("build: %v", err)
	}

	var goArchitectures []string
	codesignCalls := 0
	var sidecarSign buildProcessSpec
	var appSign buildProcessSpec
	for _, specification := range specifications {
		if specification.Name == "go" {
			goArchitectures = append(
				goArchitectures,
				buildEnvironmentValue(specification.Environment, "GOARCH"),
			)
			if buildEnvironmentValue(specification.Environment, "CGO_ENABLED") != "1" ||
				!containsString(specification.Arguments, macosNativeBuildTag) {
				t.Fatalf("native Go build = %#v", specification)
			}
		}
		if specification.Name == "codesign" {
			codesignCalls++
			if containsString(specification.Arguments, "--sign") {
				target := specification.Arguments[len(specification.Arguments)-1]
				if strings.HasSuffix(target, "bridra_backend") {
					sidecarSign = specification
				} else {
					appSign = specification
				}
			}
		}
	}
	if strings.Join(goArchitectures, ",") != "arm64,amd64" || codesignCalls != 7 {
		t.Fatalf("Go architectures = %v, codesign calls = %d", goArchitectures, codesignCalls)
	}
	if buildNamedArgument(t, sidecarSign.Arguments, "--entitlements") != sidecarEntitlementsPath {
		t.Fatalf("Sidecar codesign arguments = %v", sidecarSign.Arguments)
	}
	if !containsString(appSign.Arguments, "--preserve-metadata=entitlements") {
		t.Fatalf("app codesign arguments = %v", appSign.Arguments)
	}
	sidecar := filepath.Join(
		root,
		"build", "macos", "Build", "Products", "Release", "example.app",
		"Contents", "MacOS", "libexec", "bridra_backend",
	)
	contents, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("read Sidecar: %v", err)
	}
	if string(contents) != "universal-sidecar" {
		t.Fatalf("Sidecar = %q", contents)
	}
	manifest := readBuildTestManifest(t, filepath.Join(root, "build", "bridra", "macos-release.json"))
	if manifest.SchemaVersion != buildManifestSchemaVersion ||
		manifest.Architecture != "universal" ||
		manifest.Sidecar == "" ||
		!manifest.SidecarNative {
		t.Fatalf("manifest = %#v", manifest)
	}
	artifactChecksum, err := artifactSHA256(filepath.Join(
		root,
		"build", "macos", "Build", "Products", "Release", "example.app",
	))
	if err != nil {
		t.Fatalf("checksum signed artifact: %v", err)
	}
	sidecarChecksum, err := artifactSHA256(sidecar)
	if err != nil {
		t.Fatalf("checksum signed Sidecar: %v", err)
	}
	if manifest.ArtifactSHA256 != artifactChecksum || manifest.SidecarSHA256 != sidecarChecksum {
		t.Fatalf("manifest checksums were not computed from the final signed artifact: %#v", manifest)
	}
}

func TestBuildMacOSPortableSidecarRemainsCGODisabled(t *testing.T) {
	root := buildProjectRoot(t)
	workDirectory := t.TempDir()
	var specifications []buildProcessSpec
	system := buildTestSystem("darwin", "arm64")
	system.run = func(specification buildProcessSpec) error {
		specifications = append(specifications, specification)
		switch specification.Name {
		case "go":
			writeBuildTestOutput(t, buildOutputArgument(t, specification.Arguments), "sidecar")
		case "xcrun":
			writeBuildTestOutput(
				t,
				buildNamedArgument(t, specification.Arguments, "-output"),
				"universal",
			)
		default:
			t.Fatalf("unexpected command: %#v", specification)
		}
		return nil
	}
	_, err := (buildCommand{system: system}).buildSidecar(
		buildOptions{root: root, target: buildTargetMacOS},
		workDirectory,
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("build portable Sidecar: %v", err)
	}
	for _, specification := range specifications {
		if specification.Name != "go" {
			continue
		}
		if buildEnvironmentValue(specification.Environment, "CGO_ENABLED") != "0" ||
			containsString(specification.Arguments, macosNativeBuildTag) {
			t.Fatalf("portable Go build = %#v", specification)
		}
	}
}

func TestSignMacOSArtifactRejectsEntitlementDrift(t *testing.T) {
	root := t.TempDir()
	workDirectory := filepath.Join(root, "work")
	app := filepath.Join(root, "example.app")
	sidecar := filepath.Join(app, "Contents", "MacOS", "libexec", "bridra_backend")
	writeBuildTestOutput(t, filepath.Join(app, "Contents", "MacOS", "example"), "app")
	writeBuildTestOutput(t, sidecar, "sidecar")
	system := buildTestSystem("darwin", "arm64")
	system.run = func(specification buildProcessSpec) error {
		switch specification.Name {
		case "codesign":
			if containsString(specification.Arguments, "--display") {
				destination := buildNamedArgument(t, specification.Arguments, "--entitlements")
				value := "before"
				if strings.Contains(destination, "after") {
					value = "after"
				}
				writeBuildTestOutput(
					t,
					destination,
					`<plist version="1.0"><dict><key>value</key><string>`+value+`</string></dict></plist>`,
				)
			}
		case "plutil":
			path := specification.Arguments[len(specification.Arguments)-1]
			value := "before"
			if strings.Contains(path, "after") {
				value = "after"
			}
			fmt.Fprintf(specification.Stdout, `{"value":%q}`, value)
		default:
			t.Fatalf("unexpected command: %#v", specification)
		}
		return nil
	}
	if err := os.MkdirAll(workDirectory, 0o755); err != nil {
		t.Fatalf("create work directory: %v", err)
	}
	err := (buildCommand{system: system}).signMacOSArtifact(
		buildOptions{root: root},
		buildArtifact{path: app, sidecarPath: sidecar},
		workDirectory,
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if !errors.Is(err, errBuildArtifact) || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("error = %v, want entitlement drift", err)
	}
}

func TestBuildDebugHTTPDefaults(t *testing.T) {
	for _, test := range []struct {
		name   string
		target buildTarget
		goos   string
		url    string
	}{
		{name: "Android", target: buildTargetAndroid, goos: "linux", url: "http://10.0.2.2:8080/rpc"},
		{name: "iOS", target: buildTargetIOS, goos: "darwin", url: "http://127.0.0.1:8080/rpc"},
		{name: "Web", target: buildTargetWeb, goos: "windows", url: "http://127.0.0.1:8080/rpc"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := buildProjectRoot(t)
			command := buildCommand{system: buildTestSystem(test.goos, "amd64")}
			options, err := command.resolveOptions(buildOptions{
				root: root, target: test.target, mode: buildModeDebug,
			})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if options.transport != buildTransportHTTP || options.backendURL != test.url ||
				options.token != "dev-token" {
				t.Fatalf("options = %#v", options)
			}
		})
	}
}

func TestBuildArtifactPathsAreStable(t *testing.T) {
	root := filepath.FromSlash("/workspace/example")
	tests := []struct {
		name         string
		target       buildTarget
		mode         buildMode
		architecture string
		artifact     string
		sidecar      string
		requireFile  bool
	}{
		{
			name: "Linux arm64", target: buildTargetLinux, mode: buildModeDebug,
			architecture: "arm64",
			artifact:     "build/linux/arm64/debug/bundle",
			sidecar:      "build/linux/arm64/debug/bundle/libexec/bridra_backend",
		},
		{
			name: "macOS profile", target: buildTargetMacOS, mode: buildModeProfile,
			architecture: "universal",
			artifact:     "build/macos/Build/Products/Profile/example.app",
			sidecar:      "build/macos/Build/Products/Profile/example.app/Contents/MacOS/libexec/bridra_backend",
		},
		{
			name: "Windows x64", target: buildTargetWindows, mode: buildModeRelease,
			architecture: "x64",
			artifact:     "build/windows/x64/runner/Release",
			sidecar:      "build/windows/x64/runner/Release/libexec/bridra_backend.exe",
		},
		{
			name: "Android APK", target: buildTargetAndroid, mode: buildModeProfile,
			artifact:    "build/app/outputs/flutter-apk/app-profile.apk",
			requireFile: true,
		},
		{
			name: "unsigned iOS app", target: buildTargetIOS, mode: buildModeRelease,
			artifact: "build/ios/iphoneos/Runner.app",
		},
		{
			name: "Web directory", target: buildTargetWeb, mode: buildModeDebug,
			artifact: "build/web",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			system := buildTestSystem("darwin", "arm64")
			system.readFile = func(string) ([]byte, error) {
				return []byte("example.app\n"), nil
			}
			artifact, err := (buildCommand{system: system}).resolveArtifact(buildOptions{
				root: root, target: test.target, mode: test.mode, architecture: test.architecture,
			})
			if err != nil {
				t.Fatalf("resolve artifact: %v", err)
			}
			sidecar := ""
			if artifact.sidecarPath != "" {
				sidecar = relativeBuildPath(root, artifact.sidecarPath)
			}
			if relativeBuildPath(root, artifact.path) != test.artifact ||
				sidecar != test.sidecar ||
				artifact.requireFile != test.requireFile {
				t.Fatalf("artifact = %#v", artifact)
			}
		})
	}
}

func TestBuildRejectsInvalidOptions(t *testing.T) {
	root := buildProjectRoot(t)
	tests := []struct {
		name    string
		goos    string
		goarch  string
		options buildOptions
		message string
	}{
		{
			name: "unknown target", goos: "linux", goarch: "amd64",
			options: buildOptions{root: root, target: "desktop", mode: buildModeRelease},
			message: "target must be",
		},
		{
			name: "invalid mode", goos: "linux", goarch: "amd64",
			options: buildOptions{root: root, target: buildTargetLinux, mode: "fast"},
			message: "mode must be",
		},
		{
			name: "wrong desktop host", goos: "darwin", goarch: "arm64",
			options: buildOptions{root: root, target: buildTargetLinux, mode: buildModeRelease},
			message: "require a linux host",
		},
		{
			name: "release URL missing", goos: "linux", goarch: "amd64",
			options: buildOptions{root: root, target: buildTargetWeb, mode: buildModeRelease},
			message: "require --backend-url",
		},
		{
			name: "release URL is HTTP", goos: "linux", goarch: "amd64",
			options: buildOptions{
				root: root, target: buildTargetWeb, mode: buildModeRelease,
				backendURL: "http://api.example.test/rpc", token: "token",
			},
			message: "must use HTTPS",
		},
		{
			name: "release token missing", goos: "linux", goarch: "amd64",
			options: buildOptions{
				root: root, target: buildTargetWeb, mode: buildModeRelease,
				backendURL: "https://api.example.test/rpc",
			},
			message: "require --token",
		},
		{
			name: "desktop token without URL", goos: "darwin", goarch: "arm64",
			options: buildOptions{
				root: root, target: buildTargetMacOS, mode: buildModeRelease, token: "token",
			},
			message: "requires --backend-url",
		},
		{
			name: "macOS Sidecar entitlements on Linux target", goos: "linux", goarch: "amd64",
			options: buildOptions{
				root: root, target: buildTargetLinux, mode: buildModeRelease,
				macosSidecarEntitlements: "macos/Runner/Sidecar.entitlements",
			},
			message: "requires the macos target",
		},
		{
			name: "native macOS Sidecar on Linux target", goos: "linux", goarch: "amd64",
			options: buildOptions{
				root: root, target: buildTargetLinux, mode: buildModeRelease,
				macosSidecarNative: true,
			},
			message: "requires the macos target",
		},
		{
			name: "missing macOS Sidecar entitlements", goos: "darwin", goarch: "arm64",
			options: buildOptions{
				root: root, target: buildTargetMacOS, mode: buildModeRelease,
				macosSidecarEntitlements: "macos/Runner/missing.entitlements",
			},
			message: "entitlements are unavailable",
		},
		{
			name: "unsupported architecture", goos: "linux", goarch: "386",
			options: buildOptions{root: root, target: buildTargetLinux, mode: buildModeRelease},
			message: "unsupported linux host architecture",
		},
		{
			name: "URL query", goos: "linux", goarch: "amd64",
			options: buildOptions{
				root: root, target: buildTargetWeb, mode: buildModeDebug,
				backendURL: "http://127.0.0.1:8080/rpc?debug=true",
			},
			message: "query parameters",
		},
		{
			name: "unsupported Flutter host", goos: "freebsd", goarch: "amd64",
			options: buildOptions{
				root: root, target: buildTargetWeb, mode: buildModeDebug,
			},
			message: "require a Linux, macOS, or Windows host",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := buildCommand{system: buildTestSystem(test.goos, test.goarch)}
			_, err := command.resolveOptions(test.options)
			if !errors.Is(err, errBuildInvalid) || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want invalid build containing %q", err, test.message)
			}
		})
	}
}

func TestBuildRejectsMacOSSidecarEntitlementsWithHTTPTransport(t *testing.T) {
	root := buildProjectRoot(t)
	entitlements := filepath.Join(root, "macos", "Runner", "Sidecar.entitlements")
	writeBuildTestOutput(t, entitlements, `<plist version="1.0"><dict/></plist>`)
	_, err := (buildCommand{system: buildTestSystem("darwin", "arm64")}).resolveOptions(
		buildOptions{
			root: root, target: buildTargetMacOS, mode: buildModeDebug,
			backendURL: "http://127.0.0.1:8080/rpc", token: "token",
			macosSidecarEntitlements: entitlements,
		},
	)
	if !errors.Is(err, errBuildInvalid) || !strings.Contains(err.Error(), "Sidecar transport") {
		t.Fatalf("error = %v, want Sidecar transport requirement", err)
	}
}

func TestBuildRejectsNativeMacOSSidecarWithHTTPTransport(t *testing.T) {
	root := buildProjectRoot(t)
	_, err := (buildCommand{system: buildTestSystem("darwin", "arm64")}).resolveOptions(
		buildOptions{
			root: root, target: buildTargetMacOS, mode: buildModeDebug,
			backendURL: "http://127.0.0.1:8080/rpc", token: "token",
			macosSidecarNative: true,
		},
	)
	if !errors.Is(err, errBuildInvalid) || !strings.Contains(err.Error(), "Sidecar transport") {
		t.Fatalf("error = %v, want Sidecar transport requirement", err)
	}
}

func TestBuildRejectsTargetOutsideProjectPlatformScope(t *testing.T) {
	root := buildProjectRoot(t)
	metadata := `{
  "schemaVersion": 3,
  "projectName": "example",
  "goModule": "example.test/app",
  "frameworkModule": "github.com/cluion/bridra/backend",
  "frameworkVersion": "0.15.0",
  "templateVersion": 5,
  "protocolVersion": 1,
  "platforms": ["web"]
}
`
	if err := os.WriteFile(
		filepath.Join(root, ".bridra", "project.json"),
		[]byte(metadata),
		0o644,
	); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	_, err := (buildCommand{system: buildTestSystem("linux", "amd64")}).resolveOptions(
		buildOptions{root: root, target: buildTargetLinux, mode: buildModeRelease},
	)
	if !errors.Is(err, errBuildInvalid) || !strings.Contains(err.Error(), "not selected") {
		t.Fatalf("error = %v, want unselected target", err)
	}
}

func TestBuildInvalidProjectPreservesProjectError(t *testing.T) {
	root := makeProjectRoot(t, validProjectMetadata+"{}")
	command := buildCommand{system: buildTestSystem("linux", "amd64")}
	_, err := command.resolveOptions(buildOptions{
		root: root, target: buildTargetLinux, mode: buildModeRelease,
	})
	if !errors.Is(err, errBuildInvalid) || !errors.Is(err, errProjectInvalid) {
		t.Fatalf("error = %v, want build and project errors", err)
	}
}

func TestBuildPreservesCommandFailure(t *testing.T) {
	root := buildProjectRoot(t)
	want := errors.New("compiler failed")
	system := buildTestSystem("linux", "amd64")
	system.run = func(buildProcessSpec) error { return want }
	err := (buildCommand{system: system}).run(
		[]string{"linux", "--root", root},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if !errors.Is(err, errBuildFailed) || !errors.Is(err, want) {
		t.Fatalf("error = %v, want build and compiler errors", err)
	}
}

func TestBuildRejectsMissingFlutterArtifact(t *testing.T) {
	root := buildProjectRoot(t)
	system := buildTestSystem("linux", "amd64")
	system.run = func(specification buildProcessSpec) error {
		if specification.Name == "go" {
			writeBuildTestOutput(t, buildOutputArgument(t, specification.Arguments), "sidecar")
		}
		return nil
	}
	err := (buildCommand{system: system}).run(
		[]string{"linux", "--root", root},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if !errors.Is(err, errBuildArtifact) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want artifact and not-exist errors", err)
	}
}

func TestArtifactSHA256IsDeterministicForDirectoryTrees(t *testing.T) {
	first := filepath.Join(t.TempDir(), "artifact")
	second := filepath.Join(t.TempDir(), "artifact")
	for _, root := range []string{first, second} {
		if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
			t.Fatalf("create directory: %v", err)
		}
	}
	writeBuildTestOutput(t, filepath.Join(first, "a.txt"), "alpha")
	writeBuildTestOutput(t, filepath.Join(first, "nested", "b.txt"), "beta")
	writeBuildTestOutput(t, filepath.Join(second, "nested", "b.txt"), "beta")
	writeBuildTestOutput(t, filepath.Join(second, "a.txt"), "alpha")

	firstHash, err := artifactSHA256(first)
	if err != nil {
		t.Fatalf("first checksum: %v", err)
	}
	secondHash, err := artifactSHA256(second)
	if err != nil {
		t.Fatalf("second checksum: %v", err)
	}
	if firstHash != secondHash {
		t.Fatalf("hashes differ: %s != %s", firstHash, secondHash)
	}
	writeBuildTestOutput(t, filepath.Join(second, "a.txt"), "changed")
	changedHash, err := artifactSHA256(second)
	if err != nil {
		t.Fatalf("changed checksum: %v", err)
	}
	if changedHash == firstHash {
		t.Fatal("checksum did not change with artifact contents")
	}
}

func buildProjectRoot(t *testing.T) string {
	t.Helper()
	root := makeProjectRoot(t, validProjectMetadata)
	writeBuildTestOutput(t, filepath.Join(root, ".fvmrc"), "{\"flutter\":\"3.44.6\"}\n")
	writeBuildTestOutput(t, filepath.Join(root, "pubspec.yaml"), "name: example\n")
	if err := os.MkdirAll(filepath.Join(root, "backend", "cmd", "sidecar"), 0o755); err != nil {
		t.Fatalf("create Sidecar command: %v", err)
	}
	return root
}

func buildTestSystem(goos, goarch string) buildSystem {
	system := defaultBuildSystem()
	system.goos = goos
	system.goarch = goarch
	return system
}

func writeBuildTestOutput(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create output directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write output: %v", err)
	}
}

func buildOutputArgument(t *testing.T, arguments []string) string {
	t.Helper()
	return buildNamedArgument(t, arguments, "-o")
}

func buildNamedArgument(t *testing.T, arguments []string, name string) string {
	t.Helper()
	for index, argument := range arguments {
		if argument == name && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	t.Fatalf("arguments %v do not contain %s", arguments, name)
	return ""
}

func buildEnvironmentValue(environment []string, name string) string {
	prefix := name + "="
	for _, value := range environment {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}

func readBuildTestManifest(t *testing.T, path string) buildManifest {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest buildManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return manifest
}
