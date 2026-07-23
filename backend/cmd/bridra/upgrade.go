package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

const upgradeReportSchemaVersion = 3

var (
	errUpgradeRequired    = errors.New("upgrade migration required")
	errUpgradeUnsupported = errors.New("upgrade state is unsupported")
	errUpgradeApply       = errors.New("upgrade apply failed")
)

type upgradeStatus string

const (
	upgradeCurrent           upgradeStatus = "current"
	upgradeMigrationRequired upgradeStatus = "migration_required"
	upgradeUnsupported       upgradeStatus = "unsupported"
	upgradeApplied           upgradeStatus = "applied"
	upgradeApplyFailed       upgradeStatus = "apply_failed"
)

type upgradeCommand struct {
	catalog func() upgradeCatalog
	system  upgradeSystem
}

type upgradeTarget struct {
	CLIVersion             string `json:"cliVersion"`
	ProjectMetadataVersion int    `json:"projectMetadataVersion"`
	FrameworkVersion       string `json:"frameworkVersion"`
	TemplateVersion        int    `json:"templateVersion"`
	ProtocolVersion        int    `json:"protocolVersion"`
}

type upgradeProject struct {
	Name                   string `json:"name"`
	ProjectMetadataVersion int    `json:"projectMetadataVersion"`
	FrameworkVersion       string `json:"frameworkVersion,omitempty"`
	TemplateVersion        int    `json:"templateVersion,omitempty"`
	ProtocolVersion        int    `json:"protocolVersion,omitempty"`
}

type upgradeDiagnostic struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Action  string `json:"action,omitempty"`
}

type upgradePlanStep struct {
	Order       int    `json:"order"`
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	From        string `json:"from"`
	To          string `json:"to"`
	Automatic   bool   `json:"automatic"`
	Description string `json:"description"`
}

type upgradeReport struct {
	SchemaVersion     int                 `json:"schemaVersion"`
	Mode              string              `json:"mode"`
	Status            upgradeStatus       `json:"status"`
	ReadOnly          bool                `json:"readOnly"`
	PlanAvailable     bool                `json:"planAvailable"`
	ApplyAvailable    bool                `json:"applyAvailable"`
	Applied           bool                `json:"applied"`
	RolledBack        bool                `json:"rolledBack"`
	MigrationRequired bool                `json:"migrationRequired"`
	Project           upgradeProject      `json:"project"`
	Target            upgradeTarget       `json:"target"`
	Steps             []upgradePlanStep   `json:"steps"`
	Diagnostics       []upgradeDiagnostic `json:"diagnostics"`
}

func newUpgradeCommand() upgradeCommand {
	return upgradeCommand{
		catalog: currentUpgradeCatalog,
		system:  defaultUpgradeSystem(),
	}
}

func (upgradeCommand) name() string {
	return "upgrade"
}

func (upgradeCommand) summary() string {
	return "Plan a framework and project contract upgrade"
}

func (upgradeCommand) usage() string {
	return `Usage:
  bridra upgrade [--plan | --apply] [--to version] [--root path] [--json]
  bridra upgrade --check [--to version] [--root path] [--json]

Options:
  --plan        Build the read-only upgrade plan (default)
  --apply       Apply a complete plan containing only automatic steps
  --check       Backward-compatible alias for --plan
  --to version  Target a release known to the installed CLI (default CLI framework version)
  --json        Emit a schema-versioned JSON report
  --root path   Bridra project root (default current directory)

Plan and check modes never change files. Apply changes only managed dependency,
lockfile, and project metadata surfaces, then rolls them back if verification fails.`
}

func (item upgradeCommand) run(arguments []string, stdout, stderr io.Writer) error {
	if helpRequested(arguments) {
		fmt.Fprintln(stdout, item.usage())
		return nil
	}
	flags := flag.NewFlagSet(item.name(), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprintln(stderr, item.usage()) }
	check := flags.Bool("check", false, "run compatibility check")
	plan := flags.Bool("plan", false, "build upgrade plan")
	apply := flags.Bool("apply", false, "apply an automatic upgrade plan")
	to := flags.String("to", "", "target framework version")
	jsonOutput := flags.Bool("json", false, "emit JSON report")
	root := flags.String("root", ".", "Bridra project root")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("%w: upgrade: %v", errUsage, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: upgrade: unexpected arguments: %v", errUsage, flags.Args())
	}
	selectedModes := 0
	for _, selected := range []bool{*check, *plan, *apply} {
		if selected {
			selectedModes++
		}
	}
	if selectedModes > 1 {
		return fmt.Errorf(
			"%w: upgrade: --check, --plan, and --apply are mutually exclusive",
			errUsage,
		)
	}

	catalog := item.catalog()
	targetVersion := strings.TrimSpace(*to)
	if targetVersion == "" {
		targetVersion = catalog.DefaultTarget
	}
	target, err := catalog.target(targetVersion)
	if err != nil {
		return fmt.Errorf("%w: upgrade: %v", errUpgradeUnsupported, err)
	}

	metadata, err := loadUpgradeProjectMetadata(*root)
	if err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}
	report, err := evaluateUpgrade(metadata, target, catalog)
	if err != nil {
		return fmt.Errorf("upgrade: evaluate compatibility: %w", err)
	}
	if *apply {
		report.Mode = "apply"
		progress := stdout
		if *jsonOutput {
			progress = stderr
		}
		report, err = item.applyUpgrade(
			*root,
			metadata,
			target,
			report,
			progress,
		)
		if outputErr := writeUpgradeOutput(stdout, report, *jsonOutput); outputErr != nil {
			return outputErr
		}
		return err
	}
	if err := writeUpgradeOutput(stdout, report, *jsonOutput); err != nil {
		return err
	}
	return upgradeReportError(report)
}

func writeUpgradeOutput(output io.Writer, report upgradeReport, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return fmt.Errorf("upgrade: encode report: %w", err)
		}
		return nil
	}
	writeUpgradeReport(output, report)
	return nil
}

func upgradeReportError(report upgradeReport) error {
	switch report.Status {
	case upgradeMigrationRequired:
		return fmt.Errorf("%w: review and follow the reported upgrade plan", errUpgradeRequired)
	case upgradeUnsupported:
		return fmt.Errorf("%w: install a compatible Bridra CLI or choose a target with a complete migration path", errUpgradeUnsupported)
	default:
		return nil
	}
}

func evaluateUpgrade(
	metadata projectMetadata,
	target upgradeTarget,
	catalog upgradeCatalog,
) (upgradeReport, error) {
	targetVersion, err := parseSemanticVersion(target.FrameworkVersion)
	if err != nil {
		return upgradeReport{}, fmt.Errorf("invalid target framework version: %w", err)
	}
	report := upgradeReport{
		SchemaVersion: upgradeReportSchemaVersion,
		Status:        upgradeCurrent,
		Mode:          "plan",
		ReadOnly:      true,
		PlanAvailable: true,
		Steps:         []upgradePlanStep{},
		Project: upgradeProject{
			Name:                   metadata.ProjectName,
			ProjectMetadataVersion: metadata.SchemaVersion,
			FrameworkVersion:       metadata.FrameworkVersion,
			TemplateVersion:        metadata.TemplateVersion,
			ProtocolVersion:        metadata.ProtocolVersion,
		},
		Target: target,
	}
	if metadata.SchemaVersion > target.ProjectMetadataVersion {
		report.PlanAvailable = false
		report.markUnsupported(newerVersionDiagnostic(
			"project_metadata_newer_than_cli",
			"metadata schema",
			fmt.Sprint(metadata.SchemaVersion),
			fmt.Sprint(target.ProjectMetadataVersion),
		))
		report.finalize()
		return report, nil
	}
	if metadata.SchemaVersion == 1 {
		report.requireMigration(upgradeDiagnostic{
			Level: "migration", Code: "legacy_project_metadata",
			Message: "Project metadata v1 predates explicit framework, template, and protocol versions.",
			Action: fmt.Sprintf(
				"After updating dependencies, migrate .bridra/project.json to schema %d and record framework %s, template %d, protocol %d.",
				target.ProjectMetadataVersion,
				target.FrameworkVersion,
				target.TemplateVersion,
				target.ProtocolVersion,
			),
		})
		report.addStep(upgradePlanStep{
			ID:   "project-metadata-1-to-" + fmt.Sprint(target.ProjectMetadataVersion),
			Kind: "project_metadata",
			From: "1",
			To:   fmt.Sprint(target.ProjectMetadataVersion),
			Description: fmt.Sprintf(
				"Inspect the installed Go and Flutter dependencies, then record framework %s, template %d, and protocol %d in .bridra/project.json.",
				target.FrameworkVersion,
				target.TemplateVersion,
				target.ProtocolVersion,
			),
		})
		report.finalize()
		return report, nil
	}

	projectVersion, err := parseSemanticVersion(metadata.FrameworkVersion)
	if err != nil {
		return upgradeReport{}, fmt.Errorf("invalid project framework version: %w", err)
	}
	frameworkComparison := compareSemanticVersions(projectVersion, targetVersion)
	if frameworkComparison < 0 {
		path, available, err := catalog.migrationPath(
			metadata.FrameworkVersion,
			target.FrameworkVersion,
		)
		if err != nil {
			return upgradeReport{}, fmt.Errorf("resolve framework migration path: %w", err)
		}
		if !available {
			report.PlanAvailable = false
			report.MigrationRequired = true
			report.markUnsupported(upgradeDiagnostic{
				Level: "unsupported", Code: "framework_migration_path_missing",
				Message: fmt.Sprintf(
					"No complete migration path is registered from framework %s to %s.",
					metadata.FrameworkVersion,
					target.FrameworkVersion,
				),
				Action: "Install a CLI containing every required migration or upgrade through a documented intermediate release.",
			})
		} else {
			report.requireMigration(olderVersionDiagnostic(
				"framework_upgrade_required",
				"framework",
				metadata.FrameworkVersion,
				target.FrameworkVersion,
				"Review every ordered migration step, then update the Go and Flutter framework dependencies together.",
			))
			for _, migration := range path {
				report.addStep(upgradePlanStep{
					ID:          migration.ID,
					Kind:        "framework",
					From:        migration.From,
					To:          migration.To,
					Automatic:   migration.Automatic,
					Description: migration.Description,
				})
			}
		}
	} else if frameworkComparison > 0 {
		report.PlanAvailable = false
		report.markUnsupported(upgradeDiagnostic{
			Level: "unsupported", Code: "framework_downgrade_unsupported",
			Message: fmt.Sprintf(
				"Project framework version %s is newer than requested target %s.",
				metadata.FrameworkVersion,
				target.FrameworkVersion,
			),
			Action: "Choose the same or a newer target; Bridra upgrade plans never perform framework downgrades.",
		})
	}
	compareIntegerContract(
		&report,
		"template",
		metadata.TemplateVersion,
		target.TemplateVersion,
		"Review docs/UPGRADING.md and apply template migrations manually; Bridra will not overwrite application-owned files.",
		"project_template",
	)
	compareIntegerContract(
		&report,
		"protocol",
		metadata.ProtocolVersion,
		target.ProtocolVersion,
		"Upgrade the Go and Flutter framework dependencies together before regenerating the typed RPC contract.",
		"rpc_protocol",
	)
	if len(report.Diagnostics) == 0 {
		report.Diagnostics = append(report.Diagnostics, upgradeDiagnostic{
			Level: "ok", Code: "contract_current",
			Message: "Project metadata and the installed CLI use the same framework, template, and protocol contract.",
		})
	}
	report.finalize()
	return report, nil
}

func loadUpgradeProjectMetadata(root string) (projectMetadata, error) {
	path := filepath.Join(root, ".bridra", "project.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		return loadProjectMetadata(root)
	}
	var header struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(contents, &header); err != nil ||
		header.SchemaVersion <= releaseinfo.ProjectMetadataVersion {
		return loadProjectMetadata(root)
	}
	var metadata projectMetadata
	if err := json.Unmarshal(contents, &metadata); err != nil {
		return projectMetadata{}, fmt.Errorf(
			"%w: decode newer project metadata: %v",
			errProjectInvalid,
			err,
		)
	}
	if strings.TrimSpace(metadata.ProjectName) == "" ||
		strings.TrimSpace(metadata.GoModule) == "" ||
		strings.TrimSpace(metadata.FrameworkModule) == "" {
		return projectMetadata{}, fmt.Errorf(
			"%w: projectName, goModule, and frameworkModule are required",
			errProjectInvalid,
		)
	}
	if err := validateProjectLayout(root); err != nil {
		return projectMetadata{}, err
	}
	return metadata, nil
}

func compareIntegerContract(
	report *upgradeReport,
	name string,
	projectVersion int,
	targetVersion int,
	action string,
	kind string,
) {
	if projectVersion < targetVersion {
		report.requireMigration(olderVersionDiagnostic(
			name+"_upgrade_required",
			name,
			fmt.Sprint(projectVersion),
			fmt.Sprint(targetVersion),
			action,
		))
		if report.PlanAvailable {
			report.addStep(upgradePlanStep{
				ID:          fmt.Sprintf("%s-%d-to-%d", name, projectVersion, targetVersion),
				Kind:        kind,
				From:        fmt.Sprint(projectVersion),
				To:          fmt.Sprint(targetVersion),
				Description: action,
			})
		}
	} else if projectVersion > targetVersion {
		report.PlanAvailable = false
		report.markUnsupported(newerVersionDiagnostic(
			name+"_newer_than_cli",
			name,
			fmt.Sprint(projectVersion),
			fmt.Sprint(targetVersion),
		))
	}
}

func olderVersionDiagnostic(code, name, project, target, action string) upgradeDiagnostic {
	return upgradeDiagnostic{
		Level: "migration", Code: code,
		Message: fmt.Sprintf("Project %s version %s is older than CLI target %s.", name, project, target),
		Action:  action,
	}
}

func newerVersionDiagnostic(code, name, project, target string) upgradeDiagnostic {
	return upgradeDiagnostic{
		Level: "unsupported", Code: code,
		Message: fmt.Sprintf("Project %s version %s is newer than CLI target %s.", name, project, target),
		Action:  "Install the CLI version recorded for this project; an older CLI cannot safely evaluate a newer contract.",
	}
}

func (report *upgradeReport) requireMigration(diagnostic upgradeDiagnostic) {
	if report.Status != upgradeUnsupported {
		report.Status = upgradeMigrationRequired
	}
	report.MigrationRequired = true
	report.Diagnostics = append(report.Diagnostics, diagnostic)
}

func (report *upgradeReport) markUnsupported(diagnostic upgradeDiagnostic) {
	report.Status = upgradeUnsupported
	report.Diagnostics = append(report.Diagnostics, diagnostic)
}

func (report *upgradeReport) addStep(step upgradePlanStep) {
	step.Order = len(report.Steps) + 1
	report.Steps = append(report.Steps, step)
}

func (report *upgradeReport) finalize() {
	report.ApplyAvailable = report.Status == upgradeMigrationRequired &&
		report.PlanAvailable &&
		len(report.Steps) > 0
	if !report.ApplyAvailable {
		return
	}
	for _, step := range report.Steps {
		if !step.Automatic {
			report.ApplyAvailable = false
			return
		}
	}
}

func writeUpgradeReport(output io.Writer, report upgradeReport) {
	title := "Bridra Upgrade Plan"
	if report.Mode == "apply" {
		title = "Bridra Upgrade Apply"
	}
	fmt.Fprintln(output, title)
	fmt.Fprintf(output, "Mode: %s\n", report.Mode)
	fmt.Fprintf(output, "Project: %s\n", report.Project.Name)
	fmt.Fprintf(output, "Status: %s\n", report.Status)
	fmt.Fprintf(output, "Read only: %s\n", yesOrNo(report.ReadOnly))
	fmt.Fprintf(output, "Plan available: %s\n", yesOrNo(report.PlanAvailable))
	fmt.Fprintf(output, "Apply available: %s\n", yesOrNo(report.ApplyAvailable))
	if report.Mode == "apply" {
		fmt.Fprintf(output, "Applied: %s\n", yesOrNo(report.Applied))
		fmt.Fprintf(output, "Rolled back: %s\n", yesOrNo(report.RolledBack))
	}
	fmt.Fprintf(
		output,
		"Project contract: metadata %d, framework %s, template %d, protocol %d\n",
		report.Project.ProjectMetadataVersion,
		versionOrUnknown(report.Project.FrameworkVersion),
		report.Project.TemplateVersion,
		report.Project.ProtocolVersion,
	)
	fmt.Fprintf(
		output,
		"CLI target: metadata %d, framework %s, template %d, protocol %d\n",
		report.Target.ProjectMetadataVersion,
		report.Target.FrameworkVersion,
		report.Target.TemplateVersion,
		report.Target.ProtocolVersion,
	)
	if len(report.Steps) == 0 {
		fmt.Fprintln(output, "Steps: none")
	} else {
		fmt.Fprintln(output, "Steps:")
		for _, step := range report.Steps {
			mode := "manual"
			if step.Automatic {
				mode = "automatic"
			}
			fmt.Fprintf(
				output,
				"  %d. [%s/%s] %s -> %s: %s\n",
				step.Order,
				step.Kind,
				mode,
				step.From,
				step.To,
				step.Description,
			)
		}
	}
	for _, diagnostic := range report.Diagnostics {
		fmt.Fprintf(output, "[%s] %s: %s\n", diagnostic.Level, diagnostic.Code, diagnostic.Message)
		if diagnostic.Action != "" {
			fmt.Fprintf(output, "  Action: %s\n", diagnostic.Action)
		}
	}
}

func yesOrNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func versionOrUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
