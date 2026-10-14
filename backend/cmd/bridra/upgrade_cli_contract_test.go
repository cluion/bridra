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
)

func TestUpgradeCLIContractReleasePlansDependencyOnlyMigration(t *testing.T) {
	root := makeCLIContractUpgradeProject(t)
	before := releaseTestSnapshot(t, root)
	command := testUpgradeCommand()
	command.system.run = func(context.Context, upgradeProcess) error {
		t.Fatal("read-only plan ran a process")
		return nil
	}
	var stdout bytes.Buffer
	err := command.run([]string{"--plan", "--to", "0.21.0", "--json", "--root", root}, &stdout, &bytes.Buffer{})
	if !errors.Is(err, errUpgradeRequired) {
		t.Fatalf("plan error = %v", err)
	}
	var report upgradeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 5 || !report.ReadOnly || !report.PlanAvailable ||
		!report.ApplyAvailable || report.Status != upgradeMigrationRequired || len(report.Steps) != 1 ||
		report.Steps[0].ID != "framework-0.20.0-to-0.21.0" || !report.Steps[0].Automatic ||
		report.Project.ProtocolVersion != 3 || report.Target.FrameworkVersion != "0.21.0" ||
		report.Target.TemplateVersion != 8 || report.Target.ProjectMetadataVersion != 3 ||
		report.Target.TemplateProtocolVersion != 1 ||
		!slices.Equal(report.Project.Platforms, []string{"linux", "windows"}) {
		t.Fatalf("report = %#v", report)
	}
	if releaseTestSnapshot(t, root) != before {
		t.Fatal("read-only plan changed project files")
	}
}

func TestUpgradeCLIContractReleaseKeepsEarlierManualAdoption(t *testing.T) {
	for _, mode := range []string{"--plan", "--apply"} {
		t.Run(mode, func(t *testing.T) {
			root := makeUpgradeProjectRoot(t, upgradeMetadataForPlatforms(t, "0.19.0", 7, []string{"linux"}))
			before := releaseTestSnapshot(t, root)
			command := testUpgradeCommand()
			command.system.run = func(context.Context, upgradeProcess) error {
				t.Fatal("manual adoption ran a process")
				return nil
			}
			var stdout bytes.Buffer
			err := command.run([]string{mode, "--to", "0.21.0", "--json", "--root", root}, &stdout, &bytes.Buffer{})
			if !errors.Is(err, errUpgradeRequired) {
				t.Fatalf("upgrade error = %v", err)
			}
			var report upgradeReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if !report.ReadOnly || !report.PlanAvailable || report.ApplyAvailable || len(report.Steps) != 2 ||
				report.Steps[0].ID != "framework-0.19.0-to-0.20.0" || report.Steps[0].Automatic ||
				report.Steps[1].ID != "framework-0.20.0-to-0.21.0" || !report.Steps[1].Automatic ||
				!hasUpgradeDiagnostic(report, "template_adoption_required") {
				t.Fatalf("report = %#v", report)
			}
			if releaseTestSnapshot(t, root) != before {
				t.Fatal("manual adoption changed project files")
			}
		})
	}
}

func TestUpgradeCLIContractReleaseApplyPreservesApplicationAndRollsBack(t *testing.T) {
	for _, failVerification := range []bool{false, true} {
		name := "success"
		if failVerification {
			name = "verification failure rolls back"
		}
		t.Run(name, func(t *testing.T) {
			root := makeCLIContractUpgradeProject(t)
			before := releaseTestSnapshot(t, root)
			preserved := map[string][]byte{}
			for _, relative := range []string{
				"backend/app/owned.go", "schema/bridra.json", "schema/bridra.baseline.json",
				"backend/framework/protocol_version.gen.go", "lib/api/generated/bridra_api.g.dart",
			} {
				preserved[relative] = readTestFile(t, filepath.Join(root, relative))
			}
			var calls []string
			command := testUpgradeCommand()
			command.system.run = func(_ context.Context, process upgradeProcess) error {
				calls = append(calls, process.Name)
				assertTestFileContains(t, filepath.Join(root, "backend/go.mod"), "v0.21.0")
				assertTestFileContains(t, filepath.Join(root, "pubspec.yaml"), "'^0.21.0'")
				var metadata projectMetadata
				if err := json.Unmarshal(readTestFile(t, filepath.Join(root, ".bridra/project.json")), &metadata); err != nil {
					t.Fatal(err)
				}
				if metadata.FrameworkVersion != "0.21.0" || metadata.SchemaVersion != 3 ||
					metadata.TemplateVersion != 8 || metadata.ProtocolVersion != 3 ||
					!slices.Equal(metadata.Platforms, []string{"linux", "windows"}) {
					t.Fatalf("metadata = %#v", metadata)
				}
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
					return errors.New("unexpected process: " + process.Label)
				}
			}
			var stdout bytes.Buffer
			err := command.run([]string{"--apply", "--to", "0.21.0", "--json", "--root", root}, &stdout, &bytes.Buffer{})
			if failVerification {
				if !errors.Is(err, errUpgradeApply) || releaseTestSnapshot(t, root) != before {
					t.Fatalf("rollback failed: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var report upgradeReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.SchemaVersion != 5 || report.Applied == failVerification ||
				report.RolledBack != failVerification || !slices.Equal(calls, []string{"go", "fvm", "make"}) {
				t.Fatalf("report = %#v, calls = %v", report, calls)
			}
			for relative, contents := range preserved {
				assertFileContents(t, filepath.Join(root, relative), contents)
			}
		})
	}
}

func makeCLIContractUpgradeProject(t *testing.T) string {
	t.Helper()
	root := makeUpgradeProjectRoot(t, upgradeMetadataForPlatforms(t, "0.20.0", 8, []string{"linux", "windows"}))
	for relative, contents := range map[string]string{
		"backend/go.mod":              "module example.test/app\n\ngo 1.25\n\nrequire github.com/cluion/bridra/backend v0.20.0\n",
		"backend/go.sum":              "initial go sum\n",
		"pubspec.yaml":                "name: example\ndependencies:\n  bridra_flutter: '^0.20.0'\n",
		"pubspec.lock":                "initial pub lock\n",
		"backend/app/owned.go":        "package app\n\nconst Owned = true\n",
		"schema/bridra.baseline.json": string(readTestFile(t, filepath.Join(root, "schema/bridra.json"))),
	} {
		if err := os.WriteFile(filepath.Join(root, relative), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
