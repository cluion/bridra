package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReleasePrepareDateSynchronizesChangelogs(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		for _, pending := range []bool{false, true} {
			name := map[string]string{"\n": "LF", "\r\n": "CRLF"}[eol]
			t.Run(name+map[bool]string{false: "/new", true: "/pending"}[pending], func(t *testing.T) {
				root := releaseTestRoot(t, "0.1.0", "2026-07-23")
				history := map[string]string{}
				for _, path := range []string{"CHANGELOG.md", "packages/bridra_flutter/CHANGELOG.md"} {
					old := strings.ReplaceAll(readReleaseTestFile(t, root, path), "\n", eol)
					history[path] = strings.TrimPrefix(old, "# Changelog"+eol+eol)
					if pending {
						old = strings.Replace(old, "# Changelog"+eol+eol,
							"# Changelog"+eol+eol+"## [Unreleased]"+eol+eol+"- Pending note."+eol+eol, 1)
					}
					writeReleaseTestFile(t, root, path, old)
				}
				untouched := map[string]string{}
				for _, path := range append([]string{"SUPPORT.md", "SECURITY.md"}, releaseTestDocumentationPaths...) {
					untouched[path] = readReleaseTestFile(t, root, path)
				}
				arguments := []string{"prepare", "0.1.1", "--date", "2026-07-24", "--root", root}
				if err := (releaseCommand{}).run(arguments, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
				for path, old := range history {
					got := readReleaseTestFile(t, root, path)
					heading := "## 0.1.1 - 2026-07-24"
					if path == "CHANGELOG.md" {
						heading = "## [0.1.1] - 2026-07-24"
					}
					if !strings.Contains(got, heading+eol) || !strings.HasSuffix(got, old) ||
						(pending && !strings.Contains(got, "- Pending note.")) {
						t.Fatalf("date synchronization changed history or notes in %s: %q", path, got)
					}
					if eol == "\r\n" && strings.Contains(strings.ReplaceAll(got, eol, ""), "\n") {
						t.Fatalf("date preparation changed CRLF in %s", path)
					}
				}
				before := releaseTestSnapshot(t, root)
				if err := (releaseCommand{}).run(arguments, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
				if releaseTestSnapshot(t, root) != before {
					t.Fatal("same version/date preparation was not idempotent")
				}
				for path, text := range untouched {
					if readReleaseTestFile(t, root, path) != text {
						t.Fatalf("date preparation rewrote general policy/documentation: %s", path)
					}
				}
			})
		}
	}
}

func TestReleasePrepareDateUpdatesOnlyCurrentCandidate(t *testing.T) {
	for _, status := range []string{"Unreleased", "2026-07-23"} {
		t.Run(status, func(t *testing.T) {
			root := releaseTestRoot(t, "0.1.1", status)
			history := map[string]string{
				"CHANGELOG.md":                         "\n## [0.1.0] - 2026-07-22\n\n- Keep historical 2026-07-23.\n",
				"packages/bridra_flutter/CHANGELOG.md": "\n## 0.1.0 - 2026-07-22\n\n- Keep historical 2026-07-23.\n",
			}
			for path, old := range history {
				writeReleaseTestFile(t, root, path, readReleaseTestFile(t, root, path)+old)
			}
			if err := (releaseCommand{}).run([]string{"prepare", "--date=2028-02-29", "0.1.1", "--root=" + root}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			for path, old := range history {
				got := readReleaseTestFile(t, root, path)
				if !strings.Contains(got, " - 2028-02-29\n") || !strings.HasSuffix(got, old) {
					t.Fatalf("current candidate date changed history in %s: %s", path, got)
				}
			}
		})
	}
}

func TestReleasePrepareDateRejectsInvalidInputWithoutWriting(t *testing.T) {
	for _, arguments := range [][]string{
		{"--date", "2026-02-29"}, {"--date=2026-04-31"}, {"--date=2026-7-24"},
		{"--date=2026-07-24T00:00:00Z"}, {"--date="}, {"--date"},
		{"--date=2026-07-24", "--date=2026-07-25"},
	} {
		t.Run(strings.Join(arguments, "/"), func(t *testing.T) {
			root := releaseTestRoot(t, "0.1.0", "2026-07-23")
			before := releaseTestSnapshot(t, root)
			args := append([]string{"prepare", "0.1.1", "--root", root}, arguments...)
			err := (releaseCommand{}).run(args, &bytes.Buffer{}, &bytes.Buffer{})
			if !errors.Is(err, errReleaseInvalid) || releaseTestSnapshot(t, root) != before {
				t.Fatalf("invalid date changed files or wrong error: %v", err)
			}
		})
	}
}

func TestReleasePrepareDateFailureLeavesBothChangelogsUntouched(t *testing.T) {
	for _, invalid := range []string{
		"# Changelog\n\n## invalid\n",
		"# Changelog\n\n## [Unreleased]\n\n- Next release.\n\n## 0.1.0 - 2026-07-23\n",
		"# Changelog\n\n## 0.1.0 - 2026-07-23\n\n## 0.1.0 - 2026-07-22\n",
	} {
		root := releaseTestRoot(t, "0.1.0", "2026-07-23")
		writeReleaseTestFile(t, root, "packages/bridra_flutter/CHANGELOG.md", invalid)
		before := releaseTestSnapshot(t, root)
		err := (releaseCommand{}).run([]string{"prepare", "0.1.0", "--date=2026-07-24", "--root", root}, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || releaseTestSnapshot(t, root) != before {
			t.Fatalf("partial date write or ambiguous changelog accepted: %v", err)
		}
	}
}

func TestReleasePrepareDateRetainsVersionGuards(t *testing.T) {
	for _, test := range []struct{ name, status, target, drift string }{
		{"downgrade", "2026-07-23", "0.0.9", ""},
		{"metadata drift", "2026-07-23", "0.1.1", "9.9.9"},
		{"unfinished previous version", "Unreleased", "0.1.1", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := releaseTestRoot(t, "0.1.0", test.status)
			if test.drift != "" {
				writeReleaseTestFile(t, root, "packages/bridra_flutter/pubspec.yaml", "name: bridra_flutter\nversion: "+test.drift+"\n")
			}
			before := releaseTestSnapshot(t, root)
			err := (releaseCommand{}).run([]string{"prepare", test.target, "--date=2026-07-24", "--root", root}, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || releaseTestSnapshot(t, root) != before {
				t.Fatalf("date option bypassed version guard or changed files: %v", err)
			}
		})
	}
}

func TestReleaseFinalCheckRequiresMatchingCalendarDates(t *testing.T) {
	for _, date := range []string{"2026-02-29", "2026-13-01", "2026-07-25"} {
		t.Run(date, func(t *testing.T) {
			root := releaseTestRoot(t, "0.1.0", "2026-07-24")
			if err := checkRelease(root, "0.1.0", true, &bytes.Buffer{}); err != nil {
				t.Fatalf("valid control failed: %v", err)
			}
			path := "packages/bridra_flutter/CHANGELOG.md"
			writeReleaseTestFile(t, root, path, strings.Replace(readReleaseTestFile(t, root, path), "2026-07-24", date, 1))
			err := checkRelease(root, "0.1.0", true, &bytes.Buffer{})
			if !errors.Is(err, errReleaseInconsistent) {
				t.Fatalf("accepted invalid or inconsistent release date %s: %v", date, err)
			}
		})
	}
}
