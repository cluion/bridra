package framework

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type retryJob struct{}

func TestJobQueueRetriesUntilHandlerSucceeds(t *testing.T) {
	var attempts atomic.Int32
	var reported atomic.Int32
	queue, err := NewJobQueue(JobQueueOptions{
		ReportFailure: func(JobFailure) {
			reported.Add(1)
		},
	})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := HandleJobWithOptions(
		queue,
		"retry.success",
		JobHandlerOptions{MaxAttempts: 3},
		func(context.Context, retryJob) error {
			if attempts.Add(1) < 3 {
				return errors.New("temporary failure")
			}
			return nil
		},
	); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJob(context.Background(), queue, retryJob{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
	if reported.Load() != 0 {
		t.Fatalf("reported failures = %d, want 0", reported.Load())
	}
}

func TestJobQueueReportsRetryExhaustionAndOriginalCause(t *testing.T) {
	providerError := errors.New("provider unavailable")
	failures := make(chan JobFailure, 1)
	var attempts atomic.Int32
	queue, err := NewJobQueue(JobQueueOptions{
		ReportFailure: func(failure JobFailure) {
			failures <- failure
		},
	})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := HandleJobWithOptions(
		queue,
		"retry.exhausted",
		JobHandlerOptions{MaxAttempts: 3},
		func(context.Context, retryJob) error {
			attempts.Add(1)
			return providerError
		},
	); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJob(context.Background(), queue, retryJob{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	failure := <-failures
	if failure.Attempts != 3 || failure.MaxAttempts != 3 || attempts.Load() != 3 {
		t.Fatalf("failure = %#v, attempts = %d", failure, attempts.Load())
	}
	if !errors.Is(failure.Err, ErrJobRetriesExhausted) ||
		!errors.Is(failure.Err, ErrJobExecutionFailed) ||
		!errors.Is(failure.Err, providerError) {
		t.Fatalf("failure error = %v", failure.Err)
	}
}

type retryPanicJob struct{}

func TestJobQueueRetriesRecoveredHandlerPanic(t *testing.T) {
	var attempts atomic.Int32
	queue, err := NewJobQueue(JobQueueOptions{})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := HandleJobWithOptions(
		queue,
		"retry.panic",
		JobHandlerOptions{MaxAttempts: 2},
		func(context.Context, retryPanicJob) error {
			if attempts.Add(1) == 1 {
				panic("temporary panic")
			}
			return nil
		},
	); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJob(context.Background(), queue, retryPanicJob{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

type retryTimeoutJob struct{}

func TestJobQueueAppliesTimeoutToEachRetryAttempt(t *testing.T) {
	failures := make(chan JobFailure, 1)
	var attempts atomic.Int32
	queue, err := NewJobQueue(JobQueueOptions{
		JobTimeout: 10 * time.Millisecond,
		ReportFailure: func(failure JobFailure) {
			failures <- failure
		},
	})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := HandleJobWithOptions(
		queue,
		"retry.timeout",
		JobHandlerOptions{MaxAttempts: 2},
		func(ctx context.Context, _ retryTimeoutJob) error {
			attempts.Add(1)
			<-ctx.Done()
			return ctx.Err()
		},
	); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJob(context.Background(), queue, retryTimeoutJob{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	failure := <-failures
	if attempts.Load() != 2 || failure.Attempts != 2 {
		t.Fatalf("attempts = %d, failure = %#v", attempts.Load(), failure)
	}
	if !errors.Is(failure.Err, context.DeadlineExceeded) ||
		!errors.Is(failure.Err, ErrJobRetriesExhausted) {
		t.Fatalf("failure error = %v", failure.Err)
	}
}

type retryDrainJob struct{}

func TestJobQueueShutdownWaitsForRetryBackoffAndSuccessfulDrain(t *testing.T) {
	firstAttempt := make(chan struct{})
	var attempts atomic.Int32
	queue, err := NewJobQueue(JobQueueOptions{})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := HandleJobWithOptions(
		queue,
		"retry.drain",
		JobHandlerOptions{MaxAttempts: 2, RetryBackoff: 100 * time.Millisecond},
		func(context.Context, retryDrainJob) error {
			if attempts.Add(1) == 1 {
				close(firstAttempt)
				return errors.New("retry")
			}
			return nil
		},
	); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJob(context.Background(), queue, retryDrainJob{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	<-firstAttempt

	short, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if err := queue.Shutdown(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("short shutdown error = %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("drain shutdown: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

func TestJobQueueRejectsInvalidRetryOptions(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	cases := []JobHandlerOptions{
		{MaxAttempts: -1},
		{RetryBackoff: -time.Millisecond},
	}
	for _, options := range cases {
		err := HandleJobWithOptions(
			queue,
			"invalid",
			options,
			func(context.Context, retryJob) error { return nil },
		)
		if !errors.Is(err, ErrInvalidJobHandlerOptions) {
			t.Fatalf("options %#v error = %v", options, err)
		}
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestJobQueueReportsFinalRecoveredPanicAfterRetries(t *testing.T) {
	failures := make(chan JobFailure, 1)
	queue, err := NewJobQueue(JobQueueOptions{
		ReportFailure: func(failure JobFailure) { failures <- failure },
	})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := HandleJobWithOptions(
		queue,
		"retry.final-panic",
		JobHandlerOptions{MaxAttempts: 2},
		func(context.Context, retryPanicJob) error { panic("still broken") },
	); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJob(context.Background(), queue, retryPanicJob{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	failure := <-failures
	if failure.Attempts != 2 || !strings.Contains(failure.Err.Error(), "still broken") {
		t.Fatalf("failure = %#v", failure)
	}
}
