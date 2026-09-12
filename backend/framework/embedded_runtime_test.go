package framework

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmbeddedRuntimeDispatchesUnaryJSON(t *testing.T) {
	router := NewRouter()
	router.Handle("echo", func(ctx *Context) (any, error) {
		return map[string]any{"method": ctx.Request.Method}, nil
	})
	runtime, err := NewEmbeddedRuntime(router, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("NewEmbeddedRuntime() error = %v", err)
	}

	encoded, err := runtime.CallJSON(`{"id":"request-1","method":"echo"}`)
	if err != nil {
		t.Fatalf("CallJSON() error = %v", err)
	}
	var response Response
	if err := json.Unmarshal([]byte(encoded), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	result, ok := response.Result.(map[string]any)
	if !ok || result["method"] != "echo" {
		t.Fatalf("response result = %#v", response.Result)
	}
	if response.ID != "request-1" || response.Error != nil {
		t.Fatalf("response = %#v", response)
	}
}

func TestEmbeddedRuntimeStreamsOrderedJSONWithPullBackpressure(t *testing.T) {
	router := NewRouter()
	router.Handle("reports.build", func(ctx *Context) (any, error) {
		return ProduceStream(ctx, func(stream *StreamWriter) error {
			if err := stream.Report(Progress{Completed: 1, Total: 2}); err != nil {
				return err
			}
			return stream.Send(map[string]any{"page": 1})
		})
	})
	runtime, err := NewEmbeddedRuntime(router, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("NewEmbeddedRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	stream, err := runtime.StreamJSON(
		`{"id":"stream-1","method":"reports.build","meta":{"stream":"1"}}`,
	)
	if err != nil {
		t.Fatalf("StreamJSON() error = %v", err)
	}
	var responses []Response
	for {
		encoded, nextErr := stream.NextJSON()
		if nextErr != nil {
			t.Fatalf("NextJSON() error = %v", nextErr)
		}
		if encoded == "" {
			break
		}
		var response Response
		if err := json.Unmarshal([]byte(encoded), &response); err != nil {
			t.Fatalf("decode stream response: %v", err)
		}
		responses = append(responses, response)
	}
	if len(responses) != 3 {
		t.Fatalf("responses = %d, want 3", len(responses))
	}
	for index, response := range responses {
		if response.ID != "stream-1" || response.Stream == nil ||
			response.Stream.Sequence != int64(index+1) {
			t.Fatalf("response %d = %#v", index, response)
		}
	}
	if responses[0].Stream.Kind != streamProgressKind ||
		responses[1].Stream.Kind != streamDataKind ||
		responses[2].Stream.Kind != streamCompleteKind {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestEmbeddedRuntimeStreamCancellationUnblocksNext(t *testing.T) {
	started := make(chan struct{})
	router := NewRouter()
	router.Handle("wait", func(ctx *Context) (any, error) {
		return ProduceStream(ctx, func(stream *StreamWriter) error {
			close(started)
			<-stream.Context().Done()
			return stream.Context().Err()
		})
	})
	runtime, err := NewEmbeddedRuntime(router, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("NewEmbeddedRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	stream, err := runtime.StreamJSON(
		`{"id":"stream-1","method":"wait","meta":{"stream":"1"}}`,
	)
	if err != nil {
		t.Fatalf("StreamJSON() error = %v", err)
	}
	<-started
	if !runtime.Cancel("stream-1") {
		t.Fatal("Cancel() did not match active stream")
	}
	if encoded, err := stream.NextJSON(); encoded != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("NextJSON() = %q, %v", encoded, err)
	}
}

func TestEmbeddedRuntimeStreamRequiresMetadataAndID(t *testing.T) {
	runtime, err := NewEmbeddedRuntime(NewRouter(), func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("NewEmbeddedRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	if _, err := runtime.StreamJSON(`{"id":"one","method":"echo"}`); !errors.Is(
		err,
		ErrEmbeddedRuntimeStreamingRequired,
	) {
		t.Fatalf("missing stream metadata error = %v", err)
	}
	if _, err := runtime.StreamJSON(`{"method":"echo","meta":{"stream":"1"}}`); !errors.Is(
		err,
		ErrEmbeddedRuntimeStreamIDRequired,
	) {
		t.Fatalf("missing stream id error = %v", err)
	}
	var stream *EmbeddedJSONStream
	if _, err := stream.NextJSON(); !errors.Is(err, ErrEmbeddedRuntimeInvalid) {
		t.Fatalf("nil stream error = %v", err)
	}
}

func TestEmbeddedRuntimeCloseCancelsUnconsumedStream(t *testing.T) {
	started := make(chan struct{})
	router := NewRouter()
	router.Handle("blocked", func(ctx *Context) (any, error) {
		return ProduceStream(ctx, func(stream *StreamWriter) error {
			close(started)
			return stream.Send("never consumed")
		})
	})
	runtime, err := NewEmbeddedRuntime(router, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("NewEmbeddedRuntime() error = %v", err)
	}
	if _, err := runtime.StreamJSON(
		`{"id":"stream-1","method":"blocked","meta":{"stream":"1"}}`,
	); err != nil {
		t.Fatalf("StreamJSON() error = %v", err)
	}
	<-started
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestEmbeddedRuntimeRejectsInvalidTransportRequests(t *testing.T) {
	runtime, err := NewEmbeddedRuntime(
		NewRouter(),
		func(context.Context) error { return nil },
	)
	if err != nil {
		t.Fatalf("NewEmbeddedRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close(context.Background())
	})

	tests := []struct {
		name    string
		request string
		target  error
	}{
		{name: "empty", target: ErrEmbeddedRuntimeInvalidJSON},
		{
			name:    "multiple objects",
			request: `{"id":"one","method":"echo"} {"id":"two","method":"echo"}`,
			target:  ErrEmbeddedRuntimeInvalidJSON,
		},
		{
			name:    "stream",
			request: `{"id":"one","method":"echo","meta":{"stream":"1"}}`,
			target:  ErrEmbeddedRuntimeStreamingUnsupported,
		},
		{
			name:    "too large",
			request: strings.Repeat("x", MaxRequestBytes+1),
			target:  ErrEmbeddedRuntimeRequestTooLarge,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := runtime.CallJSON(test.request)
			if !errors.Is(err, test.target) {
				t.Fatalf("CallJSON() error = %v, want %v", err, test.target)
			}
		})
	}
}

func TestEmbeddedRuntimeCloseCancelsAndShutsDownExactlyOnce(t *testing.T) {
	started := make(chan struct{})
	router := NewRouter()
	router.Handle("wait", func(ctx *Context) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	var shutdownCalls atomic.Int32
	runtime, err := NewEmbeddedRuntime(router, func(context.Context) error {
		shutdownCalls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("NewEmbeddedRuntime() error = %v", err)
	}
	callDone := make(chan error, 1)
	go func() {
		_, err := runtime.CallJSON(`{"id":"request-1","method":"wait"}`)
		callDone <- err
	}()
	<-started

	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := <-callDone; err != nil {
		t.Fatalf("CallJSON() transport error = %v", err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if shutdownCalls.Load() != 1 {
		t.Fatalf("shutdown calls = %d, want 1", shutdownCalls.Load())
	}
	if _, err := runtime.CallJSON(`{"id":"request-2","method":"wait"}`); !errors.Is(err, ErrEmbeddedRuntimeClosed) {
		t.Fatalf("CallJSON() after close error = %v", err)
	}
}

func TestEmbeddedRuntimeCloseTimeoutDoesNotAbandonShutdown(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	router := NewRouter()
	router.Handle("blocked", func(*Context) (any, error) {
		close(started)
		<-release
		return "done", nil
	})
	var shutdownCalls atomic.Int32
	runtime, err := NewEmbeddedRuntimeWithOptions(
		router,
		func(context.Context) error {
			shutdownCalls.Add(1)
			return nil
		},
		EmbeddedRuntimeOptions{ShutdownTimeout: time.Second},
	)
	if err != nil {
		t.Fatalf("NewEmbeddedRuntimeWithOptions() error = %v", err)
	}
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = runtime.CallJSON(`{"id":"request-1","method":"blocked"}`)
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := runtime.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() error = %v", err)
	}
	close(release)
	<-callDone
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if shutdownCalls.Load() != 1 {
		t.Fatalf("shutdown calls = %d, want 1", shutdownCalls.Load())
	}
}

func TestEmbeddedRuntimeCancelInterruptsOnlyMatchingRequest(t *testing.T) {
	started := make(chan string, 2)
	router := NewRouter()
	router.Handle("wait", func(ctx *Context) (any, error) {
		started <- ctx.Request.ID
		<-ctx.Done()
		return map[string]any{"cause": context.Cause(ctx).Error()}, nil
	})
	runtime, err := NewEmbeddedRuntime(router, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("NewEmbeddedRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close(context.Background())
	})

	type callResult struct {
		encoded string
		err     error
	}
	firstDone := make(chan callResult, 1)
	secondDone := make(chan callResult, 1)
	go func() {
		encoded, err := runtime.CallJSON(`{"id":"first","method":"wait"}`)
		firstDone <- callResult{encoded: encoded, err: err}
	}()
	go func() {
		encoded, err := runtime.CallJSON(`{"id":"second","method":"wait"}`)
		secondDone <- callResult{encoded: encoded, err: err}
	}()

	seen := map[string]bool{<-started: true, <-started: true}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("started requests = %#v", seen)
	}
	if runtime.Cancel("") || runtime.Cancel("missing") {
		t.Fatal("Cancel() accepted an empty or unknown request id")
	}
	if !runtime.Cancel("first") || !runtime.Cancel("first") {
		t.Fatal("Cancel() did not remain idempotent for the active request")
	}
	first := <-firstDone
	if first.err != nil {
		t.Fatalf("first CallJSON() error = %v", first.err)
	}
	var response Response
	if err := json.Unmarshal([]byte(first.encoded), &response); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	result, ok := response.Result.(map[string]any)
	if !ok || result["cause"] != context.Canceled.Error() {
		t.Fatalf("first response result = %#v", response.Result)
	}
	select {
	case second := <-secondDone:
		t.Fatalf("second request completed early: %#v", second)
	case <-time.After(10 * time.Millisecond):
	}
	if !runtime.Cancel("second") {
		t.Fatal("Cancel() did not find the second request")
	}
	if second := <-secondDone; second.err != nil {
		t.Fatalf("second CallJSON() error = %v", second.err)
	}
}

func TestEmbeddedRuntimeRejectsDuplicateActiveRequestID(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	router := NewRouter()
	router.Handle("wait", func(*Context) (any, error) {
		close(started)
		<-release
		return "done", nil
	})
	runtime, err := NewEmbeddedRuntime(router, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("NewEmbeddedRuntime() error = %v", err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := runtime.CallJSON(`{"id":"duplicate","method":"wait"}`)
		firstDone <- err
	}()
	<-started

	if _, err := runtime.CallJSON(`{"id":"duplicate","method":"wait"}`); !errors.Is(
		err,
		ErrEmbeddedRuntimeDuplicateRequest,
	) {
		t.Fatalf("duplicate CallJSON() error = %v", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first CallJSON() error = %v", err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestEmbeddedRuntimeRejectsInvalidConfiguration(t *testing.T) {
	shutdown := func(context.Context) error { return nil }
	if _, err := NewEmbeddedRuntime(nil, shutdown); !errors.Is(err, ErrEmbeddedRuntimeInvalid) {
		t.Fatalf("nil router error = %v", err)
	}
	if _, err := NewEmbeddedRuntime(NewRouter(), nil); !errors.Is(err, ErrEmbeddedRuntimeInvalid) {
		t.Fatalf("nil shutdown error = %v", err)
	}
	if _, err := NewEmbeddedRuntimeWithOptions(
		NewRouter(), shutdown, EmbeddedRuntimeOptions{ShutdownTimeout: -1},
	); !errors.Is(err, ErrEmbeddedRuntimeInvalid) {
		t.Fatalf("negative timeout error = %v", err)
	}
	var runtime *EmbeddedRuntime
	if _, err := runtime.CallJSON(`{"id":"request-1","method":"echo"}`); !errors.Is(
		err,
		ErrEmbeddedRuntimeInvalid,
	) {
		t.Fatalf("nil runtime call error = %v", err)
	}
	if err := runtime.Close(context.Background()); !errors.Is(err, ErrEmbeddedRuntimeInvalid) {
		t.Fatalf("nil runtime close error = %v", err)
	}
}
