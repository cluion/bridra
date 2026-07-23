package framework

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

var (
	ErrJobQueueUnavailable          = errors.New("framework: job queue is unavailable")
	ErrJobContextUnavailable        = errors.New("framework: job context is unavailable")
	ErrInvalidJobQueueOptions       = errors.New("framework: job queue options are invalid")
	ErrInvalidJobHandler            = errors.New("framework: job handler is invalid")
	ErrInvalidJobHandlerOptions     = errors.New("framework: job handler options are invalid")
	ErrJobHandlerAlreadyDefined     = errors.New("framework: job handler is already defined")
	ErrJobHandlerRegistrationClosed = errors.New("framework: job handler registration is closed")
	ErrJobHandlerNotFound           = errors.New("framework: job handler is not registered")
	ErrJobQueueNotRunning           = errors.New("framework: job queue is not running")
	ErrJobQueueStopped              = errors.New("framework: job queue has stopped")
	ErrJobDispatchFailed            = errors.New("framework: job dispatch failed")
	ErrJobExecutionFailed           = errors.New("framework: job execution failed")
	ErrJobRetriesExhausted          = errors.New("framework: job retries exhausted")
)

const (
	defaultJobQueueCapacity = 64
	defaultJobQueueWorkers  = 1
)

type JobHandler[T any] func(context.Context, T) error

type JobFailure struct {
	Handler     string
	JobType     reflect.Type
	Attempts    int
	MaxAttempts int
	Err         error
}

type JobFailureReporter func(JobFailure)

type JobQueueOptions struct {
	Capacity      int
	Workers       int
	JobTimeout    time.Duration
	ReportFailure JobFailureReporter
}

type JobHandlerOptions struct {
	MaxAttempts  int
	RetryBackoff time.Duration
}

func DefaultJobQueueOptions() JobQueueOptions {
	return JobQueueOptions{
		Capacity:   defaultJobQueueCapacity,
		Workers:    defaultJobQueueWorkers,
		JobTimeout: 30 * time.Second,
	}
}

type jobQueueState uint8

const (
	jobQueueCollecting jobQueueState = iota
	jobQueueRunning
	jobQueueStopping
	jobQueueStopped
)

type JobQueue struct {
	options      JobQueueOptions
	handlers     map[reflect.Type]jobHandlerEntry
	jobs         chan queuedJob
	stopping     chan struct{}
	shutdownDone chan struct{}
	state        jobQueueState
	workers      sync.WaitGroup
	dispatches   sync.WaitGroup
	mu           sync.Mutex
}

type jobHandlerEntry struct {
	name    string
	options JobHandlerOptions
	handle  func(context.Context, any) error
}

type queuedJob struct {
	jobType reflect.Type
	value   any
	handler jobHandlerEntry
}

func NewJobQueue(options JobQueueOptions) (*JobQueue, error) {
	normalized, err := normalizeJobQueueOptions(options)
	if err != nil {
		return nil, err
	}
	return &JobQueue{
		options:      normalized,
		handlers:     make(map[reflect.Type]jobHandlerEntry),
		jobs:         make(chan queuedJob, normalized.Capacity),
		stopping:     make(chan struct{}),
		shutdownDone: make(chan struct{}),
	}, nil
}

func normalizeJobQueueOptions(options JobQueueOptions) (JobQueueOptions, error) {
	if options.Capacity < 0 || options.Workers < 0 || options.JobTimeout < 0 {
		return JobQueueOptions{}, ErrInvalidJobQueueOptions
	}
	if options.Capacity == 0 {
		options.Capacity = defaultJobQueueCapacity
	}
	if options.Workers == 0 {
		options.Workers = defaultJobQueueWorkers
	}
	return options, nil
}

func HandleJob[T any](queue *JobQueue, name string, handler JobHandler[T]) error {
	return HandleJobWithOptions(queue, name, JobHandlerOptions{}, handler)
}

func HandleJobWithOptions[T any](
	queue *JobQueue,
	name string,
	options JobHandlerOptions,
	handler JobHandler[T],
) error {
	if queue == nil {
		return ErrJobQueueUnavailable
	}
	name = strings.TrimSpace(name)
	if name == "" || handler == nil {
		return ErrInvalidJobHandler
	}
	normalized, err := normalizeJobHandlerOptions(options)
	if err != nil {
		return err
	}
	jobType := reflect.TypeFor[T]()
	entry := jobHandlerEntry{
		name:    name,
		options: normalized,
		handle: func(ctx context.Context, job any) error {
			typed, ok := job.(T)
			if !ok {
				return fmt.Errorf("framework: job %s has an invalid runtime type", jobType)
			}
			return handler(ctx, typed)
		},
	}

	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.state != jobQueueCollecting {
		return ErrJobHandlerRegistrationClosed
	}
	if registered, exists := queue.handlers[jobType]; exists {
		return fmt.Errorf(
			"%w: %s handler %q",
			ErrJobHandlerAlreadyDefined,
			jobType,
			registered.name,
		)
	}
	queue.handlers[jobType] = entry
	return nil
}

func normalizeJobHandlerOptions(options JobHandlerOptions) (JobHandlerOptions, error) {
	if options.MaxAttempts < 0 || options.RetryBackoff < 0 {
		return JobHandlerOptions{}, ErrInvalidJobHandlerOptions
	}
	if options.MaxAttempts == 0 {
		options.MaxAttempts = 1
	}
	return options, nil
}

func DispatchJob[T any](ctx context.Context, queue *JobQueue, job T) error {
	if queue == nil {
		return ErrJobQueueUnavailable
	}
	if ctx == nil {
		return ErrJobContextUnavailable
	}
	jobType := reflect.TypeFor[T]()

	queue.mu.Lock()
	if err := ctx.Err(); err != nil {
		queue.mu.Unlock()
		return fmt.Errorf("%w: job %s enqueue context: %w", ErrJobDispatchFailed, jobType, err)
	}
	switch queue.state {
	case jobQueueCollecting:
		queue.mu.Unlock()
		return fmt.Errorf("%w: job %s: %w", ErrJobDispatchFailed, jobType, ErrJobQueueNotRunning)
	case jobQueueStopping, jobQueueStopped:
		queue.mu.Unlock()
		return fmt.Errorf("%w: job %s: %w", ErrJobDispatchFailed, jobType, ErrJobQueueStopped)
	}
	handler, exists := queue.handlers[jobType]
	if !exists {
		queue.mu.Unlock()
		return fmt.Errorf("%w: job %s: %w", ErrJobDispatchFailed, jobType, ErrJobHandlerNotFound)
	}
	queued := queuedJob{jobType: jobType, value: job, handler: handler}
	jobs := queue.jobs
	stopping := queue.stopping
	queue.dispatches.Add(1)
	queue.mu.Unlock()
	defer queue.dispatches.Done()

	select {
	case <-stopping:
		return fmt.Errorf("%w: job %s: %w", ErrJobDispatchFailed, jobType, ErrJobQueueStopped)
	case <-ctx.Done():
		return fmt.Errorf("%w: job %s enqueue context: %w", ErrJobDispatchFailed, jobType, ctx.Err())
	case jobs <- queued:
		return nil
	}
}

func (queue *JobQueue) Start() error {
	if queue == nil {
		return ErrJobQueueUnavailable
	}
	queue.mu.Lock()
	switch queue.state {
	case jobQueueRunning:
		queue.mu.Unlock()
		return nil
	case jobQueueStopping, jobQueueStopped:
		queue.mu.Unlock()
		return ErrJobQueueStopped
	}
	queue.state = jobQueueRunning
	workers := queue.options.Workers
	queue.workers.Add(workers)
	queue.mu.Unlock()

	for range workers {
		go queue.work()
	}
	return nil
}

func (queue *JobQueue) work() {
	defer queue.workers.Done()
	for job := range queue.jobs {
		queue.execute(job)
	}
}

func (queue *JobQueue) execute(job queuedJob) {
	var executionError error
	for attempt := 1; attempt <= job.handler.options.MaxAttempts; attempt++ {
		executionError = queue.executeAttempt(job)
		if executionError == nil {
			return
		}
		if attempt < job.handler.options.MaxAttempts {
			waitForJobRetry(job.handler.options.RetryBackoff)
		}
	}

	failureError := fmt.Errorf(
		"%w: job %s handler %q after %d attempt(s): %w",
		ErrJobExecutionFailed,
		job.jobType,
		job.handler.name,
		job.handler.options.MaxAttempts,
		executionError,
	)
	if job.handler.options.MaxAttempts > 1 {
		failureError = fmt.Errorf("%w: %w", ErrJobRetriesExhausted, failureError)
	}
	queue.report(JobFailure{
		Handler:     job.handler.name,
		JobType:     job.jobType,
		Attempts:    job.handler.options.MaxAttempts,
		MaxAttempts: job.handler.options.MaxAttempts,
		Err:         failureError,
	})
}

func (queue *JobQueue) executeAttempt(job queuedJob) error {
	ctx := context.Background()
	cancel := func() {}
	if queue.options.JobTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, queue.options.JobTimeout)
	}
	defer cancel()

	var executionError error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				executionError = fmt.Errorf("handler panic: %v", recovered)
			}
		}()
		executionError = job.handler.handle(ctx, job.value)
	}()
	return executionError
}

func waitForJobRetry(backoff time.Duration) {
	if backoff <= 0 {
		return
	}
	timer := time.NewTimer(backoff)
	<-timer.C
}

func (queue *JobQueue) report(failure JobFailure) {
	reporter := queue.options.ReportFailure
	if reporter == nil {
		return
	}
	func() {
		defer func() {
			_ = recover()
		}()
		reporter(failure)
	}()
}

func (queue *JobQueue) Shutdown(ctx context.Context) error {
	if queue == nil {
		return ErrJobQueueUnavailable
	}
	if ctx == nil {
		return ErrJobContextUnavailable
	}

	queue.mu.Lock()
	switch queue.state {
	case jobQueueStopped:
		queue.mu.Unlock()
		return nil
	case jobQueueStopping:
		done := queue.shutdownDone
		queue.mu.Unlock()
		return waitForJobQueueShutdown(ctx, done)
	}
	if err := ctx.Err(); err != nil {
		queue.mu.Unlock()
		return err
	}
	if queue.state == jobQueueCollecting {
		queue.state = jobQueueStopped
		close(queue.stopping)
		close(queue.jobs)
		close(queue.shutdownDone)
		queue.mu.Unlock()
		return nil
	}

	queue.state = jobQueueStopping
	close(queue.stopping)
	done := queue.shutdownDone
	queue.mu.Unlock()

	queue.dispatches.Wait()
	close(queue.jobs)
	go queue.finishShutdown()
	return waitForJobQueueShutdown(ctx, done)
}

func (queue *JobQueue) finishShutdown() {
	queue.workers.Wait()
	queue.mu.Lock()
	queue.state = jobQueueStopped
	close(queue.shutdownDone)
	queue.mu.Unlock()
}

func waitForJobQueueShutdown(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (queue *JobQueue) Running() bool {
	if queue == nil {
		return false
	}
	queue.mu.Lock()
	running := queue.state == jobQueueRunning
	queue.mu.Unlock()
	return running
}

func (queue *JobQueue) Stopped() bool {
	if queue == nil {
		return false
	}
	queue.mu.Lock()
	stopped := queue.state == jobQueueStopped
	queue.mu.Unlock()
	return stopped
}
