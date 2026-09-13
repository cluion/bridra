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
	ErrEmbeddedRuntimeDuplicateRequest     = errors.New("framework: embedded runtime request id is already active")
	ErrEmbeddedRuntimeStreamingUnsupported = errors.New("framework: embedded runtime streaming requires the streaming API")
	ErrEmbeddedRuntimeStreamingRequired    = errors.New("framework: embedded runtime streaming request metadata is required")
	ErrEmbeddedRuntimeStreamIDRequired     = errors.New("framework: embedded runtime streaming request id is required")
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
	requests  map[string]context.CancelCauseFunc
	closeDone chan struct{}
	closeErr  error
}

// EmbeddedJSONStream exposes one server-streaming RPC as ordered JSON frames.
// NextJSON applies pull-based backpressure: the Router cannot produce the next
// frame until the current call receives it.
type EmbeddedJSONStream struct {
	requestID string
	frames    chan string

	mu  sync.Mutex
	err error
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
		requests:        make(map[string]context.CancelCauseFunc),
		closeDone:       make(chan struct{}),
	}, nil
}

// CallJSON dispatches one unary JSON RPC request and returns one JSON response.
// Transport failures are returned as Go errors; application RPC failures remain
// encoded in the response envelope.
func (runtime *EmbeddedRuntime) CallJSON(requestJSON string) (string, error) {
	if runtime == nil {
		return "", ErrEmbeddedRuntimeInvalid
	}
	request, err := decodeEmbeddedRuntimeRequest(requestJSON)
	if err != nil {
		return "", err
	}
	if requestsStream(request) {
		return "", ErrEmbeddedRuntimeStreamingUnsupported
	}
	requestContext, finish, err := runtime.beginRequest(request.ID)
	if err != nil {
		return "", err
	}
	defer finish()

	response := runtime.router.Dispatch(requestContext, request)
	encoded, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("framework: encode embedded runtime response: %w", err)
	}
	return string(encoded), nil
}

// StreamJSON starts one server-streaming JSON RPC request. The caller must read
// NextJSON until it returns an empty string. Application RPC errors remain in a
// terminal completion frame; transport and lifecycle failures are Go errors.
func (runtime *EmbeddedRuntime) StreamJSON(requestJSON string) (*EmbeddedJSONStream, error) {
	if runtime == nil {
		return nil, ErrEmbeddedRuntimeInvalid
	}
	request, err := decodeEmbeddedRuntimeRequest(requestJSON)
	if err != nil {
		return nil, err
	}
	if !requestsStream(request) {
		return nil, ErrEmbeddedRuntimeStreamingRequired
	}
	if request.ID == "" {
		return nil, ErrEmbeddedRuntimeStreamIDRequired
	}
	requestContext, finish, err := runtime.beginRequest(request.ID)
	if err != nil {
		return nil, err
	}
	stream := &EmbeddedJSONStream{
		requestID: request.ID,
		frames:    make(chan string),
	}
	go func() {
		dispatchErr := runtime.router.DispatchStream(
			requestContext,
			request,
			func(response Response) error {
				encoded, encodeErr := json.Marshal(response)
				if encodeErr != nil {
					return fmt.Errorf(
						"framework: encode embedded runtime stream response: %w",
						encodeErr,
					)
				}
				select {
				case stream.frames <- string(encoded):
					return nil
				case <-requestContext.Done():
					return context.Cause(requestContext)
				}
			},
		)
		stream.finish(dispatchErr)
		finish()
	}()
	return stream, nil
}

func (stream *EmbeddedJSONStream) RequestID() string {
	if stream == nil {
		return ""
	}
	return stream.requestID
}

// NextJSON blocks until the next ordered stream frame is available. An empty
// response with a nil error marks a normally completed stream.
func (stream *EmbeddedJSONStream) NextJSON() (string, error) {
	if stream == nil || stream.frames == nil {
		return "", ErrEmbeddedRuntimeInvalid
	}
	frame, ok := <-stream.frames
	if ok {
		return frame, nil
	}
	stream.mu.Lock()
	err := stream.err
	stream.mu.Unlock()
	return "", err
}

func (stream *EmbeddedJSONStream) finish(err error) {
	stream.mu.Lock()
	stream.err = err
	close(stream.frames)
	stream.mu.Unlock()
}

// Cancel interrupts one active request by its RPC id. It returns false when
// the id is empty or no matching request is active. Cancellation is idempotent
// while the request remains active.
func (runtime *EmbeddedRuntime) Cancel(requestID string) bool {
	if runtime == nil || requestID == "" {
		return false
	}
	runtime.mu.Lock()
	cancel, exists := runtime.requests[requestID]
	runtime.mu.Unlock()
	if exists {
		cancel(context.Canceled)
	}
	return exists
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

func (runtime *EmbeddedRuntime) beginRequest(requestID string) (
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
	if requestID != "" {
		if _, exists := runtime.requests[requestID]; exists {
			return nil, nil, ErrEmbeddedRuntimeDuplicateRequest
		}
	}
	requestContext, cancelRequest := context.WithCancelCause(runtime.rootContext)
	runtime.active.Add(1)
	if requestID != "" {
		runtime.requests[requestID] = cancelRequest
	}
	return requestContext, func() {
		cancelRequest(nil)
		runtime.mu.Lock()
		delete(runtime.requests, requestID)
		runtime.mu.Unlock()
		runtime.active.Done()
	}, nil
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

func decodeEmbeddedRuntimeRequest(requestJSON string) (Request, error) {
	if len(requestJSON) > MaxRequestBytes {
		return Request{}, ErrEmbeddedRuntimeRequestTooLarge
	}
	decoder := json.NewDecoder(strings.NewReader(requestJSON))
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, fmt.Errorf("%w: %v", ErrEmbeddedRuntimeInvalidJSON, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Request{}, ErrEmbeddedRuntimeInvalidJSON
	}
	return request, nil
}
