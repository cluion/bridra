package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cluion/bridra/backend/framework"
)

var errDoctorFailed = errors.New("Bridra doctor checks failed")

var semanticVersionPattern = regexp.MustCompile(`(?:^|[^0-9])(\d+)\.(\d+)(?:\.\d+)?`)

type doctorSystem struct {
	goos     string
	goarch   string
	timeout  time.Duration
	lookPath func(string) (string, error)
	run      func(context.Context, string, ...string) ([]byte, error)
	readFile func(string) ([]byte, error)
	abs      func(string) (string, error)
}

type doctorCommand struct {
	system doctorSystem
}

type doctorStatus string

const (
	doctorOK      doctorStatus = "ok"
	doctorWarning doctorStatus = "warn"
	doctorFailure doctorStatus = "fail"
)

type doctorCheck struct {
	status doctorStatus
	name   string
	detail string
}

type doctorReport struct {
	checks []doctorCheck
}

type fvmConfiguration struct {
	Flutter string `json:"flutter"`
}

type flutterVersion struct {
	FrameworkVersion string `json:"frameworkVersion"`
	DartSDKVersion   string `json:"dartSdkVersion"`
}

func defaultDoctorSystem() doctorSystem {
	return doctorSystem{
		goos:     runtime.GOOS,
		goarch:   runtime.GOARCH,
		timeout:  45 * time.Second,
		lookPath: exec.LookPath,
		run: func(ctx context.Context, name string, arguments ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, arguments...).CombinedOutput()
		},
		readFile: os.ReadFile,
		abs:      filepath.Abs,
	}
}

func (doctorCommand) name() string {
	return "doctor"
}

func (doctorCommand) summary() string {
	return "Check Go, FVM, Flutter, and host build requirements"
}

func (doctorCommand) usage() string {
	return `Usage:
  bridra doctor [--root path] [--strict]

Options:
  --root path  Bridra project root containing .fvmrc (default .)
  --strict     Treat missing optional host build tools as failures`
}

func (item doctorCommand) run(arguments []string, stdout, stderr io.Writer) error {
	if helpRequested(arguments) {
		fmt.Fprintln(stdout, item.usage())
		return nil
	}

	flags := flag.NewFlagSet(item.name(), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, item.usage())
	}
	root := flags.String("root", ".", "Bridra project root containing .fvmrc")
	strict := flags.Bool("strict", false, "treat host build tool warnings as failures")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("%w: doctor: %v", errUsage, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: doctor: unexpected arguments: %v", errUsage, flags.Args())
	}

	absoluteRoot, err := item.system.abs(*root)
	if err != nil {
		return fmt.Errorf("doctor: resolve project root: %w", err)
	}
	report := item.inspect(filepath.Clean(absoluteRoot))

	fmt.Fprintf(stdout, "Bridra Doctor %s\n", framework.FrameworkVersion)
	fmt.Fprintf(stdout, "Project: %s\n\n", filepath.Clean(absoluteRoot))
	for _, check := range report.checks {
		fmt.Fprintf(stdout, "[%s] %s: %s\n", check.status, check.name, check.detail)
	}

	failures := report.count(doctorFailure)
	warnings := report.count(doctorWarning)
	fmt.Fprintln(stdout)
	switch {
	case failures > 0:
		fmt.Fprintf(stdout, "Doctor failed with %d failure(s) and %d warning(s).\n", failures, warnings)
		return fmt.Errorf("%w: %d failure(s), %d warning(s)", errDoctorFailed, failures, warnings)
	case *strict && warnings > 0:
		fmt.Fprintf(stdout, "Doctor strict mode failed with %d warning(s).\n", warnings)
		return fmt.Errorf("%w: strict mode found %d warning(s)", errDoctorFailed, warnings)
	case warnings > 0:
		fmt.Fprintf(stdout, "Core checks passed with %d host build warning(s).\n", warnings)
	default:
		fmt.Fprintln(stdout, "All checks passed.")
	}
	return nil
}

func (item doctorCommand) inspect(root string) doctorReport {
	report := doctorReport{}
	configuration, configurationOK := item.checkFVMConfiguration(root, &report)
	item.checkGo(&report)
	fvmOK := item.checkFVM(&report)
	if configurationOK && fvmOK {
		item.checkFlutter(configuration.Flutter, &report)
	} else {
		report.add(doctorFailure, "Flutter", "not checked because .fvmrc or FVM is unavailable")
	}
	item.checkHost(&report)
	return report
}

func (item doctorCommand) checkFVMConfiguration(
	root string,
	report *doctorReport,
) (fvmConfiguration, bool) {
	path := filepath.Join(root, ".fvmrc")
	contents, err := item.system.readFile(path)
	if err != nil {
		report.add(doctorFailure, ".fvmrc", fmt.Sprintf("cannot read %s: %v", path, err))
		return fvmConfiguration{}, false
	}
	var configuration fvmConfiguration
	if err := json.Unmarshal(contents, &configuration); err != nil {
		report.add(doctorFailure, ".fvmrc", fmt.Sprintf("invalid JSON: %v", err))
		return fvmConfiguration{}, false
	}
	configuration.Flutter = strings.TrimSpace(configuration.Flutter)
	if configuration.Flutter == "" {
		report.add(doctorFailure, ".fvmrc", "missing non-empty flutter version")
		return fvmConfiguration{}, false
	}
	report.add(doctorOK, ".fvmrc", "pins Flutter "+configuration.Flutter)
	return configuration, true
}

func (item doctorCommand) checkGo(report *doctorReport) {
	if _, err := item.system.lookPath("go"); err != nil {
		report.add(doctorFailure, "Go", "not found in PATH (Go 1.25 or newer is required)")
		return
	}
	output, err := item.execute("go", "version")
	if err != nil {
		report.add(doctorFailure, "Go", commandFailureDetail(output, err))
		return
	}
	version := strings.TrimSpace(string(output))
	if !minimumVersion(version, 1, 25) {
		report.add(doctorFailure, "Go", version+"; Go 1.25 or newer is required")
		return
	}
	report.add(doctorOK, "Go", version)
}

func (item doctorCommand) checkFVM(report *doctorReport) bool {
	if _, err := item.system.lookPath("fvm"); err != nil {
		report.add(doctorFailure, "FVM", "not found in PATH (FVM 4 or newer is required)")
		return false
	}
	output, err := item.execute("fvm", "--version")
	if err != nil {
		report.add(doctorFailure, "FVM", commandFailureDetail(output, err))
		return false
	}
	version := firstLine(output)
	if !minimumVersion(version, 4, 0) {
		report.add(doctorFailure, "FVM", version+"; FVM 4 or newer is required")
		return false
	}
	report.add(doctorOK, "FVM", version)
	return true
}

func (item doctorCommand) checkFlutter(expected string, report *doctorReport) {
	output, err := item.execute("fvm", "flutter", "--version", "--machine")
	if err != nil {
		report.add(doctorFailure, "Flutter", commandFailureDetail(output, err))
		return
	}
	var version flutterVersion
	if err := json.Unmarshal(output, &version); err != nil {
		report.add(
			doctorFailure,
			"Flutter",
			"cannot parse 'fvm flutter --version --machine': "+err.Error(),
		)
		return
	}
	if version.FrameworkVersion != expected {
		report.add(
			doctorFailure,
			"Flutter",
			fmt.Sprintf(
				"found %s, but .fvmrc pins %s; run 'fvm install'",
				version.FrameworkVersion,
				expected,
			),
		)
		return
	}
	detail := version.FrameworkVersion + " (pinned)"
	if version.DartSDKVersion != "" {
		detail += ", Dart " + version.DartSDKVersion
	}
	report.add(doctorOK, "Flutter", detail)
}

func (item doctorCommand) checkHost(report *doctorReport) {
	report.add(doctorOK, "Host", item.system.goos+"/"+item.system.goarch)
	if item.system.goarch != "amd64" && item.system.goarch != "arm64" {
		report.add(
			doctorWarning,
			"Architecture",
			item.system.goarch+" is not in Bridra's release build matrix",
		)
	}

	switch item.system.goos {
	case "linux":
		for _, tool := range []string{"clang", "cmake", "ninja", "pkg-config"} {
			item.checkOptionalTool(tool, report)
		}
		if _, err := item.system.lookPath("pkg-config"); err == nil {
			output, commandErr := item.execute("pkg-config", "--modversion", "gtk+-3.0")
			if commandErr != nil {
				report.add(doctorWarning, "GTK 3", "development package not found by pkg-config")
			} else {
				report.add(doctorOK, "GTK 3", strings.TrimSpace(string(output)))
			}
		}
	case "darwin":
		item.checkOptionalTool("xcodebuild", report)
		item.checkOptionalTool("xcrun", report)
	case "windows":
		item.checkOptionalTool("cmake", report)
		item.checkOptionalTool("ninja", report)
		item.checkOptionalTool("cl", report)
	default:
		report.add(
			doctorWarning,
			"Platform",
			item.system.goos+" is not in Bridra's release build matrix",
		)
	}
}

func (item doctorCommand) checkOptionalTool(name string, report *doctorReport) {
	path, err := item.system.lookPath(name)
	if err != nil {
		report.add(doctorWarning, name, "not found; desktop release builds may be unavailable")
		return
	}
	report.add(doctorOK, name, path)
}

func (item doctorCommand) execute(name string, arguments ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), item.system.timeout)
	defer cancel()
	return item.system.run(ctx, name, arguments...)
}

func (report *doctorReport) add(status doctorStatus, name, detail string) {
	report.checks = append(
		report.checks,
		doctorCheck{status: status, name: name, detail: detail},
	)
}

func (report doctorReport) count(status doctorStatus) int {
	count := 0
	for _, check := range report.checks {
		if check.status == status {
			count++
		}
	}
	return count
}

func minimumVersion(value string, requiredMajor, requiredMinor int) bool {
	matches := semanticVersionPattern.FindStringSubmatch(value)
	if len(matches) != 3 {
		return false
	}
	major, majorErr := strconv.Atoi(matches[1])
	minor, minorErr := strconv.Atoi(matches[2])
	if majorErr != nil || minorErr != nil {
		return false
	}
	return major > requiredMajor || major == requiredMajor && minor >= requiredMinor
}

func firstLine(output []byte) string {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	return strings.TrimSpace(lines[0])
}

func commandFailureDetail(output []byte, err error) string {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return err.Error()
	}
	return detail + ": " + err.Error()
}
