package sqljobstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cluion/bridra/backend/framework"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

func TestSQLiteStoresCoordinateReservationAndLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	firstDatabase := openSQLite(t, path)
	secondDatabase := openSQLite(t, path)
	firstStore := newSQLJobStore(t, firstDatabase, framework.DefaultSQLJobStoreOptions())
	secondStore := newSQLJobStore(t, secondDatabase, framework.DefaultSQLJobStoreOptions())
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	job := storedJob(strings.Repeat("1", 64), now)
	if err := firstStore.Enqueue(ctx, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	const contenders = 24
	start := make(chan struct{})
	results := make(chan reservationResult, contenders)
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
			results <- reservationResult{reservation: reservation, err: err}
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)

	var winner framework.JobReservation
	var reserved int
	var empty int
	for result := range results {
		switch {
		case result.err == nil:
			winner = result.reservation
			reserved++
		case errors.Is(result.err, framework.ErrJobStoreEmpty):
			empty++
		default:
			t.Fatalf("reserve: %v", result.err)
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

	retryAt := now.Add(2 * time.Minute)
	if err := secondStore.Release(ctx, winner, retryAt, "temporary"); err != nil {
		t.Fatalf("release: %v", err)
	}
	retried, err := firstStore.Reserve(ctx, retryAt, time.Minute)
	if err != nil {
		t.Fatalf("reserve retry: %v", err)
	}
	if retried.Job.Attempts != 2 {
		t.Fatalf("retry attempts = %d", retried.Job.Attempts)
	}
	if err := firstStore.Fail(ctx, retried, "permanent"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	failed, err := secondStore.FailedJobs(ctx)
	if err != nil {
		t.Fatalf("failed jobs: %v", err)
	}
	if len(failed) != 1 ||
		failed[0].Job.ID != job.ID ||
		failed[0].Error != "permanent" {
		t.Fatalf("failed jobs = %#v", failed)
	}
	retryFailedAt := now.Add(4 * time.Minute)
	if err := secondStore.RetryFailed(ctx, job.ID, retryFailedAt); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	recovered, err := firstStore.Reserve(ctx, retryFailedAt, time.Minute)
	if err != nil {
		t.Fatalf("reserve recovered: %v", err)
	}
	if recovered.Job.Attempts != 1 {
		t.Fatalf("recovered attempts = %d", recovered.Job.Attempts)
	}
	if err := secondStore.Complete(ctx, recovered); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func TestSQLiteStoreSupportsExpiredLeasesAndDollarPlaceholders(t *testing.T) {
	database := openSQLite(t, filepath.Join(t.TempDir(), "leases.db"))
	options := framework.DefaultSQLJobStoreOptions()
	options.Table = "dollar_jobs"
	options.PlaceholderStyle = framework.SQLPlaceholderDollar
	store := newSQLJobStore(t, database, options)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC)
	job := storedJob(strings.Repeat("2", 64), now)
	if err := store.Enqueue(ctx, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	first, err := store.Reserve(ctx, now, time.Minute)
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	if _, err := store.Reserve(
		ctx,
		now.Add(30*time.Second),
		time.Minute,
	); !errors.Is(err, framework.ErrJobStoreEmpty) {
		t.Fatalf("active lease error = %v", err)
	}
	recovered, err := store.Reserve(ctx, now.Add(2*time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("reserve recovered: %v", err)
	}
	if recovered.Token == first.Token || recovered.Job.Attempts != 2 {
		t.Fatalf("recovered reservation = %#v", recovered)
	}
	if err := store.Complete(ctx, recovered); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func TestPostgreSQLStoresCoordinateReservationAndLifecycle(t *testing.T) {
	dsn := os.Getenv("BRIDRA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BRIDRA_TEST_POSTGRES_DSN is not configured")
	}
	firstDatabase := openPostgreSQL(t, dsn)
	secondDatabase := openPostgreSQL(t, dsn)
	options := framework.DefaultSQLJobStoreOptions()
	options.Table = fmt.Sprintf("bridra_jobs_%d", time.Now().UnixNano())
	options.PlaceholderStyle = framework.SQLPlaceholderDollar
	t.Cleanup(func() {
		if _, err := firstDatabase.Exec("DROP TABLE IF EXISTS " + options.Table); err != nil {
			t.Errorf("drop PostgreSQL table: %v", err)
		}
	})
	firstStore := newSQLJobStore(t, firstDatabase, options)
	secondStore := newSQLJobStore(t, secondDatabase, options)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	job := storedJob(strings.Repeat("3", 64), now)
	if err := firstStore.Enqueue(ctx, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	const contenders = 24
	start := make(chan struct{})
	results := make(chan reservationResult, contenders)
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
			results <- reservationResult{reservation: reservation, err: err}
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)

	var winner framework.JobReservation
	var reserved int
	var empty int
	for result := range results {
		switch {
		case result.err == nil:
			winner = result.reservation
			reserved++
		case errors.Is(result.err, framework.ErrJobStoreEmpty):
			empty++
		default:
			t.Fatalf("reserve: %v", result.err)
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
	retryAt := now.Add(2 * time.Minute)
	if err := secondStore.Release(ctx, winner, retryAt, "temporary"); err != nil {
		t.Fatalf("release: %v", err)
	}
	retried, err := firstStore.Reserve(ctx, retryAt, time.Minute)
	if err != nil {
		t.Fatalf("reserve retry: %v", err)
	}
	if retried.Job.Attempts != 2 {
		t.Fatalf("retry attempts = %d", retried.Job.Attempts)
	}
	if err := firstStore.Fail(ctx, retried, "permanent"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	failed, err := secondStore.FailedJobs(ctx)
	if err != nil {
		t.Fatalf("failed jobs: %v", err)
	}
	if len(failed) != 1 ||
		failed[0].Job.ID != job.ID ||
		failed[0].Error != "permanent" {
		t.Fatalf("failed jobs = %#v", failed)
	}
	retryFailedAt := now.Add(4 * time.Minute)
	if err := secondStore.RetryFailed(ctx, job.ID, retryFailedAt); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	recovered, err := firstStore.Reserve(ctx, retryFailedAt, time.Minute)
	if err != nil {
		t.Fatalf("reserve recovered: %v", err)
	}
	if recovered.Job.Attempts != 1 {
		t.Fatalf("recovered attempts = %d", recovered.Job.Attempts)
	}
	if err := secondStore.Complete(ctx, recovered); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

type reservationResult struct {
	reservation framework.JobReservation
	err         error
}

func newSQLJobStore(
	t *testing.T,
	database *sql.DB,
	options framework.SQLJobStoreOptions,
) *framework.SQLJobStore {
	t.Helper()
	store, err := framework.NewSQLJobStore(database, options)
	if err != nil {
		t.Fatalf("new SQL job store: %v", err)
	}
	if err := store.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure SQL job store: %v", err)
	}
	return store
}

func openSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close SQLite: %v", err)
		}
	})
	if _, err := database.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("set SQLite busy timeout: %v", err)
	}
	if _, err := database.Exec("PRAGMA journal_mode = WAL"); err != nil {
		t.Fatalf("set SQLite WAL: %v", err)
	}
	return database
}

func openPostgreSQL(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	database.SetMaxOpenConns(8)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close PostgreSQL: %v", err)
		}
	})
	pingContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := database.PingContext(pingContext); err != nil {
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	return database
}

func storedJob(id string, now time.Time) framework.StoredJob {
	return framework.StoredJob{
		ID:          id,
		Handler:     "integration.handle",
		Payload:     json.RawMessage(`{"value":"test"}`),
		AvailableAt: now,
		EnqueuedAt:  now,
	}
}
