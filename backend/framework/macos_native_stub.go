//go:build !darwin || !cgo || !bridra_macos_native

package framework

// MacOSNativeFoundationAvailable reports whether this process was built with
// Bridra's opt-in native macOS adapter and can call Foundation.
func MacOSNativeFoundationAvailable() bool {
	return false
}

type unavailableMacOSResourceBookmarkResolver struct{}

// NewMacOSResourceBookmarkResolver returns an unavailable resolver unless the
// process uses Bridra's opt-in native macOS build.
func NewMacOSResourceBookmarkResolver() ResourceBookmarkResolver {
	return unavailableMacOSResourceBookmarkResolver{}
}

func (unavailableMacOSResourceBookmarkResolver) ResolveBookmark(
	[]byte,
	ResourceBookmarkScope,
) (ResourceLease, error) {
	return nil, ErrResourceBookmarkUnavailable
}
