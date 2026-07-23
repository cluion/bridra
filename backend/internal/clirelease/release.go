package clirelease

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cluion/bridra/backend/framework"
	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

const ManifestSchemaVersion = 2

var (
	ErrInvalidConfiguration = errors.New("CLI release: invalid configuration")
	versionPattern          = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
	commitPattern           = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]*$`)
)

type Target struct {
	GOOS   string
	GOARCH string
}

var DefaultTargets = []Target{
	{GOOS: "darwin", GOARCH: "amd64"},
	{GOOS: "darwin", GOARCH: "arm64"},
	{GOOS: "linux", GOARCH: "amd64"},
	{GOOS: "linux", GOARCH: "arm64"},
	{GOOS: "windows", GOARCH: "amd64"},
	{GOOS: "windows", GOARCH: "arm64"},
}

type Config struct {
	Root      string
	Output    string
	Version   string
	Commit    string
	BuildDate string
	Targets   []Target
}

type ProcessSpec struct {
	Name        string
	Arguments   []string
	Directory   string
	Environment []string
}

type System struct {
	Abs       func(string) (string, error)
	Stat      func(string) (os.FileInfo, error)
	MkdirAll  func(string, os.FileMode) error
	MkdirTemp func(string, string) (string, error)
	RemoveAll func(string) error
	ReadFile  func(string) ([]byte, error)
	WriteFile func(string, []byte, os.FileMode) error
	Run       func(ProcessSpec) error
}

type Manifest struct {
	SchemaVersion  int        `json:"schemaVersion"`
	Product        string     `json:"product"`
	Version        string     `json:"version"`
	License        string     `json:"license"`
	Tag            string     `json:"tag"`
	Commit         string     `json:"commit"`
	BuildDate      string     `json:"buildDate"`
	GoModule       string     `json:"goModule"`
	CLIInstallPath string     `json:"cliInstallPath"`
	Artifacts      []Artifact `json:"artifacts"`
}

type Artifact struct {
	GOOS    string `json:"goos"`
	GOARCH  string `json:"goarch"`
	Archive string `json:"archive"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

func DefaultSystem() System {
	return System{
		Abs:       filepath.Abs,
		Stat:      os.Stat,
		MkdirAll:  os.MkdirAll,
		MkdirTemp: os.MkdirTemp,
		RemoveAll: os.RemoveAll,
		ReadFile:  os.ReadFile,
		WriteFile: os.WriteFile,
		Run: func(specification ProcessSpec) error {
			command := exec.Command(specification.Name, specification.Arguments...)
			command.Dir = specification.Directory
			command.Env = append(os.Environ(), specification.Environment...)
			command.Stdout = os.Stdout
			command.Stderr = os.Stderr
			return command.Run()
		},
	}
}

func Build(config Config, system System) (Manifest, error) {
	resolved, buildTime, err := resolveConfig(config, system)
	if err != nil {
		return Manifest{}, err
	}
	work, err := system.MkdirTemp("", "bridra-cli-release-*")
	if err != nil {
		return Manifest{}, fmt.Errorf("CLI release: create work directory: %w", err)
	}
	defer system.RemoveAll(work)

	manifest := Manifest{
		SchemaVersion:  ManifestSchemaVersion,
		Product:        "bridra",
		Version:        resolved.Version,
		License:        "MIT",
		Tag:            "backend/v" + resolved.Version,
		Commit:         resolved.Commit,
		BuildDate:      buildTime.Format(time.RFC3339),
		GoModule:       releaseinfo.GoModule,
		CLIInstallPath: releaseinfo.CLIInstallPath,
	}
	licenseContents, err := system.ReadFile(filepath.Join(resolved.Root, "LICENSE"))
	if err != nil {
		return Manifest{}, fmt.Errorf("CLI release: read LICENSE: %w", err)
	}
	if !bytes.HasPrefix(licenseContents, []byte("MIT License\n")) {
		return Manifest{}, fmt.Errorf("%w: backend LICENSE must contain the MIT License", ErrInvalidConfiguration)
	}
	files := make(map[string][]byte, len(resolved.Targets)+2)
	for _, target := range resolved.Targets {
		artifact, contents, buildErr := buildTarget(
			resolved,
			target,
			buildTime,
			work,
			licenseContents,
			system,
		)
		if buildErr != nil {
			return Manifest{}, buildErr
		}
		manifest.Artifacts = append(manifest.Artifacts, artifact)
		files[artifact.Archive] = contents
	}
	sort.Slice(manifest.Artifacts, func(left, right int) bool {
		return manifest.Artifacts[left].Archive < manifest.Artifacts[right].Archive
	})
	manifestContents, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, fmt.Errorf("CLI release: encode manifest: %w", err)
	}
	files["manifest.json"] = append(manifestContents, '\n')
	files["SHA256SUMS"] = renderChecksums(manifest.Artifacts)
	if err := system.MkdirAll(resolved.Output, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("CLI release: create output directory: %w", err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := system.WriteFile(filepath.Join(resolved.Output, name), files[name], 0o644); err != nil {
			return Manifest{}, fmt.Errorf("CLI release: write %s: %w", name, err)
		}
	}
	return manifest, nil
}

func resolveConfig(config Config, system System) (Config, time.Time, error) {
	config.Version = strings.TrimSpace(config.Version)
	config.Commit = strings.TrimSpace(config.Commit)
	config.BuildDate = strings.TrimSpace(config.BuildDate)
	if !versionPattern.MatchString(config.Version) {
		return Config{}, time.Time{}, fmt.Errorf("%w: invalid version %q", ErrInvalidConfiguration, config.Version)
	}
	if config.Version != framework.FrameworkVersion {
		return Config{}, time.Time{}, fmt.Errorf(
			"%w: CLI version %s must match framework version %s",
			ErrInvalidConfiguration,
			config.Version,
			framework.FrameworkVersion,
		)
	}
	if !commitPattern.MatchString(config.Commit) {
		return Config{}, time.Time{}, fmt.Errorf("%w: invalid commit %q", ErrInvalidConfiguration, config.Commit)
	}
	buildTime, err := time.Parse(time.RFC3339, config.BuildDate)
	if err != nil {
		return Config{}, time.Time{}, fmt.Errorf("%w: build date must use RFC 3339", ErrInvalidConfiguration)
	}
	buildTime = buildTime.UTC()
	root, err := system.Abs(config.Root)
	if err != nil {
		return Config{}, time.Time{}, fmt.Errorf("CLI release: resolve backend root: %w", err)
	}
	output, err := system.Abs(config.Output)
	if err != nil {
		return Config{}, time.Time{}, fmt.Errorf("CLI release: resolve output: %w", err)
	}
	config.Root = filepath.Clean(root)
	config.Output = filepath.Clean(output)
	for _, required := range []string{"go.mod", "LICENSE", filepath.Join("cmd", "bridra")} {
		if _, err := system.Stat(filepath.Join(config.Root, required)); err != nil {
			return Config{}, time.Time{}, fmt.Errorf(
				"%w: backend root is missing %s: %v",
				ErrInvalidConfiguration,
				required,
				err,
			)
		}
	}
	if len(config.Targets) == 0 {
		config.Targets = append([]Target(nil), DefaultTargets...)
	}
	seen := make(map[Target]struct{}, len(config.Targets))
	for _, target := range config.Targets {
		if !validTarget(target) {
			return Config{}, time.Time{}, fmt.Errorf(
				"%w: unsupported target %s/%s",
				ErrInvalidConfiguration,
				target.GOOS,
				target.GOARCH,
			)
		}
		if _, exists := seen[target]; exists {
			return Config{}, time.Time{}, fmt.Errorf(
				"%w: duplicate target %s/%s",
				ErrInvalidConfiguration,
				target.GOOS,
				target.GOARCH,
			)
		}
		seen[target] = struct{}{}
	}
	return config, buildTime, nil
}

func validTarget(target Target) bool {
	if target.GOARCH != "amd64" && target.GOARCH != "arm64" {
		return false
	}
	return target.GOOS == "darwin" || target.GOOS == "linux" || target.GOOS == "windows"
}

func buildTarget(
	config Config,
	target Target,
	buildTime time.Time,
	work string,
	licenseContents []byte,
	system System,
) (Artifact, []byte, error) {
	executable := "bridra"
	if target.GOOS == "windows" {
		executable += ".exe"
	}
	binary := filepath.Join(work, target.GOOS+"-"+target.GOARCH, executable)
	if err := system.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		return Artifact{}, nil, fmt.Errorf("CLI release: create target directory: %w", err)
	}
	linkerFlags := strings.Join([]string{
		"-s", "-w", "-buildid=",
		"-X", releaseinfo.GoModule + "/internal/releaseinfo.Version=" + config.Version,
		"-X", releaseinfo.GoModule + "/internal/releaseinfo.Commit=" + config.Commit,
		"-X", releaseinfo.GoModule + "/internal/releaseinfo.BuildDate=" + buildTime.Format(time.RFC3339),
	}, " ")
	if err := system.Run(ProcessSpec{
		Name: "go",
		Arguments: []string{
			"build", "-trimpath", "-buildvcs=false", "-ldflags", linkerFlags,
			"-o", binary, "./cmd/bridra",
		},
		Directory: config.Root,
		Environment: []string{
			"CGO_ENABLED=0", "GOOS=" + target.GOOS, "GOARCH=" + target.GOARCH,
		},
	}); err != nil {
		return Artifact{}, nil, fmt.Errorf(
			"CLI release: build %s/%s: %w",
			target.GOOS,
			target.GOARCH,
			err,
		)
	}
	binaryContents, err := system.ReadFile(binary)
	if err != nil {
		return Artifact{}, nil, fmt.Errorf("CLI release: read %s/%s binary: %w", target.GOOS, target.GOARCH, err)
	}
	archiveName := fmt.Sprintf("bridra_%s_%s_%s", config.Version, target.GOOS, target.GOARCH)
	var archive []byte
	if target.GOOS == "windows" {
		archiveName += ".zip"
		archive, err = zipExecutable(executable, binaryContents, licenseContents, buildTime)
	} else {
		archiveName += ".tar.gz"
		archive, err = tarExecutable(executable, binaryContents, licenseContents, buildTime)
	}
	if err != nil {
		return Artifact{}, nil, fmt.Errorf("CLI release: archive %s/%s: %w", target.GOOS, target.GOARCH, err)
	}
	return Artifact{
		GOOS: target.GOOS, GOARCH: target.GOARCH, Archive: archiveName,
		SHA256: checksum(archive), Size: int64(len(archive)),
	}, archive, nil
}

func tarExecutable(
	name string,
	contents []byte,
	licenseContents []byte,
	modified time.Time,
) ([]byte, error) {
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	compressed.Header.ModTime = modified
	compressed.Header.OS = 255
	archive := tar.NewWriter(compressed)
	files := []struct {
		name     string
		contents []byte
		mode     int64
	}{
		{name: name, contents: contents, mode: 0o755},
		{name: "LICENSE", contents: licenseContents, mode: 0o644},
	}
	for _, file := range files {
		header := &tar.Header{
			Name: file.name, Mode: file.mode, Size: int64(len(file.contents)), ModTime: modified,
			Uid: 0, Gid: 0, Uname: "", Gname: "", Format: tar.FormatUSTAR,
		}
		if err := archive.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := archive.Write(file.contents); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	if err := compressed.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func zipExecutable(
	name string,
	contents []byte,
	licenseContents []byte,
	modified time.Time,
) ([]byte, error) {
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	files := []struct {
		name     string
		contents []byte
		mode     os.FileMode
	}{
		{name: name, contents: contents, mode: 0o755},
		{name: "LICENSE", contents: licenseContents, mode: 0o644},
	}
	for _, file := range files {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		header.SetMode(file.mode)
		header.SetModTime(modified)
		entry, err := archive.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(file.contents); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func renderChecksums(artifacts []Artifact) []byte {
	var output strings.Builder
	for _, artifact := range artifacts {
		fmt.Fprintf(&output, "%s  %s\n", artifact.SHA256, artifact.Archive)
	}
	return []byte(output.String())
}

func checksum(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}
