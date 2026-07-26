//go:build !darwin && !linux && !windows

package framework

import "fmt"

func newParentProcessWatcher(parentPID int) (parentProcessWatcher, error) {
	return nil, fmt.Errorf(
		"parent process watching is not supported on this platform for process %d",
		parentPID,
	)
}
