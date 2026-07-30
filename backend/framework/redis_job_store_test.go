package framework

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisJobStorePersistsOrderingFailureRetryAndForget(t *testing.T) {
	firstStore, secondStore, _ := newTestRedisJobStores(
		t,
		DefaultRedisJobStoreOptions(),
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

	longError := strings.Repeat("界", fileJobStoreMaxErrorBytes)
	if err := firstStore.Fail(ctx, reservation, longError); err != nil {
		t.Fatalf("fail: %v", err)
	}
	failed, err := secondStore.FailedJobs(ctx)
	if err != nil {
		t.Fatalf("failed jobs: %v", err)
	}
	if len(failed) != 1 ||
		failed[0].Job.ID != first.ID ||
		failed[0].Job.Attempts != 2 ||
		len(failed[0].Error) > fileJobStoreMaxErrorBytes {
		t.Fatalf("failed jobs = %#v", failed)
	}
	secondReservation, err := firstStore.Reserve(
		ctx,
		now.Add(2*time.Hour),
		time.Minute,
	)
	if err != nil {
		t.Fatalf("reserve second: %v", err)
	}
	if err := secondStore.Complete(ctx, secondReservation); err != nil {
		t.Fatalf("complete second: %v", err)
	}

	retryFailedAt := now.Add(3 * time.Hour)
	if err := secondStore.RetryFailed(ctx, first.ID, retryFailedAt); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	retried, err := firstStore.Reserve(ctx, retryFailedAt, time.Minute)
	if err != nil {
		t.Fatalf("reserve retried: %v", err)
	}
	if retried.Job.ID != first.ID || retried.Job.Attempts != 1 {
		t.Fatalf("retried reservation = %#v", retried)
	}
	if err := secondStore.Fail(ctx, retried, "again"); err != nil {
		t.Fatalf("fail retried: %v", err)
	}
	if err := firstStore.ForgetFailed(ctx, first.ID); err != nil {
		t.Fatalf("forget failed: %v", err)
	}
	if failed, err := secondStore.FailedJobs(ctx); err != nil || len(failed) != 0 {
		t.Fatalf("failed jobs after forget = %#v, %v", failed, err)
	}
	if err := firstStore.ForgetFailed(ctx, first.ID); !errors.Is(
		err,
		ErrJobStoreConflict,
	) {
		t.Fatalf("forget missing error = %v", err)
	}
}

func TestRedisJobStoreCoordinatesAtomicReservations(t *testing.T) {
	firstStore, secondStore, _ := newTestRedisJobStores(
		t,
		DefaultRedisJobStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC)
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

func TestRedisJobStoreRecoversExpiredLease(t *testing.T) {
	firstStore, secondStore, _ := newTestRedisJobStores(
		t,
		DefaultRedisJobStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	job := testStoredJob(t, "lease", now, now)
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
		t.Fatalf("active lease error = %v", err)
	}
	recovered, err := secondStore.Reserve(
		ctx,
		now.Add(2*time.Minute),
		time.Minute,
	)
	if err != nil {
		t.Fatalf("reserve recovered: %v", err)
	}
	if recovered.Token == first.Token || recovered.Job.Attempts != 2 {
		t.Fatalf("recovered reservation = %#v", recovered)
	}
	if err := firstStore.Complete(ctx, recovered); err != nil {
		t.Fatalf("complete recovered: %v", err)
	}
}

func TestRedisJobStoreOrdersEqualAvailabilityByEnqueueTime(t *testing.T) {
	store, _, _ := newTestRedisJobStores(t, DefaultRedisJobStoreOptions())
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 3, 30, 0, 0, time.UTC)
	first := testStoredJob(t, "first", now, now)
	second := testStoredJob(t, "second", now, now.Add(time.Microsecond))
	if err := store.Enqueue(ctx, second); err != nil {
		t.Fatalf("enqueue second: %v", err)
	}
	if err := store.Enqueue(ctx, first); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	reservation, err := store.Reserve(ctx, now, time.Minute)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if reservation.Job.ID != first.ID {
		t.Fatalf("reserved job = %s, want %s", reservation.Job.ID, first.ID)
	}
}

func TestRedisJobStoreCoordinatesPersistentQueues(t *testing.T) {
	firstStore, secondStore, client := newTestRedisJobStores(
		t,
		DefaultRedisJobStoreOptions(),
	)
	firstQueue := newTestRedisPersistentQueue(t, firstStore)
	secondQueue := newTestRedisPersistentQueue(t, secondStore)
	handled := make(chan struct{}, 2)
	var attempts atomic.Int32
	register := func(queue *JobQueue) {
		t.Helper()
		if err := HandleJob(
			queue,
			"redis.shared",
			func(context.Context, redisSharedJob) error {
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
		redisSharedJob{Value: "shared"},
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	select {
	case <-handled:
	case <-time.After(2 * time.Second):
		t.Fatal("shared Redis job was not handled")
	}
	select {
	case <-handled:
		t.Fatal("shared Redis job was handled twice")
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
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("queue shutdown closed shared Redis client: %v", err)
	}
}

func TestRedisJobStoreOptionsValidationAndInvalidCalls(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Redis client: %v", err)
		}
	})

	if _, err := NewRedisJobStore(nil, RedisJobStoreOptions{}); !errors.Is(
		err,
		ErrJobStoreUnavailable,
	) {
		t.Fatalf("nil client error = %v", err)
	}
	var typedNil *redis.Client
	if _, err := NewRedisJobStore(typedNil, RedisJobStoreOptions{}); !errors.Is(
		err,
		ErrJobStoreUnavailable,
	) {
		t.Fatalf("typed nil client error = %v", err)
	}
	for _, options := range []RedisJobStoreOptions{
		{Namespace: "invalid{slot}"},
		{Namespace: "invalid\nslot"},
		{Namespace: strings.Repeat("x", redisJobStoreMaxNamespaceBytes+1)},
		{MaxPayloadBytes: -1},
	} {
		if _, err := NewRedisJobStore(client, options); !errors.Is(
			err,
			ErrInvalidRedisJobStoreOptions,
		) {
			t.Fatalf("options %#v error = %v", options, err)
		}
	}

	options := DefaultRedisJobStoreOptions()
	options.Namespace = "test:options"
	options.MaxPayloadBytes = 32
	store, err := NewRedisJobStore(client, options)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if store.Namespace() != options.Namespace {
		t.Fatalf("namespace = %q", store.Namespace())
	}
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	if err := store.Enqueue(ctx, StoredJob{}); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("invalid job error = %v", err)
	}
	oversized := testStoredJob(t, "oversized", now, now)
	oversized.Payload = json.RawMessage(`{"payload":"this is larger than thirty-two bytes"}`)
	if err := store.Enqueue(ctx, oversized); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("oversized job error = %v", err)
	}
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
}

func TestRedisJobStoreRejectsInexactTimesAndInvalidMetadata(t *testing.T) {
	store, _, _ := newTestRedisJobStores(t, DefaultRedisJobStoreOptions())
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 4, 30, 0, 0, time.UTC)
	tooLate := time.UnixMicro(redisJobStoreMaxExactTime + 1).UTC()
	job := testStoredJob(t, "time", now, now)
	job.AvailableAt = tooLate
	if err := store.Enqueue(ctx, job); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("inexact available time error = %v", err)
	}
	job.AvailableAt = now
	job.EnqueuedAt = tooLate
	if err := store.Enqueue(ctx, job); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("inexact enqueue time error = %v", err)
	}
	if _, err := store.Reserve(ctx, tooLate, time.Minute); !errors.Is(
		err,
		ErrJobReservationInvalid,
	) {
		t.Fatalf("inexact reservation time error = %v", err)
	}
	reservation := JobReservation{Job: job, Token: "token"}
	if err := store.Release(ctx, reservation, tooLate, ""); !errors.Is(
		err,
		ErrJobReservationInvalid,
	) {
		t.Fatalf("inexact release time error = %v", err)
	}
	if err := store.RetryFailed(ctx, job.ID, tooLate); !errors.Is(
		err,
		ErrJobStoreConflict,
	) {
		t.Fatalf("inexact retry time error = %v", err)
	}
	if _, err := redisJobStoreTime(time.Time{}); err == nil {
		t.Fatal("zero Redis time should fail")
	}
	if _, err := parseRedisJobStoreTime("invalid"); err == nil {
		t.Fatal("invalid Redis time should fail")
	}

	valid := testStoredJob(t, "metadata", now, now)
	metadata, err := newRedisStoredJobMetadata(valid)
	if err != nil {
		t.Fatalf("new metadata: %v", err)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("encode metadata: %v", err)
	}
	if _, _, err := decodeRedisStoredJob(
		string(encoded)+"{}",
		string(valid.Payload),
		defaultFileJobStoreMaxPayloadBytes,
	); err == nil {
		t.Fatal("multiple metadata objects should fail")
	}
	for name, mutate := range map[string]func(*redisStoredJobMetadata){
		"available": func(value *redisStoredJobMetadata) {
			value.AvailableAt = "invalid"
		},
		"enqueued": func(value *redisStoredJobMetadata) {
			value.EnqueuedAt = "invalid"
		},
		"attempts": func(value *redisStoredJobMetadata) {
			value.Attempts = "-1"
		},
		"job": func(value *redisStoredJobMetadata) {
			value.Handler = ""
		},
		"member": func(value *redisStoredJobMetadata) {
			value.ReadyMember = "invalid"
		},
	} {
		t.Run(name, func(t *testing.T) {
			broken := metadata
			mutate(&broken)
			encoded, err := json.Marshal(broken)
			if err != nil {
				t.Fatalf("encode broken metadata: %v", err)
			}
			if _, _, err := decodeRedisStoredJob(
				string(encoded),
				string(valid.Payload),
				defaultFileJobStoreMaxPayloadBytes,
			); err == nil {
				t.Fatal("broken metadata should fail")
			}
		})
	}
}

func TestRedisJobStoreReportsCorruptionAndUnavailableServer(t *testing.T) {
	store, _, client := newTestRedisJobStores(
		t,
		DefaultRedisJobStoreOptions(),
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 5, 0, 0, 0, time.UTC)
	job := testStoredJob(t, "corrupt", now, now)
	member := redisJobStoreReadyMember(job.EnqueuedAt, job.ID)
	if err := client.HSet(ctx, store.keys[0], job.ID, "not-json").Err(); err != nil {
		t.Fatalf("write corrupt metadata: %v", err)
	}
	if err := client.HSet(ctx, store.keys[1], job.ID, []byte(job.Payload)).Err(); err != nil {
		t.Fatalf("write corrupt payload: %v", err)
	}
	if err := client.ZAdd(
		ctx,
		store.keys[2],
		redis.Z{Score: float64(now.UnixMicro()), Member: member},
	).Err(); err != nil {
		t.Fatalf("write corrupt ready job: %v", err)
	}
	if _, err := store.Reserve(ctx, now, time.Minute); !errors.Is(
		err,
		ErrJobStoreOperationFailed,
	) {
		t.Fatalf("corrupt reservation error = %v", err)
	}

	client.Options().Addr = "127.0.0.1:1"
	client.Close()
	if _, err := store.FailedJobs(ctx); !errors.Is(
		err,
		ErrJobStoreOperationFailed,
	) {
		t.Fatalf("unavailable server error = %v", err)
	}
}

func TestNilRedisJobStoreAPI(t *testing.T) {
	var store *RedisJobStore
	ctx := context.Background()
	reservation := JobReservation{
		Job:   StoredJob{ID: strings.Repeat("0", jobIdentifierBytes*2)},
		Token: "token",
	}
	if store.Namespace() != "" {
		t.Fatalf("nil namespace = %q", store.Namespace())
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

func newTestRedisJobStores(
	t *testing.T,
	options RedisJobStoreOptions,
) (*RedisJobStore, *RedisJobStore, *redis.Client) {
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
	firstStore, err := NewRedisJobStore(firstClient, options)
	if err != nil {
		t.Fatalf("new first Redis job store: %v", err)
	}
	secondStore, err := NewRedisJobStore(secondClient, options)
	if err != nil {
		t.Fatalf("new second Redis job store: %v", err)
	}
	return firstStore, secondStore, firstClient
}

type redisSharedJob struct {
	Value string
}

func newTestRedisPersistentQueue(t *testing.T, store JobStore) *JobQueue {
	t.Helper()
	options := DefaultJobQueueOptions()
	options.Store = store
	options.Workers = 1
	options.JobTimeout = 100 * time.Millisecond
	options.LeaseDuration = time.Second
	options.PollInterval = 5 * time.Millisecond
	queue, err := NewJobQueue(options)
	if err != nil {
		t.Fatalf("new Redis persistent queue: %v", err)
	}
	return queue
}
