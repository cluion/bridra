package main

import (
	"bytes"
	"errors"
	"fmt"
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
		if !strings.Contains(stdout.String(), "Support policy: latest 0.1.x patch") {
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

func TestReleaseFinalCheckRejectsSupportPolicyDrift(t *testing.T) {
	for _, test := range []struct {
		name        string
		path        string
		old         string
		replacement string
		want        string
	}{
		{
			name:        "support current release",
			path:        "SUPPORT.md",
			old:         "Bridra 0.1 is the current",
			replacement: "Bridra 0.0 is the current",
			want:        "SUPPORT.md current release line is 0.0, expected 0.1",
		},
		{
			name:        "security current release",
			path:        "SECURITY.md",
			old:         "Bridra 0.1 is a pre-1.0",
			replacement: "Bridra 0.0 is a pre-1.0",
			want:        "SECURITY.md current release line is 0.0, expected 0.1",
		},
		{
			name:        "security supported versions",
			path:        "SECURITY.md",
			old:         "Latest `0.1.x` patch",
			replacement: "Latest `0.0.x` patch",
			want:        "SECURITY.md supported versions table is 0.0, expected 0.1",
		},
		{
			name:        "changelog support section",
			path:        "CHANGELOG.md",
			old:         "### Support",
			replacement: "### Compatibility",
			want:        "CHANGELOG.md: release 0.1.0 has no Support section",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := releaseTestRoot(t, "0.1.0", "2026-07-24")
			contents := readReleaseTestFile(t, root, test.path)
			writeReleaseTestFile(
				t,
				root,
				test.path,
				strings.Replace(contents, test.old, test.replacement, 1),
			)
			err := (releaseCommand{}).run(
				[]string{"check", "--final", "--root", root, "--version", "0.1.0"},
				&bytes.Buffer{},
				&bytes.Buffer{},
			)
			if !errors.Is(err, errReleaseInconsistent) ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReleaseFinalCheckUsesMinorSupportLineForPatchRelease(t *testing.T) {
	root := releaseTestRoot(t, "0.14.1", "2026-08-24")
	var stdout bytes.Buffer
	err := (releaseCommand{}).run(
		[]string{"check", "--final", "--root", root, "--version", "0.14.1"},
		&stdout,
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("release final patch check: %v", err)
	}
	if !strings.Contains(stdout.String(), "Support policy: latest 0.14.x patch") {
		t.Fatalf("stdout = %q", stdout.String())
	}
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
	parsedVersion, err := parseSemanticVersion(version)
	if err != nil {
		t.Fatalf("parse fixture version: %v", err)
	}
	releaseLine := fmt.Sprintf("%d.%d", parsedVersion.major, parsedVersion.minor)
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
			changelogStatus + "\n\n- Existing change.\n\n### Support\n\n" +
			"- Latest `" + releaseLine + ".x` patch is supported.\n",
		"packages/bridra_flutter/CHANGELOG.md": "# Changelog\n\n## " + version +
			" - " + changelogStatus + "\n\n- Existing change.\n",
		"SUPPORT.md": "# Support\n\nBridra " + releaseLine +
			" is the current pre-1.0 framework line.\n",
		"SECURITY.md": "# Security policy\n\nBridra " + releaseLine +
			" is a pre-1.0 framework line.\n\n| Version | Security support |\n" +
			"| --- | --- |\n| Latest `" + releaseLine + ".x` patch | Supported |\n",
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
