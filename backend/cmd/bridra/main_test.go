package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

func TestHelpListsRegisteredCommands(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
	}{
		{name: "no arguments"},
		{name: "help", arguments: []string{"help"}},
		{name: "long help flag", arguments: []string{"--help"}},
		{name: "short help flag", arguments: []string{"-h"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if err := run(test.arguments, &stdout, &stderr); err != nil {
				t.Fatalf("run: %v", err)
			}
			for _, expected := range []string{
				"Bridra " + releaseinfo.Version,
				"version",
				"create",
				"upgrade",
				"release",
				"generate",
				"doctor",
				"diagnose",
				"make",
				"dev",
				"build",
			} {
				if !strings.Contains(stdout.String(), expected) {
					t.Fatalf("stdout = %q, want %q", stdout.String(), expected)
				}
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestHelpDescribesSpecificCommand(t *testing.T) {
	for _, test := range []struct {
		command string
		usage   string
	}{
		{
			command: "create",
			usage:   "github.com/cluion/bridra/backend v" + releaseinfo.Version,
		},
		{command: "version", usage: "bridra version [--json]"},
		{command: "upgrade", usage: "bridra upgrade [--plan | --apply] [--to version]"},
		{command: "release", usage: "bridra release prepare <version> [--root path]"},
		{command: "doctor", usage: "bridra doctor [--root path] [--strict]"},
		{command: "diagnose", usage: "bridra diagnose [--root path] [--output path] [--runtime path]"},
		{command: "dev", usage: "bridra dev [options]"},
		{command: "build", usage: "bridra build <target> [options]"},
	} {
		t.Run(test.command, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if err := run([]string{"help", test.command}, &stdout, &stderr); err != nil {
				t.Fatalf("run: %v", err)
			}
			if !strings.Contains(stdout.String(), test.usage) {
				t.Fatalf("stdout = %q", stdout.String())
			}
		})
	}
}

func TestUnknownCommandReturnsUsageError(t *testing.T) {
	err := run([]string{"unknown"}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, errUsage) {
		t.Fatalf("error = %v, want errUsage", err)
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("error = %v", err)
	}
}

func TestGenerateCommandWritesAndChecksProjectOutputs(t *testing.T) {
	root := t.TempDir()
	schema := filepath.Join(repositoryRoot(t), "schema", "bridra.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := run([]string{
		"generate",
		"--schema", schema,
		"--root", root,
	}, &stdout, &stderr); err != nil {
		t.Fatalf("generate: %v, stderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Generated lib/api/generated/bridra_api.g.dart") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := run([]string{
		"generate",
		"--schema", schema,
		"--root", root,
		"--check",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("check: %v, stderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "up to date") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve CLI test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}
