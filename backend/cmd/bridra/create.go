package main

import (
	"bufio"
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
	"strings"
	"time"
	"unicode"

	"github.com/cluion/bridra/backend/framework"
	"github.com/cluion/bridra/backend/internal/projectplatform"
	"github.com/cluion/bridra/backend/internal/releaseinfo"
	"github.com/cluion/bridra/backend/projecttemplate"
)

var (
	errCreateInvalid    = errors.New("create: invalid project configuration")
	projectNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	organizationPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(\.[A-Za-z][A-Za-z0-9]*)+$`)
)

type createSystem struct {
	timeout   time.Duration
	abs       func(string) (string, error)
	stat      func(string) (os.FileInfo, error)
	lstat     func(string) (os.FileInfo, error)
	mkdirTemp func(string, string) (string, error)
	removeAll func(string) error
	rename    func(string, string) error
	run       func(context.Context, string, string, ...string) ([]byte, error)
}

type createCommand struct {
	system createSystem
}

type bridraSource struct {
	goModule              string
	goVersion             string
	goPath                string
	flutterName           string
	flutterPackageVersion string
	flutterPath           string
	dartImport            string
	flutterSDKVersion     string
	localDependencies     bool
}

func defaultCreateSystem() createSystem {
	return createSystem{
		timeout:   10 * time.Minute,
		abs:       filepath.Abs,
		stat:      os.Stat,
		lstat:     os.Lstat,
		mkdirTemp: os.MkdirTemp,
		removeAll: os.RemoveAll,
		rename:    os.Rename,
		run: func(
			ctx context.Context,
			directory string,
			name string,
			arguments ...string,
		) ([]byte, error) {
			command := exec.CommandContext(ctx, name, arguments...)
			command.Dir = directory
			return command.CombinedOutput()
		},
	}
}

func (createCommand) name() string {
	return "create"
}

func (createCommand) summary() string {
	return "Create a Flutter application with a Go backend"
}

func (createCommand) usage() string {
	return fmt.Sprintf(`Usage:
  bridra create <name> --module <go-module> [options]

Required:
  --module path       Go module for the generated backend

Options:
  --bridra-root path  Add local Go and Flutter dependency overrides
  --directory path    Destination directory (default <name>)
  --display-name text Product display name (default derived from <name>)
  --description text  Project description
  --organization id   Reverse-domain application organization (default com.example)
  --platforms value   all, desktop, mobile, or a comma-separated platform list (default all)

Default dependencies:
  %s %s
  %s %s`,
		releaseinfo.GoModule,
		releaseinfo.GoModuleVersion(),
		releaseinfo.FlutterPackage,
		releaseinfo.FlutterConstraint(),
	)
}

func (item createCommand) run(arguments []string, stdout, stderr io.Writer) error {
	if helpRequested(arguments) {
		fmt.Fprintln(stdout, item.usage())
		return nil
	}

	projectName, flagArguments := splitCreateName(arguments)
	flags := flag.NewFlagSet(item.name(), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, item.usage())
	}
	module := flags.String("module", "", "Go module for the generated backend")
	bridraRoot := flags.String("bridra-root", "", "local Bridra dependency overrides")
	directory := flags.String("directory", "", "destination directory")
	displayName := flags.String("display-name", "", "product display name")
	description := flags.String("description", "A Go-powered Flutter application.", "project description")
	organization := flags.String("organization", "com.example", "reverse-domain application organization")
	platforms := flags.String("platforms", "all", "platform preset or comma-separated list")
	if err := flags.Parse(flagArguments); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if projectName == "" && flags.NArg() == 1 {
		projectName = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return fmt.Errorf("%w: create: unexpected arguments: %v", errUsage, flags.Args())
	}

	options, err := item.resolveOptions(createOptions{
		projectName:  projectName,
		module:       *module,
		bridraRoot:   *bridraRoot,
		directory:    *directory,
		displayName:  *displayName,
		description:  *description,
		organization: *organization,
		platforms:    *platforms,
	})
	if err != nil {
		return err
	}
	return item.create(options, stdout)
}

type createOptions struct {
	projectName       string
	module            string
	bridraRoot        string
	directory         string
	displayName       string
	description       string
	organization      string
	platforms         string
	selectedPlatforms []string
	source            bridraSource
}

func (item createCommand) resolveOptions(options createOptions) (createOptions, error) {
	if !projectNamePattern.MatchString(options.projectName) {
		return createOptions{}, fmt.Errorf(
			"%w: project name must use lower_snake_case and start with a letter",
			errCreateInvalid,
		)
	}
	if err := validateGoModule(options.module); err != nil {
		return createOptions{}, err
	}
	if !organizationPattern.MatchString(options.organization) {
		return createOptions{}, fmt.Errorf(
			"%w: organization must be a reverse-domain identifier",
			errCreateInvalid,
		)
	}
	if strings.TrimSpace(options.description) == "" {
		return createOptions{}, fmt.Errorf("%w: description cannot be empty", errCreateInvalid)
	}
	if options.displayName == "" {
		options.displayName = humanizeProjectName(options.projectName)
	}
	if strings.TrimSpace(options.displayName) == "" {
		return createOptions{}, fmt.Errorf("%w: display name cannot be empty", errCreateInvalid)
	}
	selectedPlatforms, err := projectplatform.Resolve(options.platforms)
	if err != nil {
		return createOptions{}, fmt.Errorf("%w: --platforms: %v", errCreateInvalid, err)
	}
	options.selectedPlatforms = selectedPlatforms
	if options.directory == "" {
		options.directory = options.projectName
	}

	destination, err := item.system.abs(options.directory)
	if err != nil {
		return createOptions{}, fmt.Errorf("create: resolve destination: %w", err)
	}
	destination = filepath.Clean(destination)
	parent := filepath.Dir(destination)
	parentInfo, err := item.system.stat(parent)
	if err != nil {
		return createOptions{}, fmt.Errorf("create: destination parent: %w", err)
	}
	if !parentInfo.IsDir() {
		return createOptions{}, fmt.Errorf("%w: destination parent is not a directory", errCreateInvalid)
	}
	if _, err := item.system.lstat(destination); err == nil {
		return createOptions{}, fmt.Errorf("%w: destination already exists: %s", errCreateInvalid, destination)
	} else if !os.IsNotExist(err) {
		return createOptions{}, fmt.Errorf("create: inspect destination: %w", err)
	}

	source := releasedBridraSource()
	if strings.TrimSpace(options.bridraRoot) != "" {
		sourceRoot, resolveErr := item.system.abs(options.bridraRoot)
		if resolveErr != nil {
			return createOptions{}, fmt.Errorf("create: resolve Bridra root: %w", resolveErr)
		}
		sourceRoot = filepath.Clean(sourceRoot)
		localSource, loadErr := loadBridraSource(sourceRoot)
		if loadErr != nil {
			return createOptions{}, loadErr
		}
		options.bridraRoot = sourceRoot
		source = localSource
	}
	options.directory = destination
	options.source = source
	return options, nil
}

func (item createCommand) create(options createOptions, stdout io.Writer) (resultError error) {
	parent := filepath.Dir(options.directory)
	staging, err := item.system.mkdirTemp(
		parent,
		"."+filepath.Base(options.directory)+".bridra-*",
	)
	if err != nil {
		return fmt.Errorf("create: create staging directory: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if cleanupError := item.system.removeAll(staging); cleanupError != nil {
			resultError = errors.Join(
				resultError,
				fmt.Errorf("create: clean staging directory: %w", cleanupError),
			)
		}
	}()

	fmt.Fprintf(stdout, "Creating %s in %s\n", options.projectName, options.directory)
	bootstrapFVM := []byte(fmt.Sprintf(
		"{\n  \"flutter\": %q\n}\n",
		options.source.flutterSDKVersion,
	))
	if err := os.WriteFile(filepath.Join(staging, ".fvmrc"), bootstrapFVM, 0o644); err != nil {
		return fmt.Errorf("create: write staging .fvmrc: %w", err)
	}
	if err := item.execute(
		staging,
		"fvm",
		"flutter", "create", "--no-pub",
		"--project-name", options.projectName,
		"--org", options.organization,
		"--platforms", strings.Join(options.selectedPlatforms, ","),
		".",
	); err != nil {
		return fmt.Errorf("create: Flutter runners: %w", err)
	}
	if err := projecttemplate.Render(staging, projecttemplate.Config{
		ProjectName:          options.projectName,
		DisplayName:          options.displayName,
		Description:          options.description,
		Organization:         options.organization,
		GoModule:             options.module,
		BridraGoModule:       options.source.goModule,
		BridraGoVersion:      options.source.goVersion,
		BridraGoPath:         options.source.goPath,
		BridraFlutterPackage: options.source.flutterName,
		BridraFlutterVersion: options.source.flutterPackageVersion,
		BridraFlutterPath:    options.source.flutterPath,
		BridraDartImport:     options.source.dartImport,
		FlutterVersion:       options.source.flutterSDKVersion,
		FrameworkVersion:     releaseinfo.Version,
		TemplateVersion:      releaseinfo.ProjectTemplateVersion,
		ProtocolVersion:      framework.ProtocolVersion,
		LocalDependencies:    options.source.localDependencies,
		Platforms:            options.selectedPlatforms,
	}); err != nil {
		return fmt.Errorf("create: render project: %w", err)
	}
	if err := item.execute(filepath.Join(staging, "backend"), "go", "mod", "tidy"); err != nil {
		return fmt.Errorf("create: resolve Go dependencies: %w", err)
	}
	if err := item.execute(filepath.Join(staging, "backend"), "go", "test", "./..."); err != nil {
		return fmt.Errorf("create: verify Go consumer: %w", err)
	}
	if err := item.execute(staging, "fvm", "flutter", "pub", "get"); err != nil {
		return fmt.Errorf("create: resolve Flutter dependencies: %w", err)
	}
	if err := item.execute(
		staging,
		"fvm",
		"dart", "format", "lib", "test", "integration_test",
	); err != nil {
		return fmt.Errorf("create: format Flutter sources: %w", err)
	}
	if err := item.system.rename(staging, options.directory); err != nil {
		return fmt.Errorf("create: publish staged project: %w", err)
	}
	committed = true
	fmt.Fprintf(stdout, "Created %s.\n\n", options.displayName)
	fmt.Fprintf(stdout, "Next:\n  cd %s\n  make doctor\n  make verify\n", options.directory)
	return nil
}

func (item createCommand) execute(directory, name string, arguments ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), item.system.timeout)
	defer cancel()
	output, err := item.system.run(ctx, directory, name, arguments...)
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, detail)
}

func splitCreateName(arguments []string) (string, []string) {
	if len(arguments) == 0 || strings.HasPrefix(arguments[0], "-") {
		return "", arguments
	}
	return arguments[0], arguments[1:]
}

func validateGoModule(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%w: --module is required", errCreateInvalid)
	}
	if strings.ContainsAny(value, " \t\r\n\\") || strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") || !strings.Contains(value, "/") {
		return fmt.Errorf("%w: invalid Go module %q", errCreateInvalid, value)
	}
	return nil
}

func humanizeProjectName(value string) string {
	words := strings.Fields(strings.ReplaceAll(value, "_", " "))
	for index, word := range words {
		runes := []rune(word)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		words[index] = string(runes)
	}
	return strings.Join(words, " ")
}

func releasedBridraSource() bridraSource {
	return bridraSource{
		goModule:              releaseinfo.GoModule,
		goVersion:             releaseinfo.GoModuleVersion(),
		flutterName:           releaseinfo.FlutterPackage,
		flutterPackageVersion: releaseinfo.FlutterConstraint(),
		dartImport:            "package:" + releaseinfo.FlutterPackage + "/bridra_flutter.dart",
		flutterSDKVersion:     releaseinfo.RecommendedFlutterVersion,
	}
}

func loadBridraSource(root string) (bridraSource, error) {
	source := releasedBridraSource()
	goPath := filepath.Join(root, "backend")
	goModule, err := readGoModule(filepath.Join(goPath, "go.mod"))
	if err != nil {
		return bridraSource{}, err
	}
	flutterPath := filepath.Join(root, "packages", "bridra_flutter")
	flutterPackageVersion, err := readPubspecVersion(filepath.Join(flutterPath, "pubspec.yaml"))
	if err != nil {
		return bridraSource{}, err
	}
	if flutterPackageVersion != releaseinfo.Version {
		return bridraSource{}, fmt.Errorf(
			"%w: local Bridra Flutter package version %s does not match CLI version %s",
			errCreateInvalid,
			flutterPackageVersion,
			releaseinfo.Version,
		)
	}
	flutterName, err := readPubspecName(filepath.Join(flutterPath, "pubspec.yaml"))
	if err != nil {
		return bridraSource{}, err
	}
	contents, err := os.ReadFile(filepath.Join(root, ".fvmrc"))
	if err != nil {
		return bridraSource{}, fmt.Errorf("create: read Bridra .fvmrc: %w", err)
	}
	var configuration fvmConfiguration
	if err := json.Unmarshal(contents, &configuration); err != nil {
		return bridraSource{}, fmt.Errorf("create: decode Bridra .fvmrc: %w", err)
	}
	if strings.TrimSpace(configuration.Flutter) == "" {
		return bridraSource{}, fmt.Errorf("%w: Bridra .fvmrc has no Flutter version", errCreateInvalid)
	}
	source.goModule = goModule
	source.goPath = goPath
	source.flutterName = flutterName
	source.flutterPath = flutterPath
	source.dartImport = "package:" + flutterName + "/bridra_flutter.dart"
	source.flutterSDKVersion = strings.TrimSpace(configuration.Flutter)
	source.localDependencies = true
	return source, nil
}

func readGoModule(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("create: read Bridra Go module: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("create: scan Bridra Go module: %w", err)
	}
	return "", fmt.Errorf("%w: Bridra backend/go.mod has no module directive", errCreateInvalid)
}

func readPubspecName(path string) (string, error) {
	name, err := readPubspecField(path, "name")
	if err != nil {
		return "", fmt.Errorf("create: read Bridra Flutter package: %w", err)
	}
	if projectNamePattern.MatchString(name) {
		return name, nil
	}
	return "", fmt.Errorf("%w: Bridra Flutter pubspec has no valid name", errCreateInvalid)
}

func readPubspecVersion(path string) (string, error) {
	version, err := readPubspecField(path, "version")
	if err != nil {
		return "", fmt.Errorf("create: read Bridra Flutter package version: %w", err)
	}
	if strings.TrimSpace(version) == "" {
		return "", fmt.Errorf("%w: Bridra Flutter pubspec has no version", errCreateInvalid)
	}
	return version, nil
}

func readPubspecField(path, field string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) != line {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if found && key == field {
			return strings.Trim(strings.TrimSpace(value), "'\""), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("pubspec has no %s field", field)
}
