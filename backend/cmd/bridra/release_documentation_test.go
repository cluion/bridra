package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReleasePreparePreservesHistoricalDocumentationAndPromotesPendingNotes(t *testing.T) {
	root := releaseTestRoot(t, "0.1.0", "2026-07-23")
	history := readReleaseTestFile(t, root, "CHANGELOG.md")
	writeReleaseTestFile(t, root, "CHANGELOG.md", strings.Replace(history,
		"# Changelog\n\n", "# Changelog\n\n## [Unreleased]\n\n- Pending Unicode fix for 0.1.0 consumers.\n\n", 1))
	if err := prepareRelease(root, "0.1.1", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, document := range managedReleaseDocumentation() {
		text := readReleaseTestFile(t, root, document.path)
		if !strings.Contains(text, "Bridra 0.1.1\n") ||
			!strings.Contains(text, "Historical Bridra 0.1.0 remains unchanged.") {
			t.Fatalf("live or historical text corrupted in %s: %s", document.path, text)
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

func TestReleaseDocumentationUpdatesOnlyMarkedBlocksWithCRLF(t *testing.T) {
	block := releaseDocumentationStart + "\r\nInstall 0.1.0\r\n" + releaseDocumentationEnd
	input := "Historical 0.1.0\r\n" + block + "\r\nPrevious 0.1.0\r\n" + block + "\r\n"
	got, err := updateReleaseDocumentation([]byte(input), "0.1.0", "0.1.1")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(input, "Install 0.1.0", "Install 0.1.1")
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	again, err := updateReleaseDocumentation(got, "0.1.1", "0.1.1")
	if err != nil || !bytes.Equal(again, got) {
		t.Fatalf("idempotent update = %q, %v", again, err)
	}
}

func TestReleaseDocumentationDoesNotReplaceVersionPrefixes(t *testing.T) {
	input := releaseDocumentationStart + "\nInstall v0.1.0. Keep 0.1.01 and 0.1.0-rc.1 unchanged.\n" + releaseDocumentationEnd
	got, err := updateReleaseDocumentation([]byte(input), "0.1.0", "0.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != strings.Replace(input, "Install v0.1.0", "Install v0.1.1", 1) {
		t.Fatalf("changed a different version: %s", got)
	}
}

func TestReleasePrepareRefusesInvalidDocumentationWithoutWriting(t *testing.T) {
	for name, text := range map[string]string{
		"missing markers":         "Historical 0.1.0",
		"unclosed":                releaseDocumentationStart + "0.1.0",
		"unexpected end":          releaseDocumentationEnd + releaseDocumentationStart + "0.1.0" + releaseDocumentationEnd,
		"nested":                  releaseDocumentationStart + releaseDocumentationStart + "0.1.0" + releaseDocumentationEnd,
		"drift hidden by history": releaseDocumentationStart + "0.0.9" + releaseDocumentationEnd + "Historical 0.1.0",
		"different patch prefix":  releaseDocumentationStart + "0.1.01" + releaseDocumentationEnd,
		"prerelease prefix":       releaseDocumentationStart + "0.1.0-rc.1" + releaseDocumentationEnd,
	} {
		t.Run(name, func(t *testing.T) {
			root := releaseTestRoot(t, "0.1.0", "2026-07-23")
			writeReleaseTestFile(t, root, "docs/UPGRADING.md", text)
			before := releaseTestSnapshot(t, root)
			if err := prepareRelease(root, "0.1.1", &bytes.Buffer{}); err == nil {
				t.Fatal("invalid documentation accepted")
			}
			if releaseTestSnapshot(t, root) != before {
				t.Fatal("failed prepare changed files")
			}
		})
	}
}
