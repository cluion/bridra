package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type devWatchEvent struct {
	paths []string
}

type devWatcher interface {
	Events() <-chan devWatchEvent
	Errors() <-chan error
	Close() error
}

type devSourceState struct {
	size        int64
	modifiedAt  int64
	permissions fs.FileMode
}

type pollingDevWatcher struct {
	events    chan devWatchEvent
	errors    chan error
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newPollingDevWatcher(
	root string,
	interval time.Duration,
	debounce time.Duration,
) (devWatcher, error) {
	if interval <= 0 || debounce < 0 {
		return nil, errors.New("dev: watcher intervals must be positive")
	}
	initial, err := scanDevSourceFiles(root)
	if err != nil {
		return nil, fmt.Errorf("dev: scan Go sources: %w", err)
	}
	watcher := &pollingDevWatcher{
		events: make(chan devWatchEvent, 1),
		errors: make(chan error, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go watcher.watch(root, interval, debounce, initial)
	return watcher, nil
}

func (watcher *pollingDevWatcher) Events() <-chan devWatchEvent {
	return watcher.events
}

func (watcher *pollingDevWatcher) Errors() <-chan error {
	return watcher.errors
}

func (watcher *pollingDevWatcher) Close() error {
	watcher.closeOnce.Do(func() {
		close(watcher.stop)
	})
	<-watcher.done
	return nil
}

func (watcher *pollingDevWatcher) watch(
	root string,
	interval time.Duration,
	debounce time.Duration,
	previous map[string]devSourceState,
) {
	defer close(watcher.done)
	defer close(watcher.events)
	defer close(watcher.errors)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	pending := map[string]struct{}{}
	var lastChange time.Time

	for {
		select {
		case <-watcher.stop:
			return
		case now := <-ticker.C:
			current, err := scanDevSourceFiles(root)
			if err != nil {
				watcher.reportError(fmt.Errorf("dev: scan Go sources: %w", err))
				continue
			}
			for _, path := range changedDevSourceFiles(previous, current) {
				pending[path] = struct{}{}
				lastChange = now
			}
			previous = current
			if len(pending) == 0 || now.Sub(lastChange) < debounce {
				continue
			}

			paths := make([]string, 0, len(pending))
			for path := range pending {
				paths = append(paths, path)
			}
			sort.Strings(paths)
			select {
			case watcher.events <- devWatchEvent{paths: paths}:
				pending = map[string]struct{}{}
			default:
			}
		}
	}
}

func (watcher *pollingDevWatcher) reportError(err error) {
	select {
	case watcher.errors <- err:
	default:
	}
}

func scanDevSourceFiles(root string) (map[string]devSourceState, error) {
	states := map[string]devSourceState{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if relative == "backend" || strings.HasPrefix(relative, "backend/") {
				return nil
			}
			return filepath.SkipDir
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !isDevSourcePath(relative) {
			return nil
		}
		information, err := entry.Info()
		if err != nil {
			return err
		}
		if !information.Mode().IsRegular() {
			return nil
		}
		states[relative] = devSourceState{
			size:        information.Size(),
			modifiedAt:  information.ModTime().UnixNano(),
			permissions: information.Mode().Perm(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return states, nil
}

func isDevSourcePath(path string) bool {
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	switch path {
	case "go.work", "go.work.sum":
		return true
	}
	if !strings.HasPrefix(path, "backend/") {
		return false
	}
	name := filepath.Base(filepath.FromSlash(path))
	switch name {
	case "go.mod", "go.sum":
		return true
	default:
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}
}

func changedDevSourceFiles(
	previous map[string]devSourceState,
	current map[string]devSourceState,
) []string {
	changed := make([]string, 0)
	for path, state := range current {
		if previousState, exists := previous[path]; !exists || previousState != state {
			changed = append(changed, path)
		}
	}
	for path := range previous {
		if _, exists := current[path]; !exists {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}

func installDevBackend(current string, candidate string) error {
	previous := current + ".previous"
	if err := os.Remove(previous); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("dev: remove stale backend backup: %w", err)
	}

	hasPrevious := false
	if err := os.Rename(current, previous); err == nil {
		hasPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("dev: preserve current backend: %w", err)
	}
	if err := os.Rename(candidate, current); err != nil {
		if hasPrevious {
			rollbackErr := os.Rename(previous, current)
			return errors.Join(
				fmt.Errorf("dev: install rebuilt backend: %w", err),
				wrapDevRollbackError(rollbackErr),
			)
		}
		return fmt.Errorf("dev: install rebuilt backend: %w", err)
	}
	if hasPrevious {
		_ = os.Remove(previous)
	}
	return nil
}

func wrapDevRollbackError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("dev: restore previous backend: %w", err)
}
