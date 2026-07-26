package framework

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// ErrParentProcessExited is the cancellation cause when the process that
// launched the current process terminates.
var ErrParentProcessExited = errors.New("framework: parent process exited")

type parentProcessWatcher interface {
	Wait(context.Context) error
	Close() error
}

// ParentProcessContext returns a context that is cancelled when the process
// that launched the current process terminates.
func ParentProcessContext(
	parent context.Context,
) (context.Context, context.CancelFunc, error) {
	parentPID := os.Getppid()
	if parentPID <= 0 {
		return nil, nil, fmt.Errorf("framework: invalid parent process id %d", parentPID)
	}
	watcher, err := newParentProcessWatcher(parentPID)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"framework: watch parent process %d: %w",
			parentPID,
			err,
		)
	}

	ctx, cancel := context.WithCancelCause(parent)
	go func() {
		defer watcher.Close()
		err := watcher.Wait(ctx)
		switch {
		case err == nil:
			cancel(ErrParentProcessExited)
		case !errors.Is(err, context.Canceled):
			cancel(fmt.Errorf(
				"framework: watch parent process %d: %w",
				parentPID,
				err,
			))
		}
	}()
	return ctx, func() { cancel(context.Canceled) }, nil
}
