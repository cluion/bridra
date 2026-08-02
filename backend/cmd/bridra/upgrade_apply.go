package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

const upgradeCommandTimeout = 30 * time.Minute

type upgradeSystem struct {
	timeout time.Duration
	run     func(context.Context, upgradeProcess) error
}

type upgradeProcess struct {
	Label       string
	Name        string
	Arguments   []string
	Directory   string
	Environment []string
	Output      io.Writer
}

type managedUpgradeFile struct {
	path     string
	required bool
	existed  bool
	mode     os.FileMode
	contents []byte
}

func defaultUpgradeSystem() upgradeSystem {
	return upgradeSystem{
		timeout: upgradeCommandTimeout,
		run: func(ctx context.Context, process upgradeProcess) error {
			command := exec.CommandContext(ctx, process.Name, process.Arguments...)
			command.Dir = process.Directory
			command.Env = append(os.Environ(), process.Environment...)
			command.Stdout = process.Output
			command.Stderr = process.Output
			return command.Run()
		},
	}
}

func (item upgradeCommand) applyUpgrade(
	root string,
	metadata projectMetadata,
	target upgradeTarget,
	report upgradeReport,
	progress io.Writer,
) (upgradeReport, error) {
	if report.Status == upgradeCurrent {
		return report, nil
	}
	if report.Status == upgradeUnsupported || !report.PlanAvailable {
		return report, upgradeReportError(report)
	}
	if !report.ApplyAvailable {
		report.Diagnostics = append(report.Diagnostics, upgradeDiagnostic{
			Level: "migration", Code: "manual_steps_required",
			Message: "The complete upgrade plan contains one or more manual steps.",
			Action:  "Complete the reported manual steps first; --apply does not guess or rewrite application-owned code.",
		})
		return report, fmt.Errorf(
			"%w: the upgrade plan contains manual steps; no files were changed",
			errUpgradeRequired,
		)
	}

	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return failUpgradeApply(report, false, false, fmt.Errorf("resolve project root: %w", err))
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	snapshots, err := snapshotUpgradeFiles(absoluteRoot)
	if err != nil {
		return failUpgradeApply(report, false, false, err)
	}
	updates, err := prepareUpgradeFiles(absoluteRoot, snapshots, metadata, target)
	if err != nil {
		return failUpgradeApply(report, false, false, err)
	}

	fmt.Fprintf(progress, "Applying Bridra %s to %s\n", metadata.FrameworkVersion, target.FrameworkVersion)
	wroteFiles := false
	paths := make([]string, 0, len(updates))
	for path := range updates {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		contents := updates[path]
		snapshot := snapshots[path]
		if err := atomicWriteUpgradeFile(path, contents, snapshot.mode); err != nil {
			return rollbackUpgradeFailure(report, snapshots, wroteFiles, fmt.Errorf(
				"write %s: %w",
				filepath.Base(path),
				err,
			))
		}
		wroteFiles = true
	}

	processes := []upgradeProcess{
		{
			Label:       "Resolve Go dependencies",
			Name:        "go",
			Arguments:   []string{"mod", "tidy"},
			Directory:   filepath.Join(absoluteRoot, "backend"),
			Environment: []string{"GOWORK=off"},
			Output:      progress,
		},
		{
			Label:     "Resolve Flutter dependencies",
			Name:      "fvm",
			Arguments: []string{"flutter", "pub", "get"},
			Directory: absoluteRoot,
			Output:    progress,
		},
		{
			Label:     "Verify upgraded project",
			Name:      "make",
			Arguments: []string{"verify"},
			Directory: absoluteRoot,
			Output:    progress,
		},
	}
	for _, process := range processes {
		fmt.Fprintf(progress, "%s...\n", process.Label)
		timeout := item.system.timeout
		if timeout <= 0 {
			timeout = upgradeCommandTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		runErr := item.system.run(ctx, process)
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancel()
		if runErr == nil {
			continue
		}
		if timedOut {
			runErr = fmt.Errorf("timed out after %s: %w", timeout, runErr)
		}
		return rollbackUpgradeFailure(
			report,
			snapshots,
			true,
			fmt.Errorf("%s: %w", process.Label, runErr),
		)
	}

	report.Status = upgradeApplied
	report.ReadOnly = false
	report.Applied = true
	report.MigrationRequired = false
	report.Diagnostics = append(report.Diagnostics, upgradeDiagnostic{
		Level: "ok", Code: "upgrade_applied",
		Message: fmt.Sprintf(
			"Updated the managed Go, Flutter, lockfile, and project metadata surfaces to Bridra %s and completed full verification.",
			target.FrameworkVersion,
		),
	})
	return report, nil
}

func failUpgradeApply(
	report upgradeReport,
	wroteFiles bool,
	rolledBack bool,
	cause error,
) (upgradeReport, error) {
	report.Status = upgradeApplyFailed
	report.ReadOnly = !wroteFiles
	report.RolledBack = rolledBack
	report.Diagnostics = append(report.Diagnostics, upgradeDiagnostic{
		Level: "error", Code: "upgrade_apply_failed",
		Message: cause.Error(),
		Action:  "Fix the reported cause and run the read-only plan again before retrying --apply.",
	})
	return report, fmt.Errorf("%w: %w", errUpgradeApply, cause)
}

func rollbackUpgradeFailure(
	report upgradeReport,
	snapshots map[string]managedUpgradeFile,
	wroteFiles bool,
	cause error,
) (upgradeReport, error) {
	if !wroteFiles {
		return failUpgradeApply(report, false, false, cause)
	}
	rollbackErr := restoreUpgradeFiles(snapshots)
	if rollbackErr != nil {
		joined := errors.Join(cause, fmt.Errorf("rollback managed files: %w", rollbackErr))
		return failUpgradeApply(report, true, false, joined)
	}
	return failUpgradeApply(report, true, true, cause)
}

func snapshotUpgradeFiles(root string) (map[string]managedUpgradeFile, error) {
	files := []managedUpgradeFile{
		{path: filepath.Join(root, "backend", "go.mod"), required: true},
		{path: filepath.Join(root, "backend", "go.sum")},
		{path: filepath.Join(root, "pubspec.yaml"), required: true},
		{path: filepath.Join(root, "pubspec.lock")},
		{path: filepath.Join(root, ".bridra", "project.json"), required: true},
	}
	snapshots := make(map[string]managedUpgradeFile, len(files))
	for _, file := range files {
		information, err := os.Lstat(file.path)
		if err != nil {
			if os.IsNotExist(err) && !file.required {
				snapshots[file.path] = file
				continue
			}
			return nil, fmt.Errorf("snapshot %s: %w", file.path, err)
		}
		if !information.Mode().IsRegular() {
			return nil, fmt.Errorf("snapshot %s: not a regular file", file.path)
		}
		contents, err := os.ReadFile(file.path)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", file.path, err)
		}
		file.existed = true
		file.mode = information.Mode().Perm()
		file.contents = contents
		snapshots[file.path] = file
	}
	return snapshots, nil
}

func prepareUpgradeFiles(
	root string,
	snapshots map[string]managedUpgradeFile,
	metadata projectMetadata,
	target upgradeTarget,
) (map[string][]byte, error) {
	goModPath := filepath.Join(root, "backend", "go.mod")
	pubspecPath := filepath.Join(root, "pubspec.yaml")
	metadataPath := filepath.Join(root, ".bridra", "project.json")

	goMod, err := updateGoModRequirement(
		snapshots[goModPath].contents,
		metadata.FrameworkModule,
		"v"+metadata.FrameworkVersion,
		"v"+target.FrameworkVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("prepare backend/go.mod: %w", err)
	}
	pubspec, err := updatePubspecDependency(
		snapshots[pubspecPath].contents,
		releaseinfo.FlutterPackage,
		"^"+metadata.FrameworkVersion,
		"^"+target.FrameworkVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("prepare pubspec.yaml: %w", err)
	}
	updatedMetadata := metadata
	updatedMetadata.SchemaVersion = target.ProjectMetadataVersion
	updatedMetadata.FrameworkVersion = target.FrameworkVersion
	updatedMetadata.TemplateVersion = target.TemplateVersion
	metadataJSON, err := json.MarshalIndent(updatedMetadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode .bridra/project.json: %w", err)
	}
	metadataJSON = append(metadataJSON, '\n')
	return map[string][]byte{
		goModPath:    goMod,
		pubspecPath:  pubspec,
		metadataPath: metadataJSON,
	}, nil
}

func updateGoModRequirement(
	contents []byte,
	module string,
	expectedVersion string,
	targetVersion string,
) ([]byte, error) {
	lines := splitLines(contents)
	inRequireBlock := false
	matches := 0
	for index, line := range lines {
		code := strings.TrimSpace(strings.SplitN(string(trimLineEnding(line)), "//", 2)[0])
		fields := strings.Fields(code)
		if len(fields) == 0 {
			continue
		}
		if inRequireBlock {
			if fields[0] == ")" {
				inRequireBlock = false
				continue
			}
			if fields[0] != module || len(fields) < 2 {
				continue
			}
			updated, err := replaceManifestToken(line, module, fields[1], expectedVersion, targetVersion)
			if err != nil {
				return nil, err
			}
			lines[index] = updated
			matches++
			continue
		}
		if fields[0] != "require" {
			continue
		}
		if len(fields) == 2 && fields[1] == "(" {
			inRequireBlock = true
			continue
		}
		if len(fields) < 3 || fields[1] != module {
			continue
		}
		updated, err := replaceManifestToken(line, module, fields[2], expectedVersion, targetVersion)
		if err != nil {
			return nil, err
		}
		lines[index] = updated
		matches++
	}
	if matches != 1 {
		return nil, fmt.Errorf(
			"expected exactly one require for %s, found %d",
			module,
			matches,
		)
	}
	return bytes.Join(lines, nil), nil
}

func replaceManifestToken(
	line []byte,
	anchor string,
	actual string,
	expected string,
	target string,
) ([]byte, error) {
	if actual != expected {
		return nil, fmt.Errorf(
			"%s declares %s, but project metadata records %s",
			anchor,
			actual,
			expected,
		)
	}
	start := bytes.Index(line, []byte(anchor))
	if start < 0 {
		return nil, fmt.Errorf("cannot locate %s in manifest line", anchor)
	}
	start += len(anchor)
	for start < len(line) && (line[start] == ' ' || line[start] == '\t') {
		start++
	}
	end := start
	for end < len(line) && line[end] != ' ' && line[end] != '\t' &&
		line[end] != '\r' && line[end] != '\n' {
		end++
	}
	updated := make([]byte, 0, len(line)-len(actual)+len(target))
	updated = append(updated, line[:start]...)
	updated = append(updated, target...)
	updated = append(updated, line[end:]...)
	return updated, nil
}

func updatePubspecDependency(
	contents []byte,
	packageName string,
	expectedConstraint string,
	targetConstraint string,
) ([]byte, error) {
	lines := splitLines(contents)
	section := ""
	matches := 0
	for index, line := range lines {
		plain := trimLineEnding(line)
		trimmed := strings.TrimSpace(string(plain))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(plain) > 0 && plain[0] != ' ' && plain[0] != '\t' {
			name, _, found := strings.Cut(trimmed, ":")
			if found {
				section = strings.TrimSpace(name)
			}
			continue
		}
		if section != "dependencies" {
			continue
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found || strings.TrimSpace(key) != packageName {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s must use a scalar version constraint", packageName)
		}
		constraint, suffix := splitYAMLComment(value)
		quote := ""
		if len(constraint) >= 2 &&
			((constraint[0] == '\'' && constraint[len(constraint)-1] == '\'') ||
				(constraint[0] == '"' && constraint[len(constraint)-1] == '"')) {
			quote = constraint[:1]
			constraint = constraint[1 : len(constraint)-1]
		}
		if constraint != expectedConstraint {
			return nil, fmt.Errorf(
				"%s declares %s, but project metadata records %s",
				packageName,
				constraint,
				expectedConstraint,
			)
		}
		colon := bytes.Index(plain, []byte(":"))
		prefix := plain[:colon+1]
		ending := line[len(plain):]
		replacement := " " + quote + targetConstraint + quote
		if suffix != "" {
			replacement += " " + suffix
		}
		lines[index] = append(append(append([]byte{}, prefix...), replacement...), ending...)
		matches++
	}
	if matches != 1 {
		return nil, fmt.Errorf(
			"expected exactly one %s entry under dependencies, found %d",
			packageName,
			matches,
		)
	}
	return bytes.Join(lines, nil), nil
}

func splitYAMLComment(value string) (string, string) {
	for index, character := range value {
		if character == '#' && (index == 0 || value[index-1] == ' ' || value[index-1] == '\t') {
			return strings.TrimSpace(value[:index]), strings.TrimSpace(value[index:])
		}
	}
	return strings.TrimSpace(value), ""
}

func splitLines(contents []byte) [][]byte {
	if len(contents) == 0 {
		return [][]byte{}
	}
	var lines [][]byte
	for len(contents) > 0 {
		index := bytes.IndexByte(contents, '\n')
		if index < 0 {
			lines = append(lines, append([]byte(nil), contents...))
			break
		}
		lines = append(lines, append([]byte(nil), contents[:index+1]...))
		contents = contents[index+1:]
	}
	return lines
}

func trimLineEnding(line []byte) []byte {
	return bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
}

func restoreUpgradeFiles(snapshots map[string]managedUpgradeFile) error {
	var restoreErr error
	for path, snapshot := range snapshots {
		if !snapshot.existed {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("remove %s: %w", path, err))
			}
			continue
		}
		if err := atomicWriteUpgradeFile(path, snapshot.contents, snapshot.mode); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore %s: %w", path, err))
		}
	}
	return restoreErr
}

func atomicWriteUpgradeFile(path string, contents []byte, mode os.FileMode) (resultErr error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".bridra-upgrade-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if removeErr := os.Remove(temporaryPath); removeErr != nil &&
			!os.IsNotExist(removeErr) && resultErr == nil {
			resultErr = removeErr
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}
