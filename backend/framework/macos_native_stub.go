//go:build !darwin || !cgo || !bridra_macos_native

package framework

// MacOSNativeFoundationAvailable reports whether this process was built with
// Bridra's opt-in native macOS adapter and can call Foundation.
func MacOSNativeFoundationAvailable() bool {
	return false
}
