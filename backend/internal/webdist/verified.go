package webdist

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
)

func ValidDigest(digest string) bool {
	if len(digest) != 64 || digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

// CopyVerified takes its input only from the named web snapshot.
func CopyVerified(project, digest, resources string) error {
	if !ValidDigest(digest) {
		return errors.New("expected a lowercase SHA-256 web dist digest")
	}
	root, err := os.OpenRoot(project)
	if err != nil {
		return err
	}
	defer root.Close()
	input, err := openDirectory(root, OutputDirectory+"/"+digest, false)
	if err != nil {
		return fmt.Errorf("open web snapshot %s: %w", digest, err)
	}
	defer input.Close()
	output, err := os.OpenRoot(resources)
	if err != nil {
		return err
	}
	defer output.Close()
	return checkSnapshot(input, output, digest)
}

// Verify checks every packaged file, including unexpected extra files.
func Verify(resources, digest string) error {
	root, err := os.OpenRoot(resources)
	if err != nil {
		return err
	}
	defer root.Close()
	return checkSnapshot(root, nil, digest)
}

func checkSnapshot(root, destination *os.Root, digest string) error {
	info, err := root.Lstat("manifest.json")
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("expected regular web manifest.json")
	}
	data, err := root.ReadFile("manifest.json")
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if m.SchemaVersion != 1 || m.Artifact != "dist" || m.ArtifactSHA256 != digest || treeSHA256(m.Files) != digest || m.Files["index.html"] == "" {
		return errors.New("web manifest does not match the requested dist SHA-256")
	}
	dist, err := openDirectory(root, "dist", false)
	if err != nil {
		return err
	}
	defer dist.Close()
	var files map[string]string
	if destination != nil {
		files, err = stage(dist, destination)
	} else {
		files, err = scan(dist, nil)
	}
	if err != nil {
		return err
	}
	if !maps.Equal(files, m.Files) {
		return fmt.Errorf("dist files differ from web manifest %s", digest)
	}
	if destination == nil {
		return nil
	}
	return destination.WriteFile("manifest.json", data, 0644)
}

func DirectorySHA256(path string) (string, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	files, err := scan(root, nil)
	if err != nil {
		return "", err
	}
	return treeSHA256(files), nil
}
