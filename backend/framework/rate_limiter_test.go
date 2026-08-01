package framework

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryRateLimiterUsesTokenBucketPerKey(t *testing.T) {
	limiter, err := NewMemoryRateLimiter(MemoryRateLimiterOptions{
		Requests: 2,
		Window:   10 * time.Second,
		MaxKeys:  2,
	})
	if err != nil {
		t.Fatalf("new rate limiter: %v", err)
	}
	now := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	first, err := limiter.Allow(context.Background(), "principal:alice")
	if err != nil || !first.Allowed || first.Limit != 2 || first.Remaining != 1 {
		t.Fatalf("first decision = %#v, error = %v", first, err)
	}
	second, err := limiter.Allow(context.Background(), "principal:alice")
	if err != nil || !second.Allowed || second.Remaining != 0 {
		t.Fatalf("second decision = %#v, error = %v", second, err)
	}
	denied, err := limiter.Allow(context.Background(), "principal:alice")
	if err != nil || denied.Allowed || denied.RetryAfter != 5*time.Second {
		t.Fatalf("denied decision = %#v, error = %v", denied, err)
	}

	bob, err := limiter.Allow(context.Background(), "principal:bob")
	if err != nil || !bob.Allowed {
		t.Fatalf("bob decision = %#v, error = %v", bob, err)
	}
	now = now.Add(5 * time.Second)
	refilled, err := limiter.Allow(context.Background(), "principal:alice")
	if err != nil || !refilled.Allowed || refilled.Remaining != 0 {
		t.Fatalf("refilled decision = %#v, error = %v", refilled, err)
	}
}

func TestMemoryRateLimiterBoundsIdentityKeys(t *testing.T) {
	limiter, err := NewMemoryRateLimiter(MemoryRateLimiterOptions{
		Requests: 1,
		Window:   10 * time.Second,
		MaxKeys:  1,
	})
	if err != nil {
		t.Fatalf("new rate limiter: %v", err)
	}
	now := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	if decision, err := limiter.Allow(context.Background(), "ip:192.0.2.1"); err != nil || !decision.Allowed {
		t.Fatalf("first identity decision = %#v, error = %v", decision, err)
	}
	full, err := limiter.Allow(context.Background(), "ip:192.0.2.2")
	if err != nil || full.Allowed || full.RetryAfter != 10*time.Second {
		t.Fatalf("capacity decision = %#v, error = %v", full, err)
	}
	if len(limiter.buckets) != 1 {
		t.Fatalf("bucket count = %d", len(limiter.buckets))
	}

	now = now.Add(10 * time.Second)
	pruned, err := limiter.Allow(context.Background(), "ip:192.0.2.2")
	if err != nil || !pruned.Allowed {
		t.Fatalf("pruned decision = %#v, error = %v", pruned, err)
	}
	if _, exists := limiter.buckets["ip:192.0.2.1"]; exists {
		t.Fatal("expired identity was not pruned")
	}
}

func TestMemoryRateLimiterIsConcurrencySafe(t *testing.T) {
	limiter, err := NewMemoryRateLimiter(MemoryRateLimiterOptions{
		Requests: 10,
		Window:   time.Minute,
		MaxKeys:  1,
	})
	if err != nil {
		t.Fatalf("new rate limiter: %v", err)
	}
	fixed := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return fixed }
	var allowed atomic.Int64
	var failures atomic.Int64
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			decision, err := limiter.Allow(context.Background(), "principal:alice")
			if err != nil {
				failures.Add(1)
				return
			}
			if decision.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wait.Wait()
	if failures.Load() != 0 || allowed.Load() != 10 {
		t.Fatalf("allowed = %d, failures = %d", allowed.Load(), failures.Load())
	}
}

func TestMemoryRateLimiterValidatesInputsAndCancellation(t *testing.T) {
	for _, options := range []MemoryRateLimiterOptions{
		{Requests: -1},
		{Window: -time.Second},
		{MaxKeys: -1},
	} {
		if _, err := NewMemoryRateLimiter(options); !errors.Is(err, ErrInvalidRateLimiterOptions) {
			t.Fatalf("options %#v error = %v", options, err)
		}
	}
	limiter, err := NewMemoryRateLimiter(DefaultMemoryRateLimiterOptions())
	if err != nil {
		t.Fatalf("new rate limiter: %v", err)
	}
	if _, err := limiter.Allow(context.Background(), " "); !errors.Is(err, ErrInvalidRateLimitKey) {
		t.Fatalf("empty key error = %v", err)
	}
	if _, err := limiter.Allow(context.Background(), strings.Repeat("x", maxRateLimitKeyBytes+1)); !errors.Is(err, ErrInvalidRateLimitKey) {
		t.Fatalf("oversized key error = %v", err)
	}
	if _, err := limiter.Allow(nil, "key"); !errors.Is(err, ErrInvalidRateLimitKey) {
		t.Fatalf("nil context error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := limiter.Allow(cancelled, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
}
