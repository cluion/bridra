//go:build darwin && cgo && bridra_macos_native

package framework

/*
#cgo LDFLAGS: -framework Foundation
int bridra_macos_foundation_available(void);
*/
import "C"

// MacOSNativeFoundationAvailable reports whether this process was built with
// Bridra's opt-in native macOS adapter and can call Foundation.
func MacOSNativeFoundationAvailable() bool {
	return C.bridra_macos_foundation_available() == 1
}
