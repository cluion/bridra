package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

type createInvocation struct {
	directory string
	name      string
	arguments []string
}

func TestCreateBuildsAndAtomicallyPublishesProject(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "hello_app")
	var invocations []createInvocation
	system := testCreateSystem(func(
		_ context.Context,
		directory string,
		name string,
		arguments ...string,
	) ([]byte, error) {
		invocations = append(invocations, createInvocation{
			directory: directory,
			name:      name,
			arguments: append([]string(nil), arguments...),
		})
		if len(invocations) == 1 {
			if err := os.WriteFile(filepath.Join(directory, "flutter-runner.marker"), []byte("ok\n"), 0o644); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := (createCommand{system: system}).run([]string{
		"hello_app",
		"--module", "example.test/acme/hello",
		"--bridra-root", repositoryRoot(t),
		"--directory", destination,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("create: %v, stderr: %s", err, stderr.String())
	}
	if len(invocations) != 4 {
		t.Fatalf("invocations = %#v, want Flutter create, Go test, pub get, and format", invocations)
	}
	if invocations[0].name != "fvm" ||
		!containsArguments(invocations[0].arguments, "flutter", "create", "--no-pub") {
		t.Fatalf("Flutter create invocation = %#v", invocations[0])
	}
	if invocations[1].name != "go" ||
		invocations[1].directory != filepath.Join(invocations[0].directory, "backend") {
		t.Fatalf("Go verification invocation = %#v", invocations[1])
	}
	if _, err := os.Stat(filepath.Join(destination, "flutter-runner.marker")); err != nil {
		t.Fatalf("published Flutter marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "backend", "go.mod")); err != nil {
		t.Fatalf("published backend: %v", err)
	}
	goMod, err := os.ReadFile(filepath.Join(destination, "backend", "go.mod"))
	if err != nil {
		t.Fatalf("read generated go.mod: %v", err)
	}
	if !strings.Contains(
		string(goMod),
		"github.com/cluion/bridra/backend v"+releaseinfo.Version,
	) ||
		!strings.Contains(string(goMod), "replace github.com/cluion/bridra/backend =>") {
		t.Fatalf("local generated go.mod = %s", goMod)
	}
	pubspec, err := os.ReadFile(filepath.Join(destination, "pubspec.yaml"))
	if err != nil {
		t.Fatalf("read generated pubspec: %v", err)
	}
	if !strings.Contains(
		string(pubspec),
		`bridra_flutter: "^`+releaseinfo.Version+`"`,
	) ||
		!strings.Contains(string(pubspec), "dependency_overrides:") {
		t.Fatalf("local generated pubspec = %s", pubspec)
	}
	if !strings.Contains(stdout.String(), "Created Hello App.") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "hello_app" {
		t.Fatalf("parent entries = %#v", entries)
	}
}

func TestCreateDefaultsToVersionedPublishedDependencies(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "hosted_app")
	var invocations []createInvocation
	system := testCreateSystem(func(
		_ context.Context,
		directory string,
		name string,
		arguments ...string,
	) ([]byte, error) {
		invocations = append(invocations, createInvocation{
			directory: directory,
			name:      name,
			arguments: append([]string(nil), arguments...),
		})
		return nil, nil
	})
	err := (createCommand{system: system}).run([]string{
		"hosted_app",
		"--module", "example.test/acme/hosted",
		"--directory", destination,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(invocations) != 4 {
		t.Fatalf("invocations = %#v, want Flutter create, Go test, pub get, and format", invocations)
	}
	goMod, err := os.ReadFile(filepath.Join(destination, "backend", "go.mod"))
	if err != nil {
		t.Fatalf("read generated go.mod: %v", err)
	}
	if !strings.Contains(
		string(goMod),
		"github.com/cluion/bridra/backend v"+releaseinfo.Version,
	) ||
		strings.Contains(string(goMod), "replace ") {
		t.Fatalf("published generated go.mod = %s", goMod)
	}
	pubspec, err := os.ReadFile(filepath.Join(destination, "pubspec.yaml"))
	if err != nil {
		t.Fatalf("read generated pubspec: %v", err)
	}
	if !strings.Contains(
		string(pubspec),
		`bridra_flutter: "^`+releaseinfo.Version+`"`,
	) ||
		strings.Contains(string(pubspec), "dependency_overrides:") ||
		strings.Contains(string(pubspec), "path:") {
		t.Fatalf("published generated pubspec = %s", pubspec)
	}
}

func TestReleasedBridraSourceMatchesRepositoryMetadata(t *testing.T) {
	released := releasedBridraSource()
	local, err := loadBridraSource(repositoryRoot(t))
	if err != nil {
		t.Fatalf("load local source: %v", err)
	}
	if released.goModule != local.goModule ||
		released.goVersion != local.goVersion ||
		released.flutterName != local.flutterName ||
		released.flutterPackageVersion != local.flutterPackageVersion ||
		released.flutterSDKVersion != local.flutterSDKVersion {
		t.Fatalf("released source = %#v, local source = %#v", released, local)
	}
	if released.localDependencies || !local.localDependencies {
		t.Fatalf("dependency modes: released = %#v, local = %#v", released, local)
	}
}

func TestLoadBridraSourceRejectsFlutterPackageVersionMismatch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "backend"), 0o755); err != nil {
		t.Fatalf("create backend: %v", err)
	}
	flutterPath := filepath.Join(root, "packages", "bridra_flutter")
	if err := os.MkdirAll(flutterPath, 0o755); err != nil {
		t.Fatalf("create Flutter package: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "backend", "go.mod"),
		[]byte("module github.com/cluion/bridra/backend\n"),
		0o644,
	); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(flutterPath, "pubspec.yaml"),
		[]byte("name: bridra_flutter\nversion: 0.2.0\n"),
		0o644,
	); err != nil {
		t.Fatalf("write pubspec: %v", err)
	}
	_, err := loadBridraSource(root)
	if !errors.Is(err, errCreateInvalid) || !strings.Contains(err.Error(), "does not match CLI version") {
		t.Fatalf("load source error = %v", err)
	}
}

func TestCreateCleansStagingDirectoryAfterFailure(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "broken_app")
	system := testCreateSystem(func(
		_ context.Context,
		directory string,
		_ string,
		_ ...string,
	) ([]byte, error) {
		if err := os.WriteFile(filepath.Join(directory, "partial"), []byte("partial"), 0o644); err != nil {
			return nil, err
		}
		return []byte("Flutter create failed"), errors.New("exit status 1")
	})
	err := (createCommand{system: system}).run([]string{
		"broken_app",
		"--module", "example.test/acme/broken",
		"--bridra-root", repositoryRoot(t),
		"--directory", destination,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Flutter create failed") {
		t.Fatalf("create error = %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination should not exist, stat error = %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging directory leaked: %#v", entries)
	}
}

func TestCreateRejectsInvalidNameAndExistingDestination(t *testing.T) {
	parent := t.TempDir()
	system := testCreateSystem(func(
		context.Context,
		string,
		string,
		...string,
	) ([]byte, error) {
		t.Fatal("process runner must not be called")
		return nil, nil
	})
	command := createCommand{system: system}
	tests := []struct {
		name        string
		projectName string
		destination string
		want        string
	}{
		{
			name: "invalid Dart package name", projectName: "Bad-Name",
			destination: filepath.Join(parent, "invalid"), want: "lower_snake_case",
		},
		{
			name: "existing destination", projectName: "existing_app",
			destination: filepath.Join(parent, "existing"), want: "already exists",
		},
	}
	if err := os.Mkdir(tests[1].destination, 0o755); err != nil {
		t.Fatalf("create collision: %v", err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := command.run([]string{
				test.projectName,
				"--module", "example.test/acme/app",
				"--bridra-root", repositoryRoot(t),
				"--directory", test.destination,
			}, &bytes.Buffer{}, &bytes.Buffer{})
			if !errors.Is(err, errCreateInvalid) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func testCreateSystem(
	runner func(context.Context, string, string, ...string) ([]byte, error),
) createSystem {
	return createSystem{
		timeout:   time.Second,
		abs:       filepath.Abs,
		stat:      os.Stat,
		lstat:     os.Lstat,
		mkdirTemp: os.MkdirTemp,
		removeAll: os.RemoveAll,
		rename:    os.Rename,
		run:       runner,
	}
}

func containsArguments(arguments []string, expected ...string) bool {
	joined := strings.Join(arguments, " ")
	return strings.Contains(joined, strings.Join(expected, " "))
}
