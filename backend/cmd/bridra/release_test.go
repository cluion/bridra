package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleasePrepareSynchronizesManagedVersionsWithoutPublishing(t *testing.T) {
	root := releaseTestRoot(t, "0.1.0", "2026-07-23")
	var stdout bytes.Buffer
	if err := (releaseCommand{}).run(
		[]string{"prepare", "0.1.1", "--root", root},
		&stdout,
		&bytes.Buffer{},
	); err != nil {
		t.Fatalf("release prepare: %v", err)
	}

	for _, path := range []string{
		"VERSION",
		"backend/framework/protocol.go",
		"packages/bridra_flutter/pubspec.yaml",
		"pubspec.yaml",
		".bridra/project.json",
		"docs/ARCHITECTURE.md",
		"docs/GUIDE.md",
		"README.md",
		"docs/RELEASING.md",
		"docs/UPGRADING.md",
	} {
		contents := readReleaseTestFile(t, root, path)
		if !strings.Contains(contents, "0.1.1") {
			t.Fatalf("%s = %q, want 0.1.1", path, contents)
		}
	}
	for _, test := range []struct {
		path       string
		heading    string
		oldHeading string
	}{
		{
			path:       "CHANGELOG.md",
			heading:    "## [0.1.1] - Unreleased",
			oldHeading: "## [0.1.0] - 2026-07-23",
		},
		{
			path:       "packages/bridra_flutter/CHANGELOG.md",
			heading:    "## 0.1.1 - Unreleased",
			oldHeading: "## 0.1.0 - 2026-07-23",
		},
	} {
		contents := readReleaseTestFile(t, root, test.path)
		if !strings.Contains(contents, test.heading) ||
			!strings.Contains(contents, test.oldHeading) {
			t.Fatalf("%s = %q", test.path, contents)
		}
	}
	for _, expected := range []string{
		"Bridra Release 0.1.1",
		"Tag: backend/v0.1.1",
		"No tag, package, or release was published.",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), expected)
		}
	}

	stdout.Reset()
	if err := (releaseCommand{}).run(
		[]string{"check", "--root", root, "--version", "0.1.1"},
		&stdout,
		&bytes.Buffer{},
	); err != nil {
		t.Fatalf("release check: %v", err)
	}
	if !strings.Contains(stdout.String(), "All managed release surfaces agree.") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestReleasePrepareIsIdempotentForCurrentUnreleasedVersion(t *testing.T) {
	root := releaseTestRoot(t, "0.1.0", "Unreleased")
	before := releaseTestSnapshot(t, root)
	var stdout bytes.Buffer
	if err := (releaseCommand{}).run(
		[]string{"prepare", "0.1.0", "--root=" + root},
		&stdout,
		&bytes.Buffer{},
	); err != nil {
		t.Fatalf("release prepare: %v", err)
	}
	after := releaseTestSnapshot(t, root)
	if before != after {
		t.Fatal("idempotent prepare modified the release fixture")
	}
	if !strings.Contains(stdout.String(), "already prepared") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestReleaseFinalCheckRequiresDatedChangelogs(t *testing.T) {
	t.Run("finalized", func(t *testing.T) {
		root := releaseTestRoot(t, "0.1.0", "2026-07-24")
		var stdout bytes.Buffer
		err := (releaseCommand{}).run(
			[]string{"check", "--final", "--root", root, "--version", "0.1.0"},
			&stdout,
			&bytes.Buffer{},
		)
		if err != nil {
			t.Fatalf("release final check: %v", err)
		}
		if !strings.Contains(stdout.String(), "Changelogs: finalized") {
			t.Fatalf("stdout = %q", stdout.String())
		}
	})

	t.Run("unreleased", func(t *testing.T) {
		root := releaseTestRoot(t, "0.1.0", "Unreleased")
		err := (releaseCommand{}).run(
			[]string{"check", "--final", "--root", root, "--version", "0.1.0"},
			&bytes.Buffer{},
			&bytes.Buffer{},
		)
		if !errors.Is(err, errReleaseInconsistent) ||
			!strings.Contains(err.Error(), "still marked Unreleased") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestReleaseFinalCheckAcceptsWindowsLineEndings(t *testing.T) {
	root := releaseTestRoot(t, "0.1.0", "2026-07-24")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(
			path,
			[]byte(strings.ReplaceAll(string(contents), "\n", "\r\n")),
			0o644,
		)
	})
	if err != nil {
		t.Fatalf("convert fixture to CRLF: %v", err)
	}

	var stdout bytes.Buffer
	err = (releaseCommand{}).run(
		[]string{"check", "--final", "--root", root, "--version", "0.1.0"},
		&stdout,
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("release final check with CRLF: %v", err)
	}
	if !strings.Contains(stdout.String(), "Changelogs: finalized") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestReleasePrepareRejectsDriftAndUnfinalizedPreviousRelease(t *testing.T) {
	t.Run("drift", func(t *testing.T) {
		root := releaseTestRoot(t, "0.1.0", "2026-07-23")
		writeReleaseTestFile(
			t,
			root,
			"packages/bridra_flutter/pubspec.yaml",
			"name: bridra_flutter\nversion: 9.9.9\n",
		)
		before := releaseTestSnapshot(t, root)
		err := (releaseCommand{}).run(
			[]string{"prepare", "0.1.1", "--root", root},
			&bytes.Buffer{},
			&bytes.Buffer{},
		)
		if !errors.Is(err, errReleaseInconsistent) {
			t.Fatalf("error = %v, want errReleaseInconsistent", err)
		}
		if after := releaseTestSnapshot(t, root); after != before {
			t.Fatal("failed prepare modified files")
		}
	})

	t.Run("unreleased previous version", func(t *testing.T) {
		root := releaseTestRoot(t, "0.1.0", "Unreleased")
		err := (releaseCommand{}).run(
			[]string{"prepare", "0.1.1", "--root", root},
			&bytes.Buffer{},
			&bytes.Buffer{},
		)
		if err == nil || !strings.Contains(err.Error(), "finalize it") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestReleaseCommandRejectsInvalidVersionsAndUsage(t *testing.T) {
	root := releaseTestRoot(t, "0.1.0", "Unreleased")
	for _, arguments := range [][]string{
		{"prepare", "v0.1.1", "--root", root},
		{"prepare", "0.0.9", "--root", root},
		{"prepare", "--root", root},
		{"check", "--version", "0.01.0", "--root", root},
		{"unknown"},
	} {
		err := (releaseCommand{}).run(arguments, &bytes.Buffer{}, &bytes.Buffer{})
		if !errors.Is(err, errReleaseInvalid) {
			t.Fatalf("%v error = %v, want errReleaseInvalid", arguments, err)
		}
	}
}

func releaseTestRoot(t *testing.T, version, changelogStatus string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"VERSION":        version + "\n",
		"backend/go.mod": "module github.com/cluion/bridra/backend\n",
		"backend/framework/protocol.go": "package framework\n\n" +
			"const FrameworkVersion = \"" + version + "\"\n",
		"packages/bridra_flutter/pubspec.yaml": "name: bridra_flutter\n" +
			"version: " + version + "\n",
		"pubspec.yaml": "name: bridra\nversion: " + version + "\n",
		".bridra/project.json": "{\n  \"frameworkVersion\": \"" + version + "\",\n" +
			"  \"templateVersion\": 2,\n  \"protocolVersion\": 1\n}\n",
		"CHANGELOG.md": "# Changelog\n\n## [" + version + "] - " +
			changelogStatus + "\n\n- Existing change.\n",
		"packages/bridra_flutter/CHANGELOG.md": "# Changelog\n\n## " + version +
			" - " + changelogStatus + "\n\n- Existing change.\n",
	}
	for _, path := range []string{
		"docs/ARCHITECTURE.md",
		"docs/GUIDE.md",
		"README.md",
		"docs/RELEASING.md",
		"docs/UPGRADING.md",
	} {
		files[path] = "Bridra " + version + "\n"
	}
	for path, contents := range files {
		writeReleaseTestFile(t, root, path, contents)
	}
	return root
}

func writeReleaseTestFile(t *testing.T, root, path, contents string) {
	t.Helper()
	resolved := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		t.Fatalf("create %s parent: %v", path, err)
	}
	if err := os.WriteFile(resolved, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readReleaseTestFile(t *testing.T, root, path string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}

func releaseTestSnapshot(t *testing.T, root string) string {
	t.Helper()
	var snapshot strings.Builder
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot.WriteString(filepath.ToSlash(relative))
		snapshot.WriteByte('\n')
		snapshot.WriteString(readReleaseTestFile(t, root, relative))
		snapshot.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return snapshot.String()
}
