package redisjobstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cluion/bridra/backend/framework"
	"github.com/redis/go-redis/v9"
)

func TestRedisStoresCoordinateReservationAndLifecycle(t *testing.T) {
	address := os.Getenv("BRIDRA_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("BRIDRA_TEST_REDIS_ADDR is not configured")
	}
	firstClient := redis.NewClient(&redis.Options{Addr: address})
	secondClient := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() {
		if err := firstClient.Close(); err != nil {
			t.Errorf("close first Redis client: %v", err)
		}
		if err := secondClient.Close(); err != nil {
			t.Errorf("close second Redis client: %v", err)
		}
	})
	ctx := context.Background()
	if err := firstClient.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}

	namespace := fmt.Sprintf("bridra:test:%d", time.Now().UnixNano())
	options := framework.DefaultRedisJobStoreOptions()
	options.Namespace = namespace
	firstStore := newRedisJobStore(t, firstClient, options)
	secondStore := newRedisJobStore(t, secondClient, options)
	t.Cleanup(func() {
		slot := "{" + namespace + "}"
		keys := []string{
			slot + ":records",
			slot + ":payloads",
			slot + ":ready",
			slot + ":reserved",
			slot + ":failed",
		}
		if err := firstClient.Del(context.Background(), keys...).Err(); err != nil {
			t.Errorf("delete Redis test keys: %v", err)
		}
	})

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

func TestRedisSchedulerStoresCoordinateReservationAndLifecycle(t *testing.T) {
	address := os.Getenv("BRIDRA_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("BRIDRA_TEST_REDIS_ADDR is not configured")
	}
	firstClient := redis.NewClient(&redis.Options{Addr: address})
	secondClient := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() {
		if err := firstClient.Close(); err != nil {
			t.Errorf("close first Redis client: %v", err)
		}
		if err := secondClient.Close(); err != nil {
			t.Errorf("close second Redis client: %v", err)
		}
	})
	ctx := context.Background()
	if err := firstClient.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}

	namespace := fmt.Sprintf("bridra:scheduler:test:%d", time.Now().UnixNano())
	options := framework.DefaultRedisSchedulerStoreOptions()
	options.Namespace = namespace
	firstStore := newRedisSchedulerStore(t, firstClient, options)
	secondStore := newRedisSchedulerStore(t, secondClient, options)
	t.Cleanup(func() {
		key := "{" + namespace + "}:tasks"
		if err := firstClient.Del(context.Background(), key).Err(); err != nil {
			t.Errorf("delete Redis scheduler test key: %v", err)
		}
	})

	now := time.Date(2026, 7, 31, 7, 0, 0, 0, time.UTC)
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

	winner := coordinateRedisSchedulerReservation(
		t,
		firstStore,
		secondStore,
		"reports.daily",
		now,
	)
	if _, err := firstStore.Reserve(
		ctx,
		"reports.daily",
		now.Add(2*time.Minute),
		time.Minute,
	); err != nil {
		t.Fatalf("recover expired lease: %v", err)
	}
	recovered := coordinateRedisSchedulerReservation(
		t,
		firstStore,
		secondStore,
		"reports.daily",
		now.Add(4*time.Minute),
	)
	if recovered.Token == winner.Token {
		t.Fatal("expired lease reused its reservation token")
	}

	completedAt := now.Add(5 * time.Minute)
	nextRunAt := now.Add(6 * time.Minute)
	if err := secondStore.Complete(
		ctx,
		recovered,
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

type reservationResult struct {
	reservation framework.JobReservation
	err         error
}

type schedulerReservationResult struct {
	reservation framework.ScheduledTaskReservation
	err         error
}

func coordinateRedisSchedulerReservation(
	t *testing.T,
	firstStore *framework.RedisSchedulerStore,
	secondStore *framework.RedisSchedulerStore,
	name string,
	now time.Time,
) framework.ScheduledTaskReservation {
	t.Helper()
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
				context.Background(),
				name,
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
			"reservation results: reserved=%d alreadyReserved=%d winner=%#v",
			reserved,
			alreadyReserved,
			winner,
		)
	}
	return winner
}

func newRedisJobStore(
	t *testing.T,
	client redis.Scripter,
	options framework.RedisJobStoreOptions,
) *framework.RedisJobStore {
	t.Helper()
	store, err := framework.NewRedisJobStore(client, options)
	if err != nil {
		t.Fatalf("new Redis job store: %v", err)
	}
	return store
}

func newRedisSchedulerStore(
	t *testing.T,
	client redis.Scripter,
	options framework.RedisSchedulerStoreOptions,
) *framework.RedisSchedulerStore {
	t.Helper()
	store, err := framework.NewRedisSchedulerStore(client, options)
	if err != nil {
		t.Fatalf("new Redis scheduler store: %v", err)
	}
	return store
}

func storedJob(id string, now time.Time) framework.StoredJob {
	return framework.StoredJob{
		ID:          id,
		Handler:     "integration.handle",
		Payload:     json.RawMessage(`{"value":"redis"}`),
		AvailableAt: now,
		EnqueuedAt:  now,
	}
}
