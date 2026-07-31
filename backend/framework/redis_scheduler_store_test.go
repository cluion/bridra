package framework

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisSchedulerStorePreservesStateAndCompletion(t *testing.T) {
	firstStore, secondStore, _ := newTestRedisSchedulerStores(
		t,
		DefaultRedisSchedulerStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 1, 0, 0, 0, time.UTC)
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

func TestRedisSchedulerStoreRecoversExpiredLeaseAcrossStores(t *testing.T) {
	firstStore, secondStore, _ := newTestRedisSchedulerStores(
		t,
		DefaultRedisSchedulerStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 2, 0, 0, 0, time.UTC)
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
		t.Fatalf("stale completion error = %v", err)
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

func TestRedisSchedulerStoreReservesOnceAcrossConcurrentStores(t *testing.T) {
	firstStore, secondStore, _ := newTestRedisSchedulerStores(
		t,
		DefaultRedisSchedulerStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 3, 0, 0, 0, time.UTC)
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

func TestRedisSchedulerStoreInitializesOnceAcrossConcurrentStores(t *testing.T) {
	firstStore, secondStore, _ := newTestRedisSchedulerStores(
		t,
		DefaultRedisSchedulerStoreOptions(),
	)
	ctx := context.Background()
	nextRunAt := time.Date(2026, 7, 31, 3, 30, 0, 0, time.UTC)
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

func TestRedisSchedulerStoreCoordinatesPersistentSchedulers(t *testing.T) {
	firstStore, secondStore, client := newTestRedisSchedulerStores(
		t,
		DefaultRedisSchedulerStoreOptions(),
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
			"redis.coordinated",
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
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("scheduler shutdown closed shared Redis client: %v", err)
	}
}

func TestRedisSchedulerStoreOptionsContextAndInvalidCalls(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		if err := client.Close(); err != nil && !errors.Is(err, redis.ErrClosed) {
			t.Errorf("close Redis client: %v", err)
		}
	})

	if _, err := NewRedisSchedulerStore(
		nil,
		DefaultRedisSchedulerStoreOptions(),
	); !errors.Is(err, ErrSchedulerStoreUnavailable) {
		t.Fatalf("nil client error = %v", err)
	}
	var typedNil *redis.Client
	if _, err := NewRedisSchedulerStore(
		typedNil,
		DefaultRedisSchedulerStoreOptions(),
	); !errors.Is(err, ErrSchedulerStoreUnavailable) {
		t.Fatalf("typed nil client error = %v", err)
	}
	for _, options := range []RedisSchedulerStoreOptions{
		{Namespace: "invalid{slot}"},
		{Namespace: "invalid\nslot"},
		{Namespace: strings.Repeat("x", redisSchedulerStoreMaxNamespaceBytes+1)},
	} {
		if _, err := NewRedisSchedulerStore(client, options); !errors.Is(
			err,
			ErrInvalidRedisSchedulerStoreOptions,
		) {
			t.Fatalf("options %#v error = %v", options, err)
		}
	}

	options := DefaultRedisSchedulerStoreOptions()
	options.Namespace = "test:scheduler"
	store, err := NewRedisSchedulerStore(client, options)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if store.Namespace() != options.Namespace {
		t.Fatalf("namespace = %q", store.Namespace())
	}
	now := time.Date(2026, 7, 31, 4, 0, 0, 0, time.UTC)
	if err := store.Initialize(
		nil,
		"invalid.context",
		now,
	); !errors.Is(err, ErrSchedulerContextUnavailable) {
		t.Fatalf("nil initialize context error = %v", err)
	}
	if _, err := store.State(
		context.Background(),
		"missing",
	); !errors.Is(err, ErrScheduledTaskStateNotFound) {
		t.Fatalf("missing state error = %v", err)
	}
	if _, err := store.Reserve(
		context.Background(),
		"missing",
		now,
		time.Minute,
	); !errors.Is(err, ErrScheduledTaskStateNotFound) {
		t.Fatalf("missing reserve error = %v", err)
	}
	if err := store.Initialize(
		context.Background(),
		"",
		now,
	); !errors.Is(err, ErrSchedulerStoreConflict) {
		t.Fatalf("invalid initialize error = %v", err)
	}
	if err := store.Initialize(
		context.Background(),
		strings.Repeat("x", redisSchedulerStoreMaxNameBytes+1),
		now,
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
		now,
		time.Minute,
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("invalid reservation name error = %v", err)
	}
	if _, err := store.Reserve(
		context.Background(),
		"task",
		time.Time{},
		time.Minute,
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("invalid reservation time error = %v", err)
	}
	if _, err := store.Reserve(
		context.Background(),
		"task",
		now,
		0,
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("invalid reservation lease error = %v", err)
	}
	if err := store.Complete(
		context.Background(),
		ScheduledTaskReservation{},
		now.Add(time.Minute),
		now,
		"",
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("invalid completion error = %v", err)
	}
	if err := store.Complete(
		context.Background(),
		ScheduledTaskReservation{
			Task:  StoredScheduledTask{Name: "task", NextRunAt: now},
			Token: "token",
		},
		now,
		now,
		"",
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("non-future completion error = %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Initialize(
		cancelled,
		"cancelled",
		now,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled initialize error = %v", err)
	}

	tooLate := time.UnixMicro(redisSchedulerStoreMaxExactTime + 1).UTC()
	if err := store.Initialize(
		context.Background(),
		"inexact",
		tooLate,
	); !errors.Is(err, ErrSchedulerStoreConflict) {
		t.Fatalf("inexact initialize error = %v", err)
	}
	if _, err := store.Reserve(
		context.Background(),
		"task",
		tooLate,
		time.Minute,
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("inexact reserve error = %v", err)
	}
	if err := store.Complete(
		context.Background(),
		ScheduledTaskReservation{
			Task:  StoredScheduledTask{Name: "task", NextRunAt: tooLate},
			Token: "token",
		},
		tooLate.Add(time.Hour),
		tooLate.Add(time.Minute),
		"",
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("inexact complete error = %v", err)
	}
}

func TestRedisSchedulerStoreRejectsCorruptionAndUnavailableServer(t *testing.T) {
	store, _, client := newTestRedisSchedulerStores(
		t,
		DefaultRedisSchedulerStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 5, 0, 0, 0, time.UTC)
	if err := client.HSet(
		ctx,
		store.keys[0],
		"corrupt.state",
		"not-json",
	).Err(); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	if _, err := store.State(ctx, "corrupt.state"); !errors.Is(
		err,
		ErrSchedulerStoreOperationFailed,
	) {
		t.Fatalf("corrupt state error = %v", err)
	}

	corrupt, err := json.Marshal(redisStoredScheduledTaskMetadata{
		Name:             "corrupt.reserve",
		NextRunAt:        "invalid",
		ReservationToken: "",
		ReservedUntil:    "",
	})
	if err != nil {
		t.Fatalf("encode corrupt state: %v", err)
	}
	if err := client.HSet(
		ctx,
		store.keys[0],
		"corrupt.reserve",
		corrupt,
	).Err(); err != nil {
		t.Fatalf("write corrupt reservation state: %v", err)
	}
	if _, err := store.Reserve(
		ctx,
		"corrupt.reserve",
		now,
		time.Minute,
	); !errors.Is(err, ErrSchedulerStoreOperationFailed) {
		t.Fatalf("corrupt reservation error = %v", err)
	}

	nowValue, err := redisSchedulerStoreTime(now)
	if err != nil {
		t.Fatalf("encode scheduled time: %v", err)
	}
	reservedUntilValue, err := redisSchedulerStoreTime(now.Add(time.Minute))
	if err != nil {
		t.Fatalf("encode reserved until: %v", err)
	}
	corrupt, err = json.Marshal(redisStoredScheduledTaskMetadata{
		Name:             "corrupt.complete",
		NextRunAt:        nowValue,
		LastError:        "unexpected",
		ReservationToken: "token",
		ReservedUntil:    reservedUntilValue,
	})
	if err != nil {
		t.Fatalf("encode corrupt completion state: %v", err)
	}
	if err := client.HSet(
		ctx,
		store.keys[0],
		"corrupt.complete",
		corrupt,
	).Err(); err != nil {
		t.Fatalf("write corrupt completion state: %v", err)
	}
	if err := store.Complete(
		ctx,
		ScheduledTaskReservation{
			Task: StoredScheduledTask{
				Name:      "corrupt.complete",
				NextRunAt: now,
			},
			Token: "token",
		},
		now.Add(2*time.Minute),
		now.Add(time.Minute),
		"",
	); !errors.Is(err, ErrSchedulerStoreOperationFailed) {
		t.Fatalf("corrupt completion error = %v", err)
	}

	client.Options().Addr = "127.0.0.1:1"
	if err := client.Close(); err != nil {
		t.Fatalf("close Redis client: %v", err)
	}
	if _, err := store.State(ctx, "unavailable"); !errors.Is(
		err,
		ErrSchedulerStoreOperationFailed,
	) {
		t.Fatalf("unavailable server error = %v", err)
	}
}

func TestRedisSchedulerStoreMetadataValidationAndNilReceiver(t *testing.T) {
	now := time.Date(2026, 7, 31, 6, 0, 0, 0, time.UTC)
	nowValue, err := redisSchedulerStoreTime(now)
	if err != nil {
		t.Fatalf("encode time: %v", err)
	}
	valid := redisStoredScheduledTaskMetadata{
		Name:      "metadata.valid",
		NextRunAt: nowValue,
	}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("encode metadata: %v", err)
	}
	if _, err := decodeRedisStoredScheduledTask(string(encoded) + "{}"); err == nil {
		t.Fatal("multiple metadata objects should fail")
	}
	for name, mutate := range map[string]func(*redisStoredScheduledTaskMetadata){
		"name": func(value *redisStoredScheduledTaskMetadata) {
			value.Name = ""
		},
		"next": func(value *redisStoredScheduledTaskMetadata) {
			value.NextRunAt = "invalid"
		},
		"completion_pair": func(value *redisStoredScheduledTaskMetadata) {
			value.LastScheduledAt = nowValue
		},
		"reservation_pair": func(value *redisStoredScheduledTaskMetadata) {
			value.ReservationToken = "token"
		},
		"error": func(value *redisStoredScheduledTaskMetadata) {
			value.LastError = strings.Repeat("x", fileSchedulerStoreMaxErrorBytes+1)
		},
		"error_without_completion": func(value *redisStoredScheduledTaskMetadata) {
			value.LastError = "unexpected"
		},
	} {
		t.Run(name, func(t *testing.T) {
			broken := valid
			mutate(&broken)
			encoded, err := json.Marshal(broken)
			if err != nil {
				t.Fatalf("encode broken metadata: %v", err)
			}
			if _, err := decodeRedisStoredScheduledTask(string(encoded)); err == nil {
				t.Fatal("broken metadata should fail")
			}
		})
	}
	if _, err := redisSchedulerStoreTime(time.Time{}); err == nil {
		t.Fatal("zero Redis scheduler time should fail")
	}
	if _, err := parseRedisSchedulerStoreTime("invalid"); err == nil {
		t.Fatal("invalid Redis scheduler time should fail")
	}

	var store *RedisSchedulerStore
	ctx := context.Background()
	reservation := ScheduledTaskReservation{
		Task:  StoredScheduledTask{Name: "task", NextRunAt: now},
		Token: "token",
	}
	if store.Namespace() != "" {
		t.Fatalf("nil namespace = %q", store.Namespace())
	}
	if err := store.Initialize(ctx, "task", now); !errors.Is(
		err,
		ErrSchedulerStoreUnavailable,
	) {
		t.Fatalf("nil initialize error = %v", err)
	}
	if _, err := store.State(ctx, "task"); !errors.Is(
		err,
		ErrSchedulerStoreUnavailable,
	) {
		t.Fatalf("nil state error = %v", err)
	}
	if _, err := store.Reserve(ctx, "task", now, time.Minute); !errors.Is(
		err,
		ErrSchedulerStoreUnavailable,
	) {
		t.Fatalf("nil reserve error = %v", err)
	}
	if err := store.Complete(
		ctx,
		reservation,
		now.Add(2*time.Minute),
		now.Add(time.Minute),
		"",
	); !errors.Is(err, ErrSchedulerStoreUnavailable) {
		t.Fatalf("nil complete error = %v", err)
	}
}

func newTestRedisSchedulerStores(
	t *testing.T,
	options RedisSchedulerStoreOptions,
) (*RedisSchedulerStore, *RedisSchedulerStore, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	firstClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	secondClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		if err := firstClient.Close(); err != nil && !errors.Is(err, redis.ErrClosed) {
			t.Errorf("close first Redis client: %v", err)
		}
		if err := secondClient.Close(); err != nil && !errors.Is(err, redis.ErrClosed) {
			t.Errorf("close second Redis client: %v", err)
		}
	})
	firstStore, err := NewRedisSchedulerStore(firstClient, options)
	if err != nil {
		t.Fatalf("new first Redis scheduler store: %v", err)
	}
	secondStore, err := NewRedisSchedulerStore(secondClient, options)
	if err != nil {
		t.Fatalf("new second Redis scheduler store: %v", err)
	}
	return firstStore, secondStore, firstClient
}
