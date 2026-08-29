//go:build darwin && cgo && bridra_macos_native

package macosbookmarktest

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>

int bridra_test_create_ephemeral_bookmark(
    const char *path,
    void **bytes,
    size_t *length
);
*/
import "C"

import (
	"errors"
	"unsafe"
)

func CreateEphemeral(path string) ([]byte, error) {
	value := C.CString(path)
	defer C.free(unsafe.Pointer(value))
	var bytes unsafe.Pointer
	var length C.size_t
	if C.bridra_test_create_ephemeral_bookmark(value, &bytes, &length) != 0 ||
		bytes == nil || length == 0 {
		return nil, errors.New("create ephemeral bookmark for testing")
	}
	defer C.free(bytes)
	return C.GoBytes(bytes, C.int(length)), nil
}
