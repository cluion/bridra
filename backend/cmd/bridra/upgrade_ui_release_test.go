package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestUpgrade0241UIReleasePlansAutomaticPaths(t *testing.T) {
	for _, from := range []string{"0.23.2", "0.24.0"} {
		for _, template := range []int{8, 9} {
			t.Run(fmt.Sprintf("%s/template-%d", from, template), func(t *testing.T) {
				root := makePublicationReleaseProject(t, from, template)
				before := releaseTestSnapshot(t, root)
				command := testUpgradeCommand()
				command.system.run = func(context.Context, upgradeProcess) error {
					t.Fatal("read-only plan ran a process")
					return nil
				}
				var stdout bytes.Buffer
				err := command.run([]string{"--plan", "--to", "0.24.1", "--json", "--root", root}, &stdout, &bytes.Buffer{})
				if !errors.Is(err, errUpgradeRequired) {
					t.Fatalf("upgrade error = %v", err)
				}
				var report upgradeReport
				if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				wantPath := []string{"0.24.0", "0.24.1"}
				if from == "0.23.2" {
					wantPath = append([]string{from}, wantPath...)
				}
				if !report.ReadOnly || !report.PlanAvailable || !report.ApplyAvailable ||
					report.Status != upgradeMigrationRequired || len(report.Steps) != len(wantPath)-1 ||
					report.Project.TemplateVersion != template || report.Project.ProtocolVersion != 3 ||
					report.Target.FrameworkVersion != "0.24.1" || report.Target.ProjectMetadataVersion != 3 ||
					report.Target.TemplateVersion != 9 || report.Target.TemplateProtocolVersion != 1 {
					t.Fatalf("UI release plan = %#v", report)
				}
				for index, step := range report.Steps {
					if step.ID != "framework-"+wantPath[index]+"-to-"+wantPath[index+1] || !step.Automatic {
						t.Fatalf("step %d = %#v", index, step)
					}
				}
				if releaseTestSnapshot(t, root) != before {
					t.Fatal("read-only plan modified application files")
				}
			})
		}
	}
}

func TestUpgrade0241UIReleaseApplyPreservesUIAndRollsBack(t *testing.T) {
	for _, failVerification := range []bool{false, true} {
		t.Run(fmt.Sprintf("rollback-%t", failVerification), func(t *testing.T) {
			root := makePublicationReleaseProject(t, "0.24.0", 8)
			preserved := map[string][]byte{}
			for _, relative := range []string{
				"lib/ui/theme/app_theme.dart", "lib/features/components/components_view.dart", "macos/Runner/MainFlutterWindow.swift",
			} {
				writeReleaseTestFile(t, root, relative, "application-owned "+relative+"\n")
			}
			for _, relative := range []string{
				"backend/app/owned.go", "lib/application_view.dart", "lib/ui/theme/app_theme.dart",
				"lib/features/components/components_view.dart", "macos/Runner/MainFlutterWindow.swift",
				"schema/bridra.json", "schema/bridra.baseline.json", "lib/api/generated/bridra_api.g.dart",
			} {
				preserved[relative] = readTestFile(t, filepath.Join(root, relative))
			}
			before := releaseTestSnapshot(t, root)
			var expectedMetadata projectMetadata
			if err := json.Unmarshal(readTestFile(t, filepath.Join(root, ".bridra/project.json")), &expectedMetadata); err != nil {
				t.Fatal(err)
			}
			expectedMetadata.FrameworkVersion = "0.24.1"
			wantProcesses := []upgradeProcess{
				{Name: "go", Arguments: []string{"mod", "tidy"}, Directory: filepath.Join(root, "backend")},
				{Name: "fvm", Arguments: []string{"flutter", "pub", "get"}, Directory: root},
				{Name: "make", Arguments: []string{"verify"}, Directory: root},
			}
			calls := 0
			command := testUpgradeCommand()
			command.system.run = func(_ context.Context, process upgradeProcess) error {
				if calls >= len(wantProcesses) {
					t.Fatalf("unexpected process: %#v", process)
				}
				want := wantProcesses[calls]
				calls++
				if process.Name != want.Name || !slices.Equal(process.Arguments, want.Arguments) || process.Directory != want.Directory {
					t.Fatalf("process = %#v, want %#v", process, want)
				}
				assertFileContents(t, filepath.Join(root, "backend/go.mod"), []byte(
					"module example.test/app\n\ngo 1.25\n\nrequire github.com/cluion/bridra/backend v0.24.1\n"))
				assertFileContents(t, filepath.Join(root, "pubspec.yaml"), []byte(
					"name: example\ndependencies:\n  bridra_flutter: '^0.24.1'\n"))
				var metadata projectMetadata
				if err := json.Unmarshal(readTestFile(t, filepath.Join(root, ".bridra/project.json")), &metadata); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(metadata, expectedMetadata) {
					t.Fatalf("metadata = %#v, want %#v", metadata, expectedMetadata)
				}
				switch process.Name {
				case "go":
					return os.WriteFile(filepath.Join(root, "backend/go.sum"), []byte("resolved 0.24.1 go sum\n"), 0o644)
				case "fvm":
					return os.WriteFile(filepath.Join(root, "pubspec.lock"), []byte("resolved 0.24.1 pub lock\n"), 0o644)
				default:
					if failVerification {
						return errors.New("verification failed")
					}
					return nil
				}
			}
			var stdout bytes.Buffer
			err := command.run([]string{"--apply", "--to", "0.24.1", "--json", "--root", root}, &stdout, &bytes.Buffer{})
			var report upgradeReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if failVerification {
				if !errors.Is(err, errUpgradeApply) || !report.RolledBack || report.Status != upgradeApplyFailed ||
					releaseTestSnapshot(t, root) != before {
					t.Fatalf("rollback failed: %v, report = %#v", err, report)
				}
			} else if err != nil || !report.Applied || report.Status != upgradeApplied {
				t.Fatalf("apply failed: %v, report = %#v", err, report)
			}
			if calls != len(wantProcesses) {
				t.Fatalf("process count = %d", calls)
			}
			for relative, contents := range preserved {
				assertFileContents(t, filepath.Join(root, relative), contents)
			}
		})
	}
}

func TestUpgrade0241UIReleaseKeepsEarlierManualAdoption(t *testing.T) {
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
			err := command.run([]string{mode, "--to", "0.24.1", "--json", "--root", root}, &stdout, &bytes.Buffer{})
			if !errors.Is(err, errUpgradeRequired) {
				t.Fatalf("upgrade error = %v", err)
			}
			var report upgradeReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.ApplyAvailable || len(report.Steps) != 8 || report.Steps[0].Automatic ||
				!hasUpgradeDiagnostic(report, "template_adoption_required") ||
				report.Steps[7].ID != "framework-0.24.0-to-0.24.1" || !report.Steps[7].Automatic ||
				releaseTestSnapshot(t, root) != before {
				t.Fatalf("manual adoption boundary = %#v", report)
			}
		})
	}
}
