package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestDevSourcePathSelectsBuildInputs(t *testing.T) {
	tests := map[string]bool{
		"backend/go.mod":                  true,
		"backend/go.sum":                  true,
		"backend/app/router.go":           true,
		"backend/app/router_test.go":      false,
		"backend/storage/development.log": false,
		"go.work":                         true,
		"go.work.sum":                     true,
		"lib/main.dart":                   false,
		"README.md":                       false,
	}
	for path, want := range tests {
		t.Run(path, func(t *testing.T) {
			if got := isDevSourcePath(path); got != want {
				t.Fatalf("isDevSourcePath(%q) = %t, want %t", path, got, want)
			}
		})
	}
}

func TestChangedDevSourceFilesDetectsAddUpdateAndDelete(t *testing.T) {
	previous := map[string]devSourceState{
		"backend/app/deleted.go": {size: 1, modifiedAt: 1},
		"backend/app/router.go":  {size: 1, modifiedAt: 1},
	}
	current := map[string]devSourceState{
		"backend/app/added.go":  {size: 1, modifiedAt: 1},
		"backend/app/router.go": {size: 2, modifiedAt: 1},
	}

	got := changedDevSourceFiles(previous, current)
	want := []string{
		"backend/app/added.go",
		"backend/app/deleted.go",
		"backend/app/router.go",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("changed files = %#v, want %#v", got, want)
	}
}

func TestPollingDevWatcherReportsGoSourceChange(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "backend", "app", "router.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("create source directory: %v", err)
	}
	if err := os.WriteFile(source, []byte("package app\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	watcher, err := newPollingDevWatcher(root, 10*time.Millisecond, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	defer func() {
		if closeErr := watcher.Close(); closeErr != nil {
			t.Fatalf("close watcher: %v", closeErr)
		}
	}()

	if err := os.WriteFile(source, []byte("package app\n\nvar changed = true\n"), 0o644); err != nil {
		t.Fatalf("update source: %v", err)
	}
	select {
	case event := <-watcher.Events():
		if !slices.Contains(event.paths, "backend/app/router.go") {
			t.Fatalf("watch event = %#v", event.paths)
		}
	case err := <-watcher.Errors():
		t.Fatalf("watch error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Go source change")
	}
}

func TestInstallDevBackendReplacesCurrentExecutable(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "bridra_backend")
	candidate := current + ".next"
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatalf("write current backend: %v", err)
	}
	if err := os.WriteFile(candidate, []byte("new"), 0o755); err != nil {
		t.Fatalf("write candidate backend: %v", err)
	}

	if err := installDevBackend(current, candidate); err != nil {
		t.Fatalf("install backend: %v", err)
	}
	contents, err := os.ReadFile(current)
	if err != nil {
		t.Fatalf("read installed backend: %v", err)
	}
	if string(contents) != "new" {
		t.Fatalf("installed backend = %q", contents)
	}
	if _, err := os.Stat(candidate); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate still exists: %v", err)
	}
	if _, err := os.Stat(current + ".previous"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup still exists: %v", err)
	}
}
