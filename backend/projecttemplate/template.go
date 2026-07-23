package projecttemplate

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/cluion/bridra/backend/codegen"
	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

const ManifestVersion = releaseinfo.ProjectTemplateVersion

var ErrInvalidConfiguration = errors.New("project template: invalid configuration")

//go:embed all:templates
var templateFiles embed.FS

type Config struct {
	ProjectName          string
	DisplayName          string
	Description          string
	Organization         string
	GoModule             string
	BridraGoModule       string
	BridraGoVersion      string
	BridraGoPath         string
	BridraFlutterPackage string
	BridraFlutterVersion string
	BridraFlutterPath    string
	BridraDartImport     string
	FlutterVersion       string
	FrameworkVersion     string
	TemplateVersion      int
	ProtocolVersion      int
	LocalDependencies    bool
}

type Manifest struct {
	Version int            `json:"version"`
	Files   []ManifestFile `json:"files"`
}

type ManifestFile struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
}

func LoadManifest() (Manifest, error) {
	contents, err := templateFiles.ReadFile("templates/manifest.json")
	if err != nil {
		return Manifest{}, fmt.Errorf("project template: read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("project template: decode manifest: %w", err)
	}
	if manifest.Version != ManifestVersion {
		return Manifest{}, fmt.Errorf(
			"project template: unsupported manifest version %d",
			manifest.Version,
		)
	}
	if len(manifest.Files) == 0 {
		return Manifest{}, errors.New("project template: manifest contains no files")
	}
	return manifest, nil
}

func Render(root string, config Config) error {
	if err := validateConfig(config); err != nil {
		return err
	}
	manifest, err := LoadManifest()
	if err != nil {
		return err
	}
	for _, file := range manifest.Files {
		if err := renderFile(root, file, config); err != nil {
			return err
		}
	}

	schema, err := codegen.LoadSchema(filepath.Join(root, "schema", "bridra.json"))
	if err != nil {
		return fmt.Errorf("project template: load generated schema: %w", err)
	}
	outputs, err := codegen.GenerateWithOptions(schema, codegen.Options{
		GoFrameworkImport: config.BridraGoModule + "/framework",
		DartRuntimeImport: config.BridraDartImport,
	})
	if err != nil {
		return fmt.Errorf("project template: generate contract: %w", err)
	}
	if err := codegen.Write(root, outputs); err != nil {
		return fmt.Errorf("project template: write generated contract: %w", err)
	}
	return nil
}

func renderFile(root string, file ManifestFile, config Config) error {
	if !safeRelativePath(file.Source) || !safeRelativePath(file.Destination) {
		return fmt.Errorf("%w: unsafe manifest path", ErrInvalidConfiguration)
	}
	mode, err := strconv.ParseUint(file.Mode, 8, 32)
	if err != nil {
		return fmt.Errorf("project template: invalid mode for %s: %w", file.Destination, err)
	}
	source, err := fs.ReadFile(templateFiles, filepath.ToSlash(filepath.Join("templates", file.Source)))
	if err != nil {
		return fmt.Errorf("project template: read %s: %w", file.Source, err)
	}
	parsed, err := template.New(file.Source).
		Option("missingkey=error").
		Funcs(template.FuncMap{"quote": strconv.Quote}).
		Parse(string(source))
	if err != nil {
		return fmt.Errorf("project template: parse %s: %w", file.Source, err)
	}
	var rendered bytes.Buffer
	if err := parsed.Execute(&rendered, config); err != nil {
		return fmt.Errorf("project template: render %s: %w", file.Source, err)
	}
	contents := rendered.Bytes()
	if strings.HasSuffix(file.Destination, ".go") {
		formatted, formatErr := format.Source(contents)
		if formatErr != nil {
			return fmt.Errorf("project template: format %s: %w", file.Destination, formatErr)
		}
		contents = formatted
	}
	destination := filepath.Join(root, filepath.FromSlash(file.Destination))
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("project template: create directory for %s: %w", file.Destination, err)
	}
	if err := os.WriteFile(destination, contents, os.FileMode(mode)); err != nil {
		return fmt.Errorf("project template: write %s: %w", file.Destination, err)
	}
	return nil
}

func validateConfig(config Config) error {
	values := map[string]string{
		"project name": config.ProjectName, "display name": config.DisplayName,
		"organization": config.Organization, "Go module": config.GoModule,
		"Bridra Go module": config.BridraGoModule, "Bridra Go version": config.BridraGoVersion,
		"Bridra Flutter package": config.BridraFlutterPackage,
		"Bridra Flutter version": config.BridraFlutterVersion,
		"Bridra Dart import":     config.BridraDartImport,
		"Flutter version":        config.FlutterVersion,
		"framework version":      config.FrameworkVersion,
	}
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: %s is required", ErrInvalidConfiguration, name)
		}
	}
	if config.TemplateVersion < 1 {
		return fmt.Errorf("%w: template version must be positive", ErrInvalidConfiguration)
	}
	if config.ProtocolVersion < 1 {
		return fmt.Errorf("%w: protocol version must be positive", ErrInvalidConfiguration)
	}
	if config.LocalDependencies {
		if strings.TrimSpace(config.BridraGoPath) == "" {
			return fmt.Errorf("%w: Bridra Go path is required in local dependency mode", ErrInvalidConfiguration)
		}
		if strings.TrimSpace(config.BridraFlutterPath) == "" {
			return fmt.Errorf("%w: Bridra Flutter path is required in local dependency mode", ErrInvalidConfiguration)
		}
	} else if strings.TrimSpace(config.BridraGoPath) != "" ||
		strings.TrimSpace(config.BridraFlutterPath) != "" {
		return fmt.Errorf(
			"%w: local dependency paths require local dependency mode",
			ErrInvalidConfiguration,
		)
	}
	return nil
}

func safeRelativePath(path string) bool {
	clean := filepath.Clean(filepath.FromSlash(path))
	return path != "" && clean != "." && !filepath.IsAbs(clean) &&
		clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
