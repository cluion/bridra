package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

var errProjectInvalid = errors.New("invalid Bridra project")

type projectMetadata struct {
	SchemaVersion    int    `json:"schemaVersion"`
	ProjectName      string `json:"projectName"`
	GoModule         string `json:"goModule"`
	FrameworkModule  string `json:"frameworkModule"`
	FrameworkVersion string `json:"frameworkVersion,omitempty"`
	TemplateVersion  int    `json:"templateVersion,omitempty"`
	ProtocolVersion  int    `json:"protocolVersion,omitempty"`
}

func loadProjectMetadata(root string) (projectMetadata, error) {
	path := filepath.Join(root, ".bridra", "project.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return projectMetadata{}, fmt.Errorf(
				"%w: %s is missing; run inside a Bridra project: %w",
				errProjectInvalid,
				path,
				err,
			)
		}
		return projectMetadata{}, fmt.Errorf(
			"%w: read Bridra project metadata: %w",
			errProjectInvalid,
			err,
		)
	}
	var metadata projectMetadata
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return projectMetadata{}, fmt.Errorf("%w: decode project metadata: %w", errProjectInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return projectMetadata{}, fmt.Errorf(
			"%w: project metadata must contain one JSON object",
			errProjectInvalid,
		)
	}
	if metadata.SchemaVersion != 1 && metadata.SchemaVersion != releaseinfo.ProjectMetadataVersion {
		return projectMetadata{}, fmt.Errorf(
			"%w: unsupported project metadata version %d",
			errProjectInvalid,
			metadata.SchemaVersion,
		)
	}
	if metadata.SchemaVersion == 1 && (metadata.FrameworkVersion != "" ||
		metadata.TemplateVersion != 0 || metadata.ProtocolVersion != 0) {
		return projectMetadata{}, fmt.Errorf(
			"%w: project metadata version 1 cannot declare version contract fields",
			errProjectInvalid,
		)
	}
	if metadata.SchemaVersion == releaseinfo.ProjectMetadataVersion {
		if _, err := parseSemanticVersion(metadata.FrameworkVersion); err != nil {
			return projectMetadata{}, fmt.Errorf(
				"%w: frameworkVersion must be a semantic version: %v",
				errProjectInvalid,
				err,
			)
		}
		if metadata.TemplateVersion < 1 || metadata.ProtocolVersion < 1 {
			return projectMetadata{}, fmt.Errorf(
				"%w: templateVersion and protocolVersion must be positive",
				errProjectInvalid,
			)
		}
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

func validateProjectLayout(root string) error {
	backend, err := os.Stat(filepath.Join(root, "backend", "app"))
	if err != nil {
		return fmt.Errorf(
			"%w: backend/app is unavailable: %w",
			errProjectInvalid,
			err,
		)
	}
	if !backend.IsDir() {
		return fmt.Errorf("%w: backend/app is not a directory", errProjectInvalid)
	}
	return nil
}
