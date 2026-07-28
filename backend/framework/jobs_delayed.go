package framework

import (
	"container/heap"
	"context"
	"fmt"
	"reflect"
	"time"
)

type scheduledJob struct {
	job      queuedJob
	readyAt  time.Time
	sequence uint64
}

type scheduledJobHeap []scheduledJob

func DispatchJobAfter[T any](
	ctx context.Context,
	queue *JobQueue,
	delay time.Duration,
	job T,
) error {
	if queue == nil {
		return ErrJobQueueUnavailable
	}
	if ctx == nil {
		return ErrJobContextUnavailable
	}
	if delay < 0 {
		return fmt.Errorf(
			"%w: %w: delay must not be negative",
			ErrJobDispatchFailed,
			ErrInvalidJobDelay,
		)
	}
	if delay == 0 {
		return DispatchJob(ctx, queue, job)
	}
	return dispatchJobAt(ctx, queue, time.Now().Add(delay), job)
}

func DispatchJobAt[T any](
	ctx context.Context,
	queue *JobQueue,
	readyAt time.Time,
	job T,
) error {
	if queue == nil {
		return ErrJobQueueUnavailable
	}
	if ctx == nil {
		return ErrJobContextUnavailable
	}
	if !readyAt.After(time.Now()) {
		return DispatchJob(ctx, queue, job)
	}
	return dispatchJobAt(ctx, queue, readyAt, job)
}

func dispatchJobAt[T any](
	ctx context.Context,
	queue *JobQueue,
	readyAt time.Time,
	job T,
) error {
	queued, stopping, err := prepareJobDispatch(ctx, queue, job)
	if err != nil {
		return err
	}
	defer queue.dispatches.Done()

	select {
	case queue.delayedSlots <- struct{}{}:
	case <-stopping:
		return stoppedJobDispatchError(queued.jobType)
	case <-ctx.Done():
		return contextJobDispatchError(queued.jobType, ctx.Err())
	}

	queue.scheduled <- scheduledJob{job: queued, readyAt: readyAt}
	return nil
}

func stoppedJobDispatchError(jobType reflect.Type) error {
	return fmt.Errorf(
		"%w: job %s: %w",
		ErrJobDispatchFailed,
		jobType,
		ErrJobQueueStopped,
	)
}

func contextJobDispatchError(jobType reflect.Type, err error) error {
	return fmt.Errorf(
		"%w: job %s enqueue context: %w",
		ErrJobDispatchFailed,
		jobType,
		err,
	)
}

func (queue *JobQueue) schedule() {
	defer close(queue.scheduleDone)

	pending := &scheduledJobHeap{}
	heap.Init(pending)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	var timerReady <-chan time.Time
	var sequence uint64

	for {
		if pending.Len() == 0 {
			timerReady = nil
		} else {
			wait := time.Until((*pending)[0].readyAt)
			if wait < 0 {
				wait = 0
			}
			resetJobScheduleTimer(timer, wait)
			timerReady = timer.C
		}

		select {
		case scheduled := <-queue.scheduled:
			sequence++
			scheduled.sequence = sequence
			heap.Push(pending, scheduled)
		case <-timerReady:
			queue.promoteScheduledJob(heap.Pop(pending).(scheduledJob))
		case <-queue.scheduleStop:
			queue.drainScheduledJobs(pending, &sequence)
			return
		}
	}
}

func (queue *JobQueue) drainScheduledJobs(
	pending *scheduledJobHeap,
	sequence *uint64,
) {
	for {
		select {
		case scheduled := <-queue.scheduled:
			*sequence++
			scheduled.sequence = *sequence
			heap.Push(pending, scheduled)
		default:
			for pending.Len() != 0 {
				queue.promoteScheduledJob(heap.Pop(pending).(scheduledJob))
			}
			return
		}
	}
}

func resetJobScheduleTimer(timer *time.Timer, wait time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(wait)
}

func (queue *JobQueue) promoteScheduledJob(scheduled scheduledJob) {
	queue.jobs <- scheduled.job
	<-queue.delayedSlots
}

func (jobs scheduledJobHeap) Len() int {
	return len(jobs)
}

func (jobs scheduledJobHeap) Less(left int, right int) bool {
	if jobs[left].readyAt.Equal(jobs[right].readyAt) {
		return jobs[left].sequence < jobs[right].sequence
	}
	return jobs[left].readyAt.Before(jobs[right].readyAt)
}

func (jobs scheduledJobHeap) Swap(left int, right int) {
	jobs[left], jobs[right] = jobs[right], jobs[left]
}

func (jobs *scheduledJobHeap) Push(value any) {
	*jobs = append(*jobs, value.(scheduledJob))
}

func (jobs *scheduledJobHeap) Pop() any {
	previous := *jobs
	last := len(previous) - 1
	value := previous[last]
	previous[last] = scheduledJob{}
	*jobs = previous[:last]
	return value
}
