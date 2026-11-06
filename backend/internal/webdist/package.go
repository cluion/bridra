// Package webdist snapshots a compiled static UI for packaging across targets.
package webdist

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const OutputDirectory = "build/bridra/web"

type manifest struct {
	SchemaVersion  int               `json:"schemaVersion"`
	Artifact       string            `json:"artifact"`
	ArtifactSHA256 string            `json:"artifactSha256"`
	Files          map[string]string `json:"files"`
}

// Package publishes a complete dist and its manifest together and returns the tree digest.
func Package(projectDirectory, dist string) (string, error) {
	sourcePath := strings.ToLower(dist) + "/"
	if strings.HasPrefix(OutputDirectory+"/", sourcePath) || strings.HasPrefix(sourcePath, OutputDirectory+"/") {
		return "", fmt.Errorf("dist %q overlaps the package output", dist)
	}
	project, err := os.OpenRoot(projectDirectory)
	if err != nil {
		return "", err
	}
	defer project.Close()
	source, err := openDirectory(project, dist, false)
	if err != nil {
		return "", fmt.Errorf("open %s (run npm ci && npm run build in frontend/): %w", dist, err)
	}
	defer source.Close()
	index, err := source.Lstat("index.html")
	if err != nil || !index.Mode().IsRegular() {
		return "", fmt.Errorf("%s/index.html must be a regular file; run npm ci && npm run build in frontend/", dist)
	}

	output, err := openDirectory(project, OutputDirectory, true)
	if err != nil {
		return "", err
	}
	defer output.Close()
	stagingName := ".staging-" + rand.Text()
	if err := output.Mkdir(stagingName, 0o755); err != nil {
		return "", err
	}
	defer output.RemoveAll(stagingName)
	staging, err := output.OpenRoot(stagingName)
	if err != nil {
		return "", err
	}
	files, err := stage(source, staging)
	closeErr := staging.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return "", err
	}
	if _, ok := files["index.html"]; !ok {
		return "", fmt.Errorf("%s/index.html disappeared while packaging", dist)
	}
	digest := treeSHA256(files)
	data, err := json.MarshalIndent(manifest{
		SchemaVersion: 1, Artifact: "dist", ArtifactSHA256: digest, Files: files,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := output.WriteFile(filepath.Join(stagingName, "manifest.json"), data, 0o644); err != nil {
		return "", err
	}
	if err := output.Rename(stagingName, digest); err != nil {
		// A previous or concurrent build may have published the same inputs.
		if _, statErr := output.Lstat(digest); statErr != nil {
			return "", err
		}
		if err := verifyExisting(output, digest, data, files); err != nil {
			return "", fmt.Errorf("verify existing package %s: %w", digest, err)
		}
	}
	return digest, nil
}

func verifyExisting(parent *os.Root, digest string, expected []byte, files map[string]string) error {
	packageRoot, err := openDirectory(parent, digest, false)
	if err != nil {
		return err
	}
	defer packageRoot.Close()
	info, err := packageRoot.Lstat("manifest.json")
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(expected)) {
		return errors.New("invalid manifest")
	}
	actual, err := packageRoot.ReadFile("manifest.json")
	if err != nil || !bytes.Equal(actual, expected) {
		return errors.New("manifest differs")
	}
	dist, err := openDirectory(packageRoot, "dist", false)
	if err != nil {
		return err
	}
	defer dist.Close()
	hashes, err := scan(dist, nil)
	if err != nil {
		return err
	}
	if !maps.Equal(files, hashes) {
		return errors.New("published dist files differ")
	}
	return nil
}

func openDirectory(root *os.Root, relative string, create bool) (*os.Root, error) {
	if !fs.ValidPath(relative) || relative == "." || strings.Contains(relative, `\`) {
		return nil, fmt.Errorf("invalid directory path %q", relative)
	}
	current := ""
	for _, part := range strings.Split(relative, "/") {
		current = filepath.Join(current, part)
		if create {
			if err := root.Mkdir(current, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return nil, err
			}
		}
		info, err := root.Lstat(current)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s must be a directory (symlinks are unsupported)", current)
		}
	}
	return root.OpenRoot(current)
}

func stage(source, staging *os.Root) (map[string]string, error) {
	destination, err := openDirectory(staging, "dist", true)
	if err != nil {
		return nil, err
	}
	defer destination.Close()
	return scan(source, destination)
}

func scan(source, destination *os.Root) (map[string]string, error) {
	files := make(map[string]string)
	err := fs.WalkDir(source.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || name == "." {
			return err
		}
		relative, err := filepath.Localize(name)
		if err != nil || strings.Contains(name, `\`) {
			return fmt.Errorf("invalid dist path %q", name)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported dist entry %s (regular files and directories only)", name)
		}
		hash, err := hashFile(source, destination, relative)
		if err == nil {
			files[name] = hash
		}
		return err
	})
	return files, err
}

func hashFile(source, destination *os.Root, relative string) (string, error) {
	in, err := source.Open(relative)
	if err != nil {
		return "", err
	}
	defer in.Close()
	hash := sha256.New()
	if destination == nil {
		_, err = io.Copy(hash, in)
	} else {
		if err := destination.MkdirAll(filepath.Dir(relative), 0o755); err != nil {
			return "", err
		}
		out, openErr := destination.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if openErr != nil {
			return "", openErr
		}
		_, err = io.Copy(io.MultiWriter(out, hash), in)
		err = errors.Join(err, out.Close())
	}
	return hex.EncodeToString(hash.Sum(nil)), err
}

// Hash byte-sorted slash paths and lowercase file digests as path + NUL + digest + LF.
func treeSHA256(files map[string]string) string {
	hash := sha256.New()
	for _, name := range slices.Sorted(maps.Keys(files)) {
		fmt.Fprintf(hash, "%s\x00%s\n", name, files[name])
	}
	return hex.EncodeToString(hash.Sum(nil))
}
