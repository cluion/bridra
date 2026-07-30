package framework

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSQLJobStorePersistsOrderingFailureAndRetry(t *testing.T) {
	firstStore, secondStore := newTestSQLJobStores(
		t,
		DefaultSQLJobStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	first := testStoredJob(t, "first", now, now)
	second := testStoredJob(t, "second", now.Add(time.Minute), now.Add(time.Second))
	if err := firstStore.Enqueue(ctx, second); err != nil {
		t.Fatalf("enqueue second: %v", err)
	}
	if err := firstStore.Enqueue(ctx, first); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	if err := secondStore.Enqueue(ctx, first); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("duplicate enqueue error = %v", err)
	}

	reservation, err := secondStore.Reserve(ctx, now, time.Minute)
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	if reservation.Job.ID != first.ID || reservation.Job.Attempts != 1 {
		t.Fatalf("first reservation = %#v", reservation)
	}
	invalid := reservation
	invalid.Token = "wrong"
	if err := firstStore.Complete(ctx, invalid); !errors.Is(
		err,
		ErrJobReservationInvalid,
	) {
		t.Fatalf("invalid completion error = %v", err)
	}

	retryAt := now.Add(30 * time.Second)
	if err := firstStore.Release(ctx, reservation, retryAt, "temporary"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := secondStore.Reserve(
		ctx,
		now.Add(15*time.Second),
		time.Minute,
	); !errors.Is(err, ErrJobStoreEmpty) {
		t.Fatalf("early reservation error = %v", err)
	}
	reservation, err = secondStore.Reserve(ctx, retryAt, time.Minute)
	if err != nil {
		t.Fatalf("reserve released: %v", err)
	}
	if reservation.Job.ID != first.ID || reservation.Job.Attempts != 2 {
		t.Fatalf("released reservation = %#v", reservation)
	}
	if err := firstStore.Complete(ctx, reservation); err != nil {
		t.Fatalf("complete first: %v", err)
	}

	reservation, err = firstStore.Reserve(ctx, now.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("reserve second: %v", err)
	}
	if reservation.Job.ID != second.ID {
		t.Fatalf("second reservation = %#v", reservation)
	}
	if err := secondStore.Fail(ctx, reservation, "permanent"); err != nil {
		t.Fatalf("fail second: %v", err)
	}
	failed, err := firstStore.FailedJobs(ctx)
	if err != nil {
		t.Fatalf("failed jobs: %v", err)
	}
	if len(failed) != 1 ||
		failed[0].Job.ID != second.ID ||
		failed[0].Job.Attempts != 1 ||
		failed[0].Error != "permanent" ||
		failed[0].FailedAt.IsZero() {
		t.Fatalf("failed jobs = %#v", failed)
	}

	retryFailedAt := now.Add(2 * time.Minute)
	if err := secondStore.RetryFailed(ctx, second.ID, retryFailedAt); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	reservation, err = firstStore.Reserve(ctx, retryFailedAt, time.Minute)
	if err != nil {
		t.Fatalf("reserve retried: %v", err)
	}
	if reservation.Job.Attempts != 1 {
		t.Fatalf("retried attempts = %d", reservation.Job.Attempts)
	}
	longError := strings.Repeat("界", fileJobStoreMaxErrorBytes)
	if err := firstStore.Fail(ctx, reservation, longError); err != nil {
		t.Fatalf("fail retried: %v", err)
	}
	failed, err = secondStore.FailedJobs(ctx)
	if err != nil {
		t.Fatalf("failed jobs after retry: %v", err)
	}
	if len(failed) != 1 || len(failed[0].Error) > fileJobStoreMaxErrorBytes {
		t.Fatalf("truncated failed jobs = %#v", failed)
	}
	if err := firstStore.ForgetFailed(ctx, second.ID); err != nil {
		t.Fatalf("forget failed: %v", err)
	}
	failed, err = secondStore.FailedJobs(ctx)
	if err != nil || len(failed) != 0 {
		t.Fatalf("failed jobs after forget = %#v, %v", failed, err)
	}
}

func TestSQLJobStoreRecoversLeaseAcrossStores(t *testing.T) {
	firstStore, secondStore := newTestSQLJobStores(
		t,
		DefaultSQLJobStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC)
	job := testStoredJob(t, "recover", now, now)
	if err := firstStore.Enqueue(ctx, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	first, err := firstStore.Reserve(ctx, now, time.Minute)
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	if _, err := secondStore.Reserve(
		ctx,
		now.Add(30*time.Second),
		time.Minute,
	); !errors.Is(err, ErrJobStoreEmpty) {
		t.Fatalf("active lease reservation error = %v", err)
	}
	recovered, err := secondStore.Reserve(
		ctx,
		now.Add(2*time.Minute),
		time.Minute,
	)
	if err != nil {
		t.Fatalf("reserve recovered: %v", err)
	}
	if recovered.Job.ID != first.Job.ID ||
		recovered.Token == first.Token ||
		recovered.Job.Attempts != 2 {
		t.Fatalf("recovered reservation = %#v", recovered)
	}
	if err := secondStore.Complete(ctx, recovered); err != nil {
		t.Fatalf("complete recovered: %v", err)
	}
	if _, err := firstStore.Reserve(
		ctx,
		now.Add(3*time.Minute),
		time.Minute,
	); !errors.Is(err, ErrJobStoreEmpty) {
		t.Fatalf("empty reservation error = %v", err)
	}
}

func TestSQLJobStoreReservesOneJobAcrossConcurrentStores(t *testing.T) {
	firstStore, secondStore := newTestSQLJobStores(
		t,
		DefaultSQLJobStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	job := testStoredJob(t, "atomic", now, now)
	if err := firstStore.Enqueue(ctx, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	const contenders = 24
	start := make(chan struct{})
	results := make(chan struct {
		reservation JobReservation
		err         error
	}, contenders)
	var wait sync.WaitGroup
	for index := range contenders {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			store := firstStore
			if index%2 == 1 {
				store = secondStore
			}
			reservation, err := store.Reserve(ctx, now, time.Minute)
			results <- struct {
				reservation JobReservation
				err         error
			}{reservation: reservation, err: err}
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)

	var winner JobReservation
	var reserved int
	var empty int
	for result := range results {
		switch {
		case result.err == nil:
			winner = result.reservation
			reserved++
		case errors.Is(result.err, ErrJobStoreEmpty):
			empty++
		default:
			t.Fatalf("reservation error = %v", result.err)
		}
	}
	if reserved != 1 || empty != contenders-1 || winner.Job.ID != job.ID {
		t.Fatalf(
			"reservation results: reserved=%d empty=%d winner=%#v",
			reserved,
			empty,
			winner,
		)
	}
	if err := secondStore.Complete(ctx, winner); err != nil {
		t.Fatalf("complete winner: %v", err)
	}
}

func TestSQLJobStoreCoordinatesPersistentQueues(t *testing.T) {
	firstStore, secondStore := newTestSQLJobStores(
		t,
		DefaultSQLJobStoreOptions(),
	)
	firstQueue := newTestSQLPersistentQueue(t, firstStore)
	secondQueue := newTestSQLPersistentQueue(t, secondStore)
	handled := make(chan struct{}, 2)
	var attempts atomic.Int32
	register := func(queue *JobQueue) {
		t.Helper()
		if err := HandleJob(
			queue,
			"sql.shared",
			func(context.Context, sqlSharedJob) error {
				attempts.Add(1)
				handled <- struct{}{}
				return nil
			},
		); err != nil {
			t.Fatalf("register handler: %v", err)
		}
		if err := queue.Start(); err != nil {
			t.Fatalf("start queue: %v", err)
		}
	}
	register(firstQueue)
	register(secondQueue)

	if err := DispatchJob(
		context.Background(),
		firstQueue,
		sqlSharedJob{Value: "shared"},
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	select {
	case <-handled:
	case <-time.After(2 * time.Second):
		t.Fatal("shared SQL job was not handled")
	}
	select {
	case <-handled:
		t.Fatal("shared SQL job was handled twice")
	case <-time.After(100 * time.Millisecond):
	}
	if attempts.Load() != 1 {
		t.Fatalf("handler attempts = %d", attempts.Load())
	}

	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := firstQueue.Shutdown(shutdown); err != nil {
		t.Fatalf("shutdown first queue: %v", err)
	}
	if err := secondQueue.Shutdown(shutdown); err != nil {
		t.Fatalf("shutdown second queue: %v", err)
	}
	if err := firstStore.pool.PingContext(context.Background()); err != nil {
		t.Fatalf("queue shutdown closed shared database pool: %v", err)
	}
}

func TestSQLJobStoreOptionsValidationAndDollarPlaceholders(t *testing.T) {
	if _, err := NewSQLJobStore(nil, SQLJobStoreOptions{}); !errors.Is(
		err,
		ErrJobStoreUnavailable,
	) {
		t.Fatalf("nil pool error = %v", err)
	}
	database := openTestSQLJobDatabase(t, filepath.Join(t.TempDir(), "options.db"))
	for _, options := range []SQLJobStoreOptions{
		{Table: "invalid-name"},
		{PlaceholderStyle: "invalid"},
		{MaxPayloadBytes: -1},
	} {
		if _, err := NewSQLJobStore(database, options); !errors.Is(
			err,
			ErrInvalidSQLJobStoreOptions,
		) {
			t.Fatalf("options %#v error = %v", options, err)
		}
	}

	options := DefaultSQLJobStoreOptions()
	options.Table = "dollar_jobs"
	options.PlaceholderStyle = SQLPlaceholderDollar
	options.MaxPayloadBytes = 32
	store, err := NewSQLJobStore(database, options)
	if err != nil {
		t.Fatalf("new dollar store: %v", err)
	}
	if store.Table() != "dollar_jobs" {
		t.Fatalf("table = %q", store.Table())
	}
	if err := store.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure dollar store: %v", err)
	}
	if err := store.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure dollar store twice: %v", err)
	}
	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	job := testStoredJob(t, "dollar", now, now)
	if err := store.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("enqueue dollar store: %v", err)
	}
	reservation, err := store.Reserve(context.Background(), now, time.Minute)
	if err != nil {
		t.Fatalf("reserve dollar store: %v", err)
	}
	if err := store.Complete(context.Background(), reservation); err != nil {
		t.Fatalf("complete dollar store: %v", err)
	}
}

func TestSQLJobStoreRejectsInvalidCallsAndDatabaseRows(t *testing.T) {
	database := openTestSQLJobDatabase(t, filepath.Join(t.TempDir(), "invalid.db"))
	options := DefaultSQLJobStoreOptions()
	store, err := NewSQLJobStore(database, options)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	ctx := context.Background()
	if err := store.Ensure(ctx); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	now := time.Date(2026, 7, 30, 5, 0, 0, 0, time.UTC)
	if err := store.Enqueue(ctx, StoredJob{}); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("invalid job error = %v", err)
	}
	oversized := testStoredJob(t, "oversized", now, now)
	oversized.Payload = json.RawMessage(`{"payload":"this is larger than eight bytes"}`)
	store.maxPayloadBytes = 8
	if err := store.Enqueue(ctx, oversized); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("oversized job error = %v", err)
	}
	store.maxPayloadBytes = defaultFileJobStoreMaxPayloadBytes
	if _, err := store.Reserve(ctx, time.Time{}, time.Minute); !errors.Is(
		err,
		ErrJobReservationInvalid,
	) {
		t.Fatalf("invalid reservation time error = %v", err)
	}
	if _, err := store.Reserve(ctx, now, 0); !errors.Is(
		err,
		ErrJobReservationInvalid,
	) {
		t.Fatalf("invalid reservation lease error = %v", err)
	}
	if err := store.Release(
		ctx,
		JobReservation{},
		time.Time{},
		"",
	); !errors.Is(err, ErrJobReservationInvalid) {
		t.Fatalf("invalid release error = %v", err)
	}
	if err := store.Complete(ctx, JobReservation{}); !errors.Is(
		err,
		ErrJobReservationInvalid,
	) {
		t.Fatalf("invalid complete error = %v", err)
	}
	if err := store.Fail(ctx, JobReservation{}, "failed"); !errors.Is(
		err,
		ErrJobReservationInvalid,
	) {
		t.Fatalf("invalid fail error = %v", err)
	}
	if err := store.RetryFailed(ctx, "", now); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("invalid retry error = %v", err)
	}
	if err := store.RetryFailed(ctx, "missing", now); !errors.Is(
		err,
		ErrJobStoreConflict,
	) {
		t.Fatalf("missing retry error = %v", err)
	}
	if err := store.ForgetFailed(ctx, ""); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("invalid forget error = %v", err)
	}
	if err := store.ForgetFailed(ctx, "missing"); !errors.Is(
		err,
		ErrJobStoreConflict,
	) {
		t.Fatalf("missing forget error = %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.Enqueue(
		cancelled,
		testStoredJob(t, "cancelled", now, now),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled enqueue error = %v", err)
	}
	if _, err := store.FailedJobs(nil); !errors.Is(err, ErrJobContextUnavailable) {
		t.Fatalf("nil failed jobs context error = %v", err)
	}

	broken := testStoredJob(t, "broken", now, now)
	_, err = database.ExecContext(ctx,
		"INSERT INTO bridra_jobs "+
			"(id, handler, payload, available_at, enqueued_at, attempts) "+
			"VALUES (?, ?, ?, ?, ?, ?)",
		broken.ID,
		broken.Handler,
		"not-json",
		sqlJobStoreTime(now),
		sqlJobStoreTime(now),
		0,
	)
	if err != nil {
		t.Fatalf("insert broken row: %v", err)
	}
	if _, err := store.Reserve(ctx, now, time.Minute); !errors.Is(
		err,
		ErrJobStoreOperationFailed,
	) {
		t.Fatalf("broken row reservation error = %v", err)
	}
}

func TestNilSQLJobStoreAPI(t *testing.T) {
	var store *SQLJobStore
	ctx := context.Background()
	reservation := JobReservation{
		Job:   StoredJob{ID: strings.Repeat("0", jobIdentifierBytes*2)},
		Token: "token",
	}
	if store.Table() != "" {
		t.Fatalf("nil table = %q", store.Table())
	}
	if err := store.Ensure(ctx); !errors.Is(err, ErrJobStoreUnavailable) {
		t.Fatalf("nil ensure error = %v", err)
	}
	if err := store.Enqueue(ctx, StoredJob{}); !errors.Is(err, ErrJobStoreUnavailable) {
		t.Fatalf("nil enqueue error = %v", err)
	}
	if _, err := store.Reserve(ctx, time.Now(), time.Second); !errors.Is(
		err,
		ErrJobStoreUnavailable,
	) {
		t.Fatalf("nil reserve error = %v", err)
	}
	if err := store.Release(ctx, reservation, time.Now(), ""); !errors.Is(
		err,
		ErrJobStoreUnavailable,
	) {
		t.Fatalf("nil release error = %v", err)
	}
	if err := store.Complete(ctx, reservation); !errors.Is(
		err,
		ErrJobStoreUnavailable,
	) {
		t.Fatalf("nil complete error = %v", err)
	}
	if err := store.Fail(ctx, reservation, "failed"); !errors.Is(
		err,
		ErrJobStoreUnavailable,
	) {
		t.Fatalf("nil fail error = %v", err)
	}
	if _, err := store.FailedJobs(ctx); !errors.Is(err, ErrJobStoreUnavailable) {
		t.Fatalf("nil failed jobs error = %v", err)
	}
	if err := store.RetryFailed(ctx, reservation.Job.ID, time.Now()); !errors.Is(
		err,
		ErrJobStoreUnavailable,
	) {
		t.Fatalf("nil retry error = %v", err)
	}
	if err := store.ForgetFailed(ctx, reservation.Job.ID); !errors.Is(
		err,
		ErrJobStoreUnavailable,
	) {
		t.Fatalf("nil forget error = %v", err)
	}
}

func newTestSQLJobStores(
	t *testing.T,
	options SQLJobStoreOptions,
) (*SQLJobStore, *SQLJobStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.db")
	firstDatabase := openTestSQLJobDatabase(t, path)
	secondDatabase := openTestSQLJobDatabase(t, path)
	firstStore, err := NewSQLJobStore(firstDatabase, options)
	if err != nil {
		t.Fatalf("new first SQL job store: %v", err)
	}
	secondStore, err := NewSQLJobStore(secondDatabase, options)
	if err != nil {
		t.Fatalf("new second SQL job store: %v", err)
	}
	if err := firstStore.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure SQL job store: %v", err)
	}
	return firstStore, secondStore
}

func openTestSQLJobDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open(testSQLJobDriverName, path)
	if err != nil {
		t.Fatalf("open test SQL database: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close test SQL database: %v", err)
		}
	})
	return database
}

type sqlSharedJob struct {
	Value string
}

func newTestSQLPersistentQueue(t *testing.T, store JobStore) *JobQueue {
	t.Helper()
	options := DefaultJobQueueOptions()
	options.Store = store
	options.Workers = 1
	options.JobTimeout = 100 * time.Millisecond
	options.LeaseDuration = time.Second
	options.PollInterval = 5 * time.Millisecond
	queue, err := NewJobQueue(options)
	if err != nil {
		t.Fatalf("new SQL persistent queue: %v", err)
	}
	return queue
}
