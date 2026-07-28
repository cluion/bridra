package framework

import (
	"context"
	"errors"
	"sync"
)

const (
	streamDataKind     = "data"
	streamProgressKind = "progress"
	streamCompleteKind = "complete"
)

type StreamProducer func(*StreamWriter) error

type StreamWriter struct {
	ctx       context.Context
	requestID string
	meta      func() map[string]any
	emit      func(Response) error

	mu        sync.Mutex
	sequence  int64
	completed bool
	err       error
}

type streamCompleted struct{}

func ProduceStream(ctx *Context, producer StreamProducer) (any, error) {
	if ctx == nil || ctx.stream == nil {
		return nil, NewError(
			"streaming_required",
			"This method must be called through the streaming RPC API.",
		)
	}
	if producer == nil {
		return nil, NewError("invalid_stream", "The stream producer is required.")
	}
	if err := producer(ctx.stream); err != nil {
		return nil, err
	}
	return streamCompleted{}, nil
}

func (w *StreamWriter) Context() context.Context {
	if w == nil {
		return context.Background()
	}
	return w.ctx
}

func (w *StreamWriter) Send(result any) error {
	return w.emitFrame(streamDataKind, result, nil, nil)
}

func (w *StreamWriter) Report(progress Progress) error {
	if progress.Completed < 0 || progress.Total <= 0 || progress.Completed > progress.Total {
		return NewError(
			"invalid_progress",
			"Progress requires 0 <= completed <= total and total > 0.",
		)
	}
	return w.emitFrame(streamProgressKind, nil, &progress, nil)
}

func (w *StreamWriter) complete(rpcError *RPCError) error {
	return w.emitFrame(streamCompleteKind, nil, nil, rpcError)
}

func (w *StreamWriter) emitFrame(
	kind string,
	result any,
	progress *Progress,
	rpcError *RPCError,
) error {
	if w == nil {
		return errors.New("framework: stream writer is nil")
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.completed {
		return NewError("stream_closed", "The stream is already complete.")
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}

	w.sequence++
	response := Response{
		ID:     w.requestID,
		Result: result,
		Error:  rpcError,
		Meta:   w.meta(),
		Stream: &StreamFrame{
			Sequence: w.sequence,
			Kind:     kind,
			Progress: progress,
		},
	}
	if err := w.emit(response); err != nil {
		w.err = err
		return err
	}
	if kind == streamCompleteKind {
		w.completed = true
	}
	return nil
}
