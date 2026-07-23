package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRunWritesPassingSummary(t *testing.T) {
	root := coverageTestRoot(t, 5, 4)
	var stdout bytes.Buffer
	if err := run([]string{"--root", root}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("coverage check: %v", err)
	}
	for _, expected := range []string{"Example Go", "80.00%", "Example Dart", "pass"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("summary = %q, want %q", stdout.String(), expected)
		}
	}
	summary, err := os.ReadFile(filepath.Join(root, "coverage", "summary.md"))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if !bytes.Equal(summary, stdout.Bytes()) {
		t.Fatalf("written summary differs from stdout")
	}
}

func TestRunFailsAfterWritingBelowMinimumSummary(t *testing.T) {
	root := coverageTestRoot(t, 5, 3)
	var stdout bytes.Buffer
	err := run([]string{"--root", root}, &stdout, &bytes.Buffer{})
	if !errors.Is(err, errCoverageBelowMinimum) {
		t.Fatalf("coverage error = %v", err)
	}
	if !strings.Contains(stdout.String(), "fail") {
		t.Fatalf("summary = %q", stdout.String())
	}
}

func TestParsersRejectMalformedProfiles(t *testing.T) {
	root := t.TempDir()
	goProfile := filepath.Join(root, "go.out")
	if err := os.WriteFile(goProfile, []byte("not a profile\n"), 0o644); err != nil {
		t.Fatalf("write Go profile: %v", err)
	}
	if _, err := parseGoProfile(goProfile); err == nil {
		t.Fatal("malformed Go profile was accepted")
	}
	lcovProfile := filepath.Join(root, "lcov.info")
	if err := os.WriteFile(lcovProfile, []byte("LF:1\nLH:2\n"), 0o644); err != nil {
		t.Fatalf("write LCOV profile: %v", err)
	}
	if _, err := parseLCOV(lcovProfile); err == nil {
		t.Fatal("malformed LCOV profile was accepted")
	}
}

func coverageTestRoot(t *testing.T, total, covered int) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tool"), 0o755); err != nil {
		t.Fatalf("create tool directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "coverage"), 0o755); err != nil {
		t.Fatalf("create coverage directory: %v", err)
	}
	config := `{
  "schemaVersion": 1,
  "goProfile": "coverage/go.out",
  "goPackages": [
    {"name": "Example Go", "package": "example.test/pkg", "minimum": 80}
  ],
  "lcovProfiles": [
    {"name": "Example Dart", "profile": "coverage/lcov.info", "minimum": 80}
  ]
}
`
	if err := os.WriteFile(
		filepath.Join(root, "tool", "coverage_thresholds.json"),
		[]byte(config),
		0o644,
	); err != nil {
		t.Fatalf("write configuration: %v", err)
	}
	goProfile := "mode: atomic\nexample.test/pkg/file.go:1.1,2.1 " +
		strconv.Itoa(total) + " 1\n"
	if err := os.WriteFile(filepath.Join(root, "coverage", "go.out"), []byte(goProfile), 0o644); err != nil {
		t.Fatalf("write Go profile: %v", err)
	}
	lcov := "SF:lib/example.dart\nLF:" + strconv.Itoa(total) +
		"\nLH:" + strconv.Itoa(covered) + "\nend_of_record\n"
	if err := os.WriteFile(filepath.Join(root, "coverage", "lcov.info"), []byte(lcov), 0o644); err != nil {
		t.Fatalf("write LCOV profile: %v", err)
	}
	return root
}
