package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cluion/bridra/backend/codegen"
)

func TestGenerateWebUsesTargetRootWithoutDart(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".bridra", "schema"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	const metadata = `{"schemaVersion":4,"projectName":"portal","goModule":"example.com/portal","frameworkModule":"github.com/cluion/bridra/backend","frameworkVersion":"0.23.2","templateVersion":9,"protocolVersion":1,"platforms":["web"],"ui":{"mode":"web","preset":"vue","dist":"frontend/dist"}}`
	if err := os.WriteFile(filepath.Join(root, ".bridra/project.json"), []byte(metadata), 0644); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("../../../schema/bridra.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "schema/bridra.json"), schema, 0644); err != nil {
		t.Fatal(err)
	}
	command := generateCommand{formatDart: func(string, []byte) ([]byte, error) { t.Fatal("Web generation invoked Dart"); return nil, nil }}
	args := []string{"--root", root}
	if err := command.run(args, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := command.run(append(args, "--check"), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, codegen.TypeScriptClientPath), []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := command.run(append(args, "--check"), io.Discard, io.Discard); !errors.Is(err, codegen.ErrGeneratedFilesStale) {
		t.Fatalf("expected stale TypeScript, got %v", err)
	}
}
