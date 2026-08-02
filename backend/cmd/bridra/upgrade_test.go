package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/codegen"
	"github.com/cluion/bridra/backend/framework"
	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

func TestUpgradeCheckReportsCurrentContractWithoutWriting(t *testing.T) {
	root := makeUpgradeProjectRoot(t, currentProjectMetadata(
		releaseinfo.Version,
		releaseinfo.ProjectTemplateVersion,
		framework.ProtocolVersion,
	))
	applicationPath := filepath.Join(root, "backend", "app", "owned.go")
	applicationContents := []byte("package app\n\nconst Owned = true\n")
	if err := os.WriteFile(applicationPath, applicationContents, 0o644); err != nil {
		t.Fatalf("write application file: %v", err)
	}
	metadataBefore := readTestFile(t, filepath.Join(root, ".bridra", "project.json"))

	var stdout bytes.Buffer
	command := testUpgradeCommand()
	if err := command.run(
		[]string{"--json", "--root", root},
		&stdout,
		&bytes.Buffer{},
	); err != nil {
		t.Fatalf("upgrade check: %v", err)
	}
	var report upgradeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != upgradeCurrent || !report.ReadOnly || report.MigrationRequired {
		t.Fatalf("report = %#v", report)
	}
	if report.Diagnostics[0].Code != "contract_current" {
		t.Fatalf("diagnostics = %#v", report.Diagnostics)
	}
	assertFileContents(t, applicationPath, applicationContents)
	assertFileContents(t, filepath.Join(root, ".bridra", "project.json"), metadataBefore)
}

func TestUpgradeCheckExplainsLegacyNMinusOneMetadataWithoutWriting(t *testing.T) {
	root := makeUpgradeProjectRoot(t, validProjectMetadata)
	applicationPath := filepath.Join(root, "backend", "app", "owned.go")
	applicationContents := []byte("package app\n\nconst Owned = true\n")
	if err := os.WriteFile(applicationPath, applicationContents, 0o644); err != nil {
		t.Fatalf("write application file: %v", err)
	}
	metadataPath := filepath.Join(root, ".bridra", "project.json")
	metadataBefore := readTestFile(t, metadataPath)

	var stdout bytes.Buffer
	err := testUpgradeCommand().run(
		[]string{"--check", "--json", "--root", root},
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
	if report.Status != upgradeMigrationRequired || !report.MigrationRequired ||
		len(report.Diagnostics) != 1 || report.Diagnostics[0].Code != "legacy_project_metadata" {
		t.Fatalf("report = %#v", report)
	}
	if !strings.Contains(report.Diagnostics[0].Action, "schema 2") {
		t.Fatalf("migration action = %q", report.Diagnostics[0].Action)
	}
	assertFileContents(t, applicationPath, applicationContents)
	assertFileContents(t, metadataPath, metadataBefore)
}

func TestUpgradeCheckReportsOlderAndNewerContracts(t *testing.T) {
	tests := []struct {
		name       string
		metadata   string
		wantError  error
		wantStatus upgradeStatus
		wantCode   string
	}{
		{
			name:      "older framework and template",
			metadata:  currentProjectMetadata("0.0.9", 1, 1),
			wantError: errUpgradeUnsupported, wantStatus: upgradeUnsupported,
			wantCode: "framework_migration_path_missing",
		},
		{
			name: "newer metadata schema",
			metadata: `{
  "schemaVersion": 3,
  "projectName": "example",
  "goModule": "example.test/app",
  "frameworkModule": "github.com/cluion/bridra/backend",
  "frameworkVersion": "0.2.0",
  "templateVersion": 3,
  "protocolVersion": 2,
  "futureContract": true
}
`,
			wantError: errUpgradeUnsupported, wantStatus: upgradeUnsupported,
			wantCode: "project_metadata_newer_than_cli",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := makeUpgradeProjectRoot(t, test.metadata)
			var stdout bytes.Buffer
			err := testUpgradeCommand().run(
				[]string{"--check", "--json", "--root", root},
				&stdout,
				&bytes.Buffer{},
			)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("upgrade error = %v, want %v", err, test.wantError)
			}
			var report upgradeReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatalf("decode report: %v", err)
			}
			if report.Status != test.wantStatus || !hasUpgradeDiagnostic(report, test.wantCode) {
				t.Fatalf("report = %#v", report)
			}
		})
	}
}

func TestUpgradeCheckAcceptsCurrentCustomApplicationProtocol(t *testing.T) {
	root := makeUpgradeProjectRoot(t, currentProjectMetadata(
		releaseinfo.Version,
		releaseinfo.ProjectTemplateVersion,
		3,
	))
	var stdout bytes.Buffer
	if err := testUpgradeCommand().run(
		[]string{"--plan", "--json", "--root", root},
		&stdout,
		&bytes.Buffer{},
	); err != nil {
		t.Fatalf("upgrade custom application protocol: %v", err)
	}
	var report upgradeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.SchemaVersion != upgradeReportSchemaVersion ||
		report.Status != upgradeCurrent || !report.PlanAvailable ||
		report.Project.ProtocolVersion != 3 ||
		report.Target.TemplateProtocolVersion != framework.ProtocolVersion ||
		!hasUpgradeDiagnostic(report, "application_protocol_custom") {
		t.Fatalf("report = %#v", report)
	}
	var raw struct {
		Target map[string]json.RawMessage `json:"target"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw report: %v", err)
	}
	if _, exists := raw.Target["protocolVersion"]; exists {
		t.Fatalf("target retained ambiguous protocolVersion: %s", stdout.String())
	}
	if _, exists := raw.Target["templateProtocolVersion"]; !exists {
		t.Fatalf("target omitted templateProtocolVersion: %s", stdout.String())
	}
}

func TestUpgradeCheckRejectsInconsistentApplicationRPCContract(t *testing.T) {
	root := makeUpgradeProjectRoot(t, currentProjectMetadata(
		releaseinfo.Version,
		releaseinfo.ProjectTemplateVersion,
		3,
	))
	goProtocolPath := filepath.Join(root, filepath.FromSlash(codegen.GoProtocolPath))
	before := readTestFile(t, goProtocolPath)
	inconsistent := bytes.Replace(before, []byte("ProtocolVersion = 3"), []byte("ProtocolVersion = 2"), 1)
	if bytes.Equal(inconsistent, before) {
		t.Fatal("test fixture did not contain protocol 3")
	}
	if err := os.WriteFile(goProtocolPath, inconsistent, 0o644); err != nil {
		t.Fatalf("write inconsistent generated protocol: %v", err)
	}

	var stdout bytes.Buffer
	err := testUpgradeCommand().run(
		[]string{"--plan", "--json", "--root", root},
		&stdout,
		&bytes.Buffer{},
	)
	if !errors.Is(err, errUpgradeUnsupported) {
		t.Fatalf("upgrade error = %v, want errUpgradeUnsupported", err)
	}
	var report upgradeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != upgradeUnsupported || report.PlanAvailable ||
		!hasUpgradeDiagnostic(report, "application_rpc_contract_inconsistent") {
		t.Fatalf("report = %#v", report)
	}
	if !strings.Contains(
		report.Diagnostics[0].Message,
		"metadata 3, schema 3, Go 2, Dart 3",
	) {
		t.Fatalf("diagnostics = %#v", report.Diagnostics)
	}
	assertFileContents(t, goProtocolPath, inconsistent)
}

func TestUpgradePlanAndCheckFlagsAreAliases(t *testing.T) {
	root := makeUpgradeProjectRoot(t, currentProjectMetadata(
		releaseinfo.Version,
		releaseinfo.ProjectTemplateVersion,
		framework.ProtocolVersion,
	))
	for _, arguments := range [][]string{
		{"--plan", "--json", "--root", root},
		{"--check", "--json", "--root", root},
	} {
		var stdout bytes.Buffer
		if err := testUpgradeCommand().run(arguments, &stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("upgrade %v: %v", arguments, err)
		}
		var report upgradeReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("decode report: %v", err)
		}
		if !report.ReadOnly || !report.PlanAvailable || len(report.Steps) != 0 {
			t.Fatalf("report = %#v", report)
		}
	}

	err := testUpgradeCommand().run(
		[]string{"--plan", "--check", "--root", root},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if !errors.Is(err, errUsage) {
		t.Fatalf("upgrade error = %v, want errUsage", err)
	}
}

func TestUpgradePlannerResolvesCrossPatchAndMinorPaths(t *testing.T) {
	catalog := testMigrationCatalog()
	tests := []struct {
		name     string
		target   string
		wantPath []string
	}{
		{
			name:     "multiple patch releases",
			target:   "0.1.2",
			wantPath: []string{"framework-0.1.0-to-0.1.1", "framework-0.1.1-to-0.1.2"},
		},
		{
			name:   "three patch hops",
			target: "0.1.3",
			wantPath: []string{
				"framework-0.1.0-to-0.1.1",
				"framework-0.1.1-to-0.1.2",
				"framework-0.1.2-to-0.1.3",
			},
		},
		{
			name:   "multiple patch and minor releases",
			target: "0.2.1",
			wantPath: []string{
				"framework-0.1.0-to-0.1.1",
				"framework-0.1.1-to-0.1.2",
				"framework-0.1.2-to-0.1.3",
				"framework-0.1.3-to-0.2.0",
				"framework-0.2.0-to-0.2.1",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := makeUpgradeProjectRoot(t, currentProjectMetadata("0.1.0", 2, 1))
			var stdout bytes.Buffer
			err := testUpgradeCommandWithCatalog(catalog).run(
				[]string{"--plan", "--to", test.target, "--json", "--root", root},
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
			if report.Status != upgradeMigrationRequired || !report.PlanAvailable {
				t.Fatalf("report = %#v", report)
			}
			var path []string
			for _, step := range report.Steps {
				if step.Kind == "framework" {
					path = append(path, step.ID)
				}
			}
			if strings.Join(path, ",") != strings.Join(test.wantPath, ",") {
				t.Fatalf("path = %v, want %v", path, test.wantPath)
			}
			for index, step := range report.Steps {
				if step.Order != index+1 {
					t.Fatalf("step %d order = %d", index, step.Order)
				}
			}
		})
	}
}

func TestUpgradePlannerKeepsCustomApplicationProtocolOutOfFrameworkSteps(t *testing.T) {
	root := makeUpgradeProjectRoot(t, currentProjectMetadata("0.1.0", 2, 3))
	var stdout bytes.Buffer
	err := testUpgradeCommandWithCatalog(automaticMigrationCatalog()).run(
		[]string{"--plan", "--to", "0.1.3", "--json", "--root", root},
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
		!report.ApplyAvailable || report.Project.ProtocolVersion != 3 ||
		report.Target.TemplateProtocolVersion != 1 ||
		!hasUpgradeDiagnostic(report, "application_protocol_custom") {
		t.Fatalf("report = %#v", report)
	}
	for _, step := range report.Steps {
		if step.Kind != "framework" {
			t.Fatalf("custom application protocol created step %#v", step)
		}
	}
}

func TestUpgradePlannerRejectsMissingMigrationHop(t *testing.T) {
	catalog := testMigrationCatalog()
	catalog.Migrations = append(
		[]frameworkMigration(nil),
		catalog.Migrations[:1]...,
	)
	root := makeUpgradeProjectRoot(t, currentProjectMetadata("0.1.0", 2, 1))
	var stdout bytes.Buffer
	err := testUpgradeCommandWithCatalog(catalog).run(
		[]string{"--to", "0.2.1", "--json", "--root", root},
		&stdout,
		&bytes.Buffer{},
	)
	if !errors.Is(err, errUpgradeUnsupported) {
		t.Fatalf("upgrade error = %v, want errUpgradeUnsupported", err)
	}
	var report upgradeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != upgradeUnsupported || report.PlanAvailable ||
		!hasUpgradeDiagnostic(report, "framework_migration_path_missing") {
		t.Fatalf("report = %#v", report)
	}
	if len(report.Steps) != 0 {
		t.Fatalf("incomplete plan exposed steps = %#v", report.Steps)
	}
}

func TestUpgradePlannerRejectsUnknownTarget(t *testing.T) {
	err := testUpgradeCommandWithCatalog(testMigrationCatalog()).run(
		[]string{"--to", "0.9.0"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if !errors.Is(err, errUpgradeUnsupported) ||
		!strings.Contains(err.Error(), "not available") {
		t.Fatalf("upgrade error = %v", err)
	}
}

func TestUpgradeApplyPreservesCustomApplicationProtocolAndVerifies(t *testing.T) {
	root := makeUpgradeableProjectWithProtocol(t, 3)
	applicationPath := filepath.Join(root, "backend", "app", "owned.go")
	applicationBefore := readTestFile(t, applicationPath)
	generatedBefore := map[string][]byte{}
	for _, relative := range []string{
		codegen.GoProtocolPath,
		codegen.GoRoutesPath,
		codegen.GoRequestsPath,
		codegen.GoResponsesPath,
		codegen.DartClientPath,
	} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		generatedBefore[path] = readTestFile(t, path)
	}
	var calls []upgradeProcess
	system := upgradeSystem{
		run: func(_ context.Context, process upgradeProcess) error {
			calls = append(calls, process)
			switch process.Label {
			case "Resolve Go dependencies":
				assertTestFileContains(t, filepath.Join(root, "backend", "go.mod"), "v0.1.3")
				return os.WriteFile(
					filepath.Join(root, "backend", "go.sum"),
					[]byte("target go sum\n"),
					0o644,
				)
			case "Resolve Flutter dependencies":
				assertTestFileContains(t, filepath.Join(root, "pubspec.yaml"), "'^0.1.3'")
				return os.WriteFile(
					filepath.Join(root, "pubspec.lock"),
					[]byte("target pub lock\n"),
					0o644,
				)
			case "Verify upgraded project":
				assertTestFileContains(
					t,
					filepath.Join(root, ".bridra", "project.json"),
					`"frameworkVersion": "0.1.3"`,
				)
				assertTestFileContains(
					t,
					filepath.Join(root, ".bridra", "project.json"),
					`"protocolVersion": 3`,
				)
				return nil
			default:
				return fmt.Errorf("unexpected process %s", process.Label)
			}
		},
	}
	command := upgradeCommand{
		catalog: func() upgradeCatalog { return automaticMigrationCatalog() },
		system:  system,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := command.run(
		[]string{"--apply", "--to", "0.1.3", "--json", "--root", root},
		&stdout,
		&stderr,
	); err != nil {
		t.Fatalf("upgrade apply: %v\nstderr: %s", err, stderr.String())
	}
	var report upgradeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != upgradeApplied || report.Mode != "apply" ||
		report.ReadOnly || !report.ApplyAvailable || !report.Applied ||
		report.RolledBack || report.MigrationRequired {
		t.Fatalf("report = %#v", report)
	}
	wantSteps := []string{
		"framework-0.1.0-to-0.1.1",
		"framework-0.1.1-to-0.1.2",
		"framework-0.1.2-to-0.1.3",
	}
	if len(report.Steps) != len(wantSteps) {
		t.Fatalf("steps = %#v, want %v", report.Steps, wantSteps)
	}
	for index, want := range wantSteps {
		if report.Steps[index].ID != want || !report.Steps[index].Automatic {
			t.Fatalf("step %d = %#v, want automatic %s", index, report.Steps[index], want)
		}
	}
	if len(calls) != 3 ||
		calls[0].Name != "go" ||
		calls[1].Name != "fvm" ||
		calls[2].Name != "make" {
		t.Fatalf("processes = %#v", calls)
	}
	assertTestFileContains(t, filepath.Join(root, "backend", "go.mod"), "v0.1.3")
	assertTestFileContains(t, filepath.Join(root, "pubspec.yaml"), "'^0.1.3'")
	assertTestFileContains(t, filepath.Join(root, "pubspec.yaml"), "dependency_overrides:")
	assertTestFileContains(t, filepath.Join(root, "backend", "go.sum"), "target go sum")
	assertTestFileContains(t, filepath.Join(root, "pubspec.lock"), "target pub lock")
	assertTestFileContains(
		t,
		filepath.Join(root, ".bridra", "project.json"),
		`"frameworkVersion": "0.1.3"`,
	)
	assertTestFileContains(
		t,
		filepath.Join(root, ".bridra", "project.json"),
		`"protocolVersion": 3`,
	)
	assertFileContents(t, applicationPath, applicationBefore)
	for path, expected := range generatedBefore {
		assertFileContents(t, path, expected)
	}
	if !strings.Contains(stderr.String(), "Verify upgraded project") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUpgradeApplyRollsBackEveryManagedFileOnVerificationFailure(t *testing.T) {
	root := makeUpgradeableProjectWithProtocol(t, 3)
	goSumPath := filepath.Join(root, "backend", "go.sum")
	if err := os.Remove(goSumPath); err != nil {
		t.Fatalf("remove initial go.sum: %v", err)
	}
	managedPaths := []string{
		filepath.Join(root, "backend", "go.mod"),
		filepath.Join(root, "pubspec.yaml"),
		filepath.Join(root, "pubspec.lock"),
		filepath.Join(root, ".bridra", "project.json"),
		filepath.Join(root, "backend", "app", "owned.go"),
		filepath.Join(root, filepath.FromSlash(codegen.GoProtocolPath)),
		filepath.Join(root, filepath.FromSlash(codegen.DartClientPath)),
	}
	before := make(map[string][]byte, len(managedPaths))
	for _, path := range managedPaths {
		before[path] = readTestFile(t, path)
	}
	var calls int
	system := upgradeSystem{
		run: func(_ context.Context, process upgradeProcess) error {
			calls++
			switch process.Label {
			case "Resolve Go dependencies":
				return os.WriteFile(goSumPath, []byte("created go sum\n"), 0o644)
			case "Resolve Flutter dependencies":
				return os.WriteFile(
					filepath.Join(root, "pubspec.lock"),
					[]byte("changed pub lock\n"),
					0o644,
				)
			case "Verify upgraded project":
				return errors.New("verification failed")
			default:
				return fmt.Errorf("unexpected process %s", process.Label)
			}
		},
	}
	command := upgradeCommand{
		catalog: func() upgradeCatalog { return automaticMigrationCatalog() },
		system:  system,
	}
	var stdout bytes.Buffer
	err := command.run(
		[]string{"--apply", "--to", "0.1.2", "--json", "--root", root},
		&stdout,
		&bytes.Buffer{},
	)
	if !errors.Is(err, errUpgradeApply) {
		t.Fatalf("upgrade error = %v, want errUpgradeApply", err)
	}
	var report upgradeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != upgradeApplyFailed || report.ReadOnly ||
		report.Applied || !report.RolledBack ||
		!hasUpgradeDiagnostic(report, "upgrade_apply_failed") {
		t.Fatalf("report = %#v", report)
	}
	if calls != 3 {
		t.Fatalf("process calls = %d", calls)
	}
	for path, expected := range before {
		assertFileContents(t, path, expected)
	}
	if _, err := os.Stat(goSumPath); !os.IsNotExist(err) {
		t.Fatalf("created go.sum survived rollback: %v", err)
	}
}

func TestUpgradeApplyRefusesManualStepsWithoutWriting(t *testing.T) {
	root := makeUpgradeableProject(t)
	goModPath := filepath.Join(root, "backend", "go.mod")
	goModBefore := readTestFile(t, goModPath)
	command := upgradeCommand{
		catalog: func() upgradeCatalog { return testMigrationCatalog() },
		system: upgradeSystem{
			run: func(context.Context, upgradeProcess) error {
				t.Fatal("manual plan ran a process")
				return nil
			},
		},
	}
	var stdout bytes.Buffer
	err := command.run(
		[]string{"--apply", "--to", "0.1.1", "--json", "--root", root},
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
	if report.Status != upgradeMigrationRequired || report.ApplyAvailable ||
		!report.ReadOnly || !hasUpgradeDiagnostic(report, "manual_steps_required") {
		t.Fatalf("report = %#v", report)
	}
	assertFileContents(t, goModPath, goModBefore)
}

func TestUpgradeApplyRejectsDriftBeforeWriting(t *testing.T) {
	root := makeUpgradeableProject(t)
	pubspecPath := filepath.Join(root, "pubspec.yaml")
	contents := bytes.Replace(
		readTestFile(t, pubspecPath),
		[]byte("^0.1.0"),
		[]byte("^0.0.9"),
		1,
	)
	if err := os.WriteFile(pubspecPath, contents, 0o644); err != nil {
		t.Fatalf("write drifted pubspec: %v", err)
	}
	before := readTestFile(t, pubspecPath)
	command := upgradeCommand{
		catalog: func() upgradeCatalog { return automaticMigrationCatalog() },
		system: upgradeSystem{
			run: func(context.Context, upgradeProcess) error {
				t.Fatal("drifted plan ran a process")
				return nil
			},
		},
	}
	var stdout bytes.Buffer
	err := command.run(
		[]string{"--apply", "--to", "0.1.2", "--json", "--root", root},
		&stdout,
		&bytes.Buffer{},
	)
	if !errors.Is(err, errUpgradeApply) {
		t.Fatalf("upgrade error = %v, want errUpgradeApply", err)
	}
	var report upgradeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != upgradeApplyFailed || !report.ReadOnly || report.RolledBack {
		t.Fatalf("report = %#v", report)
	}
	assertFileContents(t, pubspecPath, before)
}

func TestUpgradeApplyIsNoOpForCurrentProject(t *testing.T) {
	root := makeUpgradeProjectRoot(t, currentProjectMetadata(
		releaseinfo.Version,
		releaseinfo.ProjectTemplateVersion,
		framework.ProtocolVersion,
	))
	var stdout bytes.Buffer
	if err := testUpgradeCommand().run(
		[]string{"--apply", "--json", "--root", root},
		&stdout,
		&bytes.Buffer{},
	); err != nil {
		t.Fatalf("upgrade current project: %v", err)
	}
	var report upgradeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != upgradeCurrent || report.Mode != "apply" ||
		!report.ReadOnly || report.Applied {
		t.Fatalf("report = %#v", report)
	}
}

func TestUpgradeApplyManifestEditorsPreserveUnmanagedContent(t *testing.T) {
	goMod := []byte(`module example.test/app

go 1.25

require (
	example.test/other v1.2.3
	github.com/cluion/bridra/backend v0.1.0 // framework
)

replace github.com/cluion/bridra/backend => ../bridra/backend
`)
	updatedGoMod, err := updateGoModRequirement(
		goMod,
		"github.com/cluion/bridra/backend",
		"v0.1.0",
		"v0.1.1",
	)
	if err != nil {
		t.Fatalf("update go.mod: %v", err)
	}
	for _, expected := range []string{
		"github.com/cluion/bridra/backend v0.1.1 // framework",
		"example.test/other v1.2.3",
		"replace github.com/cluion/bridra/backend => ../bridra/backend",
	} {
		if !strings.Contains(string(updatedGoMod), expected) {
			t.Fatalf("updated go.mod = %s, want %q", updatedGoMod, expected)
		}
	}

	pubspec := []byte(`name: example
dependencies:
  flutter:
    sdk: flutter
  bridra_flutter: '^0.1.0' # framework
dependency_overrides:
  bridra_flutter:
    path: ../bridra/packages/bridra_flutter
`)
	updatedPubspec, err := updatePubspecDependency(
		pubspec,
		"bridra_flutter",
		"^0.1.0",
		"^0.1.1",
	)
	if err != nil {
		t.Fatalf("update pubspec: %v", err)
	}
	for _, expected := range []string{
		"bridra_flutter: '^0.1.1' # framework",
		"dependency_overrides:",
		"path: ../bridra/packages/bridra_flutter",
	} {
		if !strings.Contains(string(updatedPubspec), expected) {
			t.Fatalf("updated pubspec = %s, want %q", updatedPubspec, expected)
		}
	}
}

func TestCurrentUpgradeCatalogCoversEveryRegisteredRelease(t *testing.T) {
	catalog := currentUpgradeCatalog()
	target, err := catalog.target(catalog.DefaultTarget)
	if err != nil {
		t.Fatalf("resolve current target: %v", err)
	}
	if target.FrameworkVersion != releaseinfo.Version ||
		target.ProjectMetadataVersion != releaseinfo.ProjectMetadataVersion ||
		target.TemplateVersion != releaseinfo.ProjectTemplateVersion ||
		target.TemplateProtocolVersion != framework.ProtocolVersion {
		t.Fatalf("current target = %#v", target)
	}

	seenReleases := make(map[string]bool)
	currentVersion, err := parseSemanticVersion(catalog.DefaultTarget)
	if err != nil {
		t.Fatalf("parse current version: %v", err)
	}
	for _, release := range catalog.Releases {
		if seenReleases[release.FrameworkVersion] {
			t.Fatalf("duplicate catalog release %s", release.FrameworkVersion)
		}
		seenReleases[release.FrameworkVersion] = true
		version, err := parseSemanticVersion(release.FrameworkVersion)
		if err != nil {
			t.Fatalf("parse release %s: %v", release.FrameworkVersion, err)
		}
		comparison := compareSemanticVersions(version, currentVersion)
		if comparison > 0 {
			t.Fatalf(
				"catalog release %s is newer than current %s",
				release.FrameworkVersion,
				catalog.DefaultTarget,
			)
		}
		if comparison == 0 {
			continue
		}
		path, available, err := catalog.migrationPath(
			release.FrameworkVersion,
			catalog.DefaultTarget,
		)
		if err != nil {
			t.Fatalf("resolve path from %s: %v", release.FrameworkVersion, err)
		}
		if !available || len(path) == 0 {
			t.Fatalf(
				"catalog has no migration path from %s to %s",
				release.FrameworkVersion,
				catalog.DefaultTarget,
			)
		}
	}
}

func TestCurrentUpgradeCatalogIncludesAutomaticCreateHotfix(t *testing.T) {
	path, available, err := currentUpgradeCatalog().migrationPath("0.1.0", "0.1.1")
	if err != nil {
		t.Fatalf("resolve hotfix migration: %v", err)
	}
	if !available || len(path) != 1 {
		t.Fatalf("hotfix path = %#v, available = %t", path, available)
	}
	if path[0].ID != "framework-0.1.0-to-0.1.1" || !path[0].Automatic {
		t.Fatalf("hotfix migration = %#v", path[0])
	}
}

func TestCurrentUpgradeCatalogIncludesAutomaticConcurrentRPCRelease(t *testing.T) {
	path, available, err := currentUpgradeCatalog().migrationPath("0.1.1", "0.2.0")
	if err != nil {
		t.Fatalf("resolve concurrent RPC migration: %v", err)
	}
	if !available || len(path) != 1 {
		t.Fatalf("concurrent RPC path = %#v, available = %t", path, available)
	}
	if path[0].ID != "framework-0.1.1-to-0.2.0" || !path[0].Automatic {
		t.Fatalf("concurrent RPC migration = %#v", path[0])
	}
}

func TestCurrentUpgradeCatalogIncludesAutomaticSchedulingRelease(t *testing.T) {
	path, available, err := currentUpgradeCatalog().migrationPath("0.2.0", "0.3.0")
	if err != nil {
		t.Fatalf("resolve scheduling migration: %v", err)
	}
	if !available || len(path) != 1 {
		t.Fatalf("scheduling path = %#v, available = %t", path, available)
	}
	if path[0].ID != "framework-0.2.0-to-0.3.0" || !path[0].Automatic {
		t.Fatalf("scheduling migration = %#v", path[0])
	}
}

func TestCurrentUpgradeCatalogIncludesManualSidecarRecoveryRelease(t *testing.T) {
	path, available, err := currentUpgradeCatalog().migrationPath("0.3.0", "0.4.0")
	if err != nil {
		t.Fatalf("resolve sidecar recovery migration: %v", err)
	}
	if !available || len(path) != 1 {
		t.Fatalf("sidecar recovery path = %#v, available = %t", path, available)
	}
	if path[0].ID != "framework-0.3.0-to-0.4.0" || path[0].Automatic {
		t.Fatalf("sidecar recovery migration = %#v", path[0])
	}
}

func TestCurrentUpgradeCatalogIncludesManualSingleInstanceRelease(t *testing.T) {
	path, available, err := currentUpgradeCatalog().migrationPath("0.4.0", "0.5.0")
	if err != nil {
		t.Fatalf("resolve single-instance migration: %v", err)
	}
	if !available || len(path) != 1 {
		t.Fatalf("single-instance path = %#v, available = %t", path, available)
	}
	if path[0].ID != "framework-0.4.0-to-0.5.0" || path[0].Automatic {
		t.Fatalf("single-instance migration = %#v", path[0])
	}
}

func TestCurrentUpgradeCatalogIncludesAutomaticStreamingAndFileRelease(t *testing.T) {
	path, available, err := currentUpgradeCatalog().migrationPath("0.5.0", "0.6.0")
	if err != nil {
		t.Fatalf("resolve streaming and file migration: %v", err)
	}
	if !available || len(path) != 1 {
		t.Fatalf("streaming and file path = %#v, available = %t", path, available)
	}
	if path[0].ID != "framework-0.5.0-to-0.6.0" || !path[0].Automatic {
		t.Fatalf("streaming and file migration = %#v", path[0])
	}
}

func TestCurrentUpgradeCatalogIncludesManualGeneratedConsumerPatch(t *testing.T) {
	path, available, err := currentUpgradeCatalog().migrationPath("0.6.0", "0.6.1")
	if err != nil {
		t.Fatalf("resolve generated consumer patch migration: %v", err)
	}
	if !available || len(path) != 1 {
		t.Fatalf("generated consumer patch path = %#v, available = %t", path, available)
	}
	if path[0].ID != "framework-0.6.0-to-0.6.1" || path[0].Automatic {
		t.Fatalf("generated consumer patch migration = %#v", path[0])
	}
}

func TestCurrentUpgradeCatalogIncludesAutomaticPersistentRuntimeRelease(t *testing.T) {
	path, available, err := currentUpgradeCatalog().migrationPath("0.6.1", "0.7.0")
	if err != nil {
		t.Fatalf("resolve persistent runtime migration: %v", err)
	}
	if !available || len(path) != 1 {
		t.Fatalf("persistent runtime path = %#v, available = %t", path, available)
	}
	if path[0].ID != "framework-0.6.1-to-0.7.0" || !path[0].Automatic {
		t.Fatalf("persistent runtime migration = %#v", path[0])
	}
}

func TestCurrentUpgradeCatalogIncludesAutomaticSQLPersistenceRelease(t *testing.T) {
	path, available, err := currentUpgradeCatalog().migrationPath("0.7.0", "0.8.0")
	if err != nil {
		t.Fatalf("resolve SQL persistence migration: %v", err)
	}
	if !available || len(path) != 1 {
		t.Fatalf("SQL persistence path = %#v, available = %t", path, available)
	}
	if path[0].ID != "framework-0.7.0-to-0.8.0" || !path[0].Automatic {
		t.Fatalf("SQL persistence migration = %#v", path[0])
	}
}

func TestCurrentUpgradeCatalogIncludesAutomaticRedisPersistenceRelease(t *testing.T) {
	path, available, err := currentUpgradeCatalog().migrationPath("0.8.0", "0.9.0")
	if err != nil {
		t.Fatalf("resolve Redis persistence migration: %v", err)
	}
	if !available || len(path) != 1 {
		t.Fatalf("Redis persistence path = %#v, available = %t", path, available)
	}
	if path[0].ID != "framework-0.8.0-to-0.9.0" || !path[0].Automatic {
		t.Fatalf("Redis persistence migration = %#v", path[0])
	}
}

func TestCurrentUpgradeCatalogIncludesAutomaticHTTPSecurityRelease(t *testing.T) {
	path, available, err := currentUpgradeCatalog().migrationPath("0.9.0", "0.10.0")
	if err != nil {
		t.Fatalf("resolve HTTP security migration: %v", err)
	}
	if !available || len(path) != 1 {
		t.Fatalf("HTTP security path = %#v, available = %t", path, available)
	}
	if path[0].ID != "framework-0.9.0-to-0.10.0" || !path[0].Automatic {
		t.Fatalf("HTTP security migration = %#v", path[0])
	}
}

func TestCurrentUpgradeCatalogIncludesAutomaticProtocolPlannerPatch(t *testing.T) {
	path, available, err := currentUpgradeCatalog().migrationPath("0.10.0", "0.10.1")
	if err != nil {
		t.Fatalf("resolve protocol planner patch migration: %v", err)
	}
	if !available || len(path) != 1 {
		t.Fatalf("protocol planner patch path = %#v, available = %t", path, available)
	}
	if path[0].ID != "framework-0.10.0-to-0.10.1" || !path[0].Automatic {
		t.Fatalf("protocol planner patch migration = %#v", path[0])
	}
}

func TestCurrentUpgradeCatalogPlansHTTPSecurityForCustomApplicationProtocol(t *testing.T) {
	root := makeUpgradeProjectRoot(t, currentProjectMetadata("0.9.0", 2, 3))
	var stdout bytes.Buffer
	err := testUpgradeCommand().run(
		[]string{"--plan", "--to", "0.10.0", "--json", "--root", root},
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
		report.Steps[0].ID != "framework-0.9.0-to-0.10.0" ||
		report.Project.ProtocolVersion != 3 ||
		report.Target.TemplateProtocolVersion != 1 ||
		!hasUpgradeDiagnostic(report, "application_protocol_custom") {
		t.Fatalf("report = %#v", report)
	}
}

func TestNMinusOneProjectMetadataUsesCurrentGoCore(t *testing.T) {
	root := makeUpgradeProjectRoot(t, validProjectMetadata)
	backendRoot := filepath.Join(root, "backend")
	frameworkPath := filepath.Join(repositoryRoot(t), "backend")
	goMod := fmt.Sprintf(`module example.test/legacy

go 1.25

require github.com/cluion/bridra/backend v%s

replace github.com/cluion/bridra/backend => %s
`, releaseinfo.Version, strconv.Quote(frameworkPath))
	if err := os.WriteFile(filepath.Join(backendRoot, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write legacy go.mod: %v", err)
	}
	testSource := `package app_test

import (
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestCurrentCorePackage(t *testing.T) {
	if framework.FrameworkVersion == "" || framework.ProtocolVersion < 1 {
		t.Fatal("current core package has no compatibility identity")
	}
}
`
	if err := os.WriteFile(
		filepath.Join(backendRoot, "app", "compatibility_test.go"),
		[]byte(testSource),
		0o644,
	); err != nil {
		t.Fatalf("write compatibility test: %v", err)
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = backendRoot
	tidy.Env = append(os.Environ(), "GOWORK=off")
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("resolve N-1 project dependencies: %v\n%s", err, output)
	}
	command := exec.Command("go", "test", "./...")
	command.Dir = backendRoot
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("N-1 project against current core: %v\n%s", err, output)
	}
}

func TestSemanticVersionPrecedence(t *testing.T) {
	versions := []string{"0.1.0-alpha.1", "0.1.0-alpha.2", "0.1.0-beta", "0.1.0"}
	for index := 0; index < len(versions)-1; index++ {
		left, err := parseSemanticVersion(versions[index])
		if err != nil {
			t.Fatalf("parse %s: %v", versions[index], err)
		}
		right, err := parseSemanticVersion(versions[index+1])
		if err != nil {
			t.Fatalf("parse %s: %v", versions[index+1], err)
		}
		if compareSemanticVersions(left, right) >= 0 {
			t.Fatalf("%s should precede %s", versions[index], versions[index+1])
		}
	}
	for _, invalid := range []string{"v0.1.0", "0.01.0", "0.1", "0.1.0-01"} {
		if _, err := parseSemanticVersion(invalid); err == nil {
			t.Fatalf("parseSemanticVersion(%q) succeeded", invalid)
		}
	}
}

func testUpgradeCommand() upgradeCommand {
	return testUpgradeCommandWithCatalog(currentUpgradeCatalog())
}

func testUpgradeCommandWithCatalog(catalog upgradeCatalog) upgradeCommand {
	return upgradeCommand{
		catalog: func() upgradeCatalog { return catalog },
		system:  defaultUpgradeSystem(),
	}
}

func testMigrationCatalog() upgradeCatalog {
	releases := []upgradeRelease{
		{FrameworkVersion: "0.1.0", ProjectMetadataVersion: 2, TemplateVersion: 2, TemplateProtocolVersion: 1},
		{FrameworkVersion: "0.1.1", ProjectMetadataVersion: 2, TemplateVersion: 2, TemplateProtocolVersion: 1},
		{FrameworkVersion: "0.1.2", ProjectMetadataVersion: 2, TemplateVersion: 2, TemplateProtocolVersion: 1},
		{FrameworkVersion: "0.1.3", ProjectMetadataVersion: 2, TemplateVersion: 2, TemplateProtocolVersion: 1},
		{FrameworkVersion: "0.2.0", ProjectMetadataVersion: 2, TemplateVersion: 3, TemplateProtocolVersion: 2},
		{FrameworkVersion: "0.2.1", ProjectMetadataVersion: 2, TemplateVersion: 3, TemplateProtocolVersion: 2},
	}
	migration := func(from, to string) frameworkMigration {
		return frameworkMigration{
			ID:          "framework-" + from + "-to-" + to,
			From:        from,
			To:          to,
			Description: "Review the release migration and update both framework dependencies.",
		}
	}
	return upgradeCatalog{
		CLIVersion:    "0.2.1",
		DefaultTarget: "0.2.1",
		Releases:      releases,
		Migrations: []frameworkMigration{
			migration("0.1.0", "0.1.1"),
			migration("0.1.1", "0.1.2"),
			migration("0.1.2", "0.1.3"),
			migration("0.1.3", "0.2.0"),
			migration("0.2.0", "0.2.1"),
		},
	}
}

func automaticMigrationCatalog() upgradeCatalog {
	catalog := testMigrationCatalog()
	for index := range catalog.Migrations {
		catalog.Migrations[index].Automatic = true
	}
	return catalog
}

func makeUpgradeableProject(t *testing.T) string {
	return makeUpgradeableProjectWithProtocol(t, 1)
}

func makeUpgradeableProjectWithProtocol(t *testing.T, protocolVersion int) string {
	t.Helper()
	root := makeUpgradeProjectRoot(t, currentProjectMetadata("0.1.0", 2, protocolVersion))
	files := map[string]string{
		"backend/go.mod": `module example.test/app

go 1.25

require github.com/cluion/bridra/backend v0.1.0

replace github.com/cluion/bridra/backend => ../bridra/backend
`,
		"backend/go.sum": "initial go sum\n",
		"pubspec.yaml": `name: example
dependencies:
  flutter:
    sdk: flutter
  bridra_flutter: '^0.1.0'
dependency_overrides:
  bridra_flutter:
    path: ../bridra/packages/bridra_flutter
`,
		"pubspec.lock":         "initial pub lock\n",
		"backend/app/owned.go": "package app\n\nconst Owned = true\n",
	}
	for relative, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
	return root
}

func makeUpgradeProjectRoot(t *testing.T, metadataJSON string) string {
	t.Helper()
	root := makeProjectRoot(t, metadataJSON)
	var metadata projectMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return root
	}
	if metadata.SchemaVersion == releaseinfo.ProjectMetadataVersion &&
		metadata.ProtocolVersion > 0 {
		writeApplicationRPCFixture(t, root, metadata)
	}
	return root
}

func writeApplicationRPCFixture(t *testing.T, root string, metadata projectMetadata) {
	t.Helper()
	schema := codegen.Schema{
		SchemaVersion:   codegen.SupportedSchemaVersion,
		ProtocolVersion: metadata.ProtocolVersion,
		Methods: []codegen.Method{
			{
				Name:       "system.health",
				ClientName: "health",
				Result: codegen.Object{
					GoType:   "HealthResponse",
					DartType: "HealthInfo",
					Fields: []codegen.Field{
						{Name: "status", Type: "string"},
					},
				},
			},
		},
	}
	contents, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		t.Fatalf("encode application RPC fixture: %v", err)
	}
	schemaPath := filepath.Join(root, "schema", "bridra.json")
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil {
		t.Fatalf("create schema directory: %v", err)
	}
	if err := os.WriteFile(schemaPath, append(contents, '\n'), 0o644); err != nil {
		t.Fatalf("write application RPC fixture: %v", err)
	}
	outputs, err := codegen.GenerateWithOptions(schema, codegen.Options{
		GoFrameworkImport: metadata.FrameworkModule + "/framework",
		DartRuntimeImport: codegen.DefaultDartRuntimeImport,
	})
	if err != nil {
		t.Fatalf("generate application RPC fixture: %v", err)
	}
	if err := codegen.Write(root, outputs); err != nil {
		t.Fatalf("write application RPC fixture: %v", err)
	}
}

func assertTestFileContains(t *testing.T, path, expected string) {
	t.Helper()
	contents := readTestFile(t, path)
	if !strings.Contains(string(contents), expected) {
		t.Fatalf("%s = %s, want %q", path, contents, expected)
	}
}

func currentProjectMetadata(frameworkVersion string, templateVersion, protocolVersion int) string {
	return fmt.Sprintf(`{
  "schemaVersion": 2,
  "projectName": "example",
  "goModule": "example.test/app",
  "frameworkModule": "github.com/cluion/bridra/backend",
  "frameworkVersion": %q,
  "templateVersion": %d,
  "protocolVersion": %d
}
`, frameworkVersion, templateVersion, protocolVersion)
}

func hasUpgradeDiagnostic(report upgradeReport, code string) bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return contents
}

func assertFileContents(t *testing.T, path string, expected []byte) {
	t.Helper()
	contents := readTestFile(t, path)
	if !bytes.Equal(contents, expected) {
		t.Fatalf("%s changed during read-only upgrade check", path)
	}
}
