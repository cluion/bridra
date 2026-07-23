package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

func validateBuildArtifact(
	stat func(string) (os.FileInfo, error),
	path string,
	requireFile bool,
) error {
	information, err := stat(path)
	if err != nil {
		return fmt.Errorf("%w: %s is unavailable: %w", errBuildArtifact, path, err)
	}
	if requireFile && !information.Mode().IsRegular() {
		return fmt.Errorf("%w: %s must be a regular file", errBuildArtifact, path)
	}
	if !requireFile && !information.IsDir() {
		return fmt.Errorf("%w: %s must be a directory", errBuildArtifact, path)
	}
	return nil
}

func (item buildCommand) writeManifest(options buildOptions, manifest buildManifest) (string, error) {
	directory := filepath.Join(options.root, "build", "bridra")
	if err := item.system.mkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("build: create manifest directory: %w", err)
	}
	contents, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("build: encode manifest: %w", err)
	}
	contents = append(contents, '\n')
	path := filepath.Join(directory, string(options.target)+"-"+string(options.mode)+".json")
	if err := item.system.writeFile(path, contents, 0o644); err != nil {
		return "", fmt.Errorf("build: write manifest: %w", err)
	}
	return path, nil
}

func relativeBuildPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func copyBuildFile(source, destination string, mode os.FileMode) (resultError error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := input.Close(); closeErr != nil {
			resultError = errors.Join(resultError, closeErr)
		}
	}()
	information, err := input.Stat()
	if err != nil {
		return err
	}
	if !information.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file")
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := output.Close(); closeErr != nil {
			resultError = errors.Join(resultError, closeErr)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return output.Chmod(mode)
}

func artifactSHA256(path string) (string, error) {
	information, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if information.Mode().IsRegular() {
		return regularFileSHA256(path)
	}
	if !information.IsDir() {
		return "", fmt.Errorf("artifact must be a regular file or directory")
	}

	var paths []string
	if err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == path {
			return nil
		}
		relative, err := filepath.Rel(path, current)
		if err != nil {
			return err
		}
		paths = append(paths, relative)
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(paths)

	digest := sha256.New()
	for _, relative := range paths {
		current := filepath.Join(path, relative)
		entry, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		slashPath := filepath.ToSlash(relative)
		switch {
		case entry.IsDir():
			fmt.Fprintf(digest, "directory\x00%s\x00", slashPath)
		case entry.Mode().IsRegular():
			fmt.Fprintf(digest, "file\x00%s\x00%d\x00", slashPath, entry.Size())
			file, openErr := os.Open(current)
			if openErr != nil {
				return "", openErr
			}
			_, copyErr := io.Copy(digest, file)
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
			digest.Write([]byte{0})
		case entry.Mode()&os.ModeSymlink != 0:
			target, readErr := os.Readlink(current)
			if readErr != nil {
				return "", readErr
			}
			fmt.Fprintf(digest, "symlink\x00%s\x00%d\x00%s\x00", slashPath, len(target), target)
		default:
			return "", fmt.Errorf("unsupported artifact entry %s", current)
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func regularFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
