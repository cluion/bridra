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
	if err := runtime.Close(context.Background()); !errors.Is(err, ErrEmbeddedRuntimeInvalid) {
		t.Fatalf("nil runtime close error = %v", err)
	}
}
