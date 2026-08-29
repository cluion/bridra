//go:build !bridra_macos_native

package framework

import (
	"errors"
	"testing"
)

func TestMacOSNativeFoundationIsOptIn(t *testing.T) {
	if MacOSNativeFoundationAvailable() {
		t.Fatal("Foundation adapter must remain disabled without the native build tag")
	}
}

func TestMacOSResourceBookmarkResolverIsUnavailableWithoutNativeBuild(t *testing.T) {
	_, err := NewMacOSResourceBookmarkResolver().ResolveBookmark(
		[]byte("opaque"),
		ResourceBookmarkEphemeral,
	)
	if !errors.Is(err, ErrResourceBookmarkUnavailable) {
		t.Fatalf("resolver error = %v", err)
	}
}
