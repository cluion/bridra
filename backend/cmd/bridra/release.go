package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	errReleaseInvalid      = errors.New("release: invalid configuration")
	errReleaseInconsistent = errors.New("release: version surfaces are inconsistent")

	frameworkVersionPattern = regexp.MustCompile(
		`(?m)^(const FrameworkVersion = ")[^"]+("\r?)$`,
	)
	pubspecVersionPattern = regexp.MustCompile(`(?m)^(version: )[^\s]+(\s*)$`)
	projectVersionPattern = regexp.MustCompile(
		`(?m)^(\s*"frameworkVersion": ")[^"]+(",\s*)$`,
	)
	rootChangelogPattern = regexp.MustCompile(
		`(?m)^## \[([^\]]+)\] - (Unreleased|[0-9]{4}-[0-9]{2}-[0-9]{2})\r?$`,
	)
	packageChangelogPattern = regexp.MustCompile(
		`(?m)^## ([^\s]+) - (Unreleased|[0-9]{4}-[0-9]{2}-[0-9]{2})\r?$`,
	)
)

type releaseCommand struct{}

type releaseUpdate struct {
	path     string
	original []byte
	updated  []byte
	mode     os.FileMode
}

type releaseSurface struct {
	path    string
	pattern *regexp.Regexp
}

type releaseDocumentation struct {
	path string
}

func newReleaseCommand() releaseCommand {
	return releaseCommand{}
}

func (releaseCommand) name() string {
	return "release"
}

func (releaseCommand) summary() string {
	return "Prepare and verify one aligned framework release version"
}

func (releaseCommand) usage() string {
	return `Usage:
  bridra release prepare <version> [--root path]
  bridra release check [--version version] [--root path] [--final]

Commands:
  prepare  Synchronize managed release surfaces without tagging or publishing
  check    Verify the canonical version, Go, Flutter, metadata, and documentation

The framework, CLI, Go module, and bridra_flutter package share one Semantic
Version. Protocol, Project Template, and project metadata versions change only
when their independent compatibility contracts change.`
}

func (releaseCommand) run(arguments []string, stdout, stderr io.Writer) error {
	if helpRequested(arguments) {
		fmt.Fprintln(stdout, (releaseCommand{}).usage())
		return nil
	}
	if len(arguments) == 0 {
		return fmt.Errorf("%w: expected prepare or check", errReleaseInvalid)
	}

	switch arguments[0] {
	case "prepare":
		options, err := parseReleasePrepareArguments(arguments[1:])
		if err != nil {
			return err
		}
		return prepareRelease(options.root, options.version, stdout)
	case "check":
		options, err := parseReleaseCheckArguments(arguments[1:])
		if err != nil {
			return err
		}
		return checkRelease(options.root, options.version, options.final, stdout)
	default:
		return fmt.Errorf(
			"%w: unknown release command %q",
			errReleaseInvalid,
			arguments[0],
		)
	}
}

type releasePrepareOptions struct {
	root    string
	version string
}

type releaseCheckOptions struct {
	root    string
	version string
	final   bool
}

func parseReleasePrepareArguments(arguments []string) (releasePrepareOptions, error) {
	options := releasePrepareOptions{root: "."}
	positionals, err := parseReleaseOptions(arguments, &options.root, nil, nil)
	if err != nil {
		return releasePrepareOptions{}, err
	}
	if len(positionals) != 1 {
		return releasePrepareOptions{}, fmt.Errorf(
			"%w: usage: bridra release prepare <version> [--root path]",
			errReleaseInvalid,
		)
	}
	options.version = positionals[0]
	return options, nil
}

func parseReleaseCheckArguments(arguments []string) (releaseCheckOptions, error) {
	options := releaseCheckOptions{root: "."}
	positionals, err := parseReleaseOptions(
		arguments,
		&options.root,
		&options.version,
		&options.final,
	)
	if err != nil {
		return releaseCheckOptions{}, err
	}
	if len(positionals) != 0 {
		return releaseCheckOptions{}, fmt.Errorf(
			"%w: usage: bridra release check [--version version] [--root path]",
			errReleaseInvalid,
		)
	}
	return options, nil
}

func parseReleaseOptions(
	arguments []string,
	root *string,
	version *string,
	final *bool,
) ([]string, error) {
	positionals := make([]string, 0, 1)
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch {
		case argument == "--root":
			if index+1 >= len(arguments) {
				return nil, fmt.Errorf("%w: --root requires a path", errReleaseInvalid)
			}
			index++
			*root = arguments[index]
		case strings.HasPrefix(argument, "--root="):
			*root = strings.TrimPrefix(argument, "--root=")
		case argument == "--version" && version != nil:
			if index+1 >= len(arguments) {
				return nil, fmt.Errorf("%w: --version requires a value", errReleaseInvalid)
			}
			index++
			*version = arguments[index]
		case strings.HasPrefix(argument, "--version=") && version != nil:
			*version = strings.TrimPrefix(argument, "--version=")
		case argument == "--final" && final != nil:
			*final = true
		case strings.HasPrefix(argument, "-"):
			return nil, fmt.Errorf("%w: unknown option %q", errReleaseInvalid, argument)
		default:
			positionals = append(positionals, argument)
		}
	}
	return positionals, nil
}

func prepareRelease(root, target string, output io.Writer) error {
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("release: resolve root: %w", err)
	}
	target = strings.TrimSpace(target)
	targetVersion, err := parseSemanticVersion(target)
	if err != nil {
		return fmt.Errorf("%w: %v", errReleaseInvalid, err)
	}
	current, err := readCanonicalReleaseVersion(resolvedRoot)
	if err != nil {
		return err
	}
	currentVersion, err := parseSemanticVersion(current)
	if err != nil {
		return fmt.Errorf("%w: VERSION: %v", errReleaseInvalid, err)
	}
	if compareSemanticVersions(targetVersion, currentVersion) < 0 {
		return fmt.Errorf(
			"%w: target %s is older than current version %s",
			errReleaseInvalid,
			target,
			current,
		)
	}
	if diagnostics := inspectReleaseSurfaces(resolvedRoot, current, false); len(diagnostics) != 0 {
		return releaseDiagnosticsError(current, diagnostics)
	}

	updates, err := buildReleaseUpdates(resolvedRoot, current, target)
	if err != nil {
		return err
	}
	changed := make([]releaseUpdate, 0, len(updates))
	for _, update := range updates {
		if string(update.original) != string(update.updated) {
			changed = append(changed, update)
		}
	}
	if err := writeReleaseUpdates(changed); err != nil {
		return err
	}

	fmt.Fprintf(output, "Bridra Release %s\n", target)
	if len(changed) == 0 {
		fmt.Fprintln(output, "All managed release surfaces were already prepared.")
	} else {
		fmt.Fprintln(output, "Updated:")
		for _, update := range changed {
			relative, relativeErr := filepath.Rel(resolvedRoot, update.path)
			if relativeErr != nil {
				relative = update.path
			}
			fmt.Fprintf(output, "  %s\n", filepath.ToSlash(relative))
		}
	}
	fmt.Fprintf(output, "Tag: backend/v%s\n", target)
	fmt.Fprintln(output, "No tag, package, or release was published.")
	fmt.Fprintln(output, "Next: refresh Flutter dependencies, then run `make release-check`.")
	return nil
}

func checkRelease(root, expected string, final bool, output io.Writer) error {
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("release: resolve root: %w", err)
	}
	canonical, err := readCanonicalReleaseVersion(resolvedRoot)
	if err != nil {
		return err
	}
	if expected == "" {
		expected = canonical
	} else {
		expected = strings.TrimSpace(expected)
		if _, err := parseSemanticVersion(expected); err != nil {
			return fmt.Errorf("%w: %v", errReleaseInvalid, err)
		}
	}
	diagnostics := inspectReleaseSurfaces(resolvedRoot, expected, final)
	if canonical != expected {
		diagnostics = append(
			diagnostics,
			fmt.Sprintf("VERSION is %s, expected %s", canonical, expected),
		)
	}
	if len(diagnostics) != 0 {
		return releaseDiagnosticsError(expected, diagnostics)
	}

	fmt.Fprintln(output, "Bridra Release Check")
	fmt.Fprintf(output, "Version: %s\n", expected)
	fmt.Fprintf(output, "Go tag: backend/v%s\n", expected)
	fmt.Fprintf(output, "Flutter package: bridra_flutter %s\n", expected)
	if final {
		fmt.Fprintln(output, "Changelogs: finalized")
	}
	fmt.Fprintln(output, "All managed release surfaces agree.")
	return nil
}

func releaseDiagnosticsError(version string, diagnostics []string) error {
	return fmt.Errorf(
		"%w for %s:\n  - %s",
		errReleaseInconsistent,
		version,
		strings.Join(diagnostics, "\n  - "),
	)
}

func readCanonicalReleaseVersion(root string) (string, error) {
	path := filepath.Join(root, "VERSION")
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("release: read %s: %w", path, err)
	}
	version := strings.TrimSpace(string(contents))
	if _, err := parseSemanticVersion(version); err != nil {
		return "", fmt.Errorf("%w: VERSION: %v", errReleaseInvalid, err)
	}
	return version, nil
}

func managedReleaseSurfaces() []releaseSurface {
	return []releaseSurface{
		{path: "backend/framework/protocol.go", pattern: frameworkVersionPattern},
		{path: "packages/bridra_flutter/pubspec.yaml", pattern: pubspecVersionPattern},
		{path: "pubspec.yaml", pattern: pubspecVersionPattern},
		{path: ".bridra/project.json", pattern: projectVersionPattern},
	}
}

func managedReleaseDocumentation() []releaseDocumentation {
	return []releaseDocumentation{
		{path: "docs/ARCHITECTURE.md"},
		{path: "docs/GUIDE.md"},
		{path: "README.md"},
		{path: "docs/RELEASING.md"},
		{path: "docs/UPGRADING.md"},
	}
}

func inspectReleaseSurfaces(root, expected string, final bool) []string {
	diagnostics := make([]string, 0)
	for _, surface := range managedReleaseSurfaces() {
		path := filepath.Join(root, filepath.FromSlash(surface.path))
		contents, err := os.ReadFile(path)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: %v", surface.path, err))
			continue
		}
		matches := surface.pattern.FindSubmatch(contents)
		if matches == nil {
			diagnostics = append(diagnostics, surface.path+": version field not found")
			continue
		}
		value := managedSurfaceValue(matches)
		if value != expected {
			diagnostics = append(
				diagnostics,
				fmt.Sprintf("%s is %s, expected %s", surface.path, value, expected),
			)
		}
	}

	for _, changelog := range []struct {
		path    string
		pattern *regexp.Regexp
	}{
		{path: "CHANGELOG.md", pattern: rootChangelogPattern},
		{path: "packages/bridra_flutter/CHANGELOG.md", pattern: packageChangelogPattern},
	} {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(changelog.path)))
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: %v", changelog.path, err))
			continue
		}
		matches := changelog.pattern.FindSubmatch(contents)
		if matches == nil {
			diagnostics = append(diagnostics, changelog.path+": release heading not found")
		} else if string(matches[1]) != expected {
			diagnostics = append(
				diagnostics,
				fmt.Sprintf(
					"%s latest release is %s, expected %s",
					changelog.path,
					matches[1],
					expected,
				),
			)
		} else if final && string(matches[2]) == "Unreleased" {
			diagnostics = append(
				diagnostics,
				changelog.path+": release is still marked Unreleased",
			)
		}
	}

	for _, document := range managedReleaseDocumentation() {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(document.path)))
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: %v", document.path, err))
			continue
		}
		if !strings.Contains(string(contents), expected) {
			diagnostics = append(
				diagnostics,
				fmt.Sprintf("%s does not reference %s", document.path, expected),
			)
		}
	}

	packagePubspec, err := os.ReadFile(
		filepath.Join(root, "packages", "bridra_flutter", "pubspec.yaml"),
	)
	if err == nil && !strings.Contains(
		strings.ReplaceAll(string(packagePubspec), "\r\n", "\n"),
		"name: bridra_flutter\n",
	) {
		diagnostics = append(
			diagnostics,
			"packages/bridra_flutter/pubspec.yaml has an unexpected package name",
		)
	}
	goModule, err := os.ReadFile(filepath.Join(root, "backend", "go.mod"))
	if err == nil && !strings.Contains(
		strings.ReplaceAll(string(goModule), "\r\n", "\n"),
		"module github.com/cluion/bridra/backend\n",
	) {
		diagnostics = append(diagnostics, "backend/go.mod has an unexpected module identity")
	}
	return diagnostics
}

func managedSurfaceValue(matches [][]byte) string {
	if len(matches) < 3 {
		return ""
	}
	full := string(matches[0])
	prefix := string(matches[1])
	suffix := string(matches[2])
	return strings.TrimSuffix(strings.TrimPrefix(full, prefix), suffix)
}

func buildReleaseUpdates(root, current, target string) ([]releaseUpdate, error) {
	paths := []string{"VERSION"}
	for _, surface := range managedReleaseSurfaces() {
		paths = append(paths, surface.path)
	}
	paths = append(paths, "CHANGELOG.md", "packages/bridra_flutter/CHANGELOG.md")
	for _, document := range managedReleaseDocumentation() {
		paths = append(paths, document.path)
	}

	updates := make([]releaseUpdate, 0, len(paths))
	for _, relative := range paths {
		path := filepath.Join(root, filepath.FromSlash(relative))
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("release: read %s: %w", relative, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("release: stat %s: %w", relative, err)
		}
		updated := append([]byte(nil), contents...)
		switch relative {
		case "VERSION":
			updated = []byte(target + "\n")
		case "CHANGELOG.md":
			updated, err = updateReleaseChangelog(
				contents,
				rootChangelogPattern,
				current,
				target,
				true,
			)
		case "packages/bridra_flutter/CHANGELOG.md":
			updated, err = updateReleaseChangelog(
				contents,
				packageChangelogPattern,
				current,
				target,
				false,
			)
		default:
			if surface := findReleaseSurface(relative); surface != nil {
				updated, err = updateReleaseSurface(contents, surface.pattern, current, target)
			} else {
				updated = []byte(strings.ReplaceAll(string(contents), current, target))
			}
		}
		if err != nil {
			return nil, fmt.Errorf("release: update %s: %w", relative, err)
		}
		updates = append(updates, releaseUpdate{
			path: path, original: contents, updated: updated, mode: info.Mode(),
		})
	}
	return updates, nil
}

func findReleaseSurface(path string) *releaseSurface {
	for _, surface := range managedReleaseSurfaces() {
		if surface.path == path {
			copy := surface
			return &copy
		}
	}
	return nil
}

func updateReleaseSurface(
	contents []byte,
	pattern *regexp.Regexp,
	current,
	target string,
) ([]byte, error) {
	matches := pattern.FindSubmatch(contents)
	if matches == nil {
		return nil, errors.New("version field not found")
	}
	if value := managedSurfaceValue(matches); value != current {
		return nil, fmt.Errorf("version is %s, expected %s", value, current)
	}
	return pattern.ReplaceAll(contents, []byte("${1}"+target+"${2}")), nil
}

func updateReleaseChangelog(
	contents []byte,
	pattern *regexp.Regexp,
	current,
	target string,
	bracketed bool,
) ([]byte, error) {
	matches := pattern.FindSubmatch(contents)
	if matches == nil {
		return nil, errors.New("release heading not found")
	}
	if value := string(matches[1]); value != current {
		return nil, fmt.Errorf("latest release is %s, expected %s", value, current)
	}
	if current == target {
		return append([]byte(nil), contents...), nil
	}
	if string(matches[2]) == "Unreleased" {
		return nil, fmt.Errorf(
			"%s is still Unreleased; finalize it before preparing %s",
			current,
			target,
		)
	}
	heading := "## " + target + " - Unreleased\n\n"
	if bracketed {
		heading = "## [" + target + "] - Unreleased\n\n"
	}
	start := pattern.FindIndex(contents)
	if start == nil {
		return nil, errors.New("release heading not found")
	}
	updated := make([]byte, 0, len(contents)+len(heading))
	updated = append(updated, contents[:start[0]]...)
	updated = append(updated, heading...)
	updated = append(updated, contents[start[0]:]...)
	return updated, nil
}

func writeReleaseUpdates(updates []releaseUpdate) error {
	written := make([]releaseUpdate, 0, len(updates))
	for _, update := range updates {
		if err := os.WriteFile(update.path, update.updated, update.mode.Perm()); err != nil {
			for index := len(written) - 1; index >= 0; index-- {
				_ = os.WriteFile(
					written[index].path,
					written[index].original,
					written[index].mode.Perm(),
				)
			}
			return fmt.Errorf("release: write %s: %w", update.path, err)
		}
		written = append(written, update)
	}
	return nil
}
