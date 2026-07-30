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

type reservationResult struct {
	reservation framework.JobReservation
	err         error
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

func storedJob(id string, now time.Time) framework.StoredJob {
	return framework.StoredJob{
		ID:          id,
		Handler:     "integration.handle",
		Payload:     json.RawMessage(`{"value":"redis"}`),
		AvailableAt: now,
		EnqueuedAt:  now,
	}
}
