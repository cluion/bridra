package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	ErrJobHandlerNameAlreadyDefined = errors.New("framework: job handler name is already defined")
	ErrJobHandlerRegistrationClosed = errors.New("framework: job handler registration is closed")
	ErrJobHandlerNotFound           = errors.New("framework: job handler is not registered")
	ErrJobQueueNotRunning           = errors.New("framework: job queue is not running")
	ErrJobQueueStopped              = errors.New("framework: job queue has stopped")
	ErrInvalidJobDelay              = errors.New("framework: job delay is invalid")
	ErrJobDispatchFailed            = errors.New("framework: job dispatch failed")
	ErrJobExecutionFailed           = errors.New("framework: job execution failed")
	ErrJobRetriesExhausted          = errors.New("framework: job retries exhausted")
)

const (
	defaultJobQueueCapacity = 64
	defaultJobQueueWorkers  = 1
	defaultJobPollInterval  = 100 * time.Millisecond
	defaultJobLeaseDuration = time.Minute
)

type JobHandler[T any] func(context.Context, T) error

type JobFailure struct {
	JobID       string
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
	Store         JobStore
	PollInterval  time.Duration
	LeaseDuration time.Duration
}

type JobHandlerOptions struct {
	MaxAttempts  int
	RetryBackoff time.Duration
}

func DefaultJobQueueOptions() JobQueueOptions {
	return JobQueueOptions{
		Capacity:      defaultJobQueueCapacity,
		Workers:       defaultJobQueueWorkers,
		JobTimeout:    30 * time.Second,
		PollInterval:  defaultJobPollInterval,
		LeaseDuration: defaultJobLeaseDuration,
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
	options        JobQueueOptions
	handlers       map[reflect.Type]jobHandlerEntry
	handlersByName map[string]jobHandlerEntry
	jobs           chan queuedJob
	scheduled      chan scheduledJob
	delayedSlots   chan struct{}
	scheduleStop   chan struct{}
	scheduleDone   chan struct{}
	stopping       chan struct{}
	shutdownDone   chan struct{}
	wake           chan struct{}
	workerCtx      context.Context
	cancelWorkers  context.CancelFunc
	state          jobQueueState
	workers        sync.WaitGroup
	dispatches     sync.WaitGroup
	mu             sync.Mutex
}

type jobHandlerEntry struct {
	jobType reflect.Type
	name    string
	options JobHandlerOptions
	handle  func(context.Context, any) error
	encode  func(any) (json.RawMessage, error)
	decode  func(json.RawMessage) (any, error)
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
		options:        normalized,
		handlers:       make(map[reflect.Type]jobHandlerEntry),
		handlersByName: make(map[string]jobHandlerEntry),
		jobs:           make(chan queuedJob, normalized.Capacity),
		scheduled:      make(chan scheduledJob, normalized.Capacity),
		delayedSlots:   make(chan struct{}, normalized.Capacity),
		scheduleStop:   make(chan struct{}),
		scheduleDone:   make(chan struct{}),
		stopping:       make(chan struct{}),
		shutdownDone:   make(chan struct{}),
		wake:           make(chan struct{}, 1),
	}, nil
}

func normalizeJobQueueOptions(options JobQueueOptions) (JobQueueOptions, error) {
	if options.Capacity < 0 ||
		options.Workers < 0 ||
		options.JobTimeout < 0 ||
		options.PollInterval < 0 ||
		options.LeaseDuration < 0 {
		return JobQueueOptions{}, ErrInvalidJobQueueOptions
	}
	if options.Capacity == 0 {
		options.Capacity = defaultJobQueueCapacity
	}
	if options.Workers == 0 {
		options.Workers = defaultJobQueueWorkers
	}
	if options.Store != nil {
		if options.JobTimeout == 0 {
			options.JobTimeout = DefaultJobQueueOptions().JobTimeout
		}
		if options.PollInterval == 0 {
			options.PollInterval = defaultJobPollInterval
		}
		if options.LeaseDuration == 0 {
			options.LeaseDuration = defaultJobLeaseDuration
		}
		if options.LeaseDuration <= options.JobTimeout {
			return JobQueueOptions{}, ErrInvalidJobQueueOptions
		}
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
		jobType: jobType,
		name:    name,
		options: normalized,
		handle: func(ctx context.Context, job any) error {
			typed, ok := job.(T)
			if !ok {
				return fmt.Errorf("framework: job %s has an invalid runtime type", jobType)
			}
			return handler(ctx, typed)
		},
		encode: func(job any) (json.RawMessage, error) {
			typed, ok := job.(T)
			if !ok {
				return nil, fmt.Errorf(
					"%w: job %s has an invalid runtime type",
					ErrJobPayloadEncodingFailed,
					jobType,
				)
			}
			payload, err := json.Marshal(typed)
			if err != nil {
				return nil, fmt.Errorf(
					"%w: job %s: %w",
					ErrJobPayloadEncodingFailed,
					jobType,
					err,
				)
			}
			return payload, nil
		},
		decode: func(payload json.RawMessage) (any, error) {
			var typed T
			decoder := json.NewDecoder(bytes.NewReader(payload))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&typed); err != nil {
				return nil, fmt.Errorf(
					"%w: handler %q: %w",
					ErrJobPayloadDecodingFailed,
					name,
					err,
				)
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				return nil, fmt.Errorf(
					"%w: handler %q has trailing payload data",
					ErrJobPayloadDecodingFailed,
					name,
				)
			}
			return typed, nil
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
	if queue.options.Store != nil {
		if registered, exists := queue.handlersByName[name]; exists {
			return fmt.Errorf(
				"%w: handler %q already belongs to %s",
				ErrJobHandlerNameAlreadyDefined,
				name,
				registered.jobType,
			)
		}
		queue.handlersByName[name] = entry
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
	queued, stopping, err := prepareJobDispatch(ctx, queue, job)
	if err != nil {
		return err
	}
	defer queue.dispatches.Done()

	if queue.options.Store != nil {
		return queue.persistJob(ctx, queued, time.Now().UTC())
	}
	select {
	case <-stopping:
		return stoppedJobDispatchError(queued.jobType)
	case <-ctx.Done():
		return contextJobDispatchError(queued.jobType, ctx.Err())
	case queue.jobs <- queued:
		return nil
	}
}

func prepareJobDispatch[T any](
	ctx context.Context,
	queue *JobQueue,
	job T,
) (queuedJob, <-chan struct{}, error) {
	if queue == nil {
		return queuedJob{}, nil, ErrJobQueueUnavailable
	}
	if ctx == nil {
		return queuedJob{}, nil, ErrJobContextUnavailable
	}
	jobType := reflect.TypeFor[T]()

	queue.mu.Lock()
	defer queue.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return queuedJob{}, nil, fmt.Errorf(
			"%w: job %s enqueue context: %w",
			ErrJobDispatchFailed,
			jobType,
			err,
		)
	}
	switch queue.state {
	case jobQueueCollecting:
		return queuedJob{}, nil, fmt.Errorf(
			"%w: job %s: %w",
			ErrJobDispatchFailed,
			jobType,
			ErrJobQueueNotRunning,
		)
	case jobQueueStopping, jobQueueStopped:
		return queuedJob{}, nil, fmt.Errorf(
			"%w: job %s: %w",
			ErrJobDispatchFailed,
			jobType,
			ErrJobQueueStopped,
		)
	}
	handler, exists := queue.handlers[jobType]
	if !exists {
		return queuedJob{}, nil, fmt.Errorf(
			"%w: job %s: %w",
			ErrJobDispatchFailed,
			jobType,
			ErrJobHandlerNotFound,
		)
	}
	queue.dispatches.Add(1)
	return queuedJob{jobType: jobType, value: job, handler: handler}, queue.stopping, nil
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
	if queue.options.Store != nil {
		queue.workerCtx, queue.cancelWorkers = context.WithCancel(context.Background())
	}
	queue.mu.Unlock()

	if queue.options.Store != nil {
		for range workers {
			go queue.workPersistent()
		}
		return nil
	}
	go queue.schedule()
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
		close(queue.scheduleStop)
		close(queue.scheduleDone)
		close(queue.jobs)
		close(queue.shutdownDone)
		queue.mu.Unlock()
		return nil
	}

	queue.state = jobQueueStopping
	close(queue.stopping)
	if queue.cancelWorkers != nil {
		queue.cancelWorkers()
	}
	done := queue.shutdownDone
	queue.mu.Unlock()

	queue.dispatches.Wait()
	if queue.options.Store != nil {
		go queue.finishPersistentShutdown()
		return waitForJobQueueShutdown(ctx, done)
	}
	close(queue.scheduleStop)
	go queue.finishShutdown()
	return waitForJobQueueShutdown(ctx, done)
}

func (queue *JobQueue) finishPersistentShutdown() {
	queue.workers.Wait()
	queue.mu.Lock()
	queue.state = jobQueueStopped
	close(queue.shutdownDone)
	queue.mu.Unlock()
}

func (queue *JobQueue) finishShutdown() {
	<-queue.scheduleDone
	close(queue.jobs)
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
