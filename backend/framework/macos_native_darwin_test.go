//go:build darwin && cgo && bridra_macos_native

package framework

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/cluion/bridra/backend/framework/internal/macosbookmarktest"
)

func TestMacOSNativeFoundationAvailable(t *testing.T) {
	if !MacOSNativeFoundationAvailable() {
		t.Fatal("Foundation is unavailable in a native macOS build")
	}
}

func TestMacOSNativeResourceBookmarkResolver(t *testing.T) {
	root := t.TempDir()
	bookmark, err := macosbookmarktest.CreateEphemeral(root)
	if err != nil {
		t.Fatalf("create bookmark: %v", err)
	}
	lease, err := NewMacOSResourceBookmarkResolver().ResolveBookmark(
		bookmark,
		ResourceBookmarkEphemeral,
	)
	if err != nil {
		t.Fatalf("resolve bookmark: %v", err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize root: %v", err)
	}
	got, err := filepath.EvalSymlinks(lease.LocalPath())
	if err != nil {
		t.Fatalf("canonicalize resolved path: %v", err)
	}
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
	if _, err := NewMacOSResourceBookmarkResolver().ResolveBookmark(
		[]byte("not-a-bookmark"),
		ResourceBookmarkEphemeral,
	); !errors.Is(err, ErrResourceBookmarkResolve) {
		t.Fatalf("invalid bookmark error = %v", err)
	}
}

func TestMacOSBookmarkResolutionErrorsAreStable(t *testing.T) {
	tests := []struct {
		result int
		want   error
	}{
		{macOSBookmarkStale, ErrResourceBookmarkStale},
		{macOSBookmarkResolveFailed, ErrResourceBookmarkResolve},
		{macOSBookmarkAccessDenied, ErrResourceBookmarkAccess},
		{99, ErrResourceBookmarkResolve},
	}
	for _, test := range tests {
		if err := macOSBookmarkResolutionError(test.result); !errors.Is(err, test.want) {
			t.Fatalf("result %d error = %v, want %v", test.result, err, test.want)
		}
	}
}
