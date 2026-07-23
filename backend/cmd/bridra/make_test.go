package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/scaffold"
)

func TestMakeGeneratesRejectsCollisionAndForceUpdates(t *testing.T) {
	root := makeProjectRoot(t, validProjectMetadata)
	command := makeCommand{}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := command.run(
		[]string{"controller", "User", "--root", root},
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("make: %v, stderr: %s", err, stderr.String())
	}
	for _, path := range []string{
		"backend/app/controllers/user_controller.go",
		"backend/app/controllers/user_controller_test.go",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("generated %s: %v", path, err)
		}
		if !strings.Contains(stdout.String(), "Created "+path) {
			t.Fatalf("stdout = %q, want Created %s", stdout.String(), path)
		}
	}

	stdout.Reset()
	err = command.run(
		[]string{"controller", "User", "--root", root},
		&stdout,
		&stderr,
	)
	if !errors.Is(err, scaffold.ErrCollision) {
		t.Fatalf("collision error = %v, want ErrCollision", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("collision stdout = %q", stdout.String())
	}

	err = command.run(
		[]string{"controller", "User", "--root", root, "--force"},
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("force make: %v", err)
	}
	if strings.Count(stdout.String(), "Updated ") != 2 {
		t.Fatalf("force stdout = %q", stdout.String())
	}
}

func TestMakeRejectsInvalidProjectMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
	}{
		{name: "trailing JSON", metadata: validProjectMetadata + "{}"},
		{name: "unknown field", metadata: "{\n" +
			"\t\"schemaVersion\": 1,\n" +
			"\t\"projectName\": \"example\",\n" +
			"\t\"goModule\": \"example.test/app\",\n" +
			"\t\"frameworkModule\": \"github.com/cluion/bridra/backend\",\n" +
			"\t\"unexpected\": true\n}"},
		{name: "missing version contract", metadata: "{\n" +
			"\t\"schemaVersion\": 2,\n" +
			"\t\"projectName\": \"example\",\n" +
			"\t\"goModule\": \"example.test/app\",\n" +
			"\t\"frameworkModule\": \"github.com/cluion/bridra/backend\"\n}"},
		{name: "unsupported version", metadata: "{\n" +
			"\t\"schemaVersion\": 3,\n" +
			"\t\"projectName\": \"example\",\n" +
			"\t\"goModule\": \"example.test/app\",\n" +
			"\t\"frameworkModule\": \"github.com/cluion/bridra/backend\",\n" +
			"\t\"frameworkVersion\": \"0.1.0\",\n" +
			"\t\"templateVersion\": 2,\n" +
			"\t\"protocolVersion\": 1\n}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := makeProjectRoot(t, test.metadata)
			err := (makeCommand{}).run(
				[]string{"model", "User", "--root", root},
				&bytes.Buffer{},
				&bytes.Buffer{},
			)
			if !errors.Is(err, errMakeInvalid) || !errors.Is(err, errProjectInvalid) {
				t.Fatalf("error = %v, want make and project errors", err)
			}
		})
	}
}

const validProjectMetadata = "{\n" +
	"  \"schemaVersion\": 1,\n" +
	"  \"projectName\": \"example\",\n" +
	"  \"goModule\": \"example.test/app\",\n" +
	"  \"frameworkModule\": \"github.com/cluion/bridra/backend\"\n" +
	"}"

func makeProjectRoot(t *testing.T, metadata string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".bridra"), 0o755); err != nil {
		t.Fatalf("create .bridra: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "backend", "app"), 0o755); err != nil {
		t.Fatalf("create backend/app: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, ".bridra", "project.json"),
		[]byte(metadata),
		0o644,
	); err != nil {
		t.Fatalf("write project metadata: %v", err)
	}
	return root
}
