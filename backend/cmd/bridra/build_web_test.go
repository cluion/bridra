package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWebUIPackagesSameOriginDistWithoutFlutter(t *testing.T) {
	for _, preset := range []string{"vue", "react"} {
		t.Run(preset, func(t *testing.T) {
			root := makeProjectRoot(t, fmt.Sprintf(`{
				"schemaVersion": 4,
				"projectName": "starter",
				"goModule": "example.com/starter",
				"frameworkModule": "github.com/cluion/bridra/backend",
				"frameworkVersion": "0.23.2",
				"templateVersion": 1,
				"protocolVersion": 1,
				"platforms": ["web","linux"],
				"ui": {"mode":"web","preset":%q,"dist":"frontend/dist"}
			}`, preset))
			writeBuildTestOutput(t, filepath.Join(root, "frontend", "dist", "index.html"), "<main>"+preset+"</main>")
			system := buildTestSystem("linux", "arm64")
			system.run = func(spec buildProcessSpec) error {
				t.Fatalf("unexpected subprocess %s", spec.Name)
				return nil
			}
			cmd := buildCommand{system: system}
			var output bytes.Buffer
			if err := cmd.run([]string{"web", "--root", root}, &output, io.Discard); err != nil {
				t.Fatal(err)
			}
			_, path, ok := strings.Cut(output.String(), "Manifest: ")
			if !ok {
				t.Fatal("missing manifest path in build output")
			}
			if _, err := os.Stat(filepath.Join(root, strings.TrimSpace(path))); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"windows"},
				{"linux", "--web-dist-sha256", "broken"},
				{"linux", "--web-dist-sha256="},
				{"web", "--web-dist-sha256", strings.Repeat("a", 64)},
				{"web", "--token", "not-public"},
				{"web", "--backend-url", "https://external.invalid/rpc"},
				{"web", "--mode", "debug"},
				{"web", "--macos-sidecar-native"},
			} {
				err := cmd.run(append(args, "--root", root), io.Discard, io.Discard)
				if !errors.Is(err, errBuildInvalid) {
					t.Errorf("build %v: got %v", args, err)
				}
			}
			if err := cmd.run([]string{"linux", "--root", root, "--web-dist-sha256", strings.Repeat("a", 64)}, io.Discard, io.Discard); !errors.Is(err, errBuildArtifact) || !strings.Contains(err.Error(), "open web snapshot") {
				t.Fatalf("a selected desktop target must require the pinned snapshot: %v", err)
			}
		})
	}
}
