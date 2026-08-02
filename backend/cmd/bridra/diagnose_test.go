package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDiagnoseCreatesRedactedBundleWithValidatedSidecarSnapshot(t *testing.T) {
	root := makeProjectRoot(t, diagnosticProjectMetadata)
	if err := os.WriteFile(
		filepath.Join(root, ".fvmrc"),
		[]byte("{\n  \"flutter\": \"3.44.6\"\n}\n"),
		0o600,
	); err != nil {
		t.Fatalf("write .fvmrc: %v", err)
	}
	runtimePath := filepath.Join(root, "runtime.json")
	if err := os.WriteFile(runtimePath, []byte(validSidecarDiagnostics), 0o600); err != nil {
		t.Fatalf("write runtime diagnostics: %v", err)
	}
	output := filepath.Join(root, "support", "diagnostics.zip")
	system := newDiagnoseSystem(healthyDoctorSystem())
	system.now = func() time.Time {
		return time.Date(2026, 8, 2, 12, 30, 0, 0, time.UTC)
	}
	command := diagnoseCommand{system: system}
	var stdout bytes.Buffer

	if err := command.run([]string{
		"--root", root,
		"--runtime", "runtime.json",
		"--output", "support/diagnostics.zip",
	}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if !strings.Contains(stdout.String(), output) ||
		!strings.Contains(stdout.String(), "credentials") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	information, err := os.Stat(output)
	if err != nil {
		t.Fatalf("stat bundle: %v", err)
	}
	if runtime.GOOS != "windows" && information.Mode().Perm() != 0o600 {
		t.Fatalf("bundle mode = %o, want 600", information.Mode().Perm())
	}

	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	defer archive.Close()
	if len(archive.File) != 2 ||
		archive.File[0].Name != "diagnostics.json" ||
		archive.File[1].Name != "README.txt" {
		t.Fatalf("bundle entries = %#v", archive.File)
	}
	reportContents := readDiagnosticEntry(t, archive.File[0])
	for _, excluded := range []string{
		root,
		"private-project",
		"example.test/private",
		"diagnostic-secret",
		"/tools/go",
	} {
		if bytes.Contains(reportContents, []byte(excluded)) {
			t.Fatalf("diagnostics contain excluded value %q: %s", excluded, reportContents)
		}
	}
	var report diagnosticBundleReport
	if err := json.Unmarshal(reportContents, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.SchemaVersion != 1 ||
		report.GeneratedAt != "2026-08-02T12:30:00Z" ||
		report.Project.Status != "ok" ||
		report.Project.FrameworkVersion != "0.10.0" ||
		report.Project.TemplateVersion != 2 ||
		report.Project.ProtocolVersion != 1 {
		t.Fatalf("report = %#v", report)
	}
	if report.SidecarDiagnostics == nil ||
		report.SidecarDiagnostics.State != "running" ||
		report.SidecarDiagnostics.ProcessStarts != 2 ||
		report.SidecarDiagnostics.SuccessfulRestarts != 1 ||
		len(report.SidecarDiagnostics.Events) != 2 {
		t.Fatalf("sidecar diagnostics = %#v", report.SidecarDiagnostics)
	}
	for _, check := range report.Doctor {
		if strings.HasPrefix(check.Detail, "/") {
			t.Fatalf("doctor detail contains path: %#v", check)
		}
	}

	err = command.run([]string{
		"--root", root,
		"--output", "support/diagnostics.zip",
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, errDiagnoseInvalid) {
		t.Fatalf("overwrite error = %v, want errDiagnoseInvalid", err)
	}
}

func TestDiagnoseRejectsRuntimeDiagnosticsWithUnknownSensitiveFields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "unsafe.json")
	contents := strings.Replace(
		validSidecarDiagnostics,
		`"events":`,
		`"token":"diagnostic-secret","events":`,
		1,
	)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write unsafe diagnostics: %v", err)
	}
	command := diagnoseCommand{system: newDiagnoseSystem(healthyDoctorSystem())}

	err := command.run([]string{
		"--root", root,
		"--runtime", path,
		"--output", "diagnostics.zip",
	}, &bytes.Buffer{}, &bytes.Buffer{})

	if !errors.Is(err, errDiagnoseInvalid) || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %v, want invalid unknown field", err)
	}
	if _, err := os.Stat(filepath.Join(root, "diagnostics.zip")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe bundle was created: %v", err)
	}
}

func TestDiagnosticDoctorDetailOnlyIncludesAllowlistedVersionShapes(t *testing.T) {
	valid := []doctorCheck{
		{status: doctorOK, name: ".fvmrc", detail: "pins Flutter 3.44.6"},
		{status: doctorOK, name: "Go", detail: "go version go1.26.5 darwin/arm64"},
		{status: doctorOK, name: "FVM", detail: "4.1.2"},
		{status: doctorOK, name: "Flutter", detail: "3.44.6 (pinned), Dart 3.12.2"},
		{status: doctorOK, name: "Host", detail: "darwin/arm64"},
	}
	for _, check := range valid {
		if detail := diagnosticDoctorDetail(check); detail != check.detail {
			t.Fatalf("%s detail = %q, want %q", check.name, detail, check.detail)
		}
	}

	for _, name := range []string{".fvmrc", "Go", "FVM", "Flutter", "Host", "GTK 3"} {
		check := doctorCheck{
			status: doctorOK,
			name:   name,
			detail: "4.1.2 diagnostic-secret /Users/example/private",
		}
		if detail := diagnosticDoctorDetail(check); detail != "available" {
			t.Fatalf("%s unsafe detail = %q", name, detail)
		}
	}
	if detail := diagnosticDoctorDetail(doctorCheck{
		status: doctorFailure,
		name:   "Go",
		detail: "diagnostic-secret /Users/example/private",
	}); detail != "check reported fail" {
		t.Fatalf("failure detail = %q", detail)
	}
}

func readDiagnosticEntry(t *testing.T, entry *zip.File) []byte {
	t.Helper()
	reader, err := entry.Open()
	if err != nil {
		t.Fatalf("open %s: %v", entry.Name, err)
	}
	defer reader.Close()
	contents, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read %s: %v", entry.Name, err)
	}
	return contents
}

const diagnosticProjectMetadata = `{
  "schemaVersion": 2,
  "projectName": "private-project",
  "goModule": "example.test/private",
  "frameworkModule": "github.com/cluion/bridra/backend",
  "frameworkVersion": "0.10.0",
  "templateVersion": 2,
  "protocolVersion": 1
}`

const validSidecarDiagnostics = `{
  "schemaVersion": 1,
  "state": "running",
  "pendingCalls": 0,
  "activeStreams": 0,
  "processStarts": 2,
  "successfulRestarts": 1,
  "failedRestartAttempts": 0,
  "lastExitCode": 17,
  "events": [
    {
      "timestamp": "2026-08-02T12:00:00Z",
      "type": "process_exited",
      "exitCode": 17,
      "errorType": "SidecarExitedException"
    },
    {
      "timestamp": "2026-08-02T12:00:01Z",
      "type": "restarted",
      "attempt": 1
    }
  ]
}`
