package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
	pendingChangelogPattern = regexp.MustCompile(`(?m)^## (?:\[Unreleased\]|Unreleased)\r?$`)
	versionedPolicyPattern  = regexp.MustCompile(
		`(?m)^(?:Bridra [0-9]+\.[0-9]+\b|\|[^\r\n]*[0-9]+\.[0-9]+)`,
	)
	supportHeadingPattern = regexp.MustCompile(`(?m)^### Support\r?$`)
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
  bridra release prepare <version> [--date YYYY-MM-DD] [--root path]
  bridra release check [--version version] [--root path] [--final]

Commands:
  prepare  Synchronize managed release surfaces without tagging or publishing
  check    Verify the canonical version, Go, Flutter, metadata, and changelogs

The framework, CLI, Go module, and bridra_flutter package share one Semantic
Version. Protocol, Project Template, and project metadata versions change only
when their independent compatibility contracts change. An explicit --date
synchronizes only the target release's two changelog headings; omitted dates
retain the Unreleased workflow. No date is inferred from the system clock.`
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
		return prepareReleaseWithDate(options.root, options.version, options.date, stdout)
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
	date    string
}

type releaseCheckOptions struct {
	root    string
	version string
	final   bool
}

func parseReleasePrepareArguments(arguments []string) (releasePrepareOptions, error) {
	options := releasePrepareOptions{root: "."}
	positionals, err := parseReleaseOptions(arguments, &options.root, nil, nil, &options.date)
	if err != nil {
		return releasePrepareOptions{}, err
	}
	if len(positionals) != 1 {
		return releasePrepareOptions{}, fmt.Errorf(
			"%w: usage: bridra release prepare <version> [--date YYYY-MM-DD] [--root path]",
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
		nil,
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
	date *string,
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
		case (argument == "--date" || strings.HasPrefix(argument, "--date=")) && date != nil:
			if *date != "" {
				return nil, fmt.Errorf("%w: --date may be supplied only once", errReleaseInvalid)
			}
			if argument == "--date" {
				if index+1 >= len(arguments) {
					return nil, fmt.Errorf("%w: --date requires YYYY-MM-DD", errReleaseInvalid)
				}
				index++
				*date = arguments[index]
			} else {
				*date = strings.TrimPrefix(argument, "--date=")
			}
			if err := validateReleaseDate(*date); err != nil {
				return nil, err
			}
		case strings.HasPrefix(argument, "-"):
			return nil, fmt.Errorf("%w: unknown option %q", errReleaseInvalid, argument)
		default:
			positionals = append(positionals, argument)
		}
	}
	return positionals, nil
}

func prepareRelease(root, target string, output io.Writer) error {
	return prepareReleaseWithDate(root, target, "", output)
}

func validateReleaseDate(value string) error {
	date, err := time.Parse(time.DateOnly, value)
	if err != nil || date.Year() < 1 || date.Format(time.DateOnly) != value {
		return fmt.Errorf("%w: release date must be a valid calendar date in YYYY-MM-DD format", errReleaseInvalid)
	}
	return nil
}

func prepareReleaseWithDate(root, target, date string, output io.Writer) error {
	if date != "" {
		if err := validateReleaseDate(date); err != nil {
			return err
		}
	}
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

	updates, err := buildReleaseUpdates(resolvedRoot, current, target, date)
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
	if date != "" {
		fmt.Fprintf(output, "Release date: %s\n", date)
	}
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
		fmt.Fprintln(output, "Support policy: latest patch of the latest published minor line")
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

	var releaseDate string
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
		if final && pendingChangelogPattern.Match(contents) {
			diagnostics = append(diagnostics, changelog.path+": pending Unreleased section must be prepared")
		}
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
		} else if final {
			date := string(matches[2])
			if err := validateReleaseDate(date); err != nil {
				diagnostics = append(diagnostics, changelog.path+": release date must be a valid calendar date")
			} else if releaseDate != "" && date != releaseDate {
				diagnostics = append(diagnostics, "both changelogs must use the same release date")
			} else {
				releaseDate = date
			}
		}
	}

	if final {
		diagnostics = append(diagnostics, inspectFinalReleasePolicy(root, expected)...)
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

func inspectFinalReleasePolicy(root, expected string) []string {
	diagnostics := make([]string, 0)
	for _, path := range []string{"SUPPORT.md", "SECURITY.md"} {
		contents, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		diagnostics = append(diagnostics, inspectReleasePolicyDocument(path, contents)...)
	}
	changelogContents, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err == nil {
		section := currentReleaseChangelogSection(changelogContents)
		if section != nil && !supportHeadingPattern.Match(section) {
			diagnostics = append(diagnostics, fmt.Sprintf("CHANGELOG.md: release %s has no Support section", expected))
		}
	}
	return diagnostics
}

func inspectReleasePolicyDocument(path string, contents []byte) []string {
	// Preparing a candidate must not make it supported before publication. Check
	// these stable policy claims instead of rewriting its minor number into docs.
	rules := []struct{ name, text string }{
		{"eligibility rule", "Before 1.0, only the latest patch of the latest published minor line is eligible for security fixes."},
		{"publication boundary", "A new minor line's support policy takes effect at publication; the previously published line remains supported until then."},
		{"published release link", "https://github.com/cluion/bridra/releases"},
		{"development exclusion", "development branches and unreleased candidates are not supported releases."},
	}
	if path == "SECURITY.md" {
		rules = append(rules, []struct{ name, text string }{
			{"supported versions table", "| Latest patch of the latest published minor line | Security fixes accepted on a best-effort basis |"},
			{"unsupported versions table", "| Older minor lines and superseded patches | Unsupported; upgrade to the latest supported patch |"},
			{"development versions table", "| `main` | Active development only |"},
		}...)
	}
	normalized := strings.Join(strings.Fields(string(contents)), " ")
	diagnostics := make([]string, 0)
	for _, rule := range rules {
		if !strings.Contains(normalized, rule.text) {
			diagnostics = append(diagnostics, path+": "+rule.name+" is missing or inconsistent")
		}
	}
	if versionedPolicyPattern.Match(contents) {
		diagnostics = append(diagnostics, path+": fixed release versions are not allowed in support policy")
	}
	return diagnostics
}

func currentReleaseChangelogSection(contents []byte) []byte {
	heading := rootChangelogPattern.FindIndex(contents)
	if heading == nil {
		return nil
	}
	section := contents[heading[1]:]
	if next := rootChangelogPattern.FindIndex(section); next != nil {
		section = section[:next[0]]
	}
	return section
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

func buildReleaseUpdates(root, current, target, date string) ([]releaseUpdate, error) {
	paths := []string{"VERSION"}
	for _, surface := range managedReleaseSurfaces() {
		paths = append(paths, surface.path)
	}
	paths = append(paths, "CHANGELOG.md", "packages/bridra_flutter/CHANGELOG.md")
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
			surface := findReleaseSurface(relative)
			if surface == nil {
				return nil, fmt.Errorf("release: unknown version surface %s", relative)
			}
			updated, err = updateReleaseSurface(contents, surface.pattern, current, target)
		}
		if err == nil && date != "" {
			switch relative {
			case "CHANGELOG.md":
				updated, err = updateReleaseChangelogDate(updated, rootChangelogPattern, target, date)
			case "packages/bridra_flutter/CHANGELOG.md":
				updated, err = updateReleaseChangelogDate(updated, packageChangelogPattern, target, date)
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
	heading := "## " + target + " - Unreleased"
	if bracketed {
		heading = "## [" + target + "] - Unreleased"
	}
	if pending := pendingChangelogPattern.FindAllIndex(contents, -1); len(pending) > 0 {
		if len(pending) != 1 || pending[0][0] > pattern.FindIndex(contents)[0] {
			return nil, errors.New("expected one leading Unreleased section")
		}
		// Promote the pending notes without changing a published release section.
		start, end := pending[0][0], pending[0][1]
		replacement := heading
		if end > start && contents[end-1] == '\r' {
			replacement += "\r"
		}
		updated := append([]byte(nil), contents[:start]...)
		updated = append(updated, replacement...)
		return append(updated, contents[end:]...), nil
	}
	start := pattern.FindIndex(contents)
	if start == nil {
		return nil, errors.New("release heading not found")
	}
	updated := make([]byte, 0, len(contents)+len(heading))
	eol := "\n"
	if strings.Contains(string(contents), "\r\n") {
		eol = "\r\n"
	}
	heading += eol + eol
	updated = append(updated, contents[:start[0]]...)
	updated = append(updated, heading...)
	updated = append(updated, contents[start[0]:]...)
	return updated, nil
}

func updateReleaseChangelogDate(contents []byte, pattern *regexp.Regexp, target, date string) ([]byte, error) {
	if pendingChangelogPattern.Match(contents) {
		return nil, errors.New("pending Unreleased section must be prepared before setting a release date")
	}
	matches := pattern.FindAllSubmatchIndex(contents, -1)
	if len(matches) == 0 || string(contents[matches[0][2]:matches[0][3]]) != target {
		return nil, errors.New("target release heading not found")
	}
	for _, match := range matches[1:] {
		if string(contents[match[2]:match[3]]) == target {
			return nil, errors.New("duplicate target release heading")
		}
	}
	// Replace only the current heading's date, never dates in notes or history.
	match := matches[0]
	updated := append([]byte(nil), contents[:match[4]]...)
	updated = append(updated, date...)
	return append(updated, contents[match[5]:]...), nil
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
