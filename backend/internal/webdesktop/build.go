// Package webdesktop builds a native launcher around an existing Web UI snapshot.
package webdesktop

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"text/template"

	"github.com/cluion/bridra/backend/internal/webdist"
)

//go:embed assets launcher.go.tmpl host/runtime.go host/gateway.go
var sources embed.FS

type Config struct {
	Root            string
	Name            string
	Target          string
	GoModule        string
	FrameworkModule string
	DistSHA256      string
}
type Artifact struct {
	Path     string
	Manifest string
	SHA256   string
}

func Build(c Config) (Artifact, error) {
	var empty Artifact
	nativeBuild := map[string]func(string, string) error{"windows": buildWindowsNative, "linux": buildLinuxNative, "macos": buildMacNative}[c.Target]
	if nativeBuild == nil || !filepath.IsLocal(c.Name) || c.Name != filepath.Base(c.Name) || c.Name == "." {
		return empty, errors.New("invalid desktop target or bundle name")
	}
	root, err := os.OpenRoot(c.Root)
	if err != nil {
		return empty, err
	}
	defer root.Close()
	parent := filepath.Join("build", "bridra", c.Target)
	for _, p := range []string{"build", filepath.Join("build", "bridra"), parent} {
		if err := root.Mkdir(p, 0755); err != nil && !errors.Is(err, fs.ErrExist) {
			return empty, err
		}
		info, err := root.Lstat(p)
		if err != nil || !info.IsDir() {
			return empty, fmt.Errorf("expected real directory at %s", p)
		}
	}
	out, err := root.OpenRoot(parent)
	if err != nil {
		return empty, err
	}
	defer out.Close()
	stage := ".staging-" + rand.Text()
	if err := out.Mkdir(stage, 0755); err != nil {
		return empty, err
	}
	defer out.RemoveAll(stage)
	name, bin, resources := c.Name, ".", "."
	suffix := ""
	if c.Target == "macos" {
		name += ".app"
		bin = filepath.Join("Contents", "MacOS")
		resources = filepath.Join("Contents", "Resources")
	}
	if c.Target == "windows" {
		suffix = ".exe"
	}
	staging := filepath.Join(c.Root, parent, stage)
	bundle := filepath.Join(staging, name)
	binaries := filepath.Join(bundle, bin)
	resourceDir := filepath.Join(bundle, resources)
	for _, path := range []string{binaries, resourceDir} {
		if err := os.MkdirAll(path, 0755); err != nil {
			return empty, err
		}
	}
	if err := webdist.CopyVerified(c.Root, c.DistSHA256, resourceDir); err != nil {
		return empty, err
	}
	work, err := os.MkdirTemp("", "bridra-build-desktop-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(work)
	files, err := fs.Sub(sources, "assets/"+c.Target)
	if err != nil {
		return empty, err
	}
	if err := os.CopyFS(work, files); err != nil {
		return empty, err
	}
	if err := nativeBuild(work, filepath.Join(binaries, "bridra-webview"+suffix)); err != nil {
		return empty, err
	}
	launcher := launcherFilename(c.Target, c.Name)
	if err := buildLauncher(c, work, filepath.Join(binaries, launcher)); err != nil {
		return empty, err
	}
	if c.Target == "windows" {
		const license = "WebView2-SDK-LICENSE.txt"
		data, err := fs.ReadFile(files, license)
		if err != nil {
			return empty, err
		}
		if err := os.WriteFile(filepath.Join(bundle, license), data, 0644); err != nil {
			return empty, err
		}
	}
	if c.Target == "macos" {
		if err := finishMacBundle(bundle, c.Name); err != nil {
			return empty, err
		}
	}
	if err := webdist.Verify(resourceDir, c.DistSHA256); err != nil {
		return empty, err
	}
	digest, err := webdist.DirectorySHA256(staging)
	if err != nil {
		return empty, err
	}
	relative := filepath.Join(parent, digest, name)
	if err := out.Rename(stage, digest); err != nil {
		info, statErr := out.Lstat(digest)
		if statErr != nil || !info.IsDir() {
			return empty, err
		}
		actual, err := webdist.DirectorySHA256(filepath.Join(c.Root, parent, digest))
		if err != nil {
			return empty, err
		}
		if actual != digest {
			return empty, fmt.Errorf("existing desktop package %s differs", digest)
		}
	}
	if runtime.GOOS != "windows" {
		for _, exe := range []string{launcher, "bridra-webview"} {
			info, err := root.Stat(filepath.Join(relative, bin, exe))
			if err != nil || info.Mode().Perm()&0111 == 0 {
				return empty, fmt.Errorf("packaged %s must be executable", exe)
			}
		}
	}
	return Artifact{Path: relative, Manifest: filepath.Join(relative, resources, "manifest.json"), SHA256: digest}, nil
}

func launcherFilename(target, name string) string {
	if target == "windows" {
		return name + ".exe"
	}
	// The Linux launcher and the dist directory share the package root.
	if target == "linux" && name == "dist" {
		return "dist-app"
	}
	return name
}

// The overlay compiles the host against the consumer's Go module without changing its sources or go.mod.
func buildLauncher(c Config, work, output string) error {
	// The Go loader canonicalizes its working directory before matching overlay keys.
	backend, err := filepath.EvalSymlinks(filepath.Join(c.Root, "backend"))
	if err != nil {
		return fmt.Errorf("resolve backend for desktop build: %w", err)
	}
	virtual := filepath.Join(backend, "bridra_desktop_runtime")
	if _, err := os.Lstat(virtual); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("desktop build needs unused overlay path %s", virtual)
	}
	overlay := make(map[string]string)
	for _, name := range []string{"gateway.go", "runtime.go"} {
		data, err := sources.ReadFile("host/" + name)
		if err != nil {
			return err
		}
		path := filepath.Join(work, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			return err
		}
		overlay[filepath.Join(virtual, name)] = path
	}
	config, err := json.Marshal(struct{ Replace map[string]string }{overlay})
	if err != nil {
		return err
	}
	overlayPath := filepath.Join(work, "overlay.json")
	if err := os.WriteFile(overlayPath, config, 0600); err != nil {
		return err
	}
	t, err := template.ParseFS(sources, "launcher.go.tmpl")
	if err != nil {
		return err
	}
	mainPath := filepath.Join(work, "main.go")
	f, err := os.Create(mainPath)
	if err != nil {
		return err
	}
	if err := errors.Join(t.Execute(f, c), f.Close()); err != nil {
		return err
	}
	args := []string{"build", "-overlay=" + overlayPath, "-buildvcs=false", "-trimpath", "-o", output}
	if c.Target == "windows" {
		args = append(args, "-ldflags=-H=windowsgui")
	}
	cmd := exec.Command("go", append(args, mainPath)...)
	cmd.Dir = backend
	cmd.Env = append(cmd.Environ(), "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if workspace := os.Getenv("GOWORK"); filepath.IsAbs(workspace) {
		// Resolve directory aliases consistently with the overlay. The work file itself can be a symlink.
		dir, err := filepath.EvalSymlinks(filepath.Dir(workspace))
		if err != nil {
			return fmt.Errorf("resolve Go workspace directory: %w", err)
		}
		cmd.Env = append(cmd.Env, "GOWORK="+filepath.Join(dir, filepath.Base(workspace)))
	}
	if message, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build desktop backend: %w: %s", err, message)
	}
	return nil
}
