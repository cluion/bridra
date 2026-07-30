package sqljobstore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cluion/bridra/backend/framework"
)

func TestSQLiteSchedulerStoresCoordinateReservationAndLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduled-tasks.db")
	firstDatabase := openSQLite(t, path)
	secondDatabase := openSQLite(t, path)
	firstStore := newSQLSchedulerStore(
		t,
		firstDatabase,
		framework.DefaultSQLSchedulerStoreOptions(),
	)
	secondStore := newSQLSchedulerStore(
		t,
		secondDatabase,
		framework.DefaultSQLSchedulerStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	if err := firstStore.Initialize(ctx, "reports.daily", now); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := secondStore.Initialize(
		ctx,
		"reports.daily",
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("idempotent initialize: %v", err)
	}

	winner := coordinateSchedulerReservation(
		t,
		firstStore,
		secondStore,
		"reports.daily",
		now,
	)
	completedAt := now.Add(time.Minute)
	nextRunAt := now.Add(2 * time.Minute)
	if err := secondStore.Complete(
		ctx,
		winner,
		nextRunAt,
		completedAt,
		"temporary",
	); err != nil {
		t.Fatalf("complete: %v", err)
	}
	state, err := firstStore.State(ctx, "reports.daily")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if !state.NextRunAt.Equal(nextRunAt) ||
		!state.LastScheduledAt.Equal(now) ||
		!state.LastCompletedAt.Equal(completedAt) ||
		state.LastError != "temporary" ||
		!state.ReservedUntil.IsZero() {
		t.Fatalf("completed state = %#v", state)
	}
}

func TestSQLiteSchedulerStoreSupportsExpiredLeaseAndDollarPlaceholders(
	t *testing.T,
) {
	database := openSQLite(
		t,
		filepath.Join(t.TempDir(), "scheduled-leases.db"),
	)
	options := framework.DefaultSQLSchedulerStoreOptions()
	options.Table = "dollar_scheduled_tasks"
	options.PlaceholderStyle = framework.SQLPlaceholderDollar
	store := newSQLSchedulerStore(t, database, options)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	if err := store.Initialize(ctx, "leases.recover", now); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	first, err := store.Reserve(ctx, "leases.recover", now, time.Minute)
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	if _, err := store.Reserve(
		ctx,
		"leases.recover",
		now.Add(30*time.Second),
		time.Minute,
	); !errors.Is(err, framework.ErrScheduledTaskReserved) {
		t.Fatalf("active lease error = %v", err)
	}
	recovered, err := store.Reserve(
		ctx,
		"leases.recover",
		now.Add(2*time.Minute),
		time.Minute,
	)
	if err != nil {
		t.Fatalf("reserve recovered: %v", err)
	}
	if recovered.Token == first.Token ||
		!recovered.Task.NextRunAt.Equal(first.Task.NextRunAt) {
		t.Fatalf("recovered reservation = %#v", recovered)
	}
	if err := store.Complete(
		ctx,
		recovered,
		now.Add(4*time.Minute),
		now.Add(3*time.Minute),
		"",
	); err != nil {
		t.Fatalf("complete recovered: %v", err)
	}
}

func TestPostgreSQLSchedulerStoresCoordinateReservationAndLifecycle(
	t *testing.T,
) {
	dsn := os.Getenv("BRIDRA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BRIDRA_TEST_POSTGRES_DSN is not configured")
	}
	firstDatabase := openPostgreSQL(t, dsn)
	secondDatabase := openPostgreSQL(t, dsn)
	options := framework.DefaultSQLSchedulerStoreOptions()
	options.Table = fmt.Sprintf("bridra_scheduled_tasks_%d", time.Now().UnixNano())
	options.PlaceholderStyle = framework.SQLPlaceholderDollar
	t.Cleanup(func() {
		if _, err := firstDatabase.Exec(
			"DROP TABLE IF EXISTS " + options.Table,
		); err != nil {
			t.Errorf("drop PostgreSQL table: %v", err)
		}
	})
	firstStore := newSQLSchedulerStore(t, firstDatabase, options)
	secondStore := newSQLSchedulerStore(t, secondDatabase, options)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)
	if err := firstStore.Initialize(ctx, "reports.hourly", now); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	winner := coordinateSchedulerReservation(
		t,
		firstStore,
		secondStore,
		"reports.hourly",
		now,
	)
	completedAt := now.Add(time.Minute)
	nextRunAt := now.Add(2 * time.Minute)
	if err := secondStore.Complete(
		ctx,
		winner,
		nextRunAt,
		completedAt,
		"temporary",
	); err != nil {
		t.Fatalf("complete: %v", err)
	}
	state, err := firstStore.State(ctx, "reports.hourly")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if !state.NextRunAt.Equal(nextRunAt) ||
		!state.LastScheduledAt.Equal(now) ||
		!state.LastCompletedAt.Equal(completedAt) ||
		state.LastError != "temporary" ||
		!state.ReservedUntil.IsZero() {
		t.Fatalf("completed state = %#v", state)
	}
}

func coordinateSchedulerReservation(
	t *testing.T,
	firstStore *framework.SQLSchedulerStore,
	secondStore *framework.SQLSchedulerStore,
	name string,
	now time.Time,
) framework.ScheduledTaskReservation {
	t.Helper()
	const contenders = 24
	start := make(chan struct{})
	results := make(chan scheduledReservationResult, contenders)
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
			reservation, err := store.Reserve(
				context.Background(),
				name,
				now,
				time.Minute,
			)
			results <- scheduledReservationResult{
				reservation: reservation,
				err:         err,
			}
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)

	var winner framework.ScheduledTaskReservation
	var reserved int
	var alreadyReserved int
	for result := range results {
		switch {
		case result.err == nil:
			winner = result.reservation
			reserved++
		case errors.Is(result.err, framework.ErrScheduledTaskReserved):
			alreadyReserved++
		default:
			t.Fatalf("reserve: %v", result.err)
		}
	}
	if reserved != 1 ||
		alreadyReserved != contenders-1 ||
		winner.Task.Name != name {
		t.Fatalf(
			"results: reserved=%d alreadyReserved=%d winner=%#v",
			reserved,
			alreadyReserved,
			winner,
		)
	}
	return winner
}

type scheduledReservationResult struct {
	reservation framework.ScheduledTaskReservation
	err         error
}

func newSQLSchedulerStore(
	t *testing.T,
	database *sql.DB,
	options framework.SQLSchedulerStoreOptions,
) *framework.SQLSchedulerStore {
	t.Helper()
	store, err := framework.NewSQLSchedulerStore(database, options)
	if err != nil {
		t.Fatalf("new SQL scheduler store: %v", err)
	}
	if err := store.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure SQL scheduler store: %v", err)
	}
	if strings.TrimSpace(store.Table()) == "" {
		t.Fatal("SQL scheduler table is empty")
	}
	return store
}
