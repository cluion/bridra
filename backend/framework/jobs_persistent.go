package framework

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (queue *JobQueue) persistJob(
	ctx context.Context,
	job queuedJob,
	availableAt time.Time,
) error {
	payload, err := job.handler.encode(job.value)
	if err != nil {
		return fmt.Errorf("%w: job %s: %w", ErrJobDispatchFailed, job.jobType, err)
	}
	id, err := newJobIdentifier()
	if err != nil {
		return fmt.Errorf("%w: job %s: %w", ErrJobDispatchFailed, job.jobType, err)
	}
	enqueuedAt := time.Now().UTC()
	if availableAt.Before(enqueuedAt) {
		availableAt = enqueuedAt
	}
	if err := queue.options.Store.Enqueue(ctx, StoredJob{
		ID:          id,
		Handler:     job.handler.name,
		Payload:     payload,
		AvailableAt: availableAt.UTC(),
		EnqueuedAt:  enqueuedAt,
	}); err != nil {
		return fmt.Errorf(
			"%w: job %s store enqueue: %w",
			ErrJobDispatchFailed,
			job.jobType,
			err,
		)
	}
	queue.wakePersistentWorkers()
	return nil
}

func (queue *JobQueue) workPersistent() {
	defer queue.workers.Done()
	for {
		select {
		case <-queue.workerCtx.Done():
			return
		default:
		}
		reservation, err := queue.options.Store.Reserve(
			queue.workerCtx,
			time.Now().UTC(),
			queue.options.LeaseDuration,
		)
		if err == nil {
			queue.executePersistent(reservation)
			continue
		}
		if !errors.Is(err, ErrJobStoreEmpty) &&
			!errors.Is(err, context.Canceled) {
			queue.report(JobFailure{
				Err: fmt.Errorf(
					"%w: reserve: %w",
					ErrJobStoreOperationFailed,
					err,
				),
			})
		}
		if !queue.waitForPersistentWork() {
			return
		}
	}
}

func (queue *JobQueue) executePersistent(reservation JobReservation) {
	queue.mu.Lock()
	handler, exists := queue.handlersByName[reservation.Job.Handler]
	queue.mu.Unlock()
	if !exists {
		failure := fmt.Errorf(
			"%w: handler %q: %w",
			ErrJobExecutionFailed,
			reservation.Job.Handler,
			ErrJobHandlerNotFound,
		)
		queue.failPersistentReservation(reservation, jobHandlerEntry{}, failure)
		return
	}
	if reservation.Job.Attempts > handler.options.MaxAttempts {
		failure := fmt.Errorf(
			"%w: job %s handler %q exceeded %d attempt(s)",
			ErrJobRetriesExhausted,
			handler.jobType,
			handler.name,
			handler.options.MaxAttempts,
		)
		queue.failPersistentReservation(reservation, handler, failure)
		return
	}
	value, err := handler.decode(reservation.Job.Payload)
	if err != nil {
		queue.failPersistentReservation(reservation, handler, err)
		return
	}
	job := queuedJob{
		jobType: handler.jobType,
		value:   value,
		handler: handler,
	}
	executionError := queue.executeAttempt(job)
	if executionError == nil {
		if err := queue.options.Store.Complete(
			context.Background(),
			reservation,
		); err != nil {
			queue.reportPersistentStoreFailure(reservation, handler, "complete", err)
		}
		return
	}
	if reservation.Job.Attempts < handler.options.MaxAttempts {
		availableAt := time.Now().UTC().Add(handler.options.RetryBackoff)
		if err := queue.options.Store.Release(
			context.Background(),
			reservation,
			availableAt,
			executionError.Error(),
		); err != nil {
			queue.reportPersistentStoreFailure(reservation, handler, "release", err)
			return
		}
		queue.wakePersistentWorkers()
		return
	}

	failure := fmt.Errorf(
		"%w: job %s handler %q after %d attempt(s): %w",
		ErrJobExecutionFailed,
		handler.jobType,
		handler.name,
		reservation.Job.Attempts,
		executionError,
	)
	if handler.options.MaxAttempts > 1 {
		failure = fmt.Errorf("%w: %w", ErrJobRetriesExhausted, failure)
	}
	queue.failPersistentReservation(reservation, handler, failure)
}

func (queue *JobQueue) failPersistentReservation(
	reservation JobReservation,
	handler jobHandlerEntry,
	failure error,
) {
	if err := queue.options.Store.Fail(
		context.Background(),
		reservation,
		failure.Error(),
	); err != nil {
		queue.reportPersistentStoreFailure(
			reservation,
			handler,
			"fail",
			errors.Join(failure, err),
		)
		return
	}
	queue.report(JobFailure{
		JobID:       reservation.Job.ID,
		Handler:     reservation.Job.Handler,
		JobType:     handler.jobType,
		Attempts:    reservation.Job.Attempts,
		MaxAttempts: handler.options.MaxAttempts,
		Err:         failure,
	})
}

func (queue *JobQueue) reportPersistentStoreFailure(
	reservation JobReservation,
	handler jobHandlerEntry,
	operation string,
	err error,
) {
	queue.report(JobFailure{
		JobID:       reservation.Job.ID,
		Handler:     reservation.Job.Handler,
		JobType:     handler.jobType,
		Attempts:    reservation.Job.Attempts,
		MaxAttempts: handler.options.MaxAttempts,
		Err: fmt.Errorf(
			"%w: %s job %s: %w",
			ErrJobStoreOperationFailed,
			operation,
			reservation.Job.ID,
			err,
		),
	})
}

func (queue *JobQueue) waitForPersistentWork() bool {
	timer := time.NewTimer(queue.options.PollInterval)
	defer timer.Stop()
	select {
	case <-queue.workerCtx.Done():
		return false
	case <-queue.wake:
		return true
	case <-timer.C:
		return true
	}
}

func (queue *JobQueue) wakePersistentWorkers() {
	select {
	case queue.wake <- struct{}{}:
	default:
	}
}
