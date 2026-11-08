package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Releases registered as dependency-only catalog transitions. A new release of
// this kind adds one row instead of a new test file.
var dependencyOnlyReleases = []struct {
	target string
	from   []string
}{
	{"0.23.0", []string{"0.22.0"}},
	{"0.23.1", []string{"0.22.0", "0.23.0"}},
	{"0.23.2", []string{"0.21.0", "0.22.0", "0.23.0", "0.23.1"}},
	{"0.24.1", []string{"0.23.2", "0.24.0"}},
}

// Registered framework versions in catalog order from the last manual baseline.
var releaseCatalogChain = []string{
	"0.19.0", "0.20.0", "0.21.0", "0.22.0", "0.23.0", "0.23.1", "0.23.2", "0.24.0", "0.24.1",
}

// Starter files an application may own; dependency-only upgrades must keep them.
var releaseStarterOwnedFiles = []string{
	"lib/ui/theme/app_theme.dart", "lib/features/components/components_view.dart",
	"macos/Runner/MainFlutterWindow.swift",
}

var releaseAllPlatforms = []string{"android", "ios", "linux", "macos", "windows", "web"}

func TestUpgradeDependencyOnlyReleasesPlanAutomaticPaths(t *testing.T) {
	type planCase struct {
		from      string
		platforms []string
	}
	for _, release := range dependencyOnlyReleases {
		chain := releaseCatalogPath(t, releaseCatalogChain[0], release.target)
		var cases []planCase
		for _, from := range release.from {
			cases = append(cases, planCase{from, []string{"linux", "windows"}})
		}
		// Platform-filtered requirements must not turn the final hop into a manual step.
		for _, platform := range releaseAllPlatforms {
			cases = append(cases, planCase{chain[len(chain)-2], []string{platform}})
		}
		for _, test := range cases {
			for _, template := range []int{8, 9} {
				name := fmt.Sprintf("%s/from-%s/%s/template-%d",
					release.target, test.from, strings.Join(test.platforms, "+"), template)
				t.Run(name, func(t *testing.T) {
					assertDependencyOnlyPlan(t, release.target, test.from, template, test.platforms)
				})
			}
		}
	}
}

func TestUpgradeDependencyOnlyReleasesKeepEarlierManualAdoption(t *testing.T) {
	for _, release := range dependencyOnlyReleases {
		path := releaseCatalogPath(t, releaseCatalogChain[0], release.target)
		for _, mode := range []string{"--plan", "--apply"} {
			t.Run(release.target+"/"+mode, func(t *testing.T) {
				root := makeUpgradeProjectRoot(t, upgradeMetadataForPlatforms(t, path[0], 7, []string{"linux"}))
				before := releaseTestSnapshot(t, root)
				report, err := runReleaseUpgrade(t, root, mode, release.target, forbidReleaseProcesses(t, "manual adoption"))
				if !errors.Is(err, errUpgradeRequired) {
					t.Fatalf("upgrade error = %v", err)
				}
				last := len(report.Steps) - 1
				if report.SchemaVersion != 5 || !report.ReadOnly || !report.PlanAvailable || report.ApplyAvailable ||
					report.Status != upgradeMigrationRequired || report.Applied || report.RolledBack ||
					len(report.Steps) != len(path)-1 ||
					report.Steps[0].ID != "framework-0.19.0-to-0.20.0" || report.Steps[0].Automatic ||
					report.Steps[last].ID != "framework-"+path[last]+"-to-"+release.target || !report.Steps[last].Automatic ||
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
}

func TestUpgradeDependencyOnlyReleasesApplyPreservesApplicationAndRollsBack(t *testing.T) {
	variants := []struct {
		template         int
		failVerification bool
	}{{8, false}, {8, true}, {9, false}, {9, true}}
	for _, release := range dependencyOnlyReleases {
		for _, from := range release.from {
			for _, variant := range variants {
				name := fmt.Sprintf("%s/from-%s/template-%d/rollback-%t",
					release.target, from, variant.template, variant.failVerification)
				t.Run(name, func(t *testing.T) {
					assertDependencyOnlyApply(t, release.target, from, variant.template, variant.failVerification)
				})
			}
		}
	}
}

func assertDependencyOnlyPlan(t *testing.T, target, from string, template int, platforms []string) {
	t.Helper()
	root := makeReleaseProject(t, from, template, platforms)
	before := releaseTestSnapshot(t, root)
	report, err := runReleaseUpgrade(t, root, "--plan", target, forbidReleaseProcesses(t, "read-only plan"))
	if !errors.Is(err, errUpgradeRequired) {
		t.Fatalf("upgrade error = %v", err)
	}
	path := releaseCatalogPath(t, from, target)
	if report.SchemaVersion != 5 || !report.ReadOnly || !report.PlanAvailable || !report.ApplyAvailable ||
		report.Status != upgradeMigrationRequired || len(report.Steps) != len(path)-1 ||
		report.Project.FrameworkVersion != from || report.Project.ProjectMetadataVersion != 3 ||
		report.Project.TemplateVersion != template || report.Project.ProtocolVersion != 3 ||
		!slices.Equal(report.Project.Platforms, platforms) ||
		report.Target.FrameworkVersion != target || report.Target.ProjectMetadataVersion != 3 ||
		report.Target.TemplateVersion != 9 || report.Target.TemplateProtocolVersion != 1 ||
		hasUpgradeDiagnostic(report, "template_update_available") != (template == 8) {
		t.Fatalf("dependency-only plan = %#v", report)
	}
	for index, step := range report.Steps {
		stepFrom, stepTo := path[index], path[index+1]
		if step.ID != "framework-"+stepFrom+"-to-"+stepTo || step.From != stepFrom || step.To != stepTo || !step.Automatic {
			t.Fatalf("step %d = %#v", index, step)
		}
	}
	if releaseTestSnapshot(t, root) != before {
		t.Fatal("read-only plan modified application files")
	}
}

func assertDependencyOnlyApply(t *testing.T, target, from string, template int, failVerification bool) {
	t.Helper()
	root := makeReleaseProject(t, from, template, []string{"linux", "windows"})
	for _, relative := range releaseStarterOwnedFiles {
		writeReleaseTestFile(t, root, relative, "application-owned "+relative+"\n")
	}
	preserved := map[string][]byte{}
	for _, relative := range append([]string{
		"backend/app/owned.go", "lib/application_view.dart", "schema/bridra.json", "schema/bridra.baseline.json",
		"backend/framework/protocol_version.gen.go", "lib/api/generated/bridra_api.g.dart",
	}, releaseStarterOwnedFiles...) {
		preserved[relative] = readTestFile(t, filepath.Join(root, relative))
	}
	before := releaseTestSnapshot(t, root)
	var expectedMetadata projectMetadata
	if err := json.Unmarshal(readTestFile(t, filepath.Join(root, ".bridra/project.json")), &expectedMetadata); err != nil {
		t.Fatal(err)
	}
	expectedMetadata.FrameworkVersion = target
	wantProcesses := []upgradeProcess{
		{Label: "Resolve Go dependencies", Name: "go", Arguments: []string{"mod", "tidy"}, Directory: filepath.Join(root, "backend")},
		{Label: "Resolve Flutter dependencies", Name: "fvm", Arguments: []string{"flutter", "pub", "get"}, Directory: root},
		{Label: "Verify upgraded project", Name: "make", Arguments: []string{"verify"}, Directory: root},
	}
	calls := 0
	run := fakeDependencyOnlyProcesses(t, root, target, expectedMetadata, wantProcesses, failVerification, &calls)
	report, err := runReleaseUpgrade(t, root, "--apply", target, run)
	wantStatus := upgradeApplied
	if failVerification {
		wantStatus = upgradeApplyFailed
		if !errors.Is(err, errUpgradeApply) || releaseTestSnapshot(t, root) != before {
			t.Fatalf("rollback failed: %v", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 5 || report.Status != wantStatus || report.ReadOnly ||
		report.Applied == failVerification || report.RolledBack != failVerification || calls != len(wantProcesses) {
		t.Fatalf("report = %#v, process count = %d", report, calls)
	}
	for relative, contents := range preserved {
		assertFileContents(t, filepath.Join(root, relative), contents)
	}
}

// fakeDependencyOnlyProcesses checks each upgrade process sees only final-target
// dependencies; intermediate hops such as the unpublished 0.23.0 are never resolved.
func fakeDependencyOnlyProcesses(t *testing.T, root, target string, expectedMetadata projectMetadata,
	want []upgradeProcess, failVerification bool, calls *int) func(context.Context, upgradeProcess) error {
	return func(_ context.Context, process upgradeProcess) error {
		if *calls >= len(want) {
			t.Fatalf("unexpected repeated process %d: %#v", *calls, process)
		}
		expected := want[*calls]
		if process.Label != expected.Label || process.Name != expected.Name ||
			!slices.Equal(process.Arguments, expected.Arguments) || process.Directory != expected.Directory {
			t.Fatalf("process %d = %#v, want %#v", *calls, process, expected)
		}
		*calls++
		assertFileContents(t, filepath.Join(root, "backend/go.mod"), []byte(
			"module example.test/app\n\ngo 1.25\n\nrequire github.com/cluion/bridra/backend v"+target+"\n"))
		assertFileContents(t, filepath.Join(root, "pubspec.yaml"), []byte(
			"name: example\ndependencies:\n  bridra_flutter: '^"+target+"'\n"))
		var metadata projectMetadata
		if err := json.Unmarshal(readTestFile(t, filepath.Join(root, ".bridra/project.json")), &metadata); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(metadata, expectedMetadata) {
			t.Fatalf("metadata = %#v, want %#v", metadata, expectedMetadata)
		}
		switch process.Name {
		case "go":
			writeReleaseTestFile(t, root, "backend/go.sum", "resolved "+target+" go sum\n")
		case "fvm":
			writeReleaseTestFile(t, root, "pubspec.lock", "resolved "+target+" pub lock\n")
		case "make":
			if failVerification {
				return errors.New("verification failed")
			}
		}
		return nil
	}
}

func runReleaseUpgrade(t *testing.T, root, mode, target string,
	run func(context.Context, upgradeProcess) error) (upgradeReport, error) {
	t.Helper()
	command := testUpgradeCommand()
	command.system.run = run
	var stdout bytes.Buffer
	err := command.run([]string{mode, "--to", target, "--json", "--root", root}, &stdout, &bytes.Buffer{})
	var report upgradeReport
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode %s report: %v (upgrade error: %v)", mode, decodeErr, err)
	}
	return report, err
}

func forbidReleaseProcesses(t *testing.T, operation string) func(context.Context, upgradeProcess) error {
	return func(context.Context, upgradeProcess) error {
		t.Fatal(operation + " ran a process")
		return nil
	}
}

func releaseCatalogPath(t *testing.T, from, to string) []string {
	t.Helper()
	start, end := slices.Index(releaseCatalogChain, from), slices.Index(releaseCatalogChain, to)
	if start < 0 || end <= start {
		t.Fatalf("no catalog path from %s to %s", from, to)
	}
	return releaseCatalogChain[start : end+1]
}

func makeReleaseProject(t *testing.T, version string, template int, platforms []string) string {
	t.Helper()
	root := makeUpgradeProjectRoot(t, upgradeMetadataForPlatforms(t, version, template, platforms))
	for relative, contents := range map[string]string{
		"backend/go.mod":              "module example.test/app\n\ngo 1.25\n\nrequire github.com/cluion/bridra/backend v" + version + "\n",
		"backend/go.sum":              "initial go sum\n",
		"pubspec.yaml":                "name: example\ndependencies:\n  bridra_flutter: '^" + version + "'\n",
		"pubspec.lock":                "initial pub lock\n",
		"backend/app/owned.go":        "package app\n\nconst Owned = true\n",
		"lib/application_view.dart":   "const applicationOwned = true;\n",
		"schema/bridra.baseline.json": string(readTestFile(t, filepath.Join(root, "schema/bridra.json"))),
	} {
		writeReleaseTestFile(t, root, relative, contents)
	}
	return root
}
