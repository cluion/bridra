package framework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const defaultEmbeddedRuntimeShutdownTimeout = 5 * time.Second

var (
	ErrEmbeddedRuntimeInvalid              = errors.New("framework: embedded runtime is invalid")
	ErrEmbeddedRuntimeClosed               = errors.New("framework: embedded runtime is closed")
	ErrEmbeddedRuntimeContextUnavailable   = errors.New("framework: embedded runtime context is unavailable")
	ErrEmbeddedRuntimeInvalidJSON          = errors.New("framework: embedded runtime request is not valid JSON")
	ErrEmbeddedRuntimeRequestTooLarge      = errors.New("framework: embedded runtime request is too large")
	ErrEmbeddedRuntimeStreamingUnsupported = errors.New("framework: embedded runtime streaming requires the streaming API")
)

type EmbeddedRuntimeShutdown func(context.Context) error

type EmbeddedRuntimeOptions struct {
	ShutdownTimeout time.Duration
}

type embeddedRuntimeState uint8

const (
	embeddedRuntimeOpen embeddedRuntimeState = iota
	embeddedRuntimeClosing
	embeddedRuntimeClosed
)

// EmbeddedRuntime dispatches RPC requests inside the application process. It
// owns request cancellation and coordinates exactly-once application shutdown,
// but leaves platform lifecycle and native resource access to the host app.
type EmbeddedRuntime struct {
	router          *Router
	shutdown        EmbeddedRuntimeShutdown
	shutdownTimeout time.Duration
	rootContext     context.Context
	cancelRoot      context.CancelCauseFunc

	mu        sync.Mutex
	state     embeddedRuntimeState
	active    sync.WaitGroup
	closeDone chan struct{}
	closeErr  error
}

func NewEmbeddedRuntime(
	router *Router,
	shutdown EmbeddedRuntimeShutdown,
) (*EmbeddedRuntime, error) {
	return NewEmbeddedRuntimeWithOptions(
		router,
		shutdown,
		EmbeddedRuntimeOptions{},
	)
}

func NewEmbeddedRuntimeWithOptions(
	router *Router,
	shutdown EmbeddedRuntimeShutdown,
	options EmbeddedRuntimeOptions,
) (*EmbeddedRuntime, error) {
	if router == nil || shutdown == nil {
		return nil, ErrEmbeddedRuntimeInvalid
	}
	shutdownTimeout := options.ShutdownTimeout
	if shutdownTimeout == 0 {
		shutdownTimeout = defaultEmbeddedRuntimeShutdownTimeout
	}
	if shutdownTimeout < 0 {
		return nil, ErrEmbeddedRuntimeInvalid
	}
	rootContext, cancelRoot := context.WithCancelCause(context.Background())
	return &EmbeddedRuntime{
		router:          router,
		shutdown:        shutdown,
		shutdownTimeout: shutdownTimeout,
		rootContext:     rootContext,
		cancelRoot:      cancelRoot,
		closeDone:       make(chan struct{}),
	}, nil
}

// CallJSON dispatches one unary JSON RPC request and returns one JSON response.
// Transport failures are returned as Go errors; application RPC failures remain
// encoded in the response envelope.
func (runtime *EmbeddedRuntime) CallJSON(requestJSON string) (string, error) {
	requestContext, finish, err := runtime.beginRequest()
	if err != nil {
		return "", err
	}
	defer finish()

	if len(requestJSON) > MaxRequestBytes {
		return "", ErrEmbeddedRuntimeRequestTooLarge
	}
	decoder := json.NewDecoder(strings.NewReader(requestJSON))
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return "", fmt.Errorf("%w: %v", ErrEmbeddedRuntimeInvalidJSON, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", ErrEmbeddedRuntimeInvalidJSON
	}
	if requestsStream(request) {
		return "", ErrEmbeddedRuntimeStreamingUnsupported
	}

	response := runtime.router.Dispatch(requestContext, request)
	encoded, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("framework: encode embedded runtime response: %w", err)
	}
	return string(encoded), nil
}

// Close cancels active requests and waits for the runtime's asynchronous,
// exactly-once shutdown. A caller timeout does not abandon shutdown; a later
// call can continue waiting for the same result.
func (runtime *EmbeddedRuntime) Close(ctx context.Context) error {
	if runtime == nil {
		return ErrEmbeddedRuntimeInvalid
	}
	if ctx == nil {
		return ErrEmbeddedRuntimeContextUnavailable
	}

	runtime.mu.Lock()
	if runtime.state == embeddedRuntimeOpen {
		runtime.state = embeddedRuntimeClosing
		runtime.cancelRoot(ErrEmbeddedRuntimeClosed)
		go runtime.finishClose()
	}
	done := runtime.closeDone
	runtime.mu.Unlock()

	select {
	case <-done:
		runtime.mu.Lock()
		err := runtime.closeErr
		runtime.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (runtime *EmbeddedRuntime) beginRequest() (
	context.Context,
	func(),
	error,
) {
	if runtime == nil {
		return nil, nil, ErrEmbeddedRuntimeInvalid
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.state != embeddedRuntimeOpen {
		return nil, nil, ErrEmbeddedRuntimeClosed
	}
	runtime.active.Add(1)
	return runtime.rootContext, runtime.active.Done, nil
}

func (runtime *EmbeddedRuntime) finishClose() {
	runtime.active.Wait()
	shutdownContext, cancel := context.WithTimeout(
		context.Background(),
		runtime.shutdownTimeout,
	)
	defer cancel()
	shutdownErr := runtime.shutdown(shutdownContext)

	runtime.mu.Lock()
	runtime.closeErr = shutdownErr
	runtime.state = embeddedRuntimeClosed
	close(runtime.closeDone)
	runtime.mu.Unlock()
}
