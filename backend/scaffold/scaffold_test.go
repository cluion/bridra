package scaffold

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/projecttemplate"
)

func TestKindsMatchManifest(t *testing.T) {
	kinds, err := Kinds()
	if err != nil {
		t.Fatalf("kinds: %v", err)
	}
	want := []string{
		"controller", "middleware", "model", "provider",
		"request", "response", "service", "test",
	}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
}

func TestGenerateAllScaffoldsMatchesGoldenAndCompiles(t *testing.T) {
	root := renderProject(t)
	components := []struct {
		kind string
		name string
	}{
		{kind: "controller", name: "User"},
		{kind: "service", name: "Billing"},
		{kind: "middleware", name: "Audit"},
		{kind: "request", name: "CreateUser"},
		{kind: "model", name: "Account"},
		{kind: "response", name: "Account"},
		{kind: "provider", name: "Metrics"},
		{kind: "test", name: "ApplicationSmoke"},
	}
	var paths []string
	for _, component := range components {
		results, err := Generate(Config{
			Root:            root,
			Kind:            component.kind,
			Name:            component.name,
			FrameworkModule: "github.com/cluion/bridra/backend",
		})
		if err != nil {
			t.Fatalf("generate %s: %v", component.kind, err)
		}
		for _, result := range results {
			if result.Replaced {
				t.Fatalf("%s unexpectedly replaced a file", result.Path)
			}
			paths = append(paths, result.Path)
		}
	}
	sort.Strings(paths)
	actual := []byte(strings.Join(paths, "\n") + "\n")
	golden, err := os.ReadFile("testdata/scaffold_tree.golden")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(actual, golden) {
		t.Fatalf("scaffold tree does not match golden:\n%s", actual)
	}

	command := exec.Command("go", "test", "./...")
	command.Dir = filepath.Join(root, "backend")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated consumer: %v\n%s", err, output)
	}
}

func TestGenerateRejectsCollisionAndForceReplacesAllFiles(t *testing.T) {
	root := t.TempDir()
	config := Config{
		Root: root, Kind: "controller", Name: "User",
		FrameworkModule: "github.com/cluion/bridra/backend",
	}
	results, err := Generate(config)
	if err != nil {
		t.Fatalf("first generate: %v", err)
	}
	first := filepath.Join(root, filepath.FromSlash(results[0].Path))
	if err := os.WriteFile(first, []byte("sentinel\n"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	_, err = Generate(config)
	if !errors.Is(err, ErrCollision) {
		t.Fatalf("collision error = %v, want ErrCollision", err)
	}
	contents, readError := os.ReadFile(first)
	if readError != nil || string(contents) != "sentinel\n" {
		t.Fatalf("collision changed first file: %q, %v", contents, readError)
	}

	config.Force = true
	results, err = Generate(config)
	if err != nil {
		t.Fatalf("force generate: %v", err)
	}
	for _, result := range results {
		if !result.Replaced {
			t.Fatalf("%s was not reported as replaced", result.Path)
		}
	}
	contents, err = os.ReadFile(first)
	if err != nil || bytes.Equal(contents, []byte("sentinel\n")) {
		t.Fatalf("force did not replace first file: %q, %v", contents, err)
	}
}

func TestGenerateRollsBackForceWhenPublishFails(t *testing.T) {
	root := t.TempDir()
	config := Config{
		Root: root, Kind: "controller", Name: "User",
		FrameworkModule: "github.com/cluion/bridra/backend",
	}
	results, err := Generate(config)
	if err != nil {
		t.Fatalf("seed scaffold: %v", err)
	}
	want := map[string][]byte{}
	for index, result := range results {
		path := filepath.Join(root, filepath.FromSlash(result.Path))
		contents := []byte{byte('a' + index), '\n'}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
		want[path] = contents
	}

	operations := defaultFileOperations()
	rename := operations.rename
	publishes := 0
	failed := false
	operations.rename = func(source, destination string) error {
		if strings.Contains(source, ".bridra-scaffold-") &&
			strings.Contains(source, string(filepath.Separator)+"files"+string(filepath.Separator)) {
			publishes++
			if publishes == 2 && !failed {
				failed = true
				return errors.New("injected publish failure")
			}
		}
		return rename(source, destination)
	}
	config.Force = true
	_, err = generate(config, operations)
	if err == nil || !strings.Contains(err.Error(), "injected publish failure") {
		t.Fatalf("generate error = %v", err)
	}
	for path, expected := range want {
		contents, readError := os.ReadFile(path)
		if readError != nil || !bytes.Equal(contents, expected) {
			t.Fatalf("rollback %s = %q, %v; want %q", path, contents, readError, expected)
		}
	}
	matches, err := filepath.Glob(filepath.Join(root, ".bridra-scaffold-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("staging leftovers = %v, %v", matches, err)
	}
}

func TestGenerateRejectsInvalidRequests(t *testing.T) {
	tests := []Config{
		{Root: t.TempDir(), Kind: "unknown", Name: "User", FrameworkModule: "example.com/framework"},
		{Root: t.TempDir(), Kind: "model", Name: "user", FrameworkModule: "example.com/framework"},
		{Root: "", Kind: "model", Name: "User", FrameworkModule: "example.com/framework"},
	}
	for _, config := range tests {
		if _, err := Generate(config); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("Generate(%#v) error = %v", config, err)
		}
	}
}

func TestSnakeCase(t *testing.T) {
	tests := map[string]string{
		"User": "user", "CreateUser": "create_user", "HTTPClient": "http_client",
	}
	for input, want := range tests {
		if got := snakeCase(input); got != want {
			t.Fatalf("snakeCase(%q) = %q, want %q", input, got, want)
		}
	}
}

func renderProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repository := repositoryRoot(t)
	err := projecttemplate.Render(root, projecttemplate.Config{
		ProjectName: "starter_app", DisplayName: "Starter App",
		Description: "Scaffold integration test.", Organization: "com.example",
		GoModule: "example.test/acme/starter", BridraGoModule: "github.com/cluion/bridra/backend",
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
	})
	if err != nil {
		t.Fatalf("render project: %v", err)
	}
	return root
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve scaffold test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
