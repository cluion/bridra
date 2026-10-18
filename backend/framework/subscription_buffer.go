package framework

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

var ErrSubscriptionClosed = errors.New("framework: subscription buffer is closed")

type SubscriptionBufferOptions struct {
	Capacity      int
	MaxEventBytes int
}

// SubscriptionBuffer is an optional, in-memory replay source. Retention is
// bounded by both event count and encoded event size. A new instance has a new
// epoch, so a restart cannot silently reuse an old cursor. Use one buffer per
// authorized audience; durable/domain-specific history belongs to the application.
type SubscriptionBuffer[T any] struct {
	mu            sync.Mutex
	epoch         string
	capacity      int
	maxEventBytes int
	sequence      uint64
	records       []subscriptionRecord
	changed       chan struct{}
	closed        bool
}

type subscriptionRecord struct {
	sequence uint64
	value    json.RawMessage
}

func NewSubscriptionBuffer[T any](options SubscriptionBufferOptions) (*SubscriptionBuffer[T], error) {
	if options.Capacity == 0 {
		options.Capacity = 256
	}
	if options.MaxEventBytes == 0 {
		options.MaxEventBytes = 64 * 1024
	}
	if options.Capacity < 1 || options.Capacity > 4096 || options.MaxEventBytes < 1 || options.MaxEventBytes > MaxRequestBytes {
		return nil, fmt.Errorf("framework: subscription capacity must be 1..4096 and event bytes must be 1..%d", MaxRequestBytes)
	}
	var epoch [16]byte
	if _, err := rand.Read(epoch[:]); err != nil {
		return nil, fmt.Errorf("framework: subscription epoch: %w", err)
	}
	return &SubscriptionBuffer[T]{
		epoch: hex.EncodeToString(epoch[:]), capacity: options.Capacity,
		maxEventBytes: options.MaxEventBytes, changed: make(chan struct{}),
	}, nil
}

// Publish snapshots the JSON value. Later changes to the caller's object, or to
// a value returned by Next, cannot alter retained history.
func (b *SubscriptionBuffer[T]) Publish(value T) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("framework: encode subscription value: %w", err)
	}
	if len(encoded) > b.maxEventBytes {
		return "", NewError("subscription_event_too_large", "The subscription event exceeds its byte limit.")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return "", ErrSubscriptionClosed
	}
	if b.sequence == ^uint64(0) {
		return "", fmt.Errorf("framework: subscription sequence exhausted")
	}
	b.sequence++
	if len(b.records) == b.capacity {
		copy(b.records, b.records[1:])
		b.records[len(b.records)-1] = subscriptionRecord{b.sequence, encoded}
	} else {
		b.records = append(b.records, subscriptionRecord{b.sequence, encoded})
	}
	close(b.changed)
	b.changed = make(chan struct{})
	return b.cursor(b.sequence), nil
}

// Cursor returns the current position, including an empty buffer's position.
// An empty AfterCursor instead starts with the oldest currently retained event.
func (b *SubscriptionBuffer[T]) Cursor() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cursor(b.sequence)
}

func (b *SubscriptionBuffer[T]) Next(ctx context.Context, after string) (SubscriptionItem[T], error) {
	var zero SubscriptionItem[T]
	var sequence uint64
	if after != "" {
		epoch, suffix, ok := strings.Cut(after, ":")
		parsed, err := strconv.ParseUint(suffix, 10, 64)
		if !ok || err != nil || strconv.FormatUint(parsed, 10) != suffix {
			return zero, NewError("subscription_cursor_invalid", "The replay cursor is invalid.")
		}
		if epoch != b.epoch {
			return zero, NewError("subscription_cursor_expired", "The replay source changed; fetch a fresh snapshot.")
		}
		sequence = parsed
	}
	for {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		b.mu.Lock()
		if after != "" && (sequence > b.sequence || (len(b.records) > 0 && sequence < b.records[0].sequence-1)) {
			code := "subscription_cursor_expired"
			if sequence > b.sequence {
				code = "subscription_cursor_invalid"
			}
			b.mu.Unlock()
			return zero, NewError(code, "The replay cursor cannot be resumed; fetch a fresh snapshot.")
		}
		for _, record := range b.records {
			if after == "" || record.sequence > sequence {
				b.mu.Unlock()
				var value T
				if err := json.Unmarshal(record.value, &value); err != nil {
					return zero, fmt.Errorf("framework: decode subscription value: %w", err)
				}
				return SubscriptionItem[T]{Cursor: b.cursor(record.sequence), Value: value}, nil
			}
		}
		closed, changed := b.closed, b.changed
		b.mu.Unlock()
		if closed {
			return zero, io.EOF
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
}

// Close wakes readers; retained events can still be drained before io.EOF.
func (b *SubscriptionBuffer[T]) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		close(b.changed)
	}
}

func (b *SubscriptionBuffer[T]) cursor(sequence uint64) string {
	return b.epoch + ":" + strconv.FormatUint(sequence, 10)
}
