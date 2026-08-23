package main

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/internal/projectplatform"
)

func TestLoadProjectMetadataPreservesProjectAndFilesystemErrors(t *testing.T) {
	_, err := loadProjectMetadata(t.TempDir())
	if !errors.Is(err, errProjectInvalid) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want project and filesystem errors", err)
	}
}

func TestLoadProjectMetadataDefaultsLegacyPlatformsAndValidatesSchemaThree(t *testing.T) {
	legacy := `{
  "schemaVersion": 2,
  "projectName": "example",
  "goModule": "example.test/app",
  "frameworkModule": "github.com/cluion/bridra/backend",
  "frameworkVersion": "0.14.0",
  "templateVersion": 3,
  "protocolVersion": 1
}`
	metadata, err := loadProjectMetadata(makeProjectRoot(t, legacy))
	if err != nil {
		t.Fatalf("load legacy metadata: %v", err)
	}
	if !reflect.DeepEqual(metadata.Platforms, projectplatform.All) {
		t.Fatalf("legacy platforms = %#v", metadata.Platforms)
	}

	current := `{
  "schemaVersion": 3,
  "projectName": "example",
  "goModule": "example.test/app",
  "frameworkModule": "github.com/cluion/bridra/backend",
  "frameworkVersion": "0.15.0",
  "templateVersion": 5,
  "protocolVersion": 1,
  "platforms": ["web", "macos"]
}`
	metadata, err = loadProjectMetadata(makeProjectRoot(t, current))
	if err != nil {
		t.Fatalf("load current metadata: %v", err)
	}
	if !reflect.DeepEqual(metadata.Platforms, []string{"macos", "web"}) {
		t.Fatalf("current platforms = %#v", metadata.Platforms)
	}

	duplicate := strings.Replace(current, `"web", "macos"`, `"web", "web"`, 1)
	_, err = loadProjectMetadata(makeProjectRoot(t, duplicate))
	if err == nil || !strings.Contains(err.Error(), "duplicate platform") {
		t.Fatalf("duplicate platforms error = %v", err)
	}
}
