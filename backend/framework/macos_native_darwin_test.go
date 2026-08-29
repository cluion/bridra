//go:build darwin && cgo && bridra_macos_native

package framework

import "testing"

func TestMacOSNativeFoundationAvailable(t *testing.T) {
	if !MacOSNativeFoundationAvailable() {
		t.Fatal("Foundation is unavailable in a native macOS build")
	}
}
