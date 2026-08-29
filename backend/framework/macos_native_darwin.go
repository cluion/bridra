//go:build darwin && cgo && bridra_macos_native

package framework

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>

int bridra_macos_foundation_available(void);
int bridra_macos_resolve_bookmark(
    const void *bytes,
    size_t length,
    int persistent,
    char **path,
    void **handle
);
void bridra_macos_release_resource(void *handle);
*/
import "C"

import (
	"sync"
	"unsafe"
)

const (
	macOSBookmarkResolved = iota
	macOSBookmarkStale
	macOSBookmarkResolveFailed
	macOSBookmarkAccessDenied
)

// MacOSNativeFoundationAvailable reports whether this process was built with
// Bridra's opt-in native macOS adapter and can call Foundation.
func MacOSNativeFoundationAvailable() bool {
	return C.bridra_macos_foundation_available() == 1
}

type macOSResourceBookmarkResolver struct{}

// NewMacOSResourceBookmarkResolver returns Bridra's native Foundation resolver.
func NewMacOSResourceBookmarkResolver() ResourceBookmarkResolver {
	return macOSResourceBookmarkResolver{}
}

func (macOSResourceBookmarkResolver) ResolveBookmark(
	bookmark []byte,
	scope ResourceBookmarkScope,
) (ResourceLease, error) {
	if len(bookmark) == 0 || !validResourceBookmarkScope(scope) {
		return nil, ErrResourceBookmarkInvalid
	}
	var path *C.char
	var handle unsafe.Pointer
	persistent := C.int(0)
	if scope == ResourceBookmarkPersistent {
		persistent = 1
	}
	result := C.bridra_macos_resolve_bookmark(
		unsafe.Pointer(&bookmark[0]),
		C.size_t(len(bookmark)),
		persistent,
		&path,
		&handle,
	)
	if err := macOSBookmarkResolutionError(int(result)); err != nil {
		return nil, err
	}
	if path == nil || handle == nil {
		if handle != nil {
			C.bridra_macos_release_resource(handle)
		}
		return nil, ErrResourceBookmarkResolve
	}
	defer C.free(unsafe.Pointer(path))
	return &macOSResourceLease{
		path:   C.GoString(path),
		handle: handle,
	}, nil
}

type macOSResourceLease struct {
	path   string
	handle unsafe.Pointer
	once   sync.Once
}

func (lease *macOSResourceLease) LocalPath() string {
	if lease == nil {
		return ""
	}
	return lease.path
}

func (lease *macOSResourceLease) Release() error {
	if lease == nil {
		return nil
	}
	lease.once.Do(func() {
		if lease.handle != nil {
			C.bridra_macos_release_resource(lease.handle)
			lease.handle = nil
		}
	})
	return nil
}

func macOSBookmarkResolutionError(result int) error {
	switch result {
	case macOSBookmarkResolved:
		return nil
	case macOSBookmarkStale:
		return ErrResourceBookmarkStale
	case macOSBookmarkAccessDenied:
		return ErrResourceBookmarkAccess
	default:
		return ErrResourceBookmarkResolve
	}
}
