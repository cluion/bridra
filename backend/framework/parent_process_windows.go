//go:build windows

package framework

import (
	"context"
	"fmt"
	"syscall"
)

const parentProcessWaitMilliseconds = 250

type windowsParentProcessWatcher struct {
	handle syscall.Handle
}

func newParentProcessWatcher(parentPID int) (parentProcessWatcher, error) {
	handle, err := syscall.OpenProcess(
		syscall.SYNCHRONIZE,
		false,
		uint32(parentPID),
	)
	if err != nil {
		return nil, err
	}
	return &windowsParentProcessWatcher{handle: handle}, nil
}

func (watcher *windowsParentProcessWatcher) Wait(ctx context.Context) error {
	for {
		result, err := syscall.WaitForSingleObject(
			watcher.handle,
			parentProcessWaitMilliseconds,
		)
		if err != nil {
			return err
		}
		switch result {
		case syscall.WAIT_OBJECT_0:
			return nil
		case syscall.WAIT_TIMEOUT:
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		default:
			return fmt.Errorf("unexpected parent wait result %#x", result)
		}
	}
}

func (watcher *windowsParentProcessWatcher) Close() error {
	return syscall.CloseHandle(watcher.handle)
}
