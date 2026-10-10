package framework

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ScheduledTaskHandle controls one process-local task. Do not copy a handle.
type ScheduledTaskHandle struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wake   chan struct{}

	mu       sync.Mutex
	interval time.Duration
	revision uint64
	trigger  bool
}

// StartTask starts a fixed-delay task on a running Scheduler without a Store.
// Its first invocation waits one interval unless TriggerNow is called.
func StartTask(scheduler *Scheduler, name string, interval time.Duration, task ScheduledTask) (*ScheduledTaskHandle, error) {
	if scheduler == nil {
		return nil, ErrSchedulerUnavailable
	}
	name = strings.TrimSpace(name)
	if name == "" || interval <= 0 || task == nil {
		return nil, ErrInvalidScheduledTask
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if scheduler.state == schedulerStopping || scheduler.state == schedulerStopped {
		return nil, ErrSchedulerStopped
	}
	if scheduler.state != schedulerRunning {
		return nil, ErrSchedulerNotRunning
	}
	if scheduler.options.Store != nil {
		return nil, ErrDynamicSchedulingUnsupported
	}
	if _, exists := scheduler.taskNames[name]; exists {
		return nil, fmt.Errorf("%w: %s", ErrScheduledTaskAlreadyDefined, name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	handle := &ScheduledTaskHandle{
		ctx: ctx, cancel: cancel, interval: interval,
		done: make(chan struct{}), wake: make(chan struct{}, 1),
	}
	scheduler.taskNames[name] = struct{}{}
	scheduler.dynamicTasks[name] = handle
	// Registration and shutdown share the lock, so Wait never races an Add.
	scheduler.runners.Add(1)
	go scheduler.runDynamicTask(scheduledTaskEntry{name: name, run: task}, handle)
	return handle, nil
}

// Stop requests cancellation without waiting. The invocation must honor its
// context. Wait for Done before reusing the name or releasing task resources.
func (handle *ScheduledTaskHandle) Stop() {
	if handle != nil && handle.cancel != nil {
		handle.cancel()
	}
}

// Done closes after the runner and any active invocation have finished.
func (handle *ScheduledTaskHandle) Done() <-chan struct{} {
	if handle == nil {
		return nil
	}
	return handle.done
}

// SetInterval resets a pending wait. During an invocation, the new delay starts
// after completion. It does not cancel the active invocation.
func (handle *ScheduledTaskHandle) SetInterval(interval time.Duration) error {
	if handle == nil || handle.ctx == nil || interval <= 0 {
		return ErrInvalidScheduledTask
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.ctx.Err() != nil {
		return ErrScheduledTaskStopped
	}
	handle.interval = interval
	handle.revision++
	handle.notify()
	return nil
}

// TriggerNow queues an immediate invocation. Multiple requests before the next
// invocation coalesce into one; an active invocation is never overlapped.
func (handle *ScheduledTaskHandle) TriggerNow() error {
	if handle == nil || handle.ctx == nil {
		return ErrInvalidScheduledTask
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.ctx.Err() != nil {
		return ErrScheduledTaskStopped
	}
	handle.trigger = true
	handle.notify()
	return nil
}

func (handle *ScheduledTaskHandle) notify() {
	select {
	case handle.wake <- struct{}{}:
	default:
	}
}

func (handle *ScheduledTaskHandle) drainWake() {
	select {
	case <-handle.wake:
	default:
	}
}

func (scheduler *Scheduler) runDynamicTask(task scheduledTaskEntry, handle *ScheduledTaskHandle) {
	defer func() {
		handle.cancel()
		scheduler.mu.Lock()
		delete(scheduler.dynamicTasks, task.name)
		delete(scheduler.taskNames, task.name)
		scheduler.mu.Unlock()
		close(handle.done)
		scheduler.runners.Done()
	}()
	for {
		handle.mu.Lock()
		interval, revision, trigger := handle.interval, handle.revision, handle.trigger
		handle.trigger = false
		handle.drainWake()
		handle.mu.Unlock()
		if handle.ctx.Err() != nil {
			return
		}
		if !trigger {
			timer := scheduler.clock.NewTimer(interval)
			select {
			case <-handle.ctx.Done():
				timer.Stop()
				return
			case <-handle.wake:
				timer.Stop()
				continue
			case <-timer.C():
			}
			handle.mu.Lock()
			changed := revision != handle.revision && !handle.trigger
			// A simultaneous timer tick and trigger count as one invocation.
			handle.trigger = false
			handle.drainWake()
			handle.mu.Unlock()
			if changed {
				continue
			}
		}
		if handle.ctx.Err() != nil {
			return
		}
		_ = scheduler.executeContext(handle.ctx, task, time.Time{})
	}
}
