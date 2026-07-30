package framework

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSQLSchedulerStorePreservesStateAndCompletion(t *testing.T) {
	firstStore, secondStore, _ := newTestSQLSchedulerStores(
		t,
		DefaultSQLSchedulerStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
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
	state, err := secondStore.State(ctx, "reports.daily")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.Name != "reports.daily" || !state.NextRunAt.Equal(now) {
		t.Fatalf("initial state = %#v", state)
	}
	if _, err := firstStore.Reserve(
		ctx,
		"reports.daily",
		now.Add(-time.Microsecond),
		time.Minute,
	); !errors.Is(err, ErrScheduledTaskNotDue) {
		t.Fatalf("early reservation error = %v", err)
	}

	reservation, err := firstStore.Reserve(
		ctx,
		"reports.daily",
		now,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if reservation.Task.Name != "reports.daily" ||
		!reservation.Task.NextRunAt.Equal(now) ||
		!reservation.ReservedUntil.Equal(now.Add(time.Minute)) ||
		reservation.Token == "" {
		t.Fatalf("reservation = %#v", reservation)
	}
	if _, err := secondStore.Reserve(
		ctx,
		"reports.daily",
		now.Add(30*time.Second),
		time.Minute,
	); !errors.Is(err, ErrScheduledTaskReserved) {
		t.Fatalf("active lease error = %v", err)
	}
	invalid := reservation
	invalid.Token = "wrong"
	if err := secondStore.Complete(
		ctx,
		invalid,
		now.Add(2*time.Minute),
		now.Add(time.Minute),
		"",
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("invalid completion error = %v", err)
	}

	completedAt := now.Add(time.Minute)
	nextRunAt := now.Add(2 * time.Minute)
	longError := strings.Repeat("界", fileSchedulerStoreMaxErrorBytes)
	if err := secondStore.Complete(
		ctx,
		reservation,
		nextRunAt,
		completedAt,
		longError,
	); err != nil {
		t.Fatalf("complete: %v", err)
	}
	state, err = firstStore.State(ctx, "reports.daily")
	if err != nil {
		t.Fatalf("completed state: %v", err)
	}
	if !state.NextRunAt.Equal(nextRunAt) ||
		!state.LastScheduledAt.Equal(now) ||
		!state.LastCompletedAt.Equal(completedAt) ||
		!state.ReservedUntil.IsZero() ||
		len(state.LastError) > fileSchedulerStoreMaxErrorBytes {
		t.Fatalf("completed state = %#v", state)
	}
}

func TestSQLSchedulerStoreRecoversExpiredLeaseAcrossStores(t *testing.T) {
	firstStore, secondStore, _ := newTestSQLSchedulerStores(
		t,
		DefaultSQLSchedulerStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	if err := firstStore.Initialize(ctx, "leases.recover", now); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	first, err := firstStore.Reserve(ctx, "leases.recover", now, time.Minute)
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	recovered, err := secondStore.Reserve(
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
	nextRunAt := now.Add(4 * time.Minute)
	completedAt := now.Add(3 * time.Minute)
	if err := firstStore.Complete(
		ctx,
		first,
		nextRunAt,
		completedAt,
		"",
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("expired completion error = %v", err)
	}
	if err := secondStore.Complete(
		ctx,
		recovered,
		nextRunAt,
		completedAt,
		"",
	); err != nil {
		t.Fatalf("complete recovered: %v", err)
	}
}

func TestSQLSchedulerStoreReservesOnceAcrossConcurrentStores(t *testing.T) {
	firstStore, secondStore, _ := newTestSQLSchedulerStores(
		t,
		DefaultSQLSchedulerStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	if err := firstStore.Initialize(ctx, "shared.once", now); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	const contenders = 24
	start := make(chan struct{})
	results := make(chan schedulerReservationResult, contenders)
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
				ctx,
				"shared.once",
				now,
				time.Minute,
			)
			results <- schedulerReservationResult{
				reservation: reservation,
				err:         err,
			}
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)

	var winner ScheduledTaskReservation
	var reserved int
	var alreadyReserved int
	for result := range results {
		switch {
		case result.err == nil:
			winner = result.reservation
			reserved++
		case errors.Is(result.err, ErrScheduledTaskReserved):
			alreadyReserved++
		default:
			t.Fatalf("reserve: %v", result.err)
		}
	}
	if reserved != 1 ||
		alreadyReserved != contenders-1 ||
		winner.Task.Name != "shared.once" {
		t.Fatalf(
			"results: reserved=%d alreadyReserved=%d winner=%#v",
			reserved,
			alreadyReserved,
			winner,
		)
	}
}

func TestSQLSchedulerStoreInitializesOnceAcrossConcurrentStores(t *testing.T) {
	firstStore, secondStore, _ := newTestSQLSchedulerStores(
		t,
		DefaultSQLSchedulerStoreOptions(),
	)
	ctx := context.Background()
	nextRunAt := time.Date(2026, 7, 30, 16, 30, 0, 0, time.UTC)
	const contenders = 24
	start := make(chan struct{})
	results := make(chan error, contenders)
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
			results <- store.Initialize(ctx, "shared.initialize", nextRunAt)
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("initialize: %v", err)
		}
	}
	state, err := firstStore.State(ctx, "shared.initialize")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if !state.NextRunAt.Equal(nextRunAt) {
		t.Fatalf("state = %#v", state)
	}
}

func TestSQLSchedulerStoreCoordinatesPersistentSchedulers(t *testing.T) {
	firstStore, secondStore, _ := newTestSQLSchedulerStores(
		t,
		DefaultSQLSchedulerStoreOptions(),
	)
	clock := newControlledSchedulerClock()
	started := make(chan string, 2)
	release := make(chan struct{})
	first := newTestPersistentScheduler(t, firstStore, clock, nil)
	second := newTestPersistentScheduler(t, secondStore, clock, nil)
	for name, scheduler := range map[string]*Scheduler{
		"first":  first,
		"second": second,
	} {
		name := name
		if err := ScheduleTask(
			scheduler,
			"sql.coordinated",
			time.Minute,
			func(context.Context) error {
				started <- name
				<-release
				return nil
			},
		); err != nil {
			t.Fatalf("schedule %s: %v", name, err)
		}
		if err := scheduler.Start(); err != nil {
			t.Fatalf("start %s: %v", name, err)
		}
	}
	firstTimer := nextControlledTimer(t, clock)
	secondTimer := nextControlledTimer(t, clock)
	firstTimer.Fire()
	secondTimer.Fire()
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("coordinated task did not start")
	}
	select {
	case duplicate := <-started:
		close(release)
		t.Fatalf("duplicate task execution by %s", duplicate)
	case <-time.After(25 * time.Millisecond):
	}

	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		firstDone <- first.Shutdown(context.Background())
	}()
	go func() {
		secondDone <- second.Shutdown(context.Background())
	}()
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("shutdown first: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("shutdown second: %v", err)
	}
}

func TestSQLSchedulerStoreOptionsContextAndInvalidRows(t *testing.T) {
	if _, err := NewSQLSchedulerStore(
		nil,
		DefaultSQLSchedulerStoreOptions(),
	); !errors.Is(err, ErrSchedulerStoreUnavailable) {
		t.Fatalf("nil pool error = %v", err)
	}
	for _, options := range []SQLSchedulerStoreOptions{
		{Table: "invalid-table"},
		{PlaceholderStyle: SQLPlaceholderStyle("invalid")},
	} {
		database, err := sql.Open(
			testSQLSchedulerDriverName,
			fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()),
		)
		if err != nil {
			t.Fatalf("open database: %v", err)
		}
		if _, err := NewSQLSchedulerStore(database, options); !errors.Is(
			err,
			ErrInvalidSQLSchedulerStoreOptions,
		) {
			t.Fatalf("options %#v error = %v", options, err)
		}
		if err := database.Close(); err != nil {
			t.Fatalf("close database: %v", err)
		}
	}

	options := DefaultSQLSchedulerStoreOptions()
	options.Table = "custom_tasks"
	options.PlaceholderStyle = SQLPlaceholderDollar
	store, _, state := newTestSQLSchedulerStores(t, options)
	if store.Table() != "custom_tasks" {
		t.Fatalf("table = %q", store.Table())
	}
	if err := store.Initialize(nil, "invalid.context", time.Now()); !errors.Is(
		err,
		ErrSchedulerContextUnavailable,
	) {
		t.Fatalf("nil context error = %v", err)
	}
	if _, err := store.State(
		context.Background(),
		"missing",
	); !errors.Is(err, ErrScheduledTaskStateNotFound) {
		t.Fatalf("missing state error = %v", err)
	}
	if err := store.Initialize(
		context.Background(),
		"",
		time.Now(),
	); !errors.Is(err, ErrSchedulerStoreConflict) {
		t.Fatalf("invalid initialize error = %v", err)
	}
	if err := store.Initialize(
		context.Background(),
		strings.Repeat("x", sqlSchedulerStoreMaxNameBytes+1),
		time.Now(),
	); !errors.Is(err, ErrSchedulerStoreConflict) {
		t.Fatalf("oversized initialize error = %v", err)
	}
	if _, err := store.State(
		context.Background(),
		"",
	); !errors.Is(err, ErrSchedulerStoreConflict) {
		t.Fatalf("invalid state error = %v", err)
	}
	if _, err := store.Reserve(
		context.Background(),
		"",
		time.Now(),
		time.Minute,
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("invalid reservation error = %v", err)
	}
	if err := store.Complete(
		context.Background(),
		ScheduledTaskReservation{},
		time.Now(),
		time.Now(),
		"",
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("invalid completion error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Ensure(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ensure error = %v", err)
	}

	state.mu.Lock()
	state.tasks["invalid.row"] = testSQLSchedulerRecord{
		name:                 "invalid.row",
		nextRunAt:            sqlSchedulerStoreTime(time.Now()),
		lastScheduledAt:      sqlSchedulerStoreTime(time.Now()),
		lastScheduledAtValid: true,
	}
	state.mu.Unlock()
	if _, err := store.State(
		context.Background(),
		"invalid.row",
	); !errors.Is(err, ErrSchedulerStoreOperationFailed) {
		t.Fatalf("invalid row error = %v", err)
	}

	var nilStore *SQLSchedulerStore
	if nilStore.Table() != "" {
		t.Fatalf("nil table = %q", nilStore.Table())
	}
	if err := nilStore.Ensure(context.Background()); !errors.Is(
		err,
		ErrSchedulerStoreUnavailable,
	) {
		t.Fatalf("nil ensure error = %v", err)
	}
	if err := nilStore.Initialize(
		context.Background(),
		"task",
		time.Now(),
	); !errors.Is(err, ErrSchedulerStoreUnavailable) {
		t.Fatalf("nil initialize error = %v", err)
	}
	if _, err := nilStore.State(
		context.Background(),
		"task",
	); !errors.Is(err, ErrSchedulerStoreUnavailable) {
		t.Fatalf("nil state error = %v", err)
	}
	if _, err := nilStore.Reserve(
		context.Background(),
		"task",
		time.Now(),
		time.Minute,
	); !errors.Is(err, ErrSchedulerStoreUnavailable) {
		t.Fatalf("nil reserve error = %v", err)
	}
	if err := nilStore.Complete(
		context.Background(),
		ScheduledTaskReservation{},
		time.Now(),
		time.Now(),
		"",
	); !errors.Is(err, ErrSchedulerStoreUnavailable) {
		t.Fatalf("nil complete error = %v", err)
	}

	uninitializedDatabase, err := sql.Open(
		testSQLSchedulerDriverName,
		fmt.Sprintf("%s-uninitialized-%d", t.Name(), time.Now().UnixNano()),
	)
	if err != nil {
		t.Fatalf("open uninitialized database: %v", err)
	}
	t.Cleanup(func() {
		if err := uninitializedDatabase.Close(); err != nil {
			t.Errorf("close uninitialized database: %v", err)
		}
	})
	uninitializedStore, err := NewSQLSchedulerStore(
		uninitializedDatabase,
		DefaultSQLSchedulerStoreOptions(),
	)
	if err != nil {
		t.Fatalf("new uninitialized store: %v", err)
	}
	if err := uninitializedStore.Initialize(
		context.Background(),
		"missing.table",
		time.Now(),
	); !errors.Is(err, ErrSchedulerStoreOperationFailed) {
		t.Fatalf("uninitialized store error = %v", err)
	}
}

type schedulerReservationResult struct {
	reservation ScheduledTaskReservation
	err         error
}

func newTestSQLSchedulerStores(
	t *testing.T,
	options SQLSchedulerStoreOptions,
) (*SQLSchedulerStore, *SQLSchedulerStore, *testSQLSchedulerState) {
	t.Helper()
	dsn := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	firstDatabase, err := sql.Open(testSQLSchedulerDriverName, dsn)
	if err != nil {
		t.Fatalf("open first database: %v", err)
	}
	secondDatabase, err := sql.Open(testSQLSchedulerDriverName, dsn)
	if err != nil {
		_ = firstDatabase.Close()
		t.Fatalf("open second database: %v", err)
	}
	t.Cleanup(func() {
		if err := firstDatabase.Close(); err != nil {
			t.Errorf("close first database: %v", err)
		}
		if err := secondDatabase.Close(); err != nil {
			t.Errorf("close second database: %v", err)
		}
		testSQLSchedulerDatabases.mu.Lock()
		delete(testSQLSchedulerDatabases.states, dsn)
		testSQLSchedulerDatabases.mu.Unlock()
	})
	firstStore, err := NewSQLSchedulerStore(firstDatabase, options)
	if err != nil {
		t.Fatalf("new first store: %v", err)
	}
	secondStore, err := NewSQLSchedulerStore(secondDatabase, options)
	if err != nil {
		t.Fatalf("new second store: %v", err)
	}
	if err := firstStore.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure first store: %v", err)
	}
	if err := secondStore.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure second store: %v", err)
	}
	testSQLSchedulerDatabases.mu.Lock()
	state := testSQLSchedulerDatabases.states[dsn]
	testSQLSchedulerDatabases.mu.Unlock()
	return firstStore, secondStore, state
}
