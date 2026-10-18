package framework

import (
	"context"
	"errors"
	"io"
	"time"
)

// SubscriptionItem is an application event with an opaque replay cursor. Cursors
// belong to the source, not to the per-connection RPC stream sequence.
type SubscriptionItem[T any] struct {
	Cursor string
	Value  T
}

// SubscriptionNext returns the first event strictly after the supplied cursor.
// It must honor context cancellation and deadlines, return io.EOF on permanent
// completion, and return subscription_cursor_expired when replay needs a resync.
// Reads must be repeatable: delivery can fail after Next returns an event.
type SubscriptionNext[T any] func(context.Context, string) (SubscriptionItem[T], error)

type SubscriptionOptions struct {
	AfterCursor       string
	HeartbeatInterval time.Duration
}

// ProduceSubscription uses ordinary data frames for {kind,cursor,value}
// envelopes. This preserves the existing wire protocol and transport flow control.
// Authorization and cursor scoping must be checked before calling this helper.
func ProduceSubscription[T any](ctx *Context, options SubscriptionOptions, next SubscriptionNext[T]) (any, error) {
	if next == nil || options.HeartbeatInterval < 0 {
		return nil, NewError("invalid_subscription", "A source and a non-negative heartbeat interval are required.")
	}
	if len(options.AfterCursor) > 4096 {
		return nil, NewError("subscription_cursor_invalid", "The replay cursor exceeds 4096 bytes.")
	}
	interval := options.HeartbeatInterval
	if interval == 0 {
		interval = 15 * time.Second
	}
	return ProduceStream(ctx, func(stream *StreamWriter) error {
		cursor := options.AfterCursor
		for {
			readContext, cancel := context.WithTimeout(stream.Context(), interval)
			item, err := next(readContext, cursor)
			timedOut := errors.Is(readContext.Err(), context.DeadlineExceeded)
			cancel()
			if stream.Context().Err() != nil {
				return stream.Context().Err()
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if errors.Is(err, context.DeadlineExceeded) && timedOut {
				if err := stream.Send(map[string]any{"kind": "heartbeat", "cursor": cursor}); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			if item.Cursor == "" || item.Cursor == cursor || len(item.Cursor) > 4096 {
				return NewError("subscription_cursor_invalid", "The source must advance a non-empty replay cursor of at most 4096 bytes.")
			}
			if err := stream.Send(map[string]any{
				"kind": "data", "cursor": item.Cursor, "value": item.Value,
			}); err != nil {
				return err
			}
			cursor = item.Cursor
		}
	})
}
