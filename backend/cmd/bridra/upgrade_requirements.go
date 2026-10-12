package main

import (
	"fmt"
	"strings"

	"github.com/cluion/bridra/backend/internal/projectplatform"
)

func migrationForPlatforms(migration frameworkMigration, platforms []string) (bool, string) {
	if len(migration.Requirements) == 0 {
		// Unclassified historical or custom changes remain conservative.
		return migration.Automatic, migration.Description
	}
	descriptions := []string{"Update the Go and Flutter framework dependencies together."}
	for _, requirement := range migration.Requirements {
		applicable := len(requirement.Platforms) == 0
		for _, platform := range platforms {
			applicable = applicable || projectplatform.Contains(requirement.Platforms, platform)
		}
		if applicable {
			descriptions = append(descriptions, requirement.Description)
		}
	}
	return len(descriptions) == 1, strings.Join(descriptions, " ")
}

// An older template identity alone is not an application migration. Reuse the
// registered requirements so missing mandatory adoption still blocks, even if
// dependencies were updated outside Bridra. Never infer adoption from SemVer.
func planTemplateAdoption(report *upgradeReport, metadata projectMetadata, target upgradeTarget, catalog upgradeCatalog) error {
	if metadata.TemplateVersion >= target.TemplateVersion {
		compareIntegerContract(report, "template", metadata.TemplateVersion, target.TemplateVersion,
			"Review the application-owned template changes.", "project_template")
		return nil
	}
	if !report.PlanAvailable {
		return nil
	}
	projectVersion, _ := parseSemanticVersion(metadata.FrameworkVersion)
	var source string
	for _, release := range catalog.Releases {
		version, err := parseSemanticVersion(release.FrameworkVersion)
		if err != nil {
			return err
		}
		if release.TemplateVersion != metadata.TemplateVersion || compareSemanticVersions(version, projectVersion) > 0 {
			continue
		}
		if source == "" {
			source = release.FrameworkVersion
		} else {
			previous, _ := parseSemanticVersion(source)
			if compareSemanticVersions(version, previous) > 0 {
				source = release.FrameworkVersion
			}
		}
	}
	var path []frameworkMigration
	var available bool
	var err error
	if source != "" {
		path, available, err = catalog.migrationPath(source, target.FrameworkVersion)
	}
	if err != nil {
		return fmt.Errorf("resolve template adoption: %w", err)
	}
	if !available {
		compareIntegerContract(report, "template", metadata.TemplateVersion, target.TemplateVersion,
			"No classified template adoption path is available; review docs/UPGRADING.md manually.", "project_template")
		return nil
	}
	required := false
	var missingAdoptions []upgradePlanStep
	for _, migration := range path {
		// Same-template patches are already accounted for by the framework path.
		var template int
		for _, release := range catalog.Releases {
			if release.FrameworkVersion == migration.To {
				template = release.TemplateVersion
				break
			}
		}
		if template <= metadata.TemplateVersion {
			continue
		}
		automatic, description := migrationForPlatforms(migration, metadata.Platforms)
		if automatic {
			continue
		}
		required = true
		present := false
		for _, step := range report.Steps {
			present = present || step.ID == migration.ID
		}
		if !present {
			missingAdoptions = append(missingAdoptions, upgradePlanStep{
				ID: migration.ID, Kind: "project_template", From: migration.From, To: migration.To,
				Description: description,
			})
		}
	}
	// Missed adoption predates the project's recorded framework dependency.
	// Keep it ahead of the remaining dependency migrations in the printed plan.
	report.Steps = append(missingAdoptions, report.Steps...)
	for index := range report.Steps {
		report.Steps[index].Order = index + 1
	}
	if required {
		report.requireMigration(upgradeDiagnostic{
			Level: "migration", Code: "template_adoption_required",
			Message: "Registered application changes still require adoption for the selected platforms.",
			Action:  "Complete and verify the applicable manual steps, then explicitly record the adopted template baseline. Dependencies alone do not prove adoption.",
		})
	} else {
		report.Diagnostics = append(report.Diagnostics, upgradeDiagnostic{
			Level: "notice", Code: "template_update_available",
			Message: "A newer starter template is available; its registered changes require no manual adoption for this project's selected platforms.",
			Action:  "Review optional starter improvements as needed. Application-owned files and the recorded template baseline remain unchanged.",
		})
	}
	return nil
}
