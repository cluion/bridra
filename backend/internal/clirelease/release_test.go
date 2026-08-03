package clirelease

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	runtimedebug "runtime/debug"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

func TestBuildCreatesDeterministicCrossPlatformArchivesAndMetadata(t *testing.T) {
	root := releaseTestRoot(t)
	targets := []Target{
		{GOOS: "linux", GOARCH: "arm64"},
		{GOOS: "windows", GOARCH: "amd64"},
	}
	var specifications []ProcessSpec
	system := DefaultSystem()
	system.ReadBuildInfo = func(string) (*runtimedebug.BuildInfo, error) {
		return testBuildInfo(), nil
	}
	system.Run = func(specification ProcessSpec) error {
		specifications = append(specifications, specification)
		output := argumentValue(t, specification.Arguments, "-o")
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			return err
		}
		contents := []byte(strings.Join(specification.Environment, "\n") + "\n")
		return os.WriteFile(output, contents, 0o755)
	}
	firstOutput := filepath.Join(t.TempDir(), "first")
	config := Config{
		Root: root, Output: firstOutput, Version: releaseinfo.Version,
		Commit: "abc123", BuildDate: "2026-07-22T08:00:00+08:00", Targets: targets,
	}
	manifest, err := Build(config, system)
	if err != nil {
		t.Fatalf("build release: %v", err)
	}
	if manifest.SchemaVersion != 3 || manifest.License != "MIT" ||
		manifest.BuildDate != "2026-07-22T00:00:00Z" ||
		manifest.Tag != "backend/v"+releaseinfo.Version || len(manifest.Artifacts) != 2 ||
		manifest.SBOM.Format != "SPDX-2.3" || manifest.SBOM.PredicateType != spdxPredicateType {
		t.Fatalf("manifest = %#v", manifest)
	}
	if len(specifications) != 2 {
		t.Fatalf("build specifications = %#v", specifications)
	}
	for _, specification := range specifications {
		joined := strings.Join(specification.Arguments, " ")
		for _, expected := range []string{
			"-trimpath", "-buildvcs=false", "-buildid=",
			"releaseinfo.Version=" + releaseinfo.Version, "releaseinfo.Commit=abc123",
			"releaseinfo.BuildDate=2026-07-22T00:00:00Z",
		} {
			if !strings.Contains(joined, expected) {
				t.Fatalf("arguments = %q, want %q", joined, expected)
			}
		}
	}
	for _, artifact := range manifest.Artifacts {
		contents, err := os.ReadFile(filepath.Join(config.Output, artifact.Archive))
		if err != nil {
			t.Fatalf("read %s: %v", artifact.Archive, err)
		}
		if checksum(contents) != artifact.SHA256 || int64(len(contents)) != artifact.Size {
			t.Fatalf("artifact metadata = %#v", artifact)
		}
		files, err := extractArchive(artifact.Archive, contents)
		if err != nil {
			t.Fatalf("extract %s: %v", artifact.Archive, err)
		}
		if string(files["LICENSE"]) != testLicense {
			t.Fatalf("archive LICENSE = %q", files["LICENSE"])
		}
		name := "bridra"
		if artifact.GOOS == "windows" {
			name = "bridra.exe"
		}
		binary, exists := files[name]
		if !exists {
			t.Fatalf("archive is missing %s; entries = %d", name, len(files))
		}
		if name != "bridra" && name != "bridra.exe" {
			t.Fatalf("archive executable = %q", name)
		}
		if !bytes.Contains(binary, []byte("CGO_ENABLED=0")) {
			t.Fatalf("archive binary = %q", binary)
		}
	}
	checksums, err := os.ReadFile(filepath.Join(config.Output, "SHA256SUMS"))
	if err != nil {
		t.Fatalf("read checksums: %v", err)
	}
	for _, artifact := range manifest.Artifacts {
		if !bytes.Contains(checksums, []byte(artifact.SHA256+"  "+artifact.Archive)) {
			t.Fatalf("checksums = %s", checksums)
		}
	}
	manifestContents, err := os.ReadFile(filepath.Join(config.Output, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var decoded Manifest
	if err := json.Unmarshal(manifestContents, &decoded); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(decoded.Artifacts) != len(manifest.Artifacts) || decoded.Commit != "abc123" {
		t.Fatalf("decoded manifest = %#v", decoded)
	}
	sbomContents, err := os.ReadFile(filepath.Join(config.Output, manifest.SBOM.File))
	if err != nil {
		t.Fatalf("read SBOM: %v", err)
	}
	if checksum(sbomContents) != manifest.SBOM.SHA256 || int64(len(sbomContents)) != manifest.SBOM.Size {
		t.Fatalf("SBOM metadata = %#v", manifest.SBOM)
	}
	var sbom spdxDocument
	if err := json.Unmarshal(sbomContents, &sbom); err != nil {
		t.Fatalf("decode SBOM: %v", err)
	}
	if sbom.SPDXVersion != "SPDX-2.3" || sbom.DataLicense != "CC0-1.0" ||
		len(sbom.Packages) != 3 || len(sbom.Relationships) != 3 {
		t.Fatalf("SBOM = %#v", sbom)
	}
	if sbom.Packages[1].Name != "example.com/alpha" || sbom.Packages[2].Name != "example.com/zeta" {
		t.Fatalf("SBOM packages = %#v", sbom.Packages)
	}
	if sbom.Packages[0].DownloadLocation != "NOASSERTION" ||
		sbom.Packages[0].ExternalRefs[0].ReferenceLocator !=
			"pkg:golang/github.com/cluion/bridra/backend/cmd/bridra@v"+releaseinfo.Version {
		t.Fatalf("SBOM root package = %#v", sbom.Packages[0])
	}

	secondOutput := filepath.Join(t.TempDir(), "second")
	config.Output = secondOutput
	if _, err := Build(config, system); err != nil {
		t.Fatalf("second build: %v", err)
	}
	for _, artifact := range manifest.Artifacts {
		assertFilesEqual(t, firstOutput, secondOutput, artifact.Archive)
	}
	for _, name := range []string{manifest.SBOM.File, "manifest.json", "SHA256SUMS"} {
		assertFilesEqual(t, firstOutput, secondOutput, name)
	}
}

func TestModuleGraphRejectsReplacementDependencies(t *testing.T) {
	info := testBuildInfo()
	info.Deps[0].Replace = &runtimedebug.Module{Path: "../local"}
	_, err := moduleGraphFromBuildInfo(info)
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("error = %v, want ErrInvalidConfiguration", err)
	}
}

func TestBuildRejectsDifferentTargetDependencyGraphs(t *testing.T) {
	root := releaseTestRoot(t)
	system := DefaultSystem()
	system.Run = func(specification ProcessSpec) error {
		output := argumentValue(t, specification.Arguments, "-o")
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			return err
		}
		return os.WriteFile(output, []byte("binary"), 0o755)
	}
	system.ReadBuildInfo = func(path string) (*runtimedebug.BuildInfo, error) {
		info := testBuildInfo()
		if strings.Contains(path, "windows-") {
			info.Deps[0].Version = "v2.0.0"
		}
		return info, nil
	}
	_, err := Build(Config{
		Root: root, Output: t.TempDir(), Version: releaseinfo.Version,
		Commit: "abc123", BuildDate: "2026-07-22T00:00:00Z",
		Targets: []Target{{GOOS: "linux", GOARCH: "arm64"}, {GOOS: "windows", GOARCH: "amd64"}},
	}, system)
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("error = %v, want ErrInvalidConfiguration", err)
	}
}

func assertFilesEqual(t *testing.T, firstOutput string, secondOutput string, name string) {
	t.Helper()
	first, err := os.ReadFile(filepath.Join(firstOutput, name))
	if err != nil {
		t.Fatalf("read first deterministic %s: %v", name, err)
	}
	second, err := os.ReadFile(filepath.Join(secondOutput, name))
	if err != nil {
		t.Fatalf("read second deterministic %s: %v", name, err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("%s is not deterministic", name)
	}
}

func TestBuildRejectsInvalidReleaseMetadata(t *testing.T) {
	tests := []Config{
		{Version: "v0.1.0", Commit: "abc123", BuildDate: "2026-07-22T00:00:00Z"},
		{Version: "9.9.9", Commit: "abc123", BuildDate: "2026-07-22T00:00:00Z"},
		{Version: releaseinfo.Version, Commit: "bad commit", BuildDate: "2026-07-22T00:00:00Z"},
		{Version: releaseinfo.Version, Commit: "abc123", BuildDate: "today"},
	}
	for _, config := range tests {
		_, err := Build(config, DefaultSystem())
		if !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("config = %#v, error = %v", config, err)
		}
	}
}

func TestBuildRequiresMITLicense(t *testing.T) {
	root := releaseTestRoot(t)
	if err := os.WriteFile(filepath.Join(root, "LICENSE"), []byte("Apache License\n"), 0o644); err != nil {
		t.Fatalf("replace LICENSE: %v", err)
	}
	_, err := Build(Config{
		Root: root, Output: t.TempDir(), Version: releaseinfo.Version,
		Commit: "abc123", BuildDate: "2026-07-22T00:00:00Z",
		Targets: []Target{{GOOS: "linux", GOARCH: "arm64"}},
	}, DefaultSystem())
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("error = %v, want ErrInvalidConfiguration", err)
	}
}

func releaseTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "bridra"), 0o755); err != nil {
		t.Fatalf("create command directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/release\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "LICENSE"), []byte(testLicense), 0o644); err != nil {
		t.Fatalf("write LICENSE: %v", err)
	}
	return root
}

const testLicense = "MIT License\n\nCopyright (c) 2026 Cluion\n"

func testBuildInfo() *runtimedebug.BuildInfo {
	return &runtimedebug.BuildInfo{
		GoVersion: "go1.25.0",
		Deps: []*runtimedebug.Module{
			{Path: "example.com/zeta", Version: "v1.2.3", Sum: "h1:zeta"},
			{Path: "example.com/alpha", Version: "v0.4.0", Sum: "h1:alpha"},
		},
	}
}

func extractArchive(archiveName string, contents []byte) (map[string][]byte, error) {
	files := make(map[string][]byte)
	if strings.HasSuffix(archiveName, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
		if err != nil {
			return nil, err
		}
		for _, archived := range reader.File {
			entry, openErr := archived.Open()
			if openErr != nil {
				return nil, openErr
			}
			data, readErr := io.ReadAll(entry)
			closeErr := entry.Close()
			if readErr != nil {
				return nil, readErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			files[archived.Name] = data
		}
		return files, nil
	}
	compressed, err := gzip.NewReader(bytes.NewReader(contents))
	if err != nil {
		return nil, err
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	for {
		header, nextErr := archive.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, nextErr
		}
		data, readErr := io.ReadAll(archive)
		if readErr != nil {
			return nil, readErr
		}
		files[header.Name] = data
	}
	return files, nil
}

func argumentValue(t *testing.T, arguments []string, name string) string {
	t.Helper()
	for index, argument := range arguments {
		if argument == name && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	t.Fatalf("argument %s missing from %v", name, arguments)
	return ""
}
