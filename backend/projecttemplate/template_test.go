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

	"github.com/cluion/bridra/backend/codegen"
	"github.com/cluion/bridra/backend/framework"
	"github.com/cluion/bridra/backend/internal/releaseinfo"
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

func TestRenderInitializesSchemaBaselineOnce(t *testing.T) {
	root := t.TempDir()
	config := testConfig(t)
	config.ProtocolVersion = 3
	if err := Render(root, config); err != nil {
		t.Fatalf("render: %v", err)
	}
	currentPath := filepath.Join(root, "schema", "bridra.json")
	baselinePath := filepath.Join(root, filepath.FromSlash(schemaBaselinePath))
	current := readProjectTemplateTestFile(t, currentPath)
	baseline := readProjectTemplateTestFile(t, baselinePath)
	if !bytes.Equal(baseline, current) {
		t.Fatalf("initial baseline differs from current schema:\nbaseline: %s\ncurrent: %s", baseline, current)
	}
	loaded, err := codegen.LoadSchema(baselinePath)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	if loaded.ProtocolVersion != 3 {
		t.Fatalf("baseline protocol = %d, want custom application protocol 3", loaded.ProtocolVersion)
	}

	reviewed := append([]byte(nil), baseline...)
	reviewed = append(reviewed, '\n')
	if err := os.WriteFile(baselinePath, reviewed, 0o644); err != nil {
		t.Fatalf("mark reviewed baseline: %v", err)
	}
	if err := Render(root, config); err != nil {
		t.Fatalf("rerender: %v", err)
	}
	if actual := readProjectTemplateTestFile(t, baselinePath); !bytes.Equal(actual, reviewed) {
		t.Fatalf("rerender overwrote application-owned baseline:\n%s", actual)
	}
}

func TestInitializeSchemaBaselineRequiresCurrentSchema(t *testing.T) {
	err := initializeSchemaBaseline(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "read current schema for baseline") {
		t.Fatalf("initialize baseline error = %v", err)
	}
}

func TestRenderedCustomProtocolProjectVerifyEnforcesSchemaBaseline(t *testing.T) {
	root := t.TempDir()
	config := testConfig(t)
	config.BridraGoVersion = "v" + releaseinfo.Version
	config.BridraFlutterVersion = releaseinfo.Version
	config.FrameworkVersion = releaseinfo.Version
	config.ProtocolVersion = 3
	if config.ProtocolVersion <= framework.ProtocolVersion {
		t.Fatalf(
			"custom protocol %d must exceed template baseline %d",
			config.ProtocolVersion,
			framework.ProtocolVersion,
		)
	}
	if err := Render(root, config); err != nil {
		t.Fatalf("render: %v", err)
	}
	runProjectTemplateCommand(t, filepath.Join(root, "backend"), "go", "mod", "tidy")
	runProjectTemplateCommand(t, root, "fvm", "flutter", "pub", "get")
	runProjectTemplateCommand(t, root, "make", "verify")

	baselinePath := filepath.Join(root, filepath.FromSlash(schemaBaselinePath))
	baselineBefore := readProjectTemplateTestFile(t, baselinePath)
	currentPath := filepath.Join(root, "schema", "bridra.json")
	current, err := codegen.LoadSchema(currentPath)
	if err != nil {
		t.Fatalf("load current schema: %v", err)
	}
	current.Methods[0].Result.Fields[0].Type = "boolean"
	contents, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		t.Fatalf("encode breaking schema: %v", err)
	}
	if err := os.WriteFile(currentPath, append(contents, '\n'), 0o644); err != nil {
		t.Fatalf("write breaking schema: %v", err)
	}

	command := exec.Command("make", "schema-check")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("schema-check accepted an unversioned breaking change:\n%s", output)
	}
	for _, expected := range []string{"Status: incompatible", "Required protocolVersion: 4 or newer"} {
		if !bytes.Contains(output, []byte(expected)) {
			t.Fatalf("schema-check output = %s, want %q", output, expected)
		}
	}
	if actual := readProjectTemplateTestFile(t, baselinePath); !bytes.Equal(actual, baselineBefore) {
		t.Fatalf("schema-check modified application-owned baseline:\n%s", actual)
	}
}

func runProjectTemplateCommand(t *testing.T, directory, name string, arguments ...string) {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(arguments, " "), err, output)
	}
}

func readProjectTemplateTestFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return contents
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
	if !strings.Contains(string(simulatorSource), "BRIDRA_SMOKE_STREAM=true") ||
		!strings.Contains(string(simulatorSource), "BRIDRA_SMOKE_DOWNLOAD=true") ||
		!strings.Contains(string(simulatorSource), "BRIDRA_SMOKE_UPLOAD_RESUME=true") ||
		!strings.Contains(string(simulatorSource), "BRIDRA_IOS_SIMULATOR_TIMEOUT_SECONDS") ||
		!strings.Contains(string(simulatorSource), "BRIDRA_IOS_SIMULATOR_NO_PROGRESS_SECONDS") ||
		!strings.Contains(string(simulatorSource), "BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_DIR") ||
		!strings.Contains(string(simulatorSource), "--smoke-stream") ||
		!strings.Contains(string(simulatorSource), "--smoke-download") ||
		!strings.Contains(string(simulatorSource), "--smoke-download-resume") ||
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
	if !strings.Contains(string(deviceSource), "BRIDRA_SMOKE_RECONNECT=true") ||
		!strings.Contains(string(deviceSource), "BRIDRA_SMOKE_STREAM=true") ||
		!strings.Contains(string(deviceSource), "BRIDRA_SMOKE_DOWNLOAD=true") ||
		!strings.Contains(string(deviceSource), "BRIDRA_SMOKE_UPLOAD_RESUME=true") ||
		!strings.Contains(string(deviceSource), "--smoke-download-resume") ||
		!strings.Contains(string(deviceSource), "Stopping Go HTTP backend") ||
		!strings.Contains(string(deviceSource), "--keep-app-running") ||
		!strings.Contains(string(deviceSource), "--driver=test_driver/integration_test.dart") {
		t.Fatalf("generated iOS device smoke script = %s", deviceSource)
	}
	androidScript := filepath.Join(root, "tool", "android_emulator_smoke.sh")
	androidInfo, err := os.Stat(androidScript)
	if err != nil {
		t.Fatalf("stat generated Android Emulator smoke script: %v", err)
	}
	if runtime.GOOS != "windows" && androidInfo.Mode().Perm() != 0o755 {
		t.Fatalf("generated Android Emulator smoke script mode = %o", androidInfo.Mode().Perm())
	}
	androidSource, err := os.ReadFile(androidScript)
	if err != nil {
		t.Fatalf("read generated Android Emulator smoke script: %v", err)
	}
	if !strings.Contains(string(androidSource), "BRIDRA_SMOKE_RECONNECT=true") ||
		!strings.Contains(string(androidSource), "BRIDRA_SMOKE_STREAM=true") ||
		!strings.Contains(string(androidSource), "BRIDRA_SMOKE_DOWNLOAD=true") ||
		!strings.Contains(string(androidSource), "BRIDRA_SMOKE_UPLOAD_RESUME=true") ||
		!strings.Contains(string(androidSource), "http://10.0.2.2:") ||
		!strings.Contains(string(androidSource), "Stopping Go HTTP backend") {
		t.Fatalf("generated Android Emulator smoke script = %s", androidSource)
	}
	smokeTest, err := os.ReadFile(
		filepath.Join(root, "integration_test", "http_smoke_test.dart"),
	)
	if err != nil {
		t.Fatalf("read generated platform HTTP smoke test: %v", err)
	}
	if !strings.Contains(string(smokeTest), "package:starter_app/main.dart") ||
		!strings.Contains(string(smokeTest), "Go core unavailable") ||
		!strings.Contains(string(smokeTest), "RpcStreamProgress<RpcReply>") ||
		!strings.Contains(string(smokeTest), "RpcFileReference.fromJson") ||
		!strings.Contains(string(smokeTest), "client.download(reference)") ||
		!strings.Contains(string(smokeTest), "RpcFileUpload(") ||
		!strings.Contains(string(smokeTest), "expect(openedOffsets, [0, smokeUploadInterruptAt])") {
		t.Fatalf("generated platform HTTP smoke test = %s", smokeTest)
	}
	driver, err := os.ReadFile(filepath.Join(root, "test_driver", "integration_test.dart"))
	if err != nil {
		t.Fatalf("read generated integration test driver: %v", err)
	}
	if !strings.Contains(string(driver), "integrationDriver()") {
		t.Fatalf("generated integration test driver = %s", driver)
	}
	for _, manifestPath := range []string{
		filepath.Join(root, "android", "app", "src", "debug", "AndroidManifest.xml"),
		filepath.Join(root, "android", "app", "src", "profile", "AndroidManifest.xml"),
	} {
		androidManifest, readErr := os.ReadFile(manifestPath)
		if readErr != nil {
			t.Fatalf("read generated Android development manifest: %v", readErr)
		}
		if !strings.Contains(string(androidManifest), `android:usesCleartextTraffic="true"`) {
			t.Fatalf("generated Android development manifest = %s", androidManifest)
		}
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
		`"smoke-download-resume",`,
		`"smoke-upload-resume",`,
		`const smokeStreamMethod = "bridra.smoke.stream"`,
		`smokeDownloadMethod`,
		`type smokeDownloadResumeHandler struct`,
		`smokeUploadVerifyMethod`,
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
