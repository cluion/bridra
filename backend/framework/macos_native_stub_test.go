//go:build !bridra_macos_native

package framework

import "testing"

func TestMacOSNativeFoundationIsOptIn(t *testing.T) {
	if MacOSNativeFoundationAvailable() {
		t.Fatal("Foundation adapter must remain disabled without the native build tag")
	}
}
