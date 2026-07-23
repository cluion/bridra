package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cluion/bridra/backend/framework"
)

var (
	errBuildInvalid  = errors.New("build: invalid configuration")
	errBuildFailed   = errors.New("build: command failed")
	errBuildArtifact = errors.New("build: invalid artifact")
)

type buildTarget string

const (
	buildTargetLinux   buildTarget = "linux"
	buildTargetMacOS   buildTarget = "macos"
	buildTargetWindows buildTarget = "windows"
	buildTargetAndroid buildTarget = "android"
	buildTargetIOS     buildTarget = "ios"
	buildTargetWeb     buildTarget = "web"
)

type buildMode string

const (
	buildModeDebug   buildMode = "debug"
	buildModeProfile buildMode = "profile"
	buildModeRelease buildMode = "release"
)

type buildTransport string

const (
	buildTransportSidecar buildTransport = "sidecar"
	buildTransportHTTP    buildTransport = "http"
)

type buildProcessSpec struct {
	Name        string
	Arguments   []string
	Directory   string
	Environment []string
	Stdout      io.Writer
	Stderr      io.Writer
}

type buildSystem struct {
	goos      string
	goarch    string
	abs       func(string) (string, error)
	stat      func(string) (os.FileInfo, error)
	readFile  func(string) ([]byte, error)
	mkdirAll  func(string, os.FileMode) error
	mkdirTemp func(string, string) (string, error)
	removeAll func(string) error
	copyFile  func(string, string, os.FileMode) error
	writeFile func(string, []byte, os.FileMode) error
	checksum  func(string) (string, error)
	run       func(buildProcessSpec) error
}

type buildCommand struct {
	system buildSystem
}

type buildOptions struct {
	root         string
	target       buildTarget
	mode         buildMode
	backendURL   string
	token        string
	transport    buildTransport
	architecture string
	metadata     projectMetadata
}

type buildArtifact struct {
	path        string
	sidecarPath string
	requireFile bool
}

type buildManifest struct {
	SchemaVersion  int            `json:"schemaVersion"`
	ProjectName    string         `json:"projectName"`
	Target         buildTarget    `json:"target"`
	Mode           buildMode      `json:"mode"`
	Transport      buildTransport `json:"transport"`
	Architecture   string         `json:"architecture,omitempty"`
	Artifact       string         `json:"artifact"`
	ArtifactSHA256 string         `json:"artifactSha256"`
	Sidecar        string         `json:"sidecar,omitempty"`
	SidecarSHA256  string         `json:"sidecarSha256,omitempty"`
	BackendURL     string         `json:"backendUrl,omitempty"`
}

func defaultBuildSystem() buildSystem {
	return buildSystem{
		goos:      runtime.GOOS,
		goarch:    runtime.GOARCH,
		abs:       filepath.Abs,
		stat:      os.Stat,
		readFile:  os.ReadFile,
		mkdirAll:  os.MkdirAll,
		mkdirTemp: os.MkdirTemp,
		removeAll: os.RemoveAll,
		copyFile:  copyBuildFile,
		writeFile: os.WriteFile,
		checksum:  artifactSHA256,
		run: func(specification buildProcessSpec) error {
			command := exec.Command(specification.Name, specification.Arguments...)
			command.Dir = specification.Directory
			command.Env = append(os.Environ(), specification.Environment...)
			command.Stdout = specification.Stdout
			command.Stderr = specification.Stderr
			return command.Run()
		},
	}
}

func (buildCommand) name() string {
	return "build"
}

func (buildCommand) summary() string {
	return "Build a validated Flutter artifact and its Go backend"
}

func (buildCommand) usage() string {
	return `Usage:
  bridra build <target> [options]

Targets:
  linux, macos, windows  Desktop bundle with a Go Sidecar by default
  android, ios, web      HTTP client artifact for an external Go backend

Options:
  --root path         Bridra project root (default .)
  --mode value        debug, profile, or release (default release)
  --backend-url URL   HTTP RPC endpoint compiled into Flutter
  --token value       HTTP RPC token compiled into Flutter

Linux builds require Linux, macOS and iOS builds require macOS, and Windows
builds require Windows. Profile and release HTTP builds require an HTTPS /rpc
URL and an explicit token. iOS output is unsigned.`
}

func (item buildCommand) run(arguments []string, stdout, stderr io.Writer) error {
	if helpRequested(arguments) {
		fmt.Fprintln(stdout, item.usage())
		return nil
	}

	target, flagArguments := splitBuildTarget(arguments)
	flags := flag.NewFlagSet(item.name(), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, item.usage())
	}
	root := flags.String("root", ".", "Bridra project root")
	mode := flags.String("mode", string(buildModeRelease), "debug, profile, or release")
	backendURL := flags.String("backend-url", "", "HTTP RPC endpoint")
	token := flags.String("token", "", "HTTP RPC token")
	if err := flags.Parse(flagArguments); err != nil {
		return fmt.Errorf("%w: build: %v", errUsage, err)
	}
	if target == "" && flags.NArg() == 1 {
		target = buildTarget(flags.Arg(0))
	} else if flags.NArg() != 0 {
		return fmt.Errorf("%w: build: unexpected arguments: %v", errUsage, flags.Args())
	}

	options, err := item.resolveOptions(buildOptions{
		root:       *root,
		target:     target,
		mode:       buildMode(*mode),
		backendURL: *backendURL,
		token:      *token,
	})
	if err != nil {
		return err
	}
	return item.build(options, stdout, stderr)
}

func splitBuildTarget(arguments []string) (buildTarget, []string) {
	if len(arguments) == 0 || strings.HasPrefix(arguments[0], "-") {
		return "", arguments
	}
	return buildTarget(arguments[0]), arguments[1:]
}

func (item buildCommand) resolveOptions(options buildOptions) (buildOptions, error) {
	absoluteRoot, err := item.system.abs(options.root)
	if err != nil {
		return buildOptions{}, fmt.Errorf("build: resolve project root: %w", err)
	}
	options.root = filepath.Clean(absoluteRoot)
	metadata, err := loadProjectMetadata(options.root)
	if err != nil {
		return buildOptions{}, fmt.Errorf("%w: project metadata: %w", errBuildInvalid, err)
	}
	options.metadata = metadata
	for _, required := range []string{".fvmrc", "pubspec.yaml", "backend/cmd/sidecar"} {
		information, statErr := item.system.stat(filepath.Join(options.root, filepath.FromSlash(required)))
		if statErr != nil {
			return buildOptions{}, fmt.Errorf(
				"%w: %s is unavailable: %w",
				errBuildInvalid,
				required,
				statErr,
			)
		}
		if required == "backend/cmd/sidecar" && !information.IsDir() {
			return buildOptions{}, fmt.Errorf("%w: %s must be a directory", errBuildInvalid, required)
		}
		if required != "backend/cmd/sidecar" && information.IsDir() {
			return buildOptions{}, fmt.Errorf("%w: %s must be a file", errBuildInvalid, required)
		}
	}

	options.target = buildTarget(strings.ToLower(strings.TrimSpace(string(options.target))))
	if !validBuildTarget(options.target) {
		return buildOptions{}, fmt.Errorf(
			"%w: target must be linux, macos, windows, android, ios, or web",
			errBuildInvalid,
		)
	}
	options.mode = buildMode(strings.ToLower(strings.TrimSpace(string(options.mode))))
	if options.mode != buildModeDebug && options.mode != buildModeProfile && options.mode != buildModeRelease {
		return buildOptions{}, fmt.Errorf(
			"%w: mode must be debug, profile, or release",
			errBuildInvalid,
		)
	}
	if err := validateBuildHost(options.target, item.system.goos); err != nil {
		return buildOptions{}, err
	}

	options.backendURL = strings.TrimSpace(options.backendURL)
	options.token = strings.TrimSpace(options.token)
	if desktopBuildTarget(options.target) && options.backendURL == "" {
		options.transport = buildTransportSidecar
		if options.token != "" {
			return buildOptions{}, fmt.Errorf(
				"%w: --token requires --backend-url for desktop builds",
				errBuildInvalid,
			)
		}
		options.architecture, err = desktopBuildArchitecture(options.target, item.system.goarch)
		if err != nil {
			return buildOptions{}, err
		}
		return options, nil
	}

	options.transport = buildTransportHTTP
	if options.backendURL == "" {
		if options.mode != buildModeDebug {
			return buildOptions{}, fmt.Errorf(
				"%w: profile and release HTTP builds require --backend-url",
				errBuildInvalid,
			)
		}
		options.backendURL = debugBuildBackendURL(options.target)
	}
	if err := validateBuildBackendURL(options.backendURL, options.mode != buildModeDebug); err != nil {
		return buildOptions{}, err
	}
	if options.token == "" {
		if options.mode != buildModeDebug {
			return buildOptions{}, fmt.Errorf(
				"%w: profile and release HTTP builds require --token",
				errBuildInvalid,
			)
		}
		options.token = "dev-token"
	}
	return options, nil
}

func validBuildTarget(target buildTarget) bool {
	switch target {
	case buildTargetLinux, buildTargetMacOS, buildTargetWindows,
		buildTargetAndroid, buildTargetIOS, buildTargetWeb:
		return true
	default:
		return false
	}
}

func desktopBuildTarget(target buildTarget) bool {
	return target == buildTargetLinux || target == buildTargetMacOS || target == buildTargetWindows
}

func validateBuildHost(target buildTarget, goos string) error {
	requiredGOOS := ""
	requiredHost := ""
	switch target {
	case buildTargetLinux:
		requiredGOOS = "linux"
		requiredHost = "linux"
	case buildTargetMacOS, buildTargetIOS:
		requiredGOOS = "darwin"
		requiredHost = "macOS"
	case buildTargetWindows:
		requiredGOOS = "windows"
		requiredHost = "windows"
	case buildTargetAndroid, buildTargetWeb:
		if goos == "linux" || goos == "darwin" || goos == "windows" {
			return nil
		}
		return fmt.Errorf(
			"%w: %s builds require a Linux, macOS, or Windows host; current host is %s",
			errBuildInvalid,
			target,
			goos,
		)
	}
	if goos == requiredGOOS {
		return nil
	}
	return fmt.Errorf(
		"%w: %s builds require a %s host; current host is %s",
		errBuildInvalid,
		target,
		requiredHost,
		goos,
	)
}

func desktopBuildArchitecture(target buildTarget, goarch string) (string, error) {
	switch target {
	case buildTargetMacOS:
		if goarch == "amd64" || goarch == "arm64" {
			return "universal", nil
		}
	case buildTargetLinux:
		switch goarch {
		case "amd64":
			return "x64", nil
		case "arm64", "riscv64":
			return goarch, nil
		}
	case buildTargetWindows:
		switch goarch {
		case "amd64":
			return "x64", nil
		case "arm64":
			return "arm64", nil
		}
	}
	return "", fmt.Errorf(
		"%w: unsupported %s host architecture %s",
		errBuildInvalid,
		target,
		goarch,
	)
}

func debugBuildBackendURL(target buildTarget) string {
	if target == buildTargetAndroid {
		return "http://10.0.2.2:8080/rpc"
	}
	return "http://127.0.0.1:8080/rpc"
}

func validateBuildBackendURL(value string, requireHTTPS bool) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%w: backend URL must be an absolute HTTP or HTTPS URL", errBuildInvalid)
	}
	if requireHTTPS && parsed.Scheme != "https" {
		return fmt.Errorf("%w: profile and release backend URLs must use HTTPS", errBuildInvalid)
	}
	if parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.Path != "/rpc" {
		return fmt.Errorf(
			"%w: backend URL must target /rpc without credentials, query parameters, or fragments",
			errBuildInvalid,
		)
	}
	return nil
}

func (item buildCommand) build(options buildOptions, stdout, stderr io.Writer) (resultError error) {
	fmt.Fprintf(stdout, "Bridra Build %s\n", framework.FrameworkVersion)
	fmt.Fprintf(stdout, "Project: %s\n", options.root)
	fmt.Fprintf(stdout, "Target: %s\n", options.target)
	fmt.Fprintf(stdout, "Mode: %s\n", options.mode)
	fmt.Fprintf(stdout, "Transport: %s\n", options.transport)

	workDirectory := ""
	sidecarSource := ""
	if options.transport == buildTransportSidecar {
		parent := filepath.Join(options.root, "build", "bridra", ".work")
		if err := item.system.mkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("build: create work directory: %w", err)
		}
		var err error
		workDirectory, err = item.system.mkdirTemp(parent, string(options.target)+"-")
		if err != nil {
			return fmt.Errorf("build: create temporary directory: %w", err)
		}
		defer func() {
			if cleanupErr := item.system.removeAll(workDirectory); cleanupErr != nil {
				resultError = errors.Join(resultError, fmt.Errorf("build: clean work directory: %w", cleanupErr))
			}
		}()
		fmt.Fprintln(stdout, "Building Go Sidecar...")
		sidecarSource, err = item.buildSidecar(options, workDirectory, stdout, stderr)
		if err != nil {
			return err
		}
	}

	fmt.Fprintf(stdout, "Building Flutter %s...\n", options.target)
	flutterArguments := []string{
		"flutter", "build", flutterBuildSubcommand(options.target), "--" + string(options.mode),
	}
	if options.target == buildTargetLinux {
		flutterArguments = append(
			flutterArguments,
			"--target-platform=linux-"+options.architecture,
		)
	}
	if options.target == buildTargetIOS {
		flutterArguments = append(flutterArguments, "--no-codesign")
	}
	if options.transport == buildTransportHTTP {
		flutterArguments = append(
			flutterArguments,
			"--dart-define=BRIDRA_BACKEND_URL="+options.backendURL,
			"--dart-define=BRIDRA_BACKEND_TOKEN="+options.token,
		)
	}
	if err := item.execute("Flutter build", buildProcessSpec{
		Name:      "fvm",
		Arguments: flutterArguments,
		Directory: options.root,
		Stdout:    stdout,
		Stderr:    stderr,
	}); err != nil {
		return err
	}

	artifact, err := item.resolveArtifact(options)
	if err != nil {
		return err
	}
	if err := validateBuildArtifact(item.system.stat, artifact.path, artifact.requireFile); err != nil {
		return err
	}
	if options.transport == buildTransportSidecar {
		fmt.Fprintln(stdout, "Bundling Go Sidecar...")
		if err := item.system.mkdirAll(filepath.Dir(artifact.sidecarPath), 0o755); err != nil {
			return fmt.Errorf("build: create Sidecar directory: %w", err)
		}
		if err := item.system.copyFile(sidecarSource, artifact.sidecarPath, 0o755); err != nil {
			return fmt.Errorf("build: install Sidecar: %w", err)
		}
		if options.target == buildTargetMacOS {
			if err := item.signMacOSArtifact(options, artifact, stdout, stderr); err != nil {
				return err
			}
		}
	}

	if err := validateBuildArtifact(item.system.stat, artifact.path, artifact.requireFile); err != nil {
		return err
	}
	if artifact.sidecarPath != "" {
		if err := validateBuildArtifact(item.system.stat, artifact.sidecarPath, true); err != nil {
			return err
		}
	}

	artifactChecksum, err := item.system.checksum(artifact.path)
	if err != nil {
		return fmt.Errorf("build: checksum artifact: %w", err)
	}
	manifest := buildManifest{
		SchemaVersion:  1,
		ProjectName:    options.metadata.ProjectName,
		Target:         options.target,
		Mode:           options.mode,
		Transport:      options.transport,
		Architecture:   options.architecture,
		Artifact:       relativeBuildPath(options.root, artifact.path),
		ArtifactSHA256: artifactChecksum,
		BackendURL:     options.backendURL,
	}
	if artifact.sidecarPath != "" {
		manifest.Sidecar = relativeBuildPath(options.root, artifact.sidecarPath)
		manifest.SidecarSHA256, err = item.system.checksum(artifact.sidecarPath)
		if err != nil {
			return fmt.Errorf("build: checksum Sidecar: %w", err)
		}
	}
	manifestPath, err := item.writeManifest(options, manifest)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Artifact: %s\n", manifest.Artifact)
	fmt.Fprintf(stdout, "SHA-256: %s\n", manifest.ArtifactSHA256)
	fmt.Fprintf(stdout, "Manifest: %s\n", relativeBuildPath(options.root, manifestPath))
	return nil
}

func flutterBuildSubcommand(target buildTarget) string {
	if target == buildTargetAndroid {
		return "apk"
	}
	return string(target)
}

func (item buildCommand) buildSidecar(
	options buildOptions,
	workDirectory string,
	stdout io.Writer,
	stderr io.Writer,
) (string, error) {
	backendDirectory := filepath.Join(options.root, "backend")
	if options.target == buildTargetMacOS {
		var binaries []string
		for _, architecture := range []string{"arm64", "amd64"} {
			output := filepath.Join(workDirectory, "bridra_backend_"+architecture)
			if err := item.execute("Go Sidecar "+architecture, buildProcessSpec{
				Name:        "go",
				Arguments:   []string{"build", "-trimpath", "-o", output, "./cmd/sidecar"},
				Directory:   backendDirectory,
				Environment: []string{"CGO_ENABLED=0", "GOOS=darwin", "GOARCH=" + architecture},
				Stdout:      stdout,
				Stderr:      stderr,
			}); err != nil {
				return "", err
			}
			binaries = append(binaries, output)
		}
		output := filepath.Join(workDirectory, "bridra_backend")
		if err := item.execute("universal macOS Sidecar", buildProcessSpec{
			Name:      "xcrun",
			Arguments: []string{"lipo", "-create", binaries[0], binaries[1], "-output", output},
			Directory: options.root,
			Stdout:    stdout,
			Stderr:    stderr,
		}); err != nil {
			return "", err
		}
		return output, nil
	}

	suffix := ""
	goos := string(options.target)
	goarch := item.system.goarch
	if options.target == buildTargetWindows {
		suffix = ".exe"
	}
	output := filepath.Join(workDirectory, "bridra_backend"+suffix)
	if err := item.execute("Go Sidecar", buildProcessSpec{
		Name:        "go",
		Arguments:   []string{"build", "-trimpath", "-o", output, "./cmd/sidecar"},
		Directory:   backendDirectory,
		Environment: []string{"CGO_ENABLED=0", "GOOS=" + goos, "GOARCH=" + goarch},
		Stdout:      stdout,
		Stderr:      stderr,
	}); err != nil {
		return "", err
	}
	return output, nil
}

func (item buildCommand) resolveArtifact(options buildOptions) (buildArtifact, error) {
	root := options.root
	mode := string(options.mode)
	switch options.target {
	case buildTargetLinux:
		path := filepath.Join(root, "build", "linux", options.architecture, mode, "bundle")
		return buildArtifact{
			path:        path,
			sidecarPath: filepath.Join(path, "libexec", "bridra_backend"),
		}, nil
	case buildTargetMacOS:
		contents, err := item.system.readFile(
			filepath.Join(root, "macos", "Flutter", "ephemeral", ".app_filename"),
		)
		if err != nil {
			return buildArtifact{}, fmt.Errorf("%w: read macOS app filename: %w", errBuildArtifact, err)
		}
		filename := strings.TrimSpace(string(contents))
		if filename == "" || filepath.Base(filename) != filename || !strings.HasSuffix(filename, ".app") {
			return buildArtifact{}, fmt.Errorf("%w: invalid macOS app filename %q", errBuildArtifact, filename)
		}
		path := filepath.Join(root, "build", "macos", "Build", "Products", titleBuildMode(options.mode), filename)
		return buildArtifact{
			path:        path,
			sidecarPath: filepath.Join(path, "Contents", "MacOS", "libexec", "bridra_backend"),
		}, nil
	case buildTargetWindows:
		path := filepath.Join(
			root,
			"build", "windows", options.architecture, "runner", titleBuildMode(options.mode),
		)
		return buildArtifact{
			path:        path,
			sidecarPath: filepath.Join(path, "libexec", "bridra_backend.exe"),
		}, nil
	case buildTargetAndroid:
		return buildArtifact{
			path: filepath.Join(
				root, "build", "app", "outputs", "flutter-apk", "app-"+mode+".apk",
			),
			requireFile: true,
		}, nil
	case buildTargetIOS:
		return buildArtifact{path: filepath.Join(root, "build", "ios", "iphoneos", "Runner.app")}, nil
	case buildTargetWeb:
		return buildArtifact{path: filepath.Join(root, "build", "web")}, nil
	default:
		return buildArtifact{}, fmt.Errorf("%w: unsupported target %s", errBuildArtifact, options.target)
	}
}

func titleBuildMode(mode buildMode) string {
	value := string(mode)
	return strings.ToUpper(value[:1]) + value[1:]
}

func (item buildCommand) signMacOSArtifact(
	options buildOptions,
	artifact buildArtifact,
	stdout io.Writer,
	stderr io.Writer,
) error {
	for _, operation := range []struct {
		label     string
		arguments []string
	}{
		{label: "sign macOS Sidecar", arguments: []string{"--force", "--sign", "-", artifact.sidecarPath}},
		{label: "sign macOS app", arguments: []string{"--force", "--sign", "-", artifact.path}},
		{label: "verify macOS app", arguments: []string{"--verify", "--deep", "--strict", artifact.path}},
	} {
		if err := item.execute(operation.label, buildProcessSpec{
			Name:      "codesign",
			Arguments: operation.arguments,
			Directory: options.root,
			Stdout:    stdout,
			Stderr:    stderr,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (item buildCommand) execute(label string, specification buildProcessSpec) error {
	if err := item.system.run(specification); err != nil {
		return fmt.Errorf("%w: %s: %w", errBuildFailed, label, err)
	}
	return nil
}
