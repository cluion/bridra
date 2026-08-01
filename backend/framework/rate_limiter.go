package framework

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"
)

const (
	defaultRateLimitRequests = 600
	defaultRateLimitWindow   = time.Minute
	defaultRateLimitMaxKeys  = 10_000
	maxRateLimitKeyBytes     = 512
)

var (
	ErrInvalidRateLimiterOptions = errors.New("framework: rate limiter options are invalid")
	ErrInvalidRateLimitKey       = errors.New("framework: rate limit key is invalid")
)

type RateLimitDecision struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
}

type RateLimiter interface {
	Allow(context.Context, string) (RateLimitDecision, error)
}

type RateLimiterFunc func(context.Context, string) (RateLimitDecision, error)

func (allow RateLimiterFunc) Allow(
	ctx context.Context,
	key string,
) (RateLimitDecision, error) {
	return allow(ctx, key)
}

type MemoryRateLimiterOptions struct {
	Requests int
	Window   time.Duration
	MaxKeys  int
}

func DefaultMemoryRateLimiterOptions() MemoryRateLimiterOptions {
	return MemoryRateLimiterOptions{
		Requests: defaultRateLimitRequests,
		Window:   defaultRateLimitWindow,
		MaxKeys:  defaultRateLimitMaxKeys,
	}
}

type rateLimitBucket struct {
	tokens float64
	last   time.Time
}

type MemoryRateLimiter struct {
	requests int
	window   time.Duration
	maxKeys  int
	now      func() time.Time

	mu      sync.Mutex
	buckets map[string]rateLimitBucket
}

func NewMemoryRateLimiter(options MemoryRateLimiterOptions) (*MemoryRateLimiter, error) {
	defaults := DefaultMemoryRateLimiterOptions()
	if options.Requests < 0 || options.Window < 0 || options.MaxKeys < 0 {
		return nil, ErrInvalidRateLimiterOptions
	}
	if options.Requests == 0 {
		options.Requests = defaults.Requests
	}
	if options.Window == 0 {
		options.Window = defaults.Window
	}
	if options.MaxKeys == 0 {
		options.MaxKeys = defaults.MaxKeys
	}
	if options.Requests < 1 || options.Window <= 0 || options.MaxKeys < 1 {
		return nil, ErrInvalidRateLimiterOptions
	}
	return &MemoryRateLimiter{
		requests: options.Requests,
		window:   options.Window,
		maxKeys:  options.MaxKeys,
		now:      time.Now,
		buckets:  make(map[string]rateLimitBucket),
	}, nil
}

func (limiter *MemoryRateLimiter) Allow(
	ctx context.Context,
	key string,
) (RateLimitDecision, error) {
	if ctx == nil {
		return RateLimitDecision{}, ErrInvalidRateLimitKey
	}
	if err := ctx.Err(); err != nil {
		return RateLimitDecision{}, err
	}
	key = strings.TrimSpace(key)
	if key == "" || len(key) > maxRateLimitKeyBytes {
		return RateLimitDecision{}, ErrInvalidRateLimitKey
	}

	now := limiter.now()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	bucket, exists := limiter.buckets[key]
	if !exists {
		limiter.pruneFullBuckets(now)
		if len(limiter.buckets) >= limiter.maxKeys {
			return limiter.capacityDecision(now), nil
		}
		bucket = rateLimitBucket{
			tokens: float64(limiter.requests),
			last:   now,
		}
	}

	elapsed := now.Sub(bucket.last)
	if elapsed < 0 {
		elapsed = 0
	}
	refillPerNanosecond := float64(limiter.requests) / float64(limiter.window)
	bucket.tokens = math.Min(
		float64(limiter.requests),
		bucket.tokens+float64(elapsed)*refillPerNanosecond,
	)
	bucket.last = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		limiter.buckets[key] = bucket
		return RateLimitDecision{
			Allowed:   true,
			Limit:     limiter.requests,
			Remaining: int(math.Floor(bucket.tokens)),
		}, nil
	}

	limiter.buckets[key] = bucket
	retryAfter := time.Duration(math.Ceil((1 - bucket.tokens) / refillPerNanosecond))
	if retryAfter < time.Nanosecond {
		retryAfter = time.Nanosecond
	}
	return RateLimitDecision{
		Limit:      limiter.requests,
		RetryAfter: retryAfter,
	}, nil
}

func (limiter *MemoryRateLimiter) pruneFullBuckets(now time.Time) {
	for key, bucket := range limiter.buckets {
		if now.Sub(bucket.last) >= limiter.window {
			delete(limiter.buckets, key)
		}
	}
}

func (limiter *MemoryRateLimiter) capacityDecision(now time.Time) RateLimitDecision {
	retryAfter := limiter.window
	for _, bucket := range limiter.buckets {
		candidate := bucket.last.Add(limiter.window).Sub(now)
		if candidate > 0 && candidate < retryAfter {
			retryAfter = candidate
		}
	}
	if retryAfter < time.Nanosecond {
		retryAfter = time.Nanosecond
	}
	return RateLimitDecision{
		Limit:      limiter.requests,
		RetryAfter: retryAfter,
	}
}
