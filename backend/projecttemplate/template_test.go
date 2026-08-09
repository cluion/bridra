package projecttemplate

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestManifestHasStableSafeDestinations(t *testing.T) {
	manifest, err := LoadManifest()
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if manifest.Version != ManifestVersion {
		t.Fatalf("manifest version = %d, want %d", manifest.Version, ManifestVersion)
	}
	seen := map[string]struct{}{}
	for _, file := range manifest.Files {
		if !safeRelativePath(file.Source) || !safeRelativePath(file.Destination) {
			t.Fatalf("unsafe manifest file: %#v", file)
		}
		if _, exists := seen[file.Destination]; exists {
			t.Fatalf("duplicate destination: %s", file.Destination)
		}
		seen[file.Destination] = struct{}{}
	}
}

func TestRenderMatchesProjectTreeGolden(t *testing.T) {
	root := t.TempDir()
	if err := Render(root, testConfig(t)); err != nil {
		t.Fatalf("render: %v", err)
	}
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatalf("walk rendered project: %v", err)
	}
	sort.Strings(paths)
	actual := []byte(strings.Join(paths, "\n") + "\n")
	golden, err := os.ReadFile("testdata/project_tree.golden")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(actual, golden) {
		t.Fatalf("rendered project tree does not match golden:\n%s", actual)
	}
	for _, path := range paths {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if bytes.Contains(contents, []byte("{{")) {
			t.Fatalf("%s contains an unresolved template expression", path)
		}
	}
}

func TestRenderedGoConsumerCompilesOutsideRepository(t *testing.T) {
	root := t.TempDir()
	config := testConfig(t)
	if err := Render(root, config); err != nil {
		t.Fatalf("render: %v", err)
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = filepath.Join(root, "backend")
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("resolve generated Go consumer: %v\n%s", err, output)
	}
	command := exec.Command("go", "test", "./...")
	command.Dir = filepath.Join(root, "backend")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated Go consumer: %v\n%s", err, output)
	}
	for _, target := range []string{"darwin", "linux", "windows"} {
		t.Run("sidecar-"+target, func(t *testing.T) {
			command := exec.Command("go", "test", "-exec=true", "./cmd/sidecar")
			command.Dir = filepath.Join(root, "backend")
			command.Env = append(
				os.Environ(),
				"CGO_ENABLED=0",
				"GOOS="+target,
				"GOARCH=amd64",
			)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("generated %s Sidecar: %v\n%s", target, err, output)
			}
		})
	}
	goMod, err := os.ReadFile(filepath.Join(root, "backend", "go.mod"))
	if err != nil {
		t.Fatalf("read generated go.mod: %v", err)
	}
	if !strings.Contains(string(goMod), "module example.test/acme/starter") {
		t.Fatalf("generated go.mod = %s", goMod)
	}
	if !strings.Contains(
		string(goMod),
		"require github.com/cluion/bridra/backend v0.1.0",
	) || !strings.Contains(string(goMod), "replace github.com/cluion/bridra/backend =>") {
		t.Fatalf("generated local go.mod = %s", goMod)
	}
	pubspec, err := os.ReadFile(filepath.Join(root, "pubspec.yaml"))
	if err != nil {
		t.Fatalf("read generated pubspec: %v", err)
	}
	if !strings.Contains(string(pubspec), `bridra_flutter: "^0.1.0"`) ||
		!strings.Contains(string(pubspec), "dependency_overrides:") ||
		!strings.Contains(string(pubspec), "integration_test:") {
		t.Fatalf("generated local pubspec = %s", pubspec)
	}
	simulatorScript := filepath.Join(root, "tool", "ios_simulator_smoke.sh")
	simulatorInfo, err := os.Stat(simulatorScript)
	if err != nil {
		t.Fatalf("stat generated iOS Simulator smoke script: %v", err)
	}
	if runtime.GOOS != "windows" && simulatorInfo.Mode().Perm() != 0o755 {
		t.Fatalf("generated iOS Simulator smoke script mode = %o", simulatorInfo.Mode().Perm())
	}
	simulatorSource, err := os.ReadFile(simulatorScript)
	if err != nil {
		t.Fatalf("read generated iOS Simulator smoke script: %v", err)
	}
	if !strings.Contains(string(simulatorSource), "BRIDRA_IOS_SMOKE_STREAM=true") ||
		!strings.Contains(string(simulatorSource), "BRIDRA_IOS_SMOKE_DOWNLOAD=true") ||
		!strings.Contains(string(simulatorSource), "BRIDRA_IOS_SMOKE_UPLOAD_RESUME=true") ||
		!strings.Contains(string(simulatorSource), "--smoke-stream") ||
		!strings.Contains(string(simulatorSource), "--smoke-download") ||
		!strings.Contains(string(simulatorSource), "--smoke-upload-resume") {
		t.Fatalf("generated iOS Simulator smoke script = %s", simulatorSource)
	}
	deviceScript := filepath.Join(root, "tool", "ios_device_smoke.sh")
	deviceInfo, err := os.Stat(deviceScript)
	if err != nil {
		t.Fatalf("stat generated iOS device smoke script: %v", err)
	}
	if runtime.GOOS != "windows" && deviceInfo.Mode().Perm() != 0o755 {
		t.Fatalf("generated iOS device smoke script mode = %o", deviceInfo.Mode().Perm())
	}
	deviceSource, err := os.ReadFile(deviceScript)
	if err != nil {
		t.Fatalf("read generated iOS device smoke script: %v", err)
	}
	if !strings.Contains(string(deviceSource), "BRIDRA_IOS_SMOKE_RECONNECT=true") ||
		!strings.Contains(string(deviceSource), "BRIDRA_IOS_SMOKE_STREAM=true") ||
		!strings.Contains(string(deviceSource), "BRIDRA_IOS_SMOKE_DOWNLOAD=true") ||
		!strings.Contains(string(deviceSource), "BRIDRA_IOS_SMOKE_UPLOAD_RESUME=true") ||
		!strings.Contains(string(deviceSource), "Stopping Go HTTP backend") ||
		!strings.Contains(string(deviceSource), "--keep-app-running") ||
		!strings.Contains(string(deviceSource), "--driver=test_driver/integration_test.dart") {
		t.Fatalf("generated iOS device smoke script = %s", deviceSource)
	}
	smokeTest, err := os.ReadFile(
		filepath.Join(root, "integration_test", "ios_http_smoke_test.dart"),
	)
	if err != nil {
		t.Fatalf("read generated iOS HTTP smoke test: %v", err)
	}
	if !strings.Contains(string(smokeTest), "package:starter_app/main.dart") ||
		!strings.Contains(string(smokeTest), "Go core unavailable") ||
		!strings.Contains(string(smokeTest), "RpcStreamProgress<RpcReply>") ||
		!strings.Contains(string(smokeTest), "RpcFileReference.fromJson") ||
		!strings.Contains(string(smokeTest), "client.download(reference)") ||
		!strings.Contains(string(smokeTest), "RpcFileUpload(") ||
		!strings.Contains(string(smokeTest), "expect(openedOffsets, [0, smokeUploadInterruptAt])") {
		t.Fatalf("generated iOS HTTP smoke test = %s", smokeTest)
	}
	driver, err := os.ReadFile(filepath.Join(root, "test_driver", "integration_test.dart"))
	if err != nil {
		t.Fatalf("read generated integration test driver: %v", err)
	}
	if !strings.Contains(string(driver), "integrationDriver()") {
		t.Fatalf("generated integration test driver = %s", driver)
	}
	iosInfo, err := os.ReadFile(filepath.Join(root, "ios", "Runner", "Info.plist"))
	if err != nil {
		t.Fatalf("read generated iOS Info.plist: %v", err)
	}
	if !strings.Contains(string(iosInfo), "NSAllowsLocalNetworking") ||
		!strings.Contains(string(iosInfo), "NSLocalNetworkUsageDescription") {
		t.Fatalf("generated iOS Info.plist = %s", iosInfo)
	}
	projectMetadata, err := os.ReadFile(filepath.Join(root, ".bridra", "project.json"))
	if err != nil {
		t.Fatalf("read generated project metadata: %v", err)
	}
	var metadata struct {
		SchemaVersion    int    `json:"schemaVersion"`
		FrameworkVersion string `json:"frameworkVersion"`
		TemplateVersion  int    `json:"templateVersion"`
		ProtocolVersion  int    `json:"protocolVersion"`
	}
	if err := json.Unmarshal(projectMetadata, &metadata); err != nil {
		t.Fatalf("decode generated project metadata: %v", err)
	}
	if metadata.SchemaVersion != 2 || metadata.FrameworkVersion != "0.1.0" ||
		metadata.TemplateVersion != 2 || metadata.ProtocolVersion != 1 {
		t.Fatalf("generated project metadata = %#v", metadata)
	}
	serverSource, err := os.ReadFile(filepath.Join(root, "backend", "cmd", "server", "main.go"))
	if err != nil {
		t.Fatalf("read generated server: %v", err)
	}
	for _, expected := range []string{
		`flag.String("cors-origin", "",`,
		`"smoke-stream",`,
		`"smoke-download",`,
		`"smoke-upload-resume",`,
		`const smokeStreamMethod = "bridra.smoke.stream"`,
		`smokeDownloadMethod     = "bridra.smoke.download"`,
		`smokeUploadVerifyMethod = "bridra.smoke.upload.verify"`,
		"framework.NewJSONHTTPObserver(os.Stderr)",
		"&framework.HTTPObservationHandler{",
		"ReadHeaderTimeout: 5 * time.Second",
		"MaxHeaderBytes:    64 << 10",
	} {
		if !strings.Contains(string(serverSource), expected) {
			t.Fatalf("generated server does not contain %q:\n%s", expected, serverSource)
		}
	}
	mainDart, err := os.ReadFile(filepath.Join(root, "lib", "main.dart"))
	if err != nil {
		t.Fatalf("read generated main.dart: %v", err)
	}
	for _, expected := range []string{
		"Future<void> main([List<String> arguments = const []])",
		"DesktopSingleInstance.acquire(",
		"applicationId: 'com.example.starter_app'",
		"instance.activations.listen(_handleActivation)",
	} {
		if !strings.Contains(string(mainDart), expected) {
			t.Fatalf("generated main.dart does not contain %q:\n%s", expected, mainDart)
		}
	}
}

func TestRenderedFlutterConsumerCompilesOutsideRepository(t *testing.T) {
	root := t.TempDir()
	if err := Render(root, testConfig(t)); err != nil {
		t.Fatalf("render: %v", err)
	}
	command := exec.Command("fvm", "flutter", "test")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated Flutter consumer: %v\n%s", err, output)
	}
}

func TestRenderUsesVersionedPublishedDependenciesWithoutLocalOverrides(t *testing.T) {
	root := t.TempDir()
	config := testConfig(t)
	config.BridraGoPath = ""
	config.BridraFlutterPath = ""
	config.LocalDependencies = false
	if err := Render(root, config); err != nil {
		t.Fatalf("render: %v", err)
	}
	goMod, err := os.ReadFile(filepath.Join(root, "backend", "go.mod"))
	if err != nil {
		t.Fatalf("read generated go.mod: %v", err)
	}
	if !strings.Contains(
		string(goMod),
		"require github.com/cluion/bridra/backend v0.1.0",
	) || strings.Contains(string(goMod), "replace ") {
		t.Fatalf("generated published go.mod = %s", goMod)
	}
	pubspec, err := os.ReadFile(filepath.Join(root, "pubspec.yaml"))
	if err != nil {
		t.Fatalf("read generated pubspec: %v", err)
	}
	if !strings.Contains(string(pubspec), `bridra_flutter: "^0.1.0"`) ||
		strings.Contains(string(pubspec), "dependency_overrides:") ||
		strings.Contains(string(pubspec), "path:") {
		t.Fatalf("generated published pubspec = %s", pubspec)
	}
}

func TestRenderRejectsIncompleteConfiguration(t *testing.T) {
	err := Render(t.TempDir(), Config{ProjectName: "incomplete"})
	if err == nil || !strings.Contains(err.Error(), "is required") {
		t.Fatalf("render error = %v", err)
	}
}

func TestRenderRejectsAmbiguousLocalDependencyConfiguration(t *testing.T) {
	config := testConfig(t)
	config.LocalDependencies = false
	err := Render(t.TempDir(), config)
	if err == nil || !strings.Contains(err.Error(), "paths require local dependency mode") {
		t.Fatalf("render error = %v", err)
	}

	config.LocalDependencies = true
	config.BridraGoPath = ""
	err = Render(t.TempDir(), config)
	if err == nil || !strings.Contains(err.Error(), "Go path is required") {
		t.Fatalf("render error = %v", err)
	}
}

func testConfig(t *testing.T) Config {
	t.Helper()
	repository := repositoryRoot(t)
	return Config{
		ProjectName:          "starter_app",
		DisplayName:          "Starter App",
		Description:          "Generated test application.",
		Organization:         "com.example",
		GoModule:             "example.test/acme/starter",
		BridraGoModule:       "github.com/cluion/bridra/backend",
		BridraGoVersion:      "v0.1.0",
		BridraGoPath:         filepath.Join(repository, "backend"),
		BridraFlutterPackage: "bridra_flutter",
		BridraFlutterVersion: "^0.1.0",
		BridraFlutterPath:    filepath.Join(repository, "packages", "bridra_flutter"),
		BridraDartImport:     "package:bridra_flutter/bridra_flutter.dart",
		FlutterVersion:       "3.44.6",
		FrameworkVersion:     "0.1.0",
		TemplateVersion:      2,
		ProtocolVersion:      1,
		LocalDependencies:    true,
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve project template test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
