package clirelease

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	runtimedebug "runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

const spdxPredicateType = "https://spdx.dev/Document/v2.3"

var spdxIDUnsafe = regexp.MustCompile(`[^A-Za-z0-9.-]+`)

type moduleGraph struct {
	GoVersion string
	Modules   []releaseModule
}

type releaseModule struct {
	Path    string
	Version string
	Sum     string
}

type spdxDocument struct {
	SPDXVersion       string             `json:"spdxVersion"`
	DataLicense       string             `json:"dataLicense"`
	SPDXID            string             `json:"SPDXID"`
	Name              string             `json:"name"`
	DocumentNamespace string             `json:"documentNamespace"`
	CreationInfo      spdxCreationInfo   `json:"creationInfo"`
	Packages          []spdxPackage      `json:"packages"`
	Relationships     []spdxRelationship `json:"relationships"`
}

type spdxCreationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type spdxPackage struct {
	SPDXID                string            `json:"SPDXID"`
	Name                  string            `json:"name"`
	VersionInfo           string            `json:"versionInfo"`
	Supplier              string            `json:"supplier,omitempty"`
	DownloadLocation      string            `json:"downloadLocation"`
	FilesAnalyzed         bool              `json:"filesAnalyzed"`
	LicenseConcluded      string            `json:"licenseConcluded"`
	LicenseDeclared       string            `json:"licenseDeclared"`
	CopyrightText         string            `json:"copyrightText"`
	PrimaryPackagePurpose string            `json:"primaryPackagePurpose"`
	ExternalRefs          []spdxExternalRef `json:"externalRefs,omitempty"`
}

type spdxExternalRef struct {
	ReferenceCategory string `json:"referenceCategory"`
	ReferenceType     string `json:"referenceType"`
	ReferenceLocator  string `json:"referenceLocator"`
}

type spdxRelationship struct {
	SPDXElementID      string `json:"spdxElementId"`
	RelationshipType   string `json:"relationshipType"`
	RelatedSPDXElement string `json:"relatedSpdxElement"`
}

func moduleGraphFromBuildInfo(info *runtimedebug.BuildInfo) (moduleGraph, error) {
	if info == nil || strings.TrimSpace(info.GoVersion) == "" {
		return moduleGraph{}, fmt.Errorf("%w: binary is missing Go build information", ErrInvalidConfiguration)
	}
	modulesByPath := make(map[string]releaseModule, len(info.Deps))
	for _, dependency := range info.Deps {
		if dependency == nil {
			continue
		}
		if dependency.Replace != nil {
			return moduleGraph{}, fmt.Errorf(
				"%w: dependency %s uses a replacement and cannot be represented as a public release dependency",
				ErrInvalidConfiguration,
				dependency.Path,
			)
		}
		module := releaseModule{
			Path:    strings.TrimSpace(dependency.Path),
			Version: strings.TrimSpace(dependency.Version),
			Sum:     strings.TrimSpace(dependency.Sum),
		}
		if module.Path == "" || module.Version == "" || module.Version == "(devel)" {
			return moduleGraph{}, fmt.Errorf(
				"%w: dependency build information must contain a public module path and version",
				ErrInvalidConfiguration,
			)
		}
		if existing, found := modulesByPath[module.Path]; found && existing != module {
			return moduleGraph{}, fmt.Errorf("%w: dependency %s has conflicting versions", ErrInvalidConfiguration, module.Path)
		}
		modulesByPath[module.Path] = module
	}
	modules := make([]releaseModule, 0, len(modulesByPath))
	for _, module := range modulesByPath {
		modules = append(modules, module)
	}
	sort.Slice(modules, func(left, right int) bool {
		if modules[left].Path == modules[right].Path {
			return modules[left].Version < modules[right].Version
		}
		return modules[left].Path < modules[right].Path
	})
	return moduleGraph{GoVersion: info.GoVersion, Modules: modules}, nil
}

func (graph moduleGraph) equal(other moduleGraph) bool {
	if graph.GoVersion != other.GoVersion || len(graph.Modules) != len(other.Modules) {
		return false
	}
	for index := range graph.Modules {
		if graph.Modules[index] != other.Modules[index] {
			return false
		}
	}
	return true
}

func renderSPDXSBOM(config Config, buildTime time.Time, graph moduleGraph) ([]byte, error) {
	rootID := "SPDXRef-Package-Bridra-CLI"
	document := spdxDocument{
		SPDXVersion: "SPDX-2.3",
		DataLicense: "CC0-1.0",
		SPDXID:      "SPDXRef-DOCUMENT",
		Name:        "bridra-cli-" + config.Version,
		DocumentNamespace: fmt.Sprintf(
			"https://github.com/cluion/bridra/releases/download/backend/v%s/bridra_%s_cli.spdx.json",
			config.Version,
			config.Version,
		),
		CreationInfo: spdxCreationInfo{
			Created: buildTime.Format(time.RFC3339),
			Creators: []string{
				"Organization: Cluion",
				"Tool: " + graph.GoVersion,
				"Tool: bridra-release-" + config.Version,
			},
		},
		Packages: []spdxPackage{
			{
				SPDXID:                rootID,
				Name:                  "bridra-cli",
				VersionInfo:           config.Version,
				Supplier:              "Organization: Cluion",
				DownloadLocation:      "NOASSERTION",
				FilesAnalyzed:         false,
				LicenseConcluded:      "MIT",
				LicenseDeclared:       "MIT",
				CopyrightText:         "Copyright (c) 2026 Cluion",
				PrimaryPackagePurpose: "APPLICATION",
				ExternalRefs:          []spdxExternalRef{packageURL(releaseinfo.CLIInstallPath, "v"+config.Version)},
			},
		},
		Relationships: []spdxRelationship{
			{
				SPDXElementID:      "SPDXRef-DOCUMENT",
				RelationshipType:   "DESCRIBES",
				RelatedSPDXElement: rootID,
			},
		},
	}
	for _, module := range graph.Modules {
		moduleID := spdxPackageID(module)
		document.Packages = append(document.Packages, spdxPackage{
			SPDXID:                moduleID,
			Name:                  module.Path,
			VersionInfo:           module.Version,
			DownloadLocation:      "NOASSERTION",
			FilesAnalyzed:         false,
			LicenseConcluded:      "NOASSERTION",
			LicenseDeclared:       "NOASSERTION",
			CopyrightText:         "NOASSERTION",
			PrimaryPackagePurpose: "LIBRARY",
			ExternalRefs:          []spdxExternalRef{packageURL(module.Path, module.Version)},
		})
		document.Relationships = append(document.Relationships, spdxRelationship{
			SPDXElementID:      rootID,
			RelationshipType:   "DEPENDS_ON",
			RelatedSPDXElement: moduleID,
		})
	}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("CLI release: encode SPDX SBOM: %w", err)
	}
	return append(contents, '\n'), nil
}

func packageURL(path string, version string) spdxExternalRef {
	return spdxExternalRef{
		ReferenceCategory: "PACKAGE-MANAGER",
		ReferenceType:     "purl",
		ReferenceLocator:  "pkg:golang/" + path + "@" + version,
	}
}

func spdxPackageID(module releaseModule) string {
	digest := sha256.Sum256([]byte(module.Path + "@" + module.Version))
	slug := strings.Trim(spdxIDUnsafe.ReplaceAllString(module.Path, "-"), "-.")
	if len(slug) > 48 {
		slug = slug[:48]
	}
	return "SPDXRef-Package-" + slug + "-" + hex.EncodeToString(digest[:6])
}
