package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cluion/bridra/backend/codegen"
)

func TestCurrentUpgradeCatalogPlansNamingReleaseForCustomProtocol(t *testing.T) {
	root := makeNamingUpgradeProject(t)
	var stdout bytes.Buffer
	err := testUpgradeCommand().run(
		[]string{"--plan", "--to", "0.19.0", "--json", "--root", root},
		&stdout,
		&bytes.Buffer{},
	)
	if !errors.Is(err, errUpgradeRequired) {
		t.Fatalf("upgrade error = %v, want errUpgradeRequired", err)
	}
	var report upgradeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != upgradeMigrationRequired || !report.PlanAvailable ||
		!report.ApplyAvailable || len(report.Steps) != 1 ||
		report.Steps[0].ID != "framework-0.18.0-to-0.19.0" ||
		!report.Steps[0].Automatic ||
		report.Project.ProjectMetadataVersion != 3 ||
		report.Project.ProtocolVersion != 3 ||
		report.Target.ProjectMetadataVersion != 3 ||
		report.Target.TemplateVersion != 7 ||
		report.Target.TemplateProtocolVersion != 1 ||
		!hasUpgradeDiagnostic(report, "application_protocol_custom") {
		t.Fatalf("report = %#v", report)
	}
}

func TestNamingReleaseApplyPreservesApplicationContract(t *testing.T) {
	for _, failVerification := range []bool{false, true} {
		name := "success"
		if failVerification {
			name = "verification failure rolls back"
		}
		t.Run(name, func(t *testing.T) {
			root := makeNamingUpgradeProject(t)
			preserved := map[string][]byte{}
			for _, relative := range []string{
				"schema/bridra.json", "schema/bridra.baseline.json", "backend/app/owned.go",
				codegen.GoProtocolPath, codegen.GoRoutesPath, codegen.GoRequestsPath,
				codegen.GoResponsesPath, codegen.DartClientPath,
			} {
				preserved[relative] = readTestFile(t, filepath.Join(root, relative))
			}
			managed := map[string][]byte{}
			for _, relative := range []string{
				"backend/go.mod", "backend/go.sum", "pubspec.yaml", "pubspec.lock", ".bridra/project.json",
			} {
				managed[relative] = readTestFile(t, filepath.Join(root, relative))
			}
			var calls []string
			command := upgradeCommand{
				catalog: currentUpgradeCatalog,
				system: upgradeSystem{run: func(_ context.Context, process upgradeProcess) error {
					calls = append(calls, process.Name)
					assertTestFileContains(t, filepath.Join(root, "backend/go.mod"), "v0.19.0")
					assertTestFileContains(t, filepath.Join(root, "pubspec.yaml"), "'^0.19.0'")
					switch process.Label {
					case "Resolve Go dependencies":
						return os.WriteFile(filepath.Join(root, "backend/go.sum"), []byte("updated go sum\n"), 0o644)
					case "Resolve Flutter dependencies":
						return os.WriteFile(filepath.Join(root, "pubspec.lock"), []byte("updated pub lock\n"), 0o644)
					case "Verify upgraded project":
						if failVerification {
							return errors.New("verification failed")
						}
						return nil
					default:
						return errors.New("unexpected upgrade process: " + process.Label)
					}
				}},
			}
			var stdout bytes.Buffer
			err := command.run(
				[]string{"--apply", "--to", "0.19.0", "--json", "--root", root},
				&stdout, &bytes.Buffer{},
			)
			if failVerification {
				if !errors.Is(err, errUpgradeApply) {
					t.Fatalf("upgrade error = %v, want errUpgradeApply", err)
				}
			} else if err != nil {
				t.Fatalf("upgrade apply: %v", err)
			}
			var report upgradeReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatalf("decode report: %v", err)
			}
			if failVerification {
				if report.Status != upgradeApplyFailed || !report.RolledBack || report.Applied {
					t.Fatalf("rollback report = %#v", report)
				}
				for relative, before := range managed {
					assertFileContents(t, filepath.Join(root, relative), before)
				}
			} else {
				if report.Status != upgradeApplied || !report.Applied || report.RolledBack {
					t.Fatalf("apply report = %#v", report)
				}
				var metadata projectMetadata
				if err := json.Unmarshal(readTestFile(t, filepath.Join(root, ".bridra/project.json")), &metadata); err != nil {
					t.Fatalf("decode metadata: %v", err)
				}
				if metadata.FrameworkVersion != "0.19.0" || metadata.SchemaVersion != 3 ||
					metadata.TemplateVersion != 7 || metadata.ProtocolVersion != 3 ||
					!slices.Equal(metadata.Platforms, []string{"ios", "macos"}) {
					t.Fatalf("metadata = %#v", metadata)
				}
			}
			if !slices.Equal(calls, []string{"go", "fvm", "make"}) {
				t.Fatalf("upgrade processes = %v", calls)
			}
			for relative, before := range preserved {
				assertFileContents(t, filepath.Join(root, relative), before)
			}
		})
	}
}

func makeNamingUpgradeProject(t *testing.T) string {
	t.Helper()
	root := makeUpgradeProjectRoot(t, `{
  "schemaVersion": 3,
  "projectName": "example",
  "goModule": "example.test/app",
  "frameworkModule": "github.com/cluion/bridra/backend",
  "frameworkVersion": "0.18.0",
  "templateVersion": 7,
  "protocolVersion": 3,
  "platforms": ["ios", "macos"]
}`)
	files := map[string]string{
		"backend/go.mod":              "module example.test/app\n\ngo 1.25\n\nrequire github.com/cluion/bridra/backend v0.18.0\n",
		"backend/go.sum":              "initial go sum\n",
		"pubspec.yaml":                "name: example\ndependencies:\n  bridra_flutter: '^0.18.0'\n",
		"pubspec.lock":                "initial pub lock\n",
		"backend/app/owned.go":        "package app\n\nconst Owned = true\n",
		"schema/bridra.baseline.json": string(readTestFile(t, filepath.Join(root, "schema/bridra.json"))),
	}
	for relative, contents := range files {
		if err := os.WriteFile(filepath.Join(root, relative), []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
	return root
}
