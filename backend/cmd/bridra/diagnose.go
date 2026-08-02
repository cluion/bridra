package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

const diagnosticSchemaVersion = 1
const maximumRuntimeDiagnosticsBytes = 64 * 1024
const maximumRuntimeDiagnosticEvents = 50

var errDiagnoseInvalid = errors.New("invalid Bridra diagnostic input")
var diagnosticTypePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.$<>-]{0,127}$`)
var diagnosticVersionPattern = regexp.MustCompile(`[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:[-+][0-9A-Za-z.-]+)?`)
var diagnosticDoctorDetailPatterns = map[string]*regexp.Regexp{
	".fvmrc": regexp.MustCompile(`^pins Flutter ` + diagnosticVersionPattern.String() + `$`),
	"Go": regexp.MustCompile(
		`^go version go` + diagnosticVersionPattern.String() + ` [a-z0-9_]+/[a-z0-9_]+$`,
	),
	"FVM": regexp.MustCompile(`^` + diagnosticVersionPattern.String() + `$`),
	"Flutter": regexp.MustCompile(
		`^` + diagnosticVersionPattern.String() +
			` \(pinned\)(?:, Dart ` + diagnosticVersionPattern.String() + `)?$`,
	),
	"Host":  regexp.MustCompile(`^[a-z0-9_]+/[a-z0-9_]+$`),
	"GTK 3": regexp.MustCompile(`^` + diagnosticVersionPattern.String() + `$`),
}

type diagnoseSystem struct {
	doctor   doctorSystem
	now      func() time.Time
	abs      func(string) (string, error)
	stat     func(string) (os.FileInfo, error)
	mkdirAll func(string, os.FileMode) error
	openFile func(string, int, os.FileMode) (*os.File, error)
	remove   func(string) error
	readFile func(string) ([]byte, error)
}

type diagnoseCommand struct {
	system diagnoseSystem
}

type diagnosticBundleReport struct {
	SchemaVersion      int                         `json:"schemaVersion"`
	GeneratedAt        string                      `json:"generatedAt"`
	CLI                releaseinfo.Metadata        `json:"cli"`
	Project            diagnosticProject           `json:"project"`
	Doctor             []diagnosticDoctorCheck     `json:"doctor"`
	SidecarDiagnostics *diagnosticSidecarSnapshot  `json:"sidecarDiagnostics,omitempty"`
	Privacy            diagnosticPrivacyDisclosure `json:"privacy"`
}

type diagnosticProject struct {
	Status                string `json:"status"`
	MetadataSchemaVersion int    `json:"metadataSchemaVersion,omitempty"`
	FrameworkVersion      string `json:"frameworkVersion,omitempty"`
	TemplateVersion       int    `json:"templateVersion,omitempty"`
	ProtocolVersion       int    `json:"protocolVersion,omitempty"`
}

type diagnosticDoctorCheck struct {
	Status doctorStatus `json:"status"`
	Name   string       `json:"name"`
	Detail string       `json:"detail"`
}

type diagnosticSidecarSnapshot struct {
	SchemaVersion         int                      `json:"schemaVersion"`
	State                 string                   `json:"state"`
	PendingCalls          int                      `json:"pendingCalls"`
	ActiveStreams         int                      `json:"activeStreams"`
	ProcessStarts         int                      `json:"processStarts"`
	SuccessfulRestarts    int                      `json:"successfulRestarts"`
	FailedRestartAttempts int                      `json:"failedRestartAttempts"`
	LastExitCode          *int                     `json:"lastExitCode,omitempty"`
	FailureType           string                   `json:"failureType,omitempty"`
	Events                []diagnosticSidecarEvent `json:"events"`
}

type diagnosticSidecarEvent struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Attempt   *int   `json:"attempt,omitempty"`
	ExitCode  *int   `json:"exitCode,omitempty"`
	ErrorType string `json:"errorType,omitempty"`
}

type diagnosticPrivacyDisclosure struct {
	Included []string `json:"included"`
	Excluded []string `json:"excluded"`
}

func newDiagnoseSystem(doctor doctorSystem) diagnoseSystem {
	return diagnoseSystem{
		doctor:   doctor,
		now:      time.Now,
		abs:      filepath.Abs,
		stat:     os.Stat,
		mkdirAll: os.MkdirAll,
		openFile: os.OpenFile,
		remove:   os.Remove,
		readFile: os.ReadFile,
	}
}

func newDiagnoseCommand(doctor doctorSystem) diagnoseCommand {
	return diagnoseCommand{system: newDiagnoseSystem(doctor)}
}

func (diagnoseCommand) name() string {
	return "diagnose"
}

func (diagnoseCommand) summary() string {
	return "Create a redacted support bundle"
}

func (diagnoseCommand) usage() string {
	return `Usage:
  bridra diagnose [--root path] [--output path] [--runtime path]

Options:
  --root path     Bridra project root (default .)
  --output path   New .zip bundle path (default build/diagnostics under root)
  --runtime path  Optional SidecarDiagnostics JSON exported by the Flutter app

The bundle excludes environment variables, credentials, request data, logs,
source files, project identity, and absolute paths.`
}

func (item diagnoseCommand) run(arguments []string, stdout, stderr io.Writer) error {
	if helpRequested(arguments) {
		fmt.Fprintln(stdout, item.usage())
		return nil
	}
	flags := flag.NewFlagSet(item.name(), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, item.usage())
	}
	root := flags.String("root", ".", "Bridra project root")
	output := flags.String("output", "", "new diagnostic bundle path")
	runtimeReport := flags.String("runtime", "", "SidecarDiagnostics JSON path")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("%w: diagnose: %v", errUsage, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: diagnose: unexpected arguments: %v", errUsage, flags.Args())
	}

	absoluteRoot, err := item.system.abs(*root)
	if err != nil {
		return fmt.Errorf("diagnose: resolve project root: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	information, err := item.system.stat(absoluteRoot)
	if err != nil {
		return fmt.Errorf("diagnose: inspect project root: %w", err)
	}
	if !information.IsDir() {
		return fmt.Errorf("%w: diagnose: project root must be a directory", errDiagnoseInvalid)
	}

	var sidecar *diagnosticSidecarSnapshot
	if strings.TrimSpace(*runtimeReport) != "" {
		runtimePath := *runtimeReport
		if !filepath.IsAbs(runtimePath) {
			runtimePath = filepath.Join(absoluteRoot, runtimePath)
		}
		sidecar, err = item.loadSidecarDiagnostics(filepath.Clean(runtimePath))
		if err != nil {
			return err
		}
	}

	generatedAt := item.system.now().UTC()
	report := diagnosticBundleReport{
		SchemaVersion:      diagnosticSchemaVersion,
		GeneratedAt:        generatedAt.Format(time.RFC3339Nano),
		CLI:                releaseinfo.Current(),
		Project:            diagnosticProjectSnapshot(absoluteRoot),
		Doctor:             diagnosticDoctorSnapshot(doctorCommand{system: item.system.doctor}.inspect(absoluteRoot)),
		SidecarDiagnostics: sidecar,
		Privacy: diagnosticPrivacyDisclosure{
			Included: []string{
				"Bridra and toolchain versions",
				"host operating system and architecture",
				"project version contract",
				"optional validated Sidecar counters and event types",
			},
			Excluded: []string{
				"environment variables and credentials",
				"request identifiers, methods, parameters, and bodies",
				"application and Sidecar logs",
				"source files and absolute paths",
				"project name and module identity",
			},
		},
	}
	contents, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("diagnose: encode report: %w", err)
	}
	contents = append(contents, '\n')

	outputPath := strings.TrimSpace(*output)
	if outputPath == "" {
		outputPath = filepath.Join(
			absoluteRoot,
			"build",
			"diagnostics",
			"bridra-diagnostics-"+generatedAt.Format("20060102T150405Z")+".zip",
		)
	} else if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(absoluteRoot, outputPath)
	}
	outputPath = filepath.Clean(outputPath)
	if !strings.EqualFold(filepath.Ext(outputPath), ".zip") {
		return fmt.Errorf("%w: diagnose: output must use the .zip extension", errDiagnoseInvalid)
	}
	if err := item.system.mkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return fmt.Errorf("diagnose: create output directory: %w", err)
	}
	if err := item.writeArchive(outputPath, generatedAt, contents); err != nil {
		return err
	}

	fmt.Fprintln(stdout, "Bridra Diagnostics")
	fmt.Fprintf(stdout, "Bundle: %s\n", outputPath)
	fmt.Fprintln(stdout, "Privacy: credentials, requests, logs, source, identity, and absolute paths excluded.")
	return nil
}

func diagnosticProjectSnapshot(root string) diagnosticProject {
	metadata, err := loadProjectMetadata(root)
	if err != nil {
		return diagnosticProject{Status: "unavailable"}
	}
	return diagnosticProject{
		Status:                "ok",
		MetadataSchemaVersion: metadata.SchemaVersion,
		FrameworkVersion:      metadata.FrameworkVersion,
		TemplateVersion:       metadata.TemplateVersion,
		ProtocolVersion:       metadata.ProtocolVersion,
	}
}

func diagnosticDoctorSnapshot(report doctorReport) []diagnosticDoctorCheck {
	checks := make([]diagnosticDoctorCheck, 0, len(report.checks))
	for _, check := range report.checks {
		checks = append(checks, diagnosticDoctorCheck{
			Status: check.status,
			Name:   check.name,
			Detail: diagnosticDoctorDetail(check),
		})
	}
	return checks
}

func diagnosticDoctorDetail(check doctorCheck) string {
	if check.status != doctorOK {
		return "check reported " + string(check.status)
	}
	switch check.name {
	case "clang", "cmake", "ninja", "pkg-config", "xcodebuild", "xcrun", "cl":
		return "available"
	case ".fvmrc", "Go", "FVM", "Flutter", "Host", "GTK 3":
		detail := strings.Join(strings.Fields(check.detail), " ")
		if diagnosticDoctorDetailPatterns[check.name].MatchString(detail) {
			return detail
		}
		return "available"
	default:
		return "available"
	}
}

func (item diagnoseCommand) loadSidecarDiagnostics(
	path string,
) (*diagnosticSidecarSnapshot, error) {
	contents, err := item.system.readFile(path)
	if err != nil {
		return nil, fmt.Errorf("diagnose: read Sidecar diagnostics: %w", err)
	}
	if len(contents) > maximumRuntimeDiagnosticsBytes {
		return nil, fmt.Errorf("%w: diagnose: Sidecar diagnostics exceed 64 KiB", errDiagnoseInvalid)
	}
	var snapshot diagnosticSidecarSnapshot
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("%w: diagnose: decode Sidecar diagnostics: %v", errDiagnoseInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: diagnose: Sidecar diagnostics must contain one JSON object", errDiagnoseInvalid)
	}
	if err := validateSidecarDiagnostics(snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func validateSidecarDiagnostics(snapshot diagnosticSidecarSnapshot) error {
	if snapshot.SchemaVersion != 1 {
		return fmt.Errorf("%w: diagnose: unsupported Sidecar diagnostics schema %d", errDiagnoseInvalid, snapshot.SchemaVersion)
	}
	validStates := map[string]bool{
		"running": true, "restarting": true, "closing": true,
		"closed": true, "failed": true,
	}
	if !validStates[snapshot.State] {
		return fmt.Errorf("%w: diagnose: invalid Sidecar state", errDiagnoseInvalid)
	}
	counts := []int{
		snapshot.PendingCalls,
		snapshot.ActiveStreams,
		snapshot.ProcessStarts,
		snapshot.SuccessfulRestarts,
		snapshot.FailedRestartAttempts,
	}
	for _, count := range counts {
		if count < 0 || count > 1_000_000_000 {
			return fmt.Errorf("%w: diagnose: invalid Sidecar counter", errDiagnoseInvalid)
		}
	}
	if snapshot.ProcessStarts < 1 || snapshot.SuccessfulRestarts >= snapshot.ProcessStarts {
		return fmt.Errorf("%w: diagnose: inconsistent Sidecar process counters", errDiagnoseInvalid)
	}
	if snapshot.FailureType != "" && !diagnosticTypePattern.MatchString(snapshot.FailureType) {
		return fmt.Errorf("%w: diagnose: invalid Sidecar failure type", errDiagnoseInvalid)
	}
	if len(snapshot.Events) > maximumRuntimeDiagnosticEvents {
		return fmt.Errorf("%w: diagnose: too many Sidecar diagnostic events", errDiagnoseInvalid)
	}
	validEvents := map[string]bool{
		"process_started": true, "process_exited": true,
		"session_failure": true, "restart_attempt": true,
		"health_check_passed": true, "restart_failed": true,
		"restarted": true, "restart_exhausted": true,
		"closing": true, "closed": true,
	}
	for _, event := range snapshot.Events {
		if !validEvents[event.Type] {
			return fmt.Errorf("%w: diagnose: invalid Sidecar event type", errDiagnoseInvalid)
		}
		if _, err := time.Parse(time.RFC3339Nano, event.Timestamp); err != nil {
			return fmt.Errorf("%w: diagnose: invalid Sidecar event timestamp", errDiagnoseInvalid)
		}
		if event.Attempt != nil && (*event.Attempt < 1 || *event.Attempt > 1000) {
			return fmt.Errorf("%w: diagnose: invalid Sidecar restart attempt", errDiagnoseInvalid)
		}
		if event.ErrorType != "" && !diagnosticTypePattern.MatchString(event.ErrorType) {
			return fmt.Errorf("%w: diagnose: invalid Sidecar error type", errDiagnoseInvalid)
		}
	}
	return nil
}

func (item diagnoseCommand) writeArchive(
	path string,
	generatedAt time.Time,
	report []byte,
) error {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, entry := range []struct {
		name     string
		contents []byte
	}{
		{name: "diagnostics.json", contents: report},
		{name: "README.txt", contents: []byte(diagnosticBundleReadme)},
	} {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.Modified = generatedAt
		header.SetMode(0o600)
		destination, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			return fmt.Errorf("diagnose: create %s: %w", entry.name, err)
		}
		if _, err := destination.Write(entry.contents); err != nil {
			_ = writer.Close()
			return fmt.Errorf("diagnose: write %s: %w", entry.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("diagnose: finish archive: %w", err)
	}

	file, err := item.system.openFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: diagnose: output already exists: %s", errDiagnoseInvalid, path)
		}
		return fmt.Errorf("diagnose: create archive: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = item.system.remove(path)
		}
	}()
	if _, err := file.Write(archive.Bytes()); err != nil {
		_ = file.Close()
		return fmt.Errorf("diagnose: write archive: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("diagnose: sync archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("diagnose: close archive: %w", err)
	}
	succeeded = true
	return nil
}

const diagnosticBundleReadme = `Bridra diagnostic support bundle

diagnostics.json contains version, toolchain, host, and optional validated
Sidecar lifecycle information. The bundle intentionally excludes environment
variables, credentials, requests, logs, source files, project identity, and
absolute paths.

Review diagnostics.json before sharing this archive. Delete the archive when it
is no longer needed. Bridra does not upload it automatically.
`
