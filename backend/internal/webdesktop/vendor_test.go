package webdesktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// A local SDK fixture makes the launcher's real Go build independent of registry access.
func TestBuildLauncherUsesProjectDependencyMode(t *testing.T) {
	root := t.TempDir()
	backend := filepath.Join(root, "backend")
	mod := "module example.test/portal\n\ngo 1.25\n\nrequire example.test/sdk v1.0.0\nreplace example.test/sdk => ../sdk\n"
	files := map[string]string{
		"go.work":        "go 1.25\nuse ./backend\n",
		"backend/go.mod": mod,
		"sdk/go.mod":     "module example.test/sdk\ngo 1.25\n",
		"backend/app/app.go": `package app
import (
	"context"
	_ "example.test/sdk/framework"
	"io"
)
type Config struct { Token, Runtime string; Logs io.Writer }
type App struct{}
func Build(Config, ...any) (App, error) { return App{}, nil }
func (App) Router() any { return nil }
func (App) Shutdown(context.Context) error { return nil }
`,
		"sdk/framework/framework.go": `package framework
import "net/http"
func DefaultFileTransferOptions() any { return nil }
func NewFileTransferServiceProvider(any) any { return nil }
func NewPrincipal(string) (any, error) { return nil, nil }
func NewStaticTokenAuthenticator(string, any) (any, error) { return nil, nil }
func DefaultMemoryRateLimiterOptions() any { return nil }
func NewMemoryRateLimiter(any) (any, error) { return nil, nil }
type HTTPHandler struct { Router, Authenticator, RateLimiter, Errors any }
func (*HTTPHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}
`,
	}
	for relative, data := range files {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GOMODCACHE", t.TempDir())
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOTOOLCHAIN", "local")
	build := func(t *testing.T) {
		t.Helper()
		cfg := Config{Root: root, Name: "portal", Target: runtime.GOOS, GoModule: "example.test/portal", FrameworkModule: "example.test/sdk"}
		if err := buildLauncher(cfg, t.TempDir(), filepath.Join(t.TempDir(), "portal")); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("module", build)
	vendor := func(args ...string) {
		t.Helper()
		cmd := exec.Command("go", args...)
		cmd.Dir = backend
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, out)
		}
	}
	vendor("mod", "vendor")
	workspace, err := filepath.EvalSymlinks(filepath.Join(root, "go.work"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", workspace)
	vendor("work", "vendor")
	if err := os.Rename(filepath.Join(root, "sdk"), filepath.Join(root, "offline-sdk")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, work, flags string }{
		{"module-auto-vendor", "off", ""},
		{"module-explicit-vendor", "off", "-mod=vendor"},
		{"workspace-auto-vendor", workspace, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOWORK", tc.work)
			t.Setenv("GOFLAGS", tc.flags)
			build(t)
		})
	}
	t.Run("workspace-via-directory-alias", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "linked")
		if err := os.Symlink(root, alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Setenv("GOWORK", filepath.Join(alias, "go.work"))
		build(t)
	})
	got, err := os.ReadFile(filepath.Join(backend, "go.mod"))
	if err != nil || string(got) != mod {
		t.Fatalf("go.mod changed: %v\n%s", err, got)
	}
	if _, err := os.Stat(filepath.Join(backend, "go.sum")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected go.sum: %v", err)
	}
}
