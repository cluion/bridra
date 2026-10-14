package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

var releaseTestDocumentationPaths = []string{
	"README.md",
	"docs/ARCHITECTURE.md",
	"docs/GUIDE.md",
	"docs/RELEASING.md",
	"docs/UPGRADING.md",
}

func TestReleasePreparePreservesHistoricalDocumentationAndPromotesPendingNotes(t *testing.T) {
	root := releaseTestRoot(t, "0.1.0", "2026-07-23")
	history := readReleaseTestFile(t, root, "CHANGELOG.md")
	writeReleaseTestFile(t, root, "CHANGELOG.md", strings.Replace(history,
		"# Changelog\n\n", "# Changelog\n\n## [Unreleased]\n\n- Pending Unicode fix for 0.1.0 consumers.\n\n", 1))
	if err := prepareRelease(root, "0.1.1", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range releaseTestDocumentationPaths {
		text := readReleaseTestFile(t, root, path)
		if text != "Source version: VERSION\nHistorical Bridra 0.1.0 remains unchanged.\n" {
			t.Fatalf("documentation changed in %s: %s", path, text)
		}
	}
	text := readReleaseTestFile(t, root, "CHANGELOG.md")
	if strings.Contains(text, "## [Unreleased]") ||
		!strings.Contains(text, "## [0.1.1] - Unreleased\n\n- Pending Unicode fix for 0.1.0 consumers.") ||
		!strings.HasSuffix(text, strings.TrimPrefix(history, "# Changelog\n\n")) {
		t.Fatalf("pending notes or published history changed: %s", text)
	}
}

func TestReleasePreparePromotesPendingPackageNotesWithCRLF(t *testing.T) {
	root := releaseTestRoot(t, "0.1.0", "2026-07-23")
	path := "packages/bridra_flutter/CHANGELOG.md"
	history := strings.ReplaceAll(readReleaseTestFile(t, root, path), "\n", "\r\n")
	writeReleaseTestFile(t, root, path, strings.Replace(history,
		"# Changelog\r\n\r\n", "# Changelog\r\n\r\n## Unreleased\r\n\r\n- Pending package fix.\r\n\r\n", 1))
	if err := prepareRelease(root, "0.1.1", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got := readReleaseTestFile(t, root, path)
	if !strings.Contains(got, "## 0.1.1 - Unreleased\r\n\r\n- Pending package fix.") ||
		!strings.HasSuffix(got, strings.TrimPrefix(history, "# Changelog\r\n\r\n")) {
		t.Fatalf("pending notes, line endings or history changed: %q", got)
	}
}

func TestReleaseFinalCheckRejectsPendingUnreleasedNotes(t *testing.T) {
	for _, path := range []string{"CHANGELOG.md", "packages/bridra_flutter/CHANGELOG.md"} {
		t.Run(path, func(t *testing.T) {
			root := releaseTestRoot(t, "0.1.0", "2026-07-23")
			history := readReleaseTestFile(t, root, path)
			writeReleaseTestFile(t, root, path, strings.Replace(history,
				"# Changelog\n\n", "# Changelog\n\n## [Unreleased]\n\n- Pending fix.\n\n", 1))
			err := checkRelease(root, "0.1.0", true, &bytes.Buffer{})
			if !errors.Is(err, errReleaseInconsistent) || !strings.Contains(err.Error(), "pending Unreleased section") {
				t.Fatalf("final check accepted pending notes: %v", err)
			}
		})
	}
}

func TestReleasePrepareRejectsAmbiguousPendingChangelogWithoutWriting(t *testing.T) {
	for _, pending := range []string{
		"## [Unreleased]\n\n- First.\n\n## Unreleased\n\n- Second.\n\n",
		"## [0.0.9] - 2026-07-22\n\n- Older.\n\n## [Unreleased]\n\n- Misplaced.\n\n",
	} {
		root := releaseTestRoot(t, "0.1.0", "2026-07-23")
		history := readReleaseTestFile(t, root, "CHANGELOG.md")
		if strings.Contains(pending, "0.0.9") {
			writeReleaseTestFile(t, root, "CHANGELOG.md", history+pending)
		} else {
			writeReleaseTestFile(t, root, "CHANGELOG.md", strings.Replace(history, "# Changelog\n\n", "# Changelog\n\n"+pending, 1))
		}
		before := releaseTestSnapshot(t, root)
		if err := prepareRelease(root, "0.1.1", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "one leading Unreleased section") {
			t.Fatalf("ambiguous pending sections accepted: %v", err)
		}
		if releaseTestSnapshot(t, root) != before {
			t.Fatal("failed prepare changed files")
		}
	}
}

func TestReleasePrepareLeavesGeneralDocumentationByteIdentical(t *testing.T) {
	root := releaseTestRoot(t, "0.1.0", "2026-07-23")
	const text = "Source: [VERSION](../VERSION)\r\nHistorical 0.1.0 and 0.0.9.\r\n<!-- bridra:release-current:start -->\r\nLegacy example 0.0.9 without a closing marker.\r\n"
	for _, path := range releaseTestDocumentationPaths {
		writeReleaseTestFile(t, root, path, text)
	}
	if err := prepareRelease(root, "0.1.1", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range releaseTestDocumentationPaths {
		if got := readReleaseTestFile(t, root, path); got != text {
			t.Fatalf("prepare rewrote %s: %q", path, got)
		}
	}
	if err := checkRelease(root, "0.1.1", false, &bytes.Buffer{}); err != nil {
		t.Fatalf("general documentation affected version check: %v", err)
	}
}
