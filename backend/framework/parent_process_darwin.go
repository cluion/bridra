//go:build darwin

package framework

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
)

const parentProcessPollInterval = 250 * time.Millisecond

type darwinParentProcessWatcher struct {
	kqueue int
}

func newParentProcessWatcher(parentPID int) (parentProcessWatcher, error) {
	kqueue, err := syscall.Kqueue()
	if err != nil {
		return nil, err
	}
	watcher := &darwinParentProcessWatcher{kqueue: kqueue}
	change := syscall.Kevent_t{}
	syscall.SetKevent(
		&change,
		parentPID,
		syscall.EVFILT_PROC,
		syscall.EV_ADD|syscall.EV_ENABLE|syscall.EV_ONESHOT,
	)
	change.Fflags = syscall.NOTE_EXIT
	timeout := syscall.NsecToTimespec(0)
	if _, err := syscall.Kevent(kqueue, []syscall.Kevent_t{change}, nil, &timeout); err != nil {
		watcher.Close()
		return nil, err
	}
	if os.Getppid() != parentPID {
		watcher.Close()
		return nil, ErrParentProcessExited
	}
	return watcher, nil
}

func (watcher *darwinParentProcessWatcher) Wait(ctx context.Context) error {
	events := make([]syscall.Kevent_t, 1)
	for {
		timeout := syscall.NsecToTimespec(parentProcessPollInterval.Nanoseconds())
		count, err := syscall.Kevent(watcher.kqueue, nil, events, &timeout)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if count > 0 {
			if events[0].Flags&syscall.EV_ERROR != 0 {
				return fmt.Errorf("kqueue parent event: %w", syscall.Errno(events[0].Data))
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func (watcher *darwinParentProcessWatcher) Close() error {
	return syscall.Close(watcher.kqueue)
}
