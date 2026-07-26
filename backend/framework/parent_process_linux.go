//go:build linux

package framework

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const parentProcessPollInterval = 250 * time.Millisecond

type linuxParentProcessWatcher struct {
	pid       int
	startTime string
}

func newParentProcessWatcher(parentPID int) (parentProcessWatcher, error) {
	startTime, err := linuxProcessStartTime(parentPID)
	if err != nil {
		return nil, err
	}
	if os.Getppid() != parentPID {
		return nil, ErrParentProcessExited
	}
	return &linuxParentProcessWatcher{
		pid:       parentPID,
		startTime: startTime,
	}, nil
}

func (watcher *linuxParentProcessWatcher) Wait(ctx context.Context) error {
	ticker := time.NewTicker(parentProcessPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if os.Getppid() != watcher.pid {
				return nil
			}
			startTime, err := linuxProcessStartTime(watcher.pid)
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if startTime != watcher.startTime {
				return nil
			}
		}
	}
}

func (watcher *linuxParentProcessWatcher) Close() error {
	return nil
}

func linuxProcessStartTime(pid int) (string, error) {
	contents, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	endName := strings.LastIndexByte(string(contents), ')')
	if endName < 0 {
		return "", errors.New("invalid Linux process stat")
	}
	fields := strings.Fields(string(contents[endName+1:]))
	const startTimeIndex = 19
	if len(fields) <= startTimeIndex {
		return "", errors.New("incomplete Linux process stat")
	}
	return fields[startTimeIndex], nil
}
