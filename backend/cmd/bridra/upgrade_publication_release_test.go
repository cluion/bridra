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

func TestUpgrade0231PublicationReleasePlansAutomaticCatalogPaths(t *testing.T) {
	for _, test := range []struct {
		from string
		path []string
	}{
		{"0.22.0", []string{"0.22.0", "0.23.0", "0.23.1"}},
		{"0.23.0", []string{"0.23.0", "0.23.1"}},
	} {
		for _, template := range []int{8, 9} {
			t.Run(fmt.Sprintf("%s/template-%d", test.from, template), func(t *testing.T) {
				root := makePublicationReleaseProject(t, test.from, template)
				before := releaseTestSnapshot(t, root)
				command := testUpgradeCommand()
				command.system.run = func(context.Context, upgradeProcess) error {
					t.Fatal("read-only plan ran a process")
					return nil
				}
				var stdout bytes.Buffer
				err := command.run([]string{"--plan", "--to", "0.23.1", "--json", "--root", root}, &stdout, &bytes.Buffer{})
				if !errors.Is(err, errUpgradeRequired) {
					t.Fatalf("upgrade error = %v", err)
				}
				var report upgradeReport
				if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if report.SchemaVersion != 5 || !report.ReadOnly || !report.PlanAvailable || !report.ApplyAvailable ||
					report.Status != upgradeMigrationRequired || len(report.Steps) != len(test.path)-1 ||
					report.Project.FrameworkVersion != test.from || report.Project.ProjectMetadataVersion != 3 ||
					report.Project.TemplateVersion != template || report.Project.ProtocolVersion != 3 ||
					!slices.Equal(report.Project.Platforms, []string{"linux", "windows"}) ||
					report.Target.FrameworkVersion != "0.23.1" || report.Target.ProjectMetadataVersion != 3 ||
					report.Target.TemplateVersion != 9 || report.Target.TemplateProtocolVersion != 1 ||
					hasUpgradeDiagnostic(report, "template_update_available") != (template == 8) {
					t.Fatalf("publication release plan = %#v", report)
				}
				for index, step := range report.Steps {
					from, to := test.path[index], test.path[index+1]
					if step.ID != "framework-"+from+"-to-"+to || step.From != from || step.To != to || !step.Automatic {
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

func TestUpgrade0231PublicationReleaseApplyResolvesOnlyFinalVersionAndRollsBack(t *testing.T) {
	for _, from := range []string{"0.22.0", "0.23.0"} {
		for _, template := range []int{8, 9} {
			for _, failVerification := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/template-%d/rollback-%t", from, template, failVerification), func(t *testing.T) {
					root := makePublicationReleaseProject(t, from, template)
					before := releaseTestSnapshot(t, root)
					var expectedMetadata projectMetadata
					if err := json.Unmarshal(readTestFile(t, filepath.Join(root, ".bridra/project.json")), &expectedMetadata); err != nil {
						t.Fatal(err)
					}
					expectedMetadata.FrameworkVersion = "0.23.1"
					preserved := map[string][]byte{}
					for _, relative := range []string{
						"backend/app/owned.go", "lib/application_view.dart", "schema/bridra.json", "schema/bridra.baseline.json",
						"backend/framework/protocol_version.gen.go", "lib/api/generated/bridra_api.g.dart",
					} {
						preserved[relative] = readTestFile(t, filepath.Join(root, relative))
					}
					wantProcesses := []upgradeProcess{
						{Label: "Resolve Go dependencies", Name: "go", Arguments: []string{"mod", "tidy"}, Directory: filepath.Join(root, "backend")},
						{Label: "Resolve Flutter dependencies", Name: "fvm", Arguments: []string{"flutter", "pub", "get"}, Directory: root},
						{Label: "Verify upgraded project", Name: "make", Arguments: []string{"verify"}, Directory: root},
					}
					calls := 0
					command := testUpgradeCommand()
					command.system.run = func(_ context.Context, process upgradeProcess) error {
						if calls >= len(wantProcesses) {
							t.Fatalf("unexpected repeated dependency resolution: %#v", process)
						}
						want := wantProcesses[calls]
						calls++
						if process.Label != want.Label || process.Name != want.Name ||
							!slices.Equal(process.Arguments, want.Arguments) || process.Directory != want.Directory {
							t.Fatalf("process %d = %#v, want %#v", calls, process, want)
						}
						// The catalog includes 0.23.0, but its Flutter package was never
						// published. Every resolver must see the final target directly.
						assertFileContents(t, filepath.Join(root, "backend/go.mod"), []byte(
							"module example.test/app\n\ngo 1.25\n\nrequire github.com/cluion/bridra/backend v0.23.1\n"))
						assertFileContents(t, filepath.Join(root, "pubspec.yaml"), []byte(
							"name: example\ndependencies:\n  bridra_flutter: '^0.23.1'\n"))
						var metadata projectMetadata
						if err := json.Unmarshal(readTestFile(t, filepath.Join(root, ".bridra/project.json")), &metadata); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(metadata, expectedMetadata) {
							t.Fatalf("metadata = %#v, want %#v", metadata, expectedMetadata)
						}
						switch process.Name {
						case "go":
							return os.WriteFile(filepath.Join(root, "backend/go.sum"), []byte("resolved 0.23.1 go sum\n"), 0o644)
						case "fvm":
							return os.WriteFile(filepath.Join(root, "pubspec.lock"), []byte("resolved 0.23.1 pub lock\n"), 0o644)
						default:
							if failVerification {
								return errors.New("verification failed")
							}
							return nil
						}
					}
					var stdout bytes.Buffer
					err := command.run([]string{"--apply", "--to", "0.23.1", "--json", "--root", root}, &stdout, &bytes.Buffer{})
					wantStatus := upgradeApplied
					if failVerification {
						wantStatus = upgradeApplyFailed
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
					if report.SchemaVersion != 5 || report.Status != wantStatus || report.ReadOnly ||
						report.Applied == failVerification || report.RolledBack != failVerification || calls != len(wantProcesses) {
						t.Fatalf("report = %#v, process count = %d", report, calls)
					}
					for relative, contents := range preserved {
						assertFileContents(t, filepath.Join(root, relative), contents)
					}
				})
			}
		}
	}
}

func TestUpgrade0231PublicationReleaseKeepsEarlierManualAdoption(t *testing.T) {
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
			err := command.run([]string{mode, "--to", "0.23.1", "--json", "--root", root}, &stdout, &bytes.Buffer{})
			if !errors.Is(err, errUpgradeRequired) {
				t.Fatalf("upgrade error = %v", err)
			}
			var report upgradeReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.SchemaVersion != 5 || !report.ReadOnly || !report.PlanAvailable || report.ApplyAvailable ||
				report.Status != upgradeMigrationRequired || report.Applied || report.RolledBack || len(report.Steps) != 5 ||
				report.Steps[0].ID != "framework-0.19.0-to-0.20.0" || report.Steps[0].Automatic ||
				report.Steps[4].ID != "framework-0.23.0-to-0.23.1" || !report.Steps[4].Automatic ||
				!hasUpgradeDiagnostic(report, "template_adoption_required") ||
				(mode == "--apply" && !hasUpgradeDiagnostic(report, "manual_steps_required")) {
				t.Fatalf("earlier manual requirements = %#v", report)
			}
			if releaseTestSnapshot(t, root) != before {
				t.Fatal("blocked upgrade modified application files")
			}
		})
	}
}

func makePublicationReleaseProject(t *testing.T, version string, template int) string {
	t.Helper()
	root := makeSubscriptionReleaseProject(t, template)
	for _, relative := range []string{"backend/go.mod", "pubspec.yaml", ".bridra/project.json"} {
		path := filepath.Join(root, relative)
		contents := readTestFile(t, path)
		if bytes.Count(contents, []byte("0.22.0")) != 1 {
			t.Fatalf("%s does not contain exactly one source version", relative)
		}
		if err := os.WriteFile(path, bytes.ReplaceAll(contents, []byte("0.22.0"), []byte(version)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
