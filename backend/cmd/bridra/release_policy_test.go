package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseFinalCheckAcceptsVersionIndependentSupportPolicy(t *testing.T) {
	for _, version := range []string{"0.14.1", "0.23.0", "0.24.0"} {
		t.Run(version, func(t *testing.T) {
			root := releaseTestRoot(t, version, "2026-07-24")
			for _, path := range []string{"SUPPORT.md", "SECURITY.md"} {
				writeReleaseTestFile(t, root, path, string(readTestFile(t, filepath.Join(repositoryRoot(t), path))))
			}
			before := releaseTestSnapshot(t, root)
			if err := checkRelease(root, version, true, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if releaseTestSnapshot(t, root) != before {
				t.Fatal("release check modified version-independent policies")
			}
		})
	}
}

func TestReleaseFinalCheckRejectsFixedPolicyVersionsAndMissingRules(t *testing.T) {
	for _, test := range []struct{ name, path, old, replacement string }{
		{"fixed support line", "SUPPORT.md", "# Support", "# Support\n\nBridra 0.1 is the current pre-1.0 framework line."},
		{"fixed security table", "SECURITY.md", "| `main` |", "| Latest `0.1.x` patch | Supported |\n| `main` |"},
		{"missing eligibility", "SUPPORT.md", "only the latest patch", "all patches"},
		{"missing publication boundary", "SECURITY.md", "takes effect at publication", "takes effect on main"},
		{"missing release link", "SUPPORT.md", "https://github.com/cluion/bridra/releases", "https://example.test/releases"},
		{"unsupported row changed", "SECURITY.md", "Unsupported; upgrade to the latest supported patch", "Supported indefinitely"},
		{"main marked supported", "SECURITY.md", "Active development only", "Security fixes accepted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := releaseTestRoot(t, "0.1.0", "2026-07-24")
			text := readReleaseTestFile(t, root, test.path)
			if !strings.Contains(text, test.old) {
				t.Fatalf("fixture missing %q", test.old)
			}
			writeReleaseTestFile(t, root, test.path, strings.Replace(text, test.old, test.replacement, 1))
			err := checkRelease(root, "0.1.0", true, &bytes.Buffer{})
			if !errors.Is(err, errReleaseInconsistent) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("policy drift accepted or unrelated failure: %v", err)
			}
		})
	}
}
