package scaffold

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
	"regexp"
	"sort"
	"strings"
	"text/template"
	"unicode"
)

const ManifestVersion = 1

var (
	ErrInvalidConfiguration = errors.New("scaffold: invalid configuration")
	ErrCollision            = errors.New("scaffold: destination already exists")
	componentNamePattern    = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
)

//go:embed all:templates
var templateFiles embed.FS

type Config struct {
	Root            string
	Kind            string
	Name            string
	FrameworkModule string
	Force           bool
}

type Result struct {
	Path     string
	Replaced bool
}

type Manifest struct {
	Version   int                  `json:"version"`
	Scaffolds map[string]Component `json:"scaffolds"`
}

type Component struct {
	Files []ManifestFile `json:"files"`
}

type ManifestFile struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

type templateData struct {
	Name            string
	SnakeName       string
	FrameworkModule string
}

type renderedFile struct {
	path    string
	content []byte
	existed bool
}

type fileOperations struct {
	stat      func(string) (os.FileInfo, error)
	mkdirAll  func(string, os.FileMode) error
	mkdirTemp func(string, string) (string, error)
	writeFile func(string, []byte, os.FileMode) error
	rename    func(string, string) error
	remove    func(string) error
	removeAll func(string) error
}

type committedFile struct {
	destination string
	backup      string
	existed     bool
}

func defaultFileOperations() fileOperations {
	return fileOperations{
		stat:      os.Lstat,
		mkdirAll:  os.MkdirAll,
		mkdirTemp: os.MkdirTemp,
		writeFile: os.WriteFile,
		rename:    os.Rename,
		remove:    os.Remove,
		removeAll: os.RemoveAll,
	}
}

func LoadManifest() (Manifest, error) {
	contents, err := templateFiles.ReadFile("templates/manifest.json")
	if err != nil {
		return Manifest{}, fmt.Errorf("scaffold: read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("scaffold: decode manifest: %w", err)
	}
	if manifest.Version != ManifestVersion {
		return Manifest{}, fmt.Errorf(
			"scaffold: unsupported manifest version %d",
			manifest.Version,
		)
	}
	if len(manifest.Scaffolds) == 0 {
		return Manifest{}, errors.New("scaffold: manifest contains no components")
	}
	return manifest, nil
}

func Kinds() ([]string, error) {
	manifest, err := LoadManifest()
	if err != nil {
		return nil, err
	}
	kinds := make([]string, 0, len(manifest.Scaffolds))
	for kind := range manifest.Scaffolds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds, nil
}

func Generate(config Config) ([]Result, error) {
	return generate(config, defaultFileOperations())
}

func generate(config Config, operations fileOperations) (results []Result, resultError error) {
	files, err := render(config)
	if err != nil {
		return nil, err
	}
	collisions := make([]string, 0)
	for index := range files {
		_, statError := operations.stat(files[index].path)
		switch {
		case statError == nil:
			files[index].existed = true
			if !config.Force {
				collisions = append(collisions, relativePath(config.Root, files[index].path))
			}
		case os.IsNotExist(statError):
		default:
			return nil, fmt.Errorf("scaffold: inspect %s: %w", files[index].path, statError)
		}
	}
	if len(collisions) > 0 {
		sort.Strings(collisions)
		return nil, fmt.Errorf("%w: %s", ErrCollision, strings.Join(collisions, ", "))
	}

	staging, err := operations.mkdirTemp(config.Root, ".bridra-scaffold-*")
	if err != nil {
		return nil, fmt.Errorf("scaffold: create staging directory: %w", err)
	}
	defer func() {
		if cleanupError := operations.removeAll(staging); cleanupError != nil {
			resultError = errors.Join(
				resultError,
				fmt.Errorf("scaffold: clean staging directory: %w", cleanupError),
			)
		}
	}()

	for index, file := range files {
		staged := filepath.Join(staging, "files", fmt.Sprintf("%d", index))
		if err := operations.mkdirAll(filepath.Dir(staged), 0o755); err != nil {
			return nil, fmt.Errorf("scaffold: create staged directory: %w", err)
		}
		if err := operations.writeFile(staged, file.content, 0o644); err != nil {
			return nil, fmt.Errorf("scaffold: write staged file: %w", err)
		}
	}

	committed := make([]committedFile, 0, len(files))
	for index, file := range files {
		if err := operations.mkdirAll(filepath.Dir(file.path), 0o755); err != nil {
			return nil, joinRollback(
				fmt.Errorf("scaffold: create destination directory: %w", err),
				rollback(committed, operations),
			)
		}
		entry := committedFile{destination: file.path, existed: file.existed}
		if file.existed {
			entry.backup = filepath.Join(staging, "backups", fmt.Sprintf("%d", index))
			if err := operations.mkdirAll(filepath.Dir(entry.backup), 0o755); err != nil {
				return nil, joinRollback(
					fmt.Errorf("scaffold: create backup directory: %w", err),
					rollback(committed, operations),
				)
			}
			if err := operations.rename(file.path, entry.backup); err != nil {
				return nil, joinRollback(
					fmt.Errorf("scaffold: back up %s: %w", file.path, err),
					rollback(committed, operations),
				)
			}
		}
		committed = append(committed, entry)
		staged := filepath.Join(staging, "files", fmt.Sprintf("%d", index))
		if err := operations.rename(staged, file.path); err != nil {
			return nil, joinRollback(
				fmt.Errorf("scaffold: publish %s: %w", file.path, err),
				rollback(committed, operations),
			)
		}
		results = append(results, Result{
			Path:     relativePath(config.Root, file.path),
			Replaced: file.existed,
		})
	}
	return results, nil
}

func render(config Config) ([]renderedFile, error) {
	if strings.TrimSpace(config.Root) == "" || strings.TrimSpace(config.FrameworkModule) == "" {
		return nil, fmt.Errorf("%w: root and framework module are required", ErrInvalidConfiguration)
	}
	config.Root = filepath.Clean(config.Root)
	if !componentNamePattern.MatchString(config.Name) {
		return nil, fmt.Errorf(
			"%w: component name must be PascalCase and start with a letter",
			ErrInvalidConfiguration,
		)
	}
	manifest, err := LoadManifest()
	if err != nil {
		return nil, err
	}
	component, exists := manifest.Scaffolds[config.Kind]
	if !exists {
		kinds, kindsError := Kinds()
		if kindsError != nil {
			return nil, kindsError
		}
		return nil, fmt.Errorf(
			"%w: unknown kind %q; available: %s",
			ErrInvalidConfiguration,
			config.Kind,
			strings.Join(kinds, ", "),
		)
	}
	data := templateData{
		Name: config.Name, SnakeName: snakeCase(config.Name),
		FrameworkModule: config.FrameworkModule,
	}
	files := make([]renderedFile, 0, len(component.Files))
	seen := map[string]struct{}{}
	for _, definition := range component.Files {
		if !safeRelativePath(definition.Source) {
			return nil, fmt.Errorf("%w: unsafe template source", ErrInvalidConfiguration)
		}
		destination, err := executeTemplate(definition.Destination, data)
		if err != nil {
			return nil, fmt.Errorf("scaffold: render destination: %w", err)
		}
		if !safeRelativePath(destination) {
			return nil, fmt.Errorf("%w: unsafe destination", ErrInvalidConfiguration)
		}
		if _, duplicate := seen[destination]; duplicate {
			return nil, fmt.Errorf("%w: duplicate destination %s", ErrInvalidConfiguration, destination)
		}
		seen[destination] = struct{}{}
		source, err := fs.ReadFile(
			templateFiles,
			filepath.ToSlash(filepath.Join("templates", definition.Source)),
		)
		if err != nil {
			return nil, fmt.Errorf("scaffold: read %s: %w", definition.Source, err)
		}
		content, err := executeTemplate(string(source), data)
		if err != nil {
			return nil, fmt.Errorf("scaffold: render %s: %w", definition.Source, err)
		}
		formatted, err := format.Source([]byte(content))
		if err != nil {
			return nil, fmt.Errorf("scaffold: format %s: %w", destination, err)
		}
		files = append(files, renderedFile{
			path:    filepath.Join(config.Root, filepath.FromSlash(destination)),
			content: formatted,
		})
	}
	return files, nil
}

func executeTemplate(source string, data templateData) (string, error) {
	parsed, err := template.New("scaffold").Option("missingkey=error").Parse(source)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, data); err != nil {
		return "", err
	}
	return output.String(), nil
}

func rollback(committed []committedFile, operations fileOperations) error {
	errorsFound := make([]error, 0)
	for index := len(committed) - 1; index >= 0; index-- {
		file := committed[index]
		if err := operations.remove(file.destination); err != nil && !os.IsNotExist(err) {
			errorsFound = append(errorsFound, fmt.Errorf("remove %s: %w", file.destination, err))
		}
		if file.existed {
			if err := operations.rename(file.backup, file.destination); err != nil {
				errorsFound = append(errorsFound, fmt.Errorf("restore %s: %w", file.destination, err))
			}
		}
	}
	return errors.Join(errorsFound...)
}

func joinRollback(primary, rollbackError error) error {
	if rollbackError == nil {
		return primary
	}
	return errors.Join(primary, fmt.Errorf("scaffold: rollback: %w", rollbackError))
}

func relativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func safeRelativePath(path string) bool {
	clean := filepath.Clean(filepath.FromSlash(path))
	return path != "" && clean != "." && !filepath.IsAbs(clean) &&
		clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func snakeCase(value string) string {
	runes := []rune(value)
	var output strings.Builder
	for index, current := range runes {
		if unicode.IsUpper(current) && index > 0 {
			previous := runes[index-1]
			hasNextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) || hasNextLower {
				output.WriteRune('_')
			}
		}
		output.WriteRune(unicode.ToLower(current))
	}
	return output.String()
}
