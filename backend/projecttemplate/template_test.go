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
		!strings.Contains(string(pubspec), "dependency_overrides:") {
		t.Fatalf("generated local pubspec = %s", pubspec)
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
