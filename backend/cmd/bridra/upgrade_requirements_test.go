package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUpgradeRequirementsFollowProjectPlatforms(t *testing.T) {
	for _, test := range []struct {
		name, from, to string
		template       int
		platforms      []string
		automatic      bool
		include        []string
		exclude        []string
	}{
		{"Android skips iOS adoption", "0.16.0", "0.17.0", 6, []string{"android"}, true, nil, []string{"Embedded Core"}},
		{"iOS keeps native adoption", "0.16.0", "0.17.0", 6, []string{"ios"}, false, []string{"Embedded Core"}, nil},
		{"mixed platforms keep iOS adoption", "0.16.0", "0.17.0", 6, []string{"android", "ios"}, false, []string{"Embedded Core"}, nil},
		{"legacy metadata stays conservative", "0.16.0", "0.17.0", 6, nil, false, []string{"Embedded Core"}, nil},
		{"Android skips desktop handoff", "0.15.0", "0.16.0", 5, []string{"android"}, true, nil, []string{"Sidecar", "macOS", "bookmark"}},
		{"Windows keeps shared Sidecar review", "0.15.0", "0.16.0", 5, []string{"windows"}, false, []string{"Sidecar"}, []string{"macOS", "bookmark", "entitlements"}},
		{"macOS keeps native handoff", "0.15.0", "0.16.0", 5, []string{"macos"}, false, []string{"Sidecar", "bookmark", "entitlements"}, nil},
		{"Android keeps shared lifecycle and codegen", "0.19.0", "0.20.0", 7, []string{"android"}, false, []string{"HTTP shutdown", "unary Dart"}, []string{"macOS", "entitlements", "display metadata"}},
		{"macOS keeps socket entitlements", "0.19.0", "0.20.0", 7, []string{"macos"}, false, []string{"HTTP shutdown", "unary Dart", "entitlements"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata := currentProjectMetadata(test.from, test.template, 3)
			if test.platforms != nil {
				metadata = upgradeMetadataForPlatforms(t, test.from, test.template, test.platforms)
			}
			root := makeUpgradeProjectRoot(t, metadata)
			before := releaseTestSnapshot(t, root)
			command := testUpgradeCommand()
			command.system.run = func(context.Context, upgradeProcess) error {
				t.Fatal("read-only plan ran a process")
				return nil
			}
			var stdout bytes.Buffer
			err := command.run([]string{"--plan", "--to", test.to, "--json", "--root", root}, &stdout, &bytes.Buffer{})
			if !errors.Is(err, errUpgradeRequired) {
				t.Fatalf("upgrade error = %v, want errUpgradeRequired", err)
			}
			var report upgradeReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if !report.PlanAvailable || !report.ReadOnly || report.ApplyAvailable != test.automatic ||
				len(report.Steps) != 1 || report.Steps[0].Automatic != test.automatic ||
				report.Project.ProtocolVersion != 3 {
				t.Fatalf("report = %#v", report)
			}
			if test.platforms != nil && !reflect.DeepEqual(report.Project.Platforms, test.platforms) {
				t.Fatalf("reported platforms = %v, want %v", report.Project.Platforms, test.platforms)
			}
			for _, text := range test.include {
				if !strings.Contains(report.Steps[0].Description, text) {
					t.Fatalf("missing %q: %s", text, report.Steps[0].Description)
				}
			}
			for _, text := range test.exclude {
				if strings.Contains(report.Steps[0].Description, text) {
					t.Fatalf("irrelevant %q: %s", text, report.Steps[0].Description)
				}
			}
			diagnostic := "template_adoption_required"
			if test.automatic {
				diagnostic = "template_update_available"
			}
			if !hasUpgradeDiagnostic(report, diagnostic) || releaseTestSnapshot(t, root) != before {
				t.Fatalf("missing %s or read-only plan changed files", diagnostic)
			}
		})
	}
}

func TestUpgradeTemplateAdoptionCannotBeInferredFromDependencies(t *testing.T) {
	for _, test := range []struct {
		name, platform, diagnostic string
		template                   int
		manual                     bool
	}{
		{"unneeded iOS changes remain optional", "android", "template_update_available", 6, false},
		{"missing iOS adoption still blocks", "ios", "template_adoption_required", 6, true},
		{"unclassified template still blocks", "android", "template_upgrade_required", 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := makeUpgradeProjectRoot(t, upgradeMetadataForPlatforms(t, "0.17.0", test.template, []string{test.platform}))
			before := releaseTestSnapshot(t, root)
			command := testUpgradeCommand()
			command.system.run = func(context.Context, upgradeProcess) error {
				t.Fatal("optional or manual adoption ran a process")
				return nil
			}
			var stdout bytes.Buffer
			err := command.run([]string{"--apply", "--to", "0.17.0", "--json", "--root", root}, &stdout, &bytes.Buffer{})
			if test.manual && !errors.Is(err, errUpgradeRequired) || !test.manual && err != nil {
				t.Fatalf("upgrade error = %v, manual = %t", err, test.manual)
			}
			var report upgradeReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if !hasUpgradeDiagnostic(report, test.diagnostic) || !report.ReadOnly || report.ApplyAvailable ||
				(test.manual && (len(report.Steps) != 1 || report.Steps[0].Automatic)) ||
				(!test.manual && (report.Status != upgradeCurrent || len(report.Steps) != 0)) ||
				releaseTestSnapshot(t, root) != before {
				t.Fatalf("report = %#v", report)
			}
		})
	}
}

func TestUpgradeApplyPreservesTemplateBaselineAcrossTemplateRelease(t *testing.T) {
	for _, failVerification := range []bool{false, true} {
		name := "success"
		if failVerification {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			root := makeUpgradeableProjectWithProtocol(t, 3)
			metadataPath := filepath.Join(root, ".bridra", "project.json")
			if err := os.WriteFile(metadataPath, []byte(upgradeMetadataForPlatforms(t, "0.1.0", 2, []string{"android"})), 0o644); err != nil {
				t.Fatal(err)
			}
			before := releaseTestSnapshot(t, root)
			ownedPath := filepath.Join(root, "backend", "app", "owned.go")
			ownedBefore := readTestFile(t, ownedPath)
			catalog := automaticMigrationCatalog()
			for index := range catalog.Releases {
				catalog.Releases[index].ProjectMetadataVersion = 3
			}
			command := testUpgradeCommandWithCatalog(catalog)
			var calls int
			command.system.run = func(_ context.Context, process upgradeProcess) error {
				calls++
				if process.Name == "make" {
					var metadata projectMetadata
					if err := json.Unmarshal(readTestFile(t, metadataPath), &metadata); err != nil {
						t.Fatal(err)
					}
					if metadata.FrameworkVersion != "0.2.1" || metadata.TemplateVersion != 2 ||
						metadata.ProtocolVersion != 3 || !reflect.DeepEqual(metadata.Platforms, []string{"android"}) {
						t.Fatalf("metadata = %#v", metadata)
					}
					if failVerification {
						return errors.New("verification failed")
					}
				}
				return nil
			}
			var stdout bytes.Buffer
			err := command.run([]string{"--apply", "--to", "0.2.1", "--json", "--root", root}, &stdout, &bytes.Buffer{})
			if failVerification {
				if !errors.Is(err, errUpgradeApply) || releaseTestSnapshot(t, root) != before {
					t.Fatalf("rollback error = %v, or files were not restored", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertTestFileContains(t, filepath.Join(root, "backend", "go.mod"), "v0.2.1")
				assertTestFileContains(t, filepath.Join(root, "pubspec.yaml"), "'^0.2.1'")
				assertFileContents(t, ownedPath, ownedBefore)
				stdout.Reset()
				if err := command.run([]string{"--plan", "--to", "0.2.1", "--json", "--root", root}, &stdout, &bytes.Buffer{}); err != nil {
					t.Fatalf("plan after apply: %v", err)
				}
				var report upgradeReport
				if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if report.Status != upgradeCurrent || !hasUpgradeDiagnostic(report, "template_update_available") {
					t.Fatalf("plan after apply = %#v", report)
				}
			}
			if calls != 3 {
				t.Fatalf("process calls = %d, want 3", calls)
			}
		})
	}
}

func TestUpgradeMissingAdoptionPrecedesNewFrameworkSteps(t *testing.T) {
	root := makeUpgradeProjectRoot(t, upgradeMetadataForPlatforms(t, "0.18.0", 6, []string{"ios"}))
	var stdout bytes.Buffer
	err := testUpgradeCommand().run([]string{"--plan", "--to", "0.19.0", "--json", "--root", root}, &stdout, &bytes.Buffer{})
	if !errors.Is(err, errUpgradeRequired) {
		t.Fatalf("upgrade error = %v", err)
	}
	var report upgradeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ApplyAvailable || len(report.Steps) != 2 ||
		report.Steps[0].ID != "framework-0.16.0-to-0.17.0" || report.Steps[0].Automatic ||
		report.Steps[0].Kind != "project_template" || report.Steps[0].Order != 1 ||
		report.Steps[1].ID != "framework-0.18.0-to-0.19.0" || !report.Steps[1].Automatic ||
		report.Steps[1].Order != 2 {
		t.Fatalf("report = %#v", report)
	}
}

func TestUpgradeRequirementCatalogValidatesAndNormalizesPlatforms(t *testing.T) {
	for _, test := range []struct {
		name, description string
		platforms         []string
		invalid           bool
	}{
		{"normalized", "Review iOS runner", []string{" IOS "}, false},
		{"unknown", "Review unknown runner", []string{"unknown"}, true},
		{"duplicate", "Review iOS runner", []string{"ios", "IOS"}, true},
		{"empty requirement", "", []string{"ios"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := automaticMigrationCatalog()
			catalog.Migrations[0].Requirements = []migrationRequirement{{Description: test.description, Platforms: test.platforms}}
			path, available, err := catalog.migrationPath("0.1.0", "0.1.1")
			if test.invalid {
				if err == nil || available {
					t.Fatalf("invalid catalog accepted: %v, %t", err, available)
				}
				return
			}
			if err != nil || !available || len(path) != 1 {
				t.Fatalf("path = %#v, %t, %v", path, available, err)
			}
			if automatic, description := migrationForPlatforms(path[0], []string{"ios"}); automatic || !strings.Contains(description, "iOS runner") {
				t.Fatalf("normalized requirement was skipped: %t, %s", automatic, description)
			}
			if !reflect.DeepEqual(catalog.Migrations[0].Requirements[0].Platforms, test.platforms) {
				t.Fatal("planning mutated the registered catalog")
			}
		})
	}
}

func upgradeMetadataForPlatforms(t *testing.T, version string, template int, platforms []string) string {
	t.Helper()
	var metadata projectMetadata
	if err := json.Unmarshal([]byte(currentProjectMetadata(version, template, 3)), &metadata); err != nil {
		t.Fatal(err)
	}
	metadata.SchemaVersion = 3
	metadata.Platforms = platforms
	contents, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(contents) + "\n"
}
