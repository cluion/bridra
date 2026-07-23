package framework

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type orderedJob struct {
	Value int
}

func TestJobQueueDispatchesTypedJobsInOrderAndDrainsOnShutdown(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 4, Workers: 1})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	handled := []int{}
	if err := HandleJob(queue, "ordered", func(_ context.Context, job orderedJob) error {
		handled = append(handled, job.Value)
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("second start: %v", err)
	}
	for value := 1; value <= 3; value++ {
		if err := DispatchJob(context.Background(), queue, orderedJob{Value: value}); err != nil {
			t.Fatalf("dispatch %d: %v", value, err)
		}
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !reflect.DeepEqual(handled, []int{1, 2, 3}) {
		t.Fatalf("handled = %#v", handled)
	}
	if queue.Running() || !queue.Stopped() {
		t.Fatal("queue should report a stopped state")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := queue.Shutdown(cancelled); err != nil {
		t.Fatalf("completed shutdown: %v", err)
	}
	if err := DispatchJob(context.Background(), queue, orderedJob{}); !errors.Is(err, ErrJobQueueStopped) {
		t.Fatalf("dispatch after shutdown = %v, want ErrJobQueueStopped", err)
	}
}

func TestJobQueueRejectsInvalidOptionsAndHandlerRegistration(t *testing.T) {
	invalidOptions := []JobQueueOptions{
		{Capacity: -1},
		{Workers: -1},
		{JobTimeout: -time.Second},
	}
	for _, options := range invalidOptions {
		if _, err := NewJobQueue(options); !errors.Is(err, ErrInvalidJobQueueOptions) {
			t.Fatalf("options %#v error = %v", options, err)
		}
	}
	queue, err := NewJobQueue(JobQueueOptions{})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := HandleJob[orderedJob](queue, "", func(context.Context, orderedJob) error {
		return nil
	}); !errors.Is(err, ErrInvalidJobHandler) {
		t.Fatalf("empty handler name error = %v", err)
	}
	if err := HandleJob(queue, "ordered", func(context.Context, orderedJob) error {
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := HandleJob(queue, "duplicate", func(context.Context, orderedJob) error {
		return nil
	}); !errors.Is(err, ErrJobHandlerAlreadyDefined) {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := HandleJob(queue, "late", func(context.Context, string) error {
		return nil
	}); !errors.Is(err, ErrJobHandlerRegistrationClosed) {
		t.Fatalf("late registration error = %v", err)
	}
	if err := DispatchJob(context.Background(), queue, "missing"); !errors.Is(err, ErrJobHandlerNotFound) {
		t.Fatalf("missing handler error = %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

type blockingJob struct {
	ID int
}

func TestJobQueueAppliesBoundedBackpressure(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 1, Workers: 1})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var handled atomic.Int32
	if err := HandleJob(queue, "blocking", func(context.Context, blockingJob) error {
		if handled.Add(1) == 1 {
			close(started)
		}
		<-release
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJob(context.Background(), queue, blockingJob{ID: 1}); err != nil {
		t.Fatalf("dispatch first: %v", err)
	}
	<-started
	if err := DispatchJob(context.Background(), queue, blockingJob{ID: 2}); err != nil {
		t.Fatalf("dispatch second: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := DispatchJob(ctx, queue, blockingJob{ID: 3}); !errors.Is(err, ErrJobDispatchFailed) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dispatch third error = %v", err)
	}
	close(release)
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if calls := handled.Load(); calls != 2 {
		t.Fatalf("handled = %d, want 2", calls)
	}
}

type concurrentJob struct{}

func TestJobQueueUsesConfiguredWorkerConcurrency(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 2, Workers: 2})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	if err := HandleJob(queue, "concurrent", func(context.Context, concurrentJob) error {
		started <- struct{}{}
		<-release
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	for range 2 {
		if err := DispatchJob(context.Background(), queue, concurrentJob{}); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("configured workers did not execute concurrently")
		}
	}
	close(release)
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

type timeoutJob struct{}
type panickingJob struct{}

func TestJobQueueReportsTimeoutAndRecoveredPanic(t *testing.T) {
	failures := make(chan JobFailure, 2)
	queue, err := NewJobQueue(JobQueueOptions{
		Capacity:   2,
		Workers:    1,
		JobTimeout: 20 * time.Millisecond,
		ReportFailure: func(failure JobFailure) {
			failures <- failure
		},
	})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := HandleJob(queue, "timeout", func(ctx context.Context, _ timeoutJob) error {
		<-ctx.Done()
		return ctx.Err()
	}); err != nil {
		t.Fatalf("handle timeout: %v", err)
	}
	if err := HandleJob(queue, "panic", func(context.Context, panickingJob) error {
		panic("broken handler")
	}); err != nil {
		t.Fatalf("handle panic: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJob(context.Background(), queue, timeoutJob{}); err != nil {
		t.Fatalf("dispatch timeout: %v", err)
	}
	if err := DispatchJob(context.Background(), queue, panickingJob{}); err != nil {
		t.Fatalf("dispatch panic: %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	timedOut := <-failures
	if timedOut.Handler != "timeout" || timedOut.JobType != reflect.TypeFor[timeoutJob]() {
		t.Fatalf("timeout failure = %#v", timedOut)
	}
	if !errors.Is(timedOut.Err, ErrJobExecutionFailed) ||
		!errors.Is(timedOut.Err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", timedOut.Err)
	}
	panicked := <-failures
	if panicked.Handler != "panic" || panicked.JobType != reflect.TypeFor[panickingJob]() {
		t.Fatalf("panic failure = %#v", panicked)
	}
	if !errors.Is(panicked.Err, ErrJobExecutionFailed) ||
		!strings.Contains(panicked.Err.Error(), "broken handler") {
		t.Fatalf("panic error = %v", panicked.Err)
	}
}

func TestJobQueueShutdownTimeoutContinuesDraining(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 1, Workers: 1})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	if err := HandleJob(queue, "blocking", func(context.Context, blockingJob) error {
		close(started)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJob(context.Background(), queue, blockingJob{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := queue.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want context deadline", err)
	}
	if err := DispatchJob(context.Background(), queue, blockingJob{}); !errors.Is(err, ErrJobQueueStopped) {
		t.Fatalf("dispatch during shutdown = %v", err)
	}
	close(release)
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("wait for shutdown: %v", err)
	}
	if !queue.Stopped() {
		t.Fatal("queue should finish draining after the first caller times out")
	}
}

func TestJobQueueConcurrentDispatchAndShutdownDoesNotLoseAcceptedJobs(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 8, Workers: 4})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	var handled atomic.Int32
	if err := HandleJob(queue, "concurrent", func(context.Context, concurrentJob) error {
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
			if DispatchJob(context.Background(), queue, concurrentJob{}) == nil {
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

func TestJobQueueUnavailableAndPreStartErrors(t *testing.T) {
	if err := HandleJob[orderedJob](nil, "ordered", func(context.Context, orderedJob) error {
		return nil
	}); !errors.Is(err, ErrJobQueueUnavailable) {
		t.Fatalf("nil handle error = %v", err)
	}
	if err := DispatchJob(context.Background(), (*JobQueue)(nil), orderedJob{}); !errors.Is(err, ErrJobQueueUnavailable) {
		t.Fatalf("nil dispatch error = %v", err)
	}
	queue, err := NewJobQueue(JobQueueOptions{})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := DispatchJob(context.Background(), queue, orderedJob{}); !errors.Is(err, ErrJobQueueNotRunning) {
		t.Fatalf("pre-start dispatch error = %v", err)
	}
	if err := queue.Shutdown(nil); !errors.Is(err, ErrJobContextUnavailable) {
		t.Fatalf("nil shutdown context error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := queue.Shutdown(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled shutdown error = %v", err)
	}
	if queue.Stopped() {
		t.Fatal("rejected shutdown should not transition the queue")
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
