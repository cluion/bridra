package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/codegen"
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
				"schema",
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
		{command: "schema", usage: "bridra schema check --against path [options]"},
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
	formatCalls := 0
	command := generateCommand{formatDart: func(_ string, source []byte) ([]byte, error) {
		formatCalls++
		return append(append([]byte(nil), source...), "\n// canonical formatter\n"...), nil
	}}

	if err := command.run([]string{
		"--schema", schema,
		"--root", root,
	}, &stdout, &stderr); err != nil {
		t.Fatalf("generate: %v, stderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Generated lib/api/generated/bridra_api.g.dart") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	dartOutput, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(codegen.DartClientPath)))
	if err != nil {
		t.Fatalf("read Dart output: %v", err)
	}
	if !bytes.Contains(dartOutput, []byte("// canonical formatter")) {
		t.Fatalf("Dart output was not canonicalized: %s", dartOutput)
	}

	stdout.Reset()
	stderr.Reset()
	if err := command.run([]string{
		"--schema", schema,
		"--root", root,
		"--check",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("check: %v, stderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "up to date") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if formatCalls != 2 {
		t.Fatalf("Dart formatter calls = %d, want 2", formatCalls)
	}
}

func TestGenerateCommandReportsDartFormatterFailureBeforeWriting(t *testing.T) {
	root := t.TempDir()
	schema := filepath.Join(repositoryRoot(t), "schema", "bridra.json")
	command := generateCommand{formatDart: func(_ string, _ []byte) ([]byte, error) {
		return nil, errors.New("formatter unavailable")
	}}

	err := command.run([]string{"--schema", schema, "--root", root}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "formatter unavailable") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(codegen.DartClientPath))); !os.IsNotExist(statErr) {
		t.Fatalf("Dart output exists after formatter failure: %v", statErr)
	}
}

func TestDartFormatterCanonicalizesClurivaShapes(t *testing.T) {
	if os.Getenv("BRIDRA_DART_FORMATTER_INTEGRATION") != "1" {
		t.Skip("set BRIDRA_DART_FORMATTER_INTEGRATION=1 to run the pinned Dart formatter")
	}
	repository := repositoryRoot(t)
	root, err := os.MkdirTemp(repository, ".bridra-dart-format-test-*")
	if err != nil {
		t.Fatalf("create project root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove project root: %v", err)
		}
	})

	schema := codegen.Schema{
		SchemaVersion:   codegen.SupportedSchemaVersion,
		ProtocolVersion: 1,
		Methods: []codegen.Method{
			{
				Name:       "transfers.cancel",
				ClientName: "cancelTransferWithExpectedRevision",
				Params: &codegen.Object{
					GoType:   "CancelTransferRequest",
					DartType: "CancelTransferRequest",
					Fields: []codegen.Field{
						{Name: "jobId", Type: "string"},
						{Name: "expectedRevision", Type: "string"},
					},
				},
				Result: codegen.Object{
					GoType:   "CancelTransferResponse",
					DartType: "CancelTransferResult",
					Fields: []codegen.Field{
						{Name: "accepted", Type: "boolean"},
						{
							Name: "cancelRequestedAt", Type: "string",
							Format: "date-time", Nullable: true,
						},
						{Name: "preview", Type: "file", Nullable: true},
					},
				},
			},
		},
	}
	schemaContents, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		t.Fatalf("encode schema: %v", err)
	}
	schemaPath := filepath.Join(root, "schema.json")
	if err := os.WriteFile(schemaPath, append(schemaContents, '\n'), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	rawOutputs, err := codegen.Generate(schema)
	if err != nil {
		t.Fatalf("generate raw outputs: %v", err)
	}
	rawDart := generatedOutput(t, rawOutputs, codegen.DartClientPath)

	command := newGenerateCommand()
	if err := command.run(
		[]string{"--schema", schemaPath, "--root", root},
		io.Discard,
		io.Discard,
	); err != nil {
		t.Fatalf("generate canonical outputs: %v", err)
	}
	generatedPath := filepath.Join(root, filepath.FromSlash(codegen.DartClientPath))
	canonicalDart, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read canonical Dart output: %v", err)
	}
	if bytes.Equal(rawDart, canonicalDart) {
		t.Fatal("Cluriva regression fixture did not require Dart canonical formatting")
	}
	if err := command.run(
		[]string{"--schema", schemaPath, "--root", root, "--check"},
		io.Discard,
		io.Discard,
	); err != nil {
		t.Fatalf("check canonical outputs: %v", err)
	}
	formattedAgain, err := formatDartWithFVM(root, canonicalDart)
	if err != nil {
		t.Fatalf("format canonical Dart output again: %v", err)
	}
	if !bytes.Equal(canonicalDart, formattedAgain) {
		t.Fatal("canonical Dart output is not formatter-idempotent")
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(root, ".bridra-dart-format-*.dart"))
	if err != nil {
		t.Fatalf("find temporary Dart sources: %v", err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary Dart sources remain: %v", temporaryFiles)
	}
}

func generatedOutput(t *testing.T, outputs []codegen.Output, path string) []byte {
	t.Helper()
	for _, output := range outputs {
		if output.Path == path {
			return output.Content
		}
	}
	t.Fatalf("generated output %s not found", path)
	return nil
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve CLI test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}
