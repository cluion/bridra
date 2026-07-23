package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

func TestVersionCommandWritesHumanAndJSONMetadata(t *testing.T) {
	metadata := releaseinfo.Metadata{
		SchemaVersion: 1, CLIVersion: "0.1.0", FrameworkVersion: "0.1.0",
		TemplateVersion: 2, ProtocolVersion: 1,
		Commit: "abc123", BuildDate: "2026-07-22T00:00:00Z",
		GoVersion: "go1.25.0", Target: "linux/amd64",
		GoModule: releaseinfo.GoModule, CLIInstallPath: releaseinfo.CLIInstallPath,
		FlutterPackage: releaseinfo.FlutterPackage, FlutterConstraint: "^0.1.0",
	}
	command := versionCommand{metadata: func() releaseinfo.Metadata { return metadata }}
	var stdout bytes.Buffer
	if err := command.run(nil, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("version: %v", err)
	}
	for _, expected := range []string{
		"Bridra CLI 0.1.0", "Framework: 0.1.0", "Template: 2", "Protocol: 1", "Commit: abc123",
		"Built: 2026-07-22T00:00:00Z", "Target: linux/amd64",
		"go install github.com/cluion/bridra/backend/cmd/bridra@v0.1.0",
		"Flutter: bridra_flutter ^0.1.0",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), expected)
		}
	}

	stdout.Reset()
	if err := command.run([]string{"--json"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("version JSON: %v", err)
	}
	var decoded releaseinfo.Metadata
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if decoded != metadata {
		t.Fatalf("metadata = %#v, want %#v", decoded, metadata)
	}
}
