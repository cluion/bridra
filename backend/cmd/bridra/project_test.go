package main

import (
	"errors"
	"os"
	"testing"
)

func TestLoadProjectMetadataPreservesProjectAndFilesystemErrors(t *testing.T) {
	_, err := loadProjectMetadata(t.TempDir())
	if !errors.Is(err, errProjectInvalid) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want project and filesystem errors", err)
	}
}
