package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLifecycleReleaseRequiresManualAdoptionWithoutWriting(t *testing.T) {
	for _, mode := range []string{"--plan", "--apply"} {
		t.Run(mode, func(t *testing.T) {
			root := makeUpgradeProjectRoot(t, `{
  "schemaVersion": 3,
  "projectName": "example",
  "goModule": "example.test/app",
  "frameworkModule": "github.com/cluion/bridra/backend",
  "frameworkVersion": "0.19.0",
  "templateVersion": 7,
  "protocolVersion": 3,
  "platforms": ["ios", "macos"]
}`)
			for relative, contents := range map[string]string{
				"backend/go.mod": "module example.test/app\n\ngo 1.25\n\nrequire github.com/cluion/bridra/backend v0.19.0\n",
				"pubspec.yaml":   "name: example\ndependencies:\n  bridra_flutter: '^0.19.0'\n",
			} {
				if err := os.WriteFile(filepath.Join(root, relative), []byte(contents), 0o644); err != nil {
					t.Fatalf("write %s: %v", relative, err)
				}
			}
			preserved := map[string][]byte{}
			for _, relative := range []string{
				".bridra/project.json", "schema/bridra.json",
				"backend/go.mod", "pubspec.yaml",
			} {
				preserved[relative] = readTestFile(t, filepath.Join(root, relative))
			}
			command := upgradeCommand{
				catalog: currentUpgradeCatalog,
				system: upgradeSystem{run: func(context.Context, upgradeProcess) error {
					t.Fatal("manual migration ran a process")
					return nil
				}},
			}
			var stdout bytes.Buffer
			err := command.run(
				[]string{mode, "--to", "0.20.0", "--json", "--root", root},
				&stdout, &bytes.Buffer{},
			)
			if !errors.Is(err, errUpgradeRequired) {
				t.Fatalf("upgrade error = %v, want errUpgradeRequired", err)
			}
			var report upgradeReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatalf("decode report: %v", err)
			}
			if report.Status != upgradeMigrationRequired || !report.PlanAvailable ||
				report.ApplyAvailable || !report.ReadOnly || report.Applied ||
				len(report.Steps) != 2 ||
				report.Steps[0].ID != "framework-0.19.0-to-0.20.0" || report.Steps[0].Automatic ||
				report.Steps[1].ID != "template-7-to-8" || report.Steps[1].Automatic ||
				report.Project.ProtocolVersion != 3 || report.Target.TemplateVersion != 8 ||
				report.Target.TemplateProtocolVersion != 1 ||
				!hasUpgradeDiagnostic(report, "application_protocol_custom") {
				t.Fatalf("report = %#v", report)
			}
			if mode == "--apply" && !hasUpgradeDiagnostic(report, "manual_steps_required") {
				t.Fatalf("apply report lacks manual-step diagnostic: %#v", report)
			}
			for relative, before := range preserved {
				assertFileContents(t, filepath.Join(root, relative), before)
			}
		})
	}
}
