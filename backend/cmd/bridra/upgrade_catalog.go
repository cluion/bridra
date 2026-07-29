package main

import (
	"fmt"
	"sort"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

type upgradeCatalog struct {
	CLIVersion    string
	DefaultTarget string
	Releases      []upgradeRelease
	Migrations    []frameworkMigration
}

type upgradeRelease struct {
	FrameworkVersion       string
	ProjectMetadataVersion int
	TemplateVersion        int
	ProtocolVersion        int
}

type frameworkMigration struct {
	ID          string
	From        string
	To          string
	Description string
	Automatic   bool
}

type migrationPredecessor struct {
	version   string
	migration frameworkMigration
}

var registeredUpgradeReleases = []upgradeRelease{
	{
		FrameworkVersion:       "0.1.0",
		ProjectMetadataVersion: 2,
		TemplateVersion:        2,
		ProtocolVersion:        1,
	},
	{
		FrameworkVersion:       "0.1.1",
		ProjectMetadataVersion: 2,
		TemplateVersion:        2,
		ProtocolVersion:        1,
	},
	{
		FrameworkVersion:       "0.2.0",
		ProjectMetadataVersion: 2,
		TemplateVersion:        2,
		ProtocolVersion:        1,
	},
	{
		FrameworkVersion:       "0.3.0",
		ProjectMetadataVersion: 2,
		TemplateVersion:        2,
		ProtocolVersion:        1,
	},
	{
		FrameworkVersion:       "0.4.0",
		ProjectMetadataVersion: 2,
		TemplateVersion:        2,
		ProtocolVersion:        1,
	},
	{
		FrameworkVersion:       "0.5.0",
		ProjectMetadataVersion: 2,
		TemplateVersion:        2,
		ProtocolVersion:        1,
	},
	{
		FrameworkVersion:       "0.6.0",
		ProjectMetadataVersion: 2,
		TemplateVersion:        2,
		ProtocolVersion:        1,
	},
	{
		FrameworkVersion:       "0.6.1",
		ProjectMetadataVersion: 2,
		TemplateVersion:        2,
		ProtocolVersion:        1,
	},
	{
		FrameworkVersion:       "0.7.0",
		ProjectMetadataVersion: 2,
		TemplateVersion:        2,
		ProtocolVersion:        1,
	},
}

var registeredFrameworkMigrations = []frameworkMigration{
	{
		ID:          "framework-0.1.0-to-0.1.1",
		From:        "0.1.0",
		To:          "0.1.1",
		Description: "Update the Go and Flutter framework dependencies to the create hotfix.",
		Automatic:   true,
	},
	{
		ID:          "framework-0.1.1-to-0.2.0",
		From:        "0.1.1",
		To:          "0.2.0",
		Description: "Update the Go and Flutter framework dependencies for concurrent and cancellable RPC.",
		Automatic:   true,
	},
	{
		ID:          "framework-0.2.0-to-0.3.0",
		From:        "0.2.0",
		To:          "0.3.0",
		Description: "Update the Go and Flutter framework dependencies for delayed Jobs and cron Tasks.",
		Automatic:   true,
	},
	{
		ID:          "framework-0.3.0-to-0.4.0",
		From:        "0.3.0",
		To:          "0.4.0",
		Description: "Update both framework dependencies and adopt parent-process observation in the application-owned Sidecar entrypoint.",
		Automatic:   false,
	},
	{
		ID:          "framework-0.4.0-to-0.5.0",
		From:        "0.4.0",
		To:          "0.5.0",
		Description: "Update both framework dependencies and adopt desktop single-instance ownership in the application-owned Flutter entrypoint.",
		Automatic:   false,
	},
	{
		ID:          "framework-0.5.0-to-0.6.0",
		From:        "0.5.0",
		To:          "0.6.0",
		Description: "Update the Go and Flutter framework dependencies for opt-in streaming and verified out-of-band file transfer.",
		Automatic:   true,
	},
	{
		ID:          "framework-0.6.0-to-0.6.1",
		From:        "0.6.0",
		To:          "0.6.1",
		Description: "Update the Go and Flutter framework dependencies to the generated-consumer verification patch.",
		Automatic:   false,
	},
	{
		ID:          "framework-0.6.1-to-0.7.0",
		From:        "0.6.1",
		To:          "0.7.0",
		Description: "Update the Go and Flutter framework dependencies for opt-in persistent Queue and Scheduler stores.",
		Automatic:   true,
	},
}

func currentUpgradeCatalog() upgradeCatalog {
	metadata := releaseinfo.Current()
	releases := append([]upgradeRelease(nil), registeredUpgradeReleases...)
	foundCurrent := false
	for _, release := range releases {
		if release.FrameworkVersion == metadata.FrameworkVersion {
			foundCurrent = true
			break
		}
	}
	if !foundCurrent {
		releases = append(releases, upgradeRelease{
			FrameworkVersion:       metadata.FrameworkVersion,
			ProjectMetadataVersion: releaseinfo.ProjectMetadataVersion,
			TemplateVersion:        metadata.TemplateVersion,
			ProtocolVersion:        metadata.ProtocolVersion,
		})
	}
	return upgradeCatalog{
		CLIVersion:    metadata.CLIVersion,
		DefaultTarget: metadata.FrameworkVersion,
		Releases:      releases,
		Migrations:    append([]frameworkMigration(nil), registeredFrameworkMigrations...),
	}
}

func (catalog upgradeCatalog) target(version string) (upgradeTarget, error) {
	if _, err := parseSemanticVersion(version); err != nil {
		return upgradeTarget{}, fmt.Errorf("invalid target framework version: %w", err)
	}
	for _, release := range catalog.Releases {
		if release.FrameworkVersion != version {
			continue
		}
		return upgradeTarget{
			CLIVersion:             catalog.CLIVersion,
			ProjectMetadataVersion: release.ProjectMetadataVersion,
			FrameworkVersion:       release.FrameworkVersion,
			TemplateVersion:        release.TemplateVersion,
			ProtocolVersion:        release.ProtocolVersion,
		}, nil
	}
	return upgradeTarget{}, fmt.Errorf(
		"framework version %s is not available in the installed Bridra CLI %s migration catalog",
		version,
		catalog.CLIVersion,
	)
}

func (catalog upgradeCatalog) migrationPath(from, to string) ([]frameworkMigration, bool, error) {
	fromVersion, err := parseSemanticVersion(from)
	if err != nil {
		return nil, false, fmt.Errorf("invalid source framework version: %w", err)
	}
	toVersion, err := parseSemanticVersion(to)
	if err != nil {
		return nil, false, fmt.Errorf("invalid target framework version: %w", err)
	}
	comparison := compareSemanticVersions(fromVersion, toVersion)
	if comparison == 0 {
		return []frameworkMigration{}, true, nil
	}
	if comparison > 0 {
		return nil, false, nil
	}

	releases := make(map[string]struct{}, len(catalog.Releases))
	for _, release := range catalog.Releases {
		if _, err := parseSemanticVersion(release.FrameworkVersion); err != nil {
			return nil, false, fmt.Errorf(
				"invalid migration catalog release %q: %w",
				release.FrameworkVersion,
				err,
			)
		}
		releases[release.FrameworkVersion] = struct{}{}
	}
	if _, exists := releases[from]; !exists {
		return nil, false, nil
	}
	if _, exists := releases[to]; !exists {
		return nil, false, nil
	}

	edges := make(map[string][]frameworkMigration)
	for _, migration := range catalog.Migrations {
		if migration.ID == "" || migration.Description == "" {
			return nil, false, fmt.Errorf("migration catalog entries require an ID and description")
		}
		if _, exists := releases[migration.From]; !exists {
			return nil, false, fmt.Errorf(
				"migration %s references unknown source release %s",
				migration.ID,
				migration.From,
			)
		}
		if _, exists := releases[migration.To]; !exists {
			return nil, false, fmt.Errorf(
				"migration %s references unknown target release %s",
				migration.ID,
				migration.To,
			)
		}
		left, _ := parseSemanticVersion(migration.From)
		right, _ := parseSemanticVersion(migration.To)
		if compareSemanticVersions(left, right) >= 0 {
			return nil, false, fmt.Errorf(
				"migration %s must move forward from %s to %s",
				migration.ID,
				migration.From,
				migration.To,
			)
		}
		edges[migration.From] = append(edges[migration.From], migration)
	}
	for version := range edges {
		sort.Slice(edges[version], func(left, right int) bool {
			leftVersion, _ := parseSemanticVersion(edges[version][left].To)
			rightVersion, _ := parseSemanticVersion(edges[version][right].To)
			comparison := compareSemanticVersions(leftVersion, rightVersion)
			if comparison == 0 {
				return edges[version][left].ID < edges[version][right].ID
			}
			return comparison < 0
		})
	}

	queue := []string{from}
	visited := map[string]bool{from: true}
	previous := make(map[string]migrationPredecessor)

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, migration := range edges[current] {
			nextVersion, _ := parseSemanticVersion(migration.To)
			if compareSemanticVersions(nextVersion, toVersion) > 0 || visited[migration.To] {
				continue
			}
			visited[migration.To] = true
			previous[migration.To] = migrationPredecessor{
				version:   current,
				migration: migration,
			}
			if migration.To == to {
				return reconstructMigrationPath(from, to, previous), true, nil
			}
			queue = append(queue, migration.To)
		}
	}
	return nil, false, nil
}

func reconstructMigrationPath(
	from string,
	to string,
	previous map[string]migrationPredecessor,
) []frameworkMigration {
	var reversed []frameworkMigration
	for current := to; current != from; {
		entry := previous[current]
		reversed = append(reversed, entry.migration)
		current = entry.version
	}
	path := make([]frameworkMigration, len(reversed))
	for index := range reversed {
		path[len(reversed)-1-index] = reversed[index]
	}
	return path
}
