package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDoctorPassesWithMatchingPinnedToolchain(t *testing.T) {
	root := writeFVMConfiguration(t, "3.44.6")
	system := healthyDoctorSystem()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := newApplication(system).run(
		[]string{"doctor", "--root", root},
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("doctor: %v, stderr: %s", err, stderr.String())
	}
	for _, expected := range []string{
		"[ok] .fvmrc: pins Flutter 3.44.6",
		"[ok] Go: go version go1.25.5 linux/amd64",
		"[ok] FVM: 4.1.2",
		"[ok] Flutter: 3.44.6 (pinned), Dart 3.12.2",
		"All checks passed.",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), expected)
		}
	}
}

func TestDoctorReportsOptionalToolsWithoutBlockingCoreChecks(t *testing.T) {
	root := writeFVMConfiguration(t, "3.44.6")
	system := healthyDoctorSystem("clang")
	var stdout bytes.Buffer

	if err := newApplication(system).run(
		[]string{"doctor", "--root", root},
		&stdout,
		&bytes.Buffer{},
	); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(stdout.String(), "[warn] clang: not found") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Core checks passed with 1 host build warning(s).") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestDoctorStrictModeFailsOnHostWarnings(t *testing.T) {
	root := writeFVMConfiguration(t, "3.44.6")
	system := healthyDoctorSystem("ninja")
	var stdout bytes.Buffer

	err := newApplication(system).run(
		[]string{"doctor", "--root", root, "--strict"},
		&stdout,
		&bytes.Buffer{},
	)
	if !errors.Is(err, errDoctorFailed) {
		t.Fatalf("error = %v, want errDoctorFailed", err)
	}
	if !strings.Contains(stdout.String(), "Doctor strict mode failed with 1 warning(s).") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestDoctorFailsWhenFlutterDoesNotMatchFVMConfiguration(t *testing.T) {
	root := writeFVMConfiguration(t, "3.44.6")
	system := healthyDoctorSystem()
	system.run = func(
		_ context.Context,
		name string,
		arguments ...string,
	) ([]byte, error) {
		key := strings.Join(append([]string{name}, arguments...), " ")
		if key == "fvm flutter --version --machine" {
			return []byte(`{"frameworkVersion":"3.40.0","dartSdkVersion":"3.10.0"}`), nil
		}
		return systemTestOutputs[key], nil
	}
	var stdout bytes.Buffer

	err := newApplication(system).run(
		[]string{"doctor", "--root", root},
		&stdout,
		&bytes.Buffer{},
	)
	if !errors.Is(err, errDoctorFailed) {
		t.Fatalf("error = %v, want errDoctorFailed", err)
	}
	if !strings.Contains(
		stdout.String(),
		"[fail] Flutter: found 3.40.0, but .fvmrc pins 3.44.6",
	) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestMinimumVersion(t *testing.T) {
	tests := []struct {
		value string
		major int
		minor int
		want  bool
	}{
		{value: "go version go1.25.5 linux/amd64", major: 1, minor: 25, want: true},
		{value: "go version go1.24.9 linux/amd64", major: 1, minor: 25, want: false},
		{value: "4.1.2", major: 4, minor: 0, want: true},
		{value: "not a version", major: 4, minor: 0, want: false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			if got := minimumVersion(test.value, test.major, test.minor); got != test.want {
				t.Fatalf("minimumVersion(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

var systemTestPaths = map[string]string{
	"go":         "/tools/go",
	"fvm":        "/tools/fvm",
	"clang":      "/tools/clang",
	"cmake":      "/tools/cmake",
	"ninja":      "/tools/ninja",
	"pkg-config": "/tools/pkg-config",
}

var systemTestOutputs = map[string][]byte{
	"go version":                       []byte("go version go1.25.5 linux/amd64\n"),
	"fvm --version":                    []byte("4.1.2\n"),
	"fvm flutter --version --machine":  []byte(`{"frameworkVersion":"3.44.6","dartSdkVersion":"3.12.2"}`),
	"pkg-config --modversion gtk+-3.0": []byte("3.24.41\n"),
}

func healthyDoctorSystem(missingTools ...string) doctorSystem {
	paths := make(map[string]string, len(systemTestPaths))
	for name, path := range systemTestPaths {
		paths[name] = path
	}
	for _, name := range missingTools {
		delete(paths, name)
	}

	return doctorSystem{
		goos:    "linux",
		goarch:  "amd64",
		timeout: time.Second,
		lookPath: func(name string) (string, error) {
			path, exists := paths[name]
			if !exists {
				return "", fmt.Errorf("%s not found", name)
			}
			return path, nil
		},
		run: func(_ context.Context, name string, arguments ...string) ([]byte, error) {
			key := strings.Join(append([]string{name}, arguments...), " ")
			output, exists := systemTestOutputs[key]
			if !exists {
				return nil, fmt.Errorf("unexpected command: %s", key)
			}
			return output, nil
		},
		readFile: os.ReadFile,
		abs:      filepath.Abs,
	}
}

func writeFVMConfiguration(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	contents := []byte(fmt.Sprintf("{\n  \"flutter\": %q\n}\n", version))
	if err := os.WriteFile(filepath.Join(root, ".fvmrc"), contents, 0o600); err != nil {
		t.Fatalf("write .fvmrc: %v", err)
	}
	return root
}
