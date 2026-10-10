package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaBaselineUpdateDefaultsAndKeepsCompatibilityGate(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir("schema", 0o755); err != nil {
		t.Fatal(err)
	}
	baselinePath := writeSchemaCheckFixture(t, "schema", "bridra.baseline.json", schemaCheckFixture(1))
	baselineInfo, err := os.Stat(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	// Windows exposes writable/read-only modes rather than Unix permission bits.
	originalMode := baselineInfo.Mode().Perm()
	current := schemaCheckFixture(1)
	current.Methods[0].Result.Fields[0].Type = "boolean"
	currentPath := writeSchemaCheckFixture(t, "schema", "bridra.json", current)
	original, _ := os.ReadFile(baselinePath)
	if err := run([]string{"schema", "baseline"}, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, errUsage) {
		t.Fatalf("missing --update: %v", err)
	}
	unchanged, _ := os.ReadFile(baselinePath)
	if !bytes.Equal(original, unchanged) {
		t.Fatal("baseline changed without --update")
	}
	check := []string{"schema", "check", "--against", baselinePath, "--schema", currentPath}
	if err := run(check, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, errSchemaIncompatible) {
		t.Fatalf("compatibility gate before explicit update: %v", err)
	}
	var stdout bytes.Buffer
	if err := run([]string{"schema", "baseline", "--update"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "protocolVersion 1") {
		t.Fatalf("output: %s", stdout.String())
	}
	want, _ := os.ReadFile(currentPath)
	got, _ := os.ReadFile(baselinePath)
	if !bytes.Equal(got, want) {
		t.Fatal("baseline is not the exact validated source snapshot")
	}
	if err := run(check, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("compatibility gate after explicit update: %v", err)
	}
	info, err := os.Stat(baselinePath)
	if err != nil || info.Mode().Perm() != originalMode {
		t.Fatalf("existing permissions not preserved (want %v): %v, %v", originalMode, info, err)
	}
}

func TestSchemaBaselineRejectsInvalidSnapshotWithoutReplacingBaseline(t *testing.T) {
	for _, invalid := range []string{
		`{"schemaVersion":1,"protocolVersion":1,"methods":[]}`,
		`{"unknown":true}`,
		`{`,
	} {
		t.Run(invalid, func(t *testing.T) {
			root := t.TempDir()
			baseline := writeSchemaCheckFixture(t, root, "baseline.json", schemaCheckFixture(1))
			original, _ := os.ReadFile(baseline)
			current := filepath.Join(root, "current.json")
			if err := os.WriteFile(current, []byte(invalid), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := updateSchemaBaseline(current, baseline); err == nil {
				t.Fatal("invalid schema accepted")
			}
			got, _ := os.ReadFile(baseline)
			if !bytes.Equal(got, original) {
				t.Fatal("invalid schema replaced baseline")
			}
			leftovers, err := filepath.Glob(filepath.Join(root, ".bridra-baseline-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("temporary files remain: %v, %v", leftovers, err)
			}
		})
	}
}

func TestSchemaBaselineCustomPathsAndFileGuards(t *testing.T) {
	root := t.TempDir()
	current := writeSchemaCheckFixture(t, root, "current.json", schemaCheckFixture(2))
	baseline := filepath.Join(root, "new-baseline.json")
	if err := run([]string{"schema", "baseline", "--update", "--schema", current, "--baseline", baseline}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{current, root} {
		if _, err := updateSchemaBaseline(current, path); err == nil {
			t.Fatalf("unsafe baseline path accepted: %s", path)
		}
	}
	link := filepath.Join(root, "baseline-link.json")
	if err := os.Symlink(baseline, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := updateSchemaBaseline(current, link); err == nil {
		t.Fatal("symlink baseline accepted")
	}
}
