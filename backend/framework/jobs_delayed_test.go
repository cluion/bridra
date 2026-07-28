package framework

import (
	"container/heap"
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type delayedJob struct {
	Value int
}

func TestJobQueueDispatchesJobAtRequestedTime(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 2, Workers: 1})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	handled := make(chan time.Time, 1)
	if err := HandleJob(queue, "delayed", func(context.Context, delayedJob) error {
		handled <- time.Now()
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	readyAt := time.Now().Add(60 * time.Millisecond)
	if err := DispatchJobAt(context.Background(), queue, readyAt, delayedJob{}); err != nil {
		t.Fatalf("dispatch at: %v", err)
	}
	select {
	case handledAt := <-handled:
		if handledAt.Before(readyAt.Add(-5 * time.Millisecond)) {
			t.Fatalf("handled at %s before requested time %s", handledAt, readyAt)
		}
	case <-time.After(time.Second):
		t.Fatal("delayed job did not run")
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestJobQueueOrdersDelayedJobsByRequestedTime(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 4, Workers: 1})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	handled := make(chan int, 4)
	if err := HandleJob(queue, "delayed", func(_ context.Context, job delayedJob) error {
		handled <- job.Value
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	now := time.Now()
	for _, scheduled := range []struct {
		readyAt time.Time
		value   int
	}{
		{readyAt: now.Add(90 * time.Millisecond), value: 4},
		{readyAt: now.Add(30 * time.Millisecond), value: 1},
		{readyAt: now.Add(60 * time.Millisecond), value: 2},
		{readyAt: now.Add(60 * time.Millisecond), value: 3},
	} {
		if err := DispatchJobAt(
			context.Background(),
			queue,
			scheduled.readyAt,
			delayedJob{Value: scheduled.value},
		); err != nil {
			t.Fatalf("dispatch %d: %v", scheduled.value, err)
		}
	}

	var values []int
	for range 4 {
		select {
		case value := <-handled:
			values = append(values, value)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for ordered delayed jobs")
		}
	}
	if !reflect.DeepEqual(values, []int{1, 2, 3, 4}) {
		t.Fatalf("handled = %#v", values)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestJobQueueDispatchesZeroAndPastDelaysImmediately(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 2, Workers: 1})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	handled := make(chan int, 2)
	if err := HandleJob(queue, "delayed", func(_ context.Context, job delayedJob) error {
		handled <- job.Value
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJobAfter(context.Background(), queue, 0, delayedJob{Value: 1}); err != nil {
		t.Fatalf("dispatch zero delay: %v", err)
	}
	if err := DispatchJobAt(
		context.Background(),
		queue,
		time.Now().Add(-time.Hour),
		delayedJob{Value: 2},
	); err != nil {
		t.Fatalf("dispatch past time: %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if values := []int{<-handled, <-handled}; !reflect.DeepEqual(values, []int{1, 2}) {
		t.Fatalf("handled = %#v", values)
	}
}

func TestJobQueueShutdownPromotesAndDrainsDelayedJobs(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 1, Workers: 1})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	var handled atomic.Int32
	if err := HandleJob(queue, "delayed", func(context.Context, delayedJob) error {
		handled.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJobAfter(context.Background(), queue, time.Hour, delayedJob{}); err != nil {
		t.Fatalf("dispatch delayed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := queue.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if handled.Load() != 1 {
		t.Fatalf("handled = %d, want 1", handled.Load())
	}
}

func TestJobQueueDrainsBufferedScheduledJobsInDueOrder(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 3, Workers: 1})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	now := time.Now()
	pending := &scheduledJobHeap{
		{
			job:      queuedJob{value: delayedJob{Value: 3}},
			readyAt:  now.Add(3 * time.Hour),
			sequence: 1,
		},
	}
	heap.Init(pending)
	for _, scheduled := range []scheduledJob{
		{
			job:     queuedJob{value: delayedJob{Value: 1}},
			readyAt: now.Add(time.Hour),
		},
		{
			job:     queuedJob{value: delayedJob{Value: 2}},
			readyAt: now.Add(2 * time.Hour),
		},
	} {
		queue.delayedSlots <- struct{}{}
		queue.scheduled <- scheduled
	}
	queue.delayedSlots <- struct{}{}

	sequence := uint64(1)
	queue.drainScheduledJobs(pending, &sequence)

	if sequence != 3 {
		t.Fatalf("sequence = %d, want 3", sequence)
	}
	for want := 1; want <= 3; want++ {
		job := <-queue.jobs
		got := job.value.(delayedJob).Value
		if got != want {
			t.Fatalf("job value = %d, want %d", got, want)
		}
	}
	if len(queue.delayedSlots) != 0 {
		t.Fatalf("delayed slots = %d, want 0", len(queue.delayedSlots))
	}
}

func TestJobQueueAppliesBoundedDelayedBackpressure(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 1, Workers: 1})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := HandleJob(queue, "delayed", func(context.Context, delayedJob) error {
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJobAfter(context.Background(), queue, time.Hour, delayedJob{}); err != nil {
		t.Fatalf("dispatch first: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err = DispatchJobAfter(ctx, queue, time.Hour, delayedJob{})
	if !errors.Is(err, ErrJobDispatchFailed) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dispatch second error = %v", err)
	}
	stopped := make(chan error, 1)
	go func() {
		stopped <- DispatchJobAfter(context.Background(), queue, time.Hour, delayedJob{})
	}()
	select {
	case err := <-stopped:
		t.Fatalf("dispatch did not apply backpressure: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := <-stopped; !errors.Is(err, ErrJobDispatchFailed) ||
		!errors.Is(err, ErrJobQueueStopped) {
		t.Fatalf("dispatch during shutdown error = %v", err)
	}
}

func TestJobQueueRejectsInvalidDelayedDispatch(t *testing.T) {
	if err := DispatchJobAfter(
		context.Background(),
		(*JobQueue)(nil),
		time.Second,
		delayedJob{},
	); !errors.Is(err, ErrJobQueueUnavailable) {
		t.Fatalf("nil queue error = %v", err)
	}
	if err := DispatchJobAt(
		context.Background(),
		(*JobQueue)(nil),
		time.Now().Add(time.Second),
		delayedJob{},
	); !errors.Is(err, ErrJobQueueUnavailable) {
		t.Fatalf("nil queue at error = %v", err)
	}
	queue, err := NewJobQueue(JobQueueOptions{})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := HandleJob(queue, "delayed", func(context.Context, delayedJob) error {
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := DispatchJobAfter(nil, queue, time.Second, delayedJob{}); !errors.Is(
		err,
		ErrJobContextUnavailable,
	) {
		t.Fatalf("nil context error = %v", err)
	}
	if err := DispatchJobAt(
		nil,
		queue,
		time.Now().Add(time.Second),
		delayedJob{},
	); !errors.Is(err, ErrJobContextUnavailable) {
		t.Fatalf("nil context at error = %v", err)
	}
	if err := DispatchJobAfter(
		context.Background(),
		queue,
		-time.Second,
		delayedJob{},
	); !errors.Is(err, ErrJobDispatchFailed) || !errors.Is(err, ErrInvalidJobDelay) {
		t.Fatalf("negative delay error = %v", err)
	}
	if err := DispatchJobAfter(
		context.Background(),
		queue,
		time.Second,
		delayedJob{},
	); !errors.Is(err, ErrJobQueueNotRunning) {
		t.Fatalf("pre-start error = %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJobAfter(
		context.Background(),
		queue,
		time.Second,
		"missing",
	); !errors.Is(err, ErrJobHandlerNotFound) {
		t.Fatalf("missing handler error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := DispatchJobAfter(
		cancelled,
		queue,
		time.Second,
		delayedJob{},
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := DispatchJobAfter(
		context.Background(),
		queue,
		time.Second,
		delayedJob{},
	); !errors.Is(err, ErrJobQueueStopped) {
		t.Fatalf("stopped queue error = %v", err)
	}
}

func TestJobQueueConcurrentDelayedDispatchAndShutdownDoesNotLoseAcceptedJobs(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 16, Workers: 4})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	var handled atomic.Int32
	if err := HandleJob(queue, "delayed", func(context.Context, delayedJob) error {
		handled.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	const dispatches = 100
	start := make(chan struct{})
	var accepted atomic.Int32
	var callers sync.WaitGroup
	callers.Add(dispatches)
	for range dispatches {
		go func() {
			defer callers.Done()
			<-start
			if DispatchJobAfter(
				context.Background(),
				queue,
				time.Hour,
				delayedJob{},
			) == nil {
				accepted.Add(1)
			}
		}()
	}
	close(start)
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	callers.Wait()
	if handled.Load() != accepted.Load() {
		t.Fatalf("handled = %d, accepted = %d", handled.Load(), accepted.Load())
	}
}
