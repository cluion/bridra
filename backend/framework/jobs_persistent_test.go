package framework

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type persistentQueueJob struct {
	Value string `json:"value"`
}

func TestPersistentJobQueueSurvivesRestartAndPreservesDelay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.log")
	firstStore := newTestFileJobStore(t, path, FileJobStoreOptions{})
	firstQueue := newTestPersistentQueue(t, firstStore, nil)
	var ranBeforeRestart atomic.Bool
	if err := HandleJob(
		firstQueue,
		"persistent.restart",
		func(context.Context, persistentQueueJob) error {
			ranBeforeRestart.Store(true)
			return nil
		},
	); err != nil {
		t.Fatalf("handle first queue: %v", err)
	}
	if err := firstQueue.Start(); err != nil {
		t.Fatalf("start first queue: %v", err)
	}
	readyAt := time.Now().Add(150 * time.Millisecond)
	if err := DispatchJobAt(
		context.Background(),
		firstQueue,
		readyAt,
		persistentQueueJob{Value: "persisted"},
	); err != nil {
		t.Fatalf("dispatch delayed: %v", err)
	}
	if err := firstQueue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown first queue: %v", err)
	}
	if ranBeforeRestart.Load() {
		t.Fatal("delayed job ran before restart")
	}
	if err := firstStore.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	secondStore := newTestFileJobStore(t, path, FileJobStoreOptions{})
	handled := make(chan persistentQueueJob, 1)
	secondQueue := newTestPersistentQueue(t, secondStore, nil)
	if err := HandleJob(
		secondQueue,
		"persistent.restart",
		func(_ context.Context, job persistentQueueJob) error {
			handled <- job
			return nil
		},
	); err != nil {
		t.Fatalf("handle second queue: %v", err)
	}
	if err := secondQueue.Start(); err != nil {
		t.Fatalf("start second queue: %v", err)
	}
	select {
	case job := <-handled:
		if job.Value != "persisted" {
			t.Fatalf("handled job = %#v", job)
		}
		if time.Now().Before(readyAt) {
			t.Fatal("delayed job ran before its persisted ready time")
		}
	case <-time.After(time.Second):
		t.Fatal("persisted job did not run after restart")
	}
	if err := secondQueue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown second queue: %v", err)
	}
	if err := secondStore.Close(); err != nil {
		t.Fatalf("close second store: %v", err)
	}
}

func TestPersistentJobQueuePersistsRetryAttemptsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.log")
	firstFileStore := newTestFileJobStore(t, path, FileJobStoreOptions{})
	released := make(chan struct{})
	firstStore := &releaseNotifyingJobStore{
		JobStore: firstFileStore,
		released: released,
	}
	firstQueue := newTestPersistentQueue(t, firstStore, nil)
	if err := HandleJobWithOptions(
		firstQueue,
		"persistent.retry",
		JobHandlerOptions{
			MaxAttempts:  2,
			RetryBackoff: 250 * time.Millisecond,
		},
		func(context.Context, persistentQueueJob) error {
			return errors.New("retry after restart")
		},
	); err != nil {
		t.Fatalf("handle first queue: %v", err)
	}
	if err := firstQueue.Start(); err != nil {
		t.Fatalf("start first queue: %v", err)
	}
	if err := DispatchJob(
		context.Background(),
		firstQueue,
		persistentQueueJob{Value: "retry"},
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("first attempt was not released")
	}
	if err := firstQueue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown first queue: %v", err)
	}
	if err := firstFileStore.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	secondFileStore := newTestFileJobStore(t, path, FileJobStoreOptions{})
	reserved := make(chan JobReservation, 1)
	secondStore := &reservationNotifyingJobStore{
		JobStore: secondFileStore,
		reserved: reserved,
	}
	handled := make(chan struct{})
	secondQueue := newTestPersistentQueue(t, secondStore, nil)
	if err := HandleJobWithOptions(
		secondQueue,
		"persistent.retry",
		JobHandlerOptions{
			MaxAttempts:  2,
			RetryBackoff: 250 * time.Millisecond,
		},
		func(context.Context, persistentQueueJob) error {
			close(handled)
			return nil
		},
	); err != nil {
		t.Fatalf("handle second queue: %v", err)
	}
	if err := secondQueue.Start(); err != nil {
		t.Fatalf("start second queue: %v", err)
	}
	select {
	case reservation := <-reserved:
		if reservation.Job.Attempts != 2 {
			t.Fatalf("attempts after restart = %d", reservation.Job.Attempts)
		}
	case <-time.After(time.Second):
		t.Fatal("released job was not reserved after restart")
	}
	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("released job was not handled after restart")
	}
	if err := secondQueue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown second queue: %v", err)
	}
	if err := secondFileStore.Close(); err != nil {
		t.Fatalf("close second store: %v", err)
	}
}

func TestPersistentJobQueueRetainsExhaustedFailureAndRetriesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.log")
	store := newTestFileJobStore(t, path, FileJobStoreOptions{})
	failures := make(chan JobFailure, 1)
	var succeed atomic.Bool
	handled := make(chan struct{}, 1)
	queue := newTestPersistentQueue(t, store, func(failure JobFailure) {
		failures <- failure
	})
	if err := HandleJobWithOptions(
		queue,
		"persistent.failure",
		JobHandlerOptions{MaxAttempts: 2, RetryBackoff: time.Millisecond},
		func(context.Context, persistentQueueJob) error {
			if !succeed.Load() {
				return errors.New("still failing")
			}
			handled <- struct{}{}
			return nil
		},
	); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := DispatchJob(
		context.Background(),
		queue,
		persistentQueueJob{Value: "failed"},
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var failure JobFailure
	select {
	case failure = <-failures:
	case <-time.After(time.Second):
		t.Fatal("exhausted failure was not reported")
	}
	if failure.JobID == "" ||
		failure.Handler != "persistent.failure" ||
		failure.Attempts != 2 ||
		failure.MaxAttempts != 2 ||
		!errors.Is(failure.Err, ErrJobRetriesExhausted) ||
		!errors.Is(failure.Err, ErrJobExecutionFailed) {
		t.Fatalf("failure = %#v", failure)
	}
	failed := store.FailedJobs()
	if len(failed) != 1 ||
		failed[0].Job.ID != failure.JobID ||
		failed[0].Job.Attempts != 2 {
		t.Fatalf("failed jobs = %#v", failed)
	}

	succeed.Store(true)
	if err := store.RetryFailed(
		context.Background(),
		failure.JobID,
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("retry failed job: %v", err)
	}
	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("retried failed job was not handled")
	}
	if failed := store.FailedJobs(); len(failed) != 0 {
		t.Fatalf("failed jobs after successful retry = %#v", failed)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func TestPersistentJobQueueRejectsInvalidOptionsNamesAndPayloads(t *testing.T) {
	store := newTestFileJobStore(
		t,
		filepath.Join(t.TempDir(), "jobs.log"),
		FileJobStoreOptions{},
	)
	for _, options := range []JobQueueOptions{
		{
			Store:         store,
			JobTimeout:    time.Second,
			LeaseDuration: time.Second,
		},
		{Store: store, PollInterval: -time.Second},
	} {
		if _, err := NewJobQueue(options); !errors.Is(
			err,
			ErrInvalidJobQueueOptions,
		) {
			t.Fatalf("options %#v error = %v", options, err)
		}
	}

	queue := newTestPersistentQueue(t, store, nil)
	if err := HandleJob(
		queue,
		"duplicate.name",
		func(context.Context, persistentQueueJob) error { return nil },
	); err != nil {
		t.Fatalf("handle first: %v", err)
	}
	if err := HandleJob(
		queue,
		"duplicate.name",
		func(context.Context, string) error { return nil },
	); !errors.Is(err, ErrJobHandlerNameAlreadyDefined) {
		t.Fatalf("duplicate name error = %v", err)
	}
	type invalidPersistentJob struct {
		Channel chan int `json:"channel"`
	}
	if err := HandleJob(
		queue,
		"invalid.payload",
		func(context.Context, invalidPersistentJob) error { return nil },
	); err != nil {
		t.Fatalf("handle invalid payload: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	err := DispatchJob(
		context.Background(),
		queue,
		invalidPersistentJob{Channel: make(chan int)},
	)
	if !errors.Is(err, ErrJobDispatchFailed) ||
		!errors.Is(err, ErrJobPayloadEncodingFailed) {
		t.Fatalf("invalid payload dispatch error = %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func TestPersistentJobQueueFailsStoredJobWithMissingHandler(t *testing.T) {
	store := newTestFileJobStore(
		t,
		filepath.Join(t.TempDir(), "jobs.log"),
		FileJobStoreOptions{},
	)
	now := time.Now().UTC()
	stored := testStoredJob(t, "missing.handler", now, now)
	if err := store.Enqueue(context.Background(), stored); err != nil {
		t.Fatalf("enqueue missing handler: %v", err)
	}
	failures := make(chan JobFailure, 1)
	queue := newTestPersistentQueue(t, store, func(failure JobFailure) {
		failures <- failure
	})
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case failure := <-failures:
		if failure.JobID != stored.ID ||
			failure.Handler != "missing.handler" ||
			!errors.Is(failure.Err, ErrJobHandlerNotFound) {
			t.Fatalf("failure = %#v", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("missing handler failure was not reported")
	}
	if failed := store.FailedJobs(); len(failed) != 1 {
		t.Fatalf("failed jobs = %#v", failed)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func TestPersistentJobQueueReportsStoreCompletionAndFailureErrors(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		operation string
		options   JobHandlerOptions
		handler   JobHandler[persistentQueueJob]
	}{
		{
			name:      "complete",
			operation: "complete",
			handler: func(context.Context, persistentQueueJob) error {
				return nil
			},
		},
		{
			name:      "fail",
			operation: "fail",
			handler: func(context.Context, persistentQueueJob) error {
				return errors.New("handler failed")
			},
		},
		{
			name:      "release",
			operation: "release",
			options: JobHandlerOptions{
				MaxAttempts:  2,
				RetryBackoff: time.Millisecond,
			},
			handler: func(context.Context, persistentQueueJob) error {
				return errors.New("handler failed")
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fileStore := newTestFileJobStore(
				t,
				filepath.Join(t.TempDir(), "jobs.log"),
				FileJobStoreOptions{},
			)
			storeError := errors.New("store failed")
			store := &operationFailingJobStore{
				JobStore:  fileStore,
				operation: testCase.operation,
				err:       storeError,
			}
			failures := make(chan JobFailure, 1)
			queue := newTestPersistentQueue(t, store, func(failure JobFailure) {
				failures <- failure
			})
			if err := HandleJobWithOptions(
				queue,
				"persistent.store-error",
				testCase.options,
				testCase.handler,
			); err != nil {
				t.Fatalf("handle: %v", err)
			}
			if err := queue.Start(); err != nil {
				t.Fatalf("start: %v", err)
			}
			if err := DispatchJob(
				context.Background(),
				queue,
				persistentQueueJob{Value: testCase.name},
			); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			select {
			case failure := <-failures:
				if failure.JobID == "" ||
					!errors.Is(failure.Err, ErrJobStoreOperationFailed) ||
					!errors.Is(failure.Err, storeError) {
					t.Fatalf("failure = %#v", failure)
				}
			case <-time.After(time.Second):
				t.Fatal("store operation failure was not reported")
			}
			if err := queue.Shutdown(context.Background()); err != nil {
				t.Fatalf("shutdown: %v", err)
			}
			if err := fileStore.Close(); err != nil {
				t.Fatalf("close store: %v", err)
			}
		})
	}
}

func newTestPersistentQueue(
	t *testing.T,
	store JobStore,
	reporter JobFailureReporter,
) *JobQueue {
	t.Helper()
	queue, err := NewJobQueue(JobQueueOptions{
		Workers:       1,
		JobTimeout:    20 * time.Millisecond,
		ReportFailure: reporter,
		Store:         store,
		PollInterval:  5 * time.Millisecond,
		LeaseDuration: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new persistent queue: %v", err)
	}
	return queue
}

type releaseNotifyingJobStore struct {
	JobStore
	released chan struct{}
	once     sync.Once
}

func (store *releaseNotifyingJobStore) Release(
	ctx context.Context,
	reservation JobReservation,
	availableAt time.Time,
	lastError string,
) error {
	err := store.JobStore.Release(ctx, reservation, availableAt, lastError)
	if err == nil {
		store.once.Do(func() {
			close(store.released)
		})
	}
	return err
}

type reservationNotifyingJobStore struct {
	JobStore
	reserved chan JobReservation
	once     sync.Once
}

func (store *reservationNotifyingJobStore) Reserve(
	ctx context.Context,
	now time.Time,
	lease time.Duration,
) (JobReservation, error) {
	reservation, err := store.JobStore.Reserve(ctx, now, lease)
	if err == nil {
		store.once.Do(func() {
			store.reserved <- reservation
		})
	}
	return reservation, err
}

type operationFailingJobStore struct {
	JobStore
	operation string
	err       error
}

func (store *operationFailingJobStore) Complete(
	ctx context.Context,
	reservation JobReservation,
) error {
	if store.operation == "complete" {
		return store.err
	}
	return store.JobStore.Complete(ctx, reservation)
}

func (store *operationFailingJobStore) Fail(
	ctx context.Context,
	reservation JobReservation,
	lastError string,
) error {
	if store.operation == "fail" {
		return store.err
	}
	return store.JobStore.Fail(ctx, reservation, lastError)
}

func (store *operationFailingJobStore) Release(
	ctx context.Context,
	reservation JobReservation,
	availableAt time.Time,
	lastError string,
) error {
	if store.operation == "release" {
		return store.err
	}
	return store.JobStore.Release(ctx, reservation, availableAt, lastError)
}
