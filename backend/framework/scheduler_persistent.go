package framework

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (scheduler *Scheduler) initializePersistentTasks(
	tasks []scheduledTaskEntry,
) ([]scheduledTaskEntry, error) {
	now := scheduler.clock.Now()
	runnable := make([]scheduledTaskEntry, 0, len(tasks))
	for _, task := range tasks {
		delay, ok := task.schedule.nextDelay(now)
		if !ok {
			continue
		}
		if err := scheduler.options.Store.Initialize(
			context.Background(),
			task.name,
			now.Add(delay),
		); err != nil {
			return nil, fmt.Errorf(
				"%w: initialize task %q: %w",
				ErrSchedulerStoreOperationFailed,
				task.name,
				err,
			)
		}
		runnable = append(runnable, task)
	}
	return runnable, nil
}

func (scheduler *Scheduler) runPersistentTaskLoop(task scheduledTaskEntry) {
	defer scheduler.runners.Done()
	for {
		select {
		case <-scheduler.stopping:
			return
		default:
		}

		state, err := scheduler.options.Store.State(
			scheduler.workerCtx,
			task.name,
		)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			scheduler.reportSchedulerStoreFailure(task.name, time.Time{}, "state", err)
			if !scheduler.waitForPersistentTask(scheduler.options.PollInterval) {
				return
			}
			continue
		}

		now := scheduler.clock.Now()
		wait := state.NextRunAt.Sub(now)
		if state.ReservedUntil.After(now) {
			wait = state.ReservedUntil.Sub(now)
		}
		if wait > 0 && !scheduler.waitForPersistentTask(wait) {
			return
		}

		reservation, err := scheduler.options.Store.Reserve(
			scheduler.workerCtx,
			task.name,
			scheduler.clock.Now(),
			scheduler.options.LeaseDuration,
		)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if !errors.Is(err, ErrScheduledTaskNotDue) &&
				!errors.Is(err, ErrScheduledTaskReserved) {
				scheduler.reportSchedulerStoreFailure(
					task.name,
					state.NextRunAt,
					"reserve",
					err,
				)
			}
			if !scheduler.waitForPersistentTask(scheduler.options.PollInterval) {
				return
			}
			continue
		}

		taskError := scheduler.execute(task, reservation.Task.NextRunAt)
		completedAt := scheduler.clock.Now()
		delay, ok := task.schedule.nextDelay(completedAt)
		if !ok {
			scheduler.reportSchedulerStoreFailure(
				task.name,
				reservation.Task.NextRunAt,
				"calculate next run",
				ErrInvalidScheduledTask,
			)
			return
		}
		lastError := ""
		if taskError != nil {
			lastError = taskError.Error()
		}
		if err := scheduler.options.Store.Complete(
			context.Background(),
			reservation,
			completedAt.Add(delay),
			completedAt,
			lastError,
		); err != nil {
			scheduler.reportSchedulerStoreFailure(
				task.name,
				reservation.Task.NextRunAt,
				"complete",
				err,
			)
		}
	}
}

func (scheduler *Scheduler) waitForPersistentTask(delay time.Duration) bool {
	if delay < 0 {
		delay = 0
	}
	timer := scheduler.clock.NewTimer(delay)
	select {
	case <-scheduler.stopping:
		timer.Stop()
		return false
	case <-timer.C():
		return true
	}
}

func (scheduler *Scheduler) reportSchedulerStoreFailure(
	task string,
	scheduledAt time.Time,
	operation string,
	err error,
) {
	scheduler.report(ScheduledTaskFailure{
		Task:        task,
		ScheduledAt: scheduledAt,
		Err: fmt.Errorf(
			"%w: %s task %q: %w",
			ErrSchedulerStoreOperationFailed,
			operation,
			task,
			err,
		),
	})
}
