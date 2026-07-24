package framework

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

const defaultMaxConcurrentRequests = 8
const defaultMaxPendingRequests = 64
const rpcCancellationMethod = "rpc.cancel"

type Server struct {
	Router                *Router
	Input                 io.Reader
	Output                io.Writer
	Errors                io.Writer
	MaxConcurrentRequests int
	MaxPendingRequests    int
}

func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil || s.Router == nil || s.Input == nil || s.Output == nil {
		return fmt.Errorf("framework: context, router, input, and output are required")
	}
	maxConcurrentRequests := s.MaxConcurrentRequests
	if maxConcurrentRequests == 0 {
		maxConcurrentRequests = defaultMaxConcurrentRequests
	}
	if maxConcurrentRequests < 0 {
		return fmt.Errorf("framework: max concurrent requests cannot be negative")
	}
	maxPendingRequests := s.MaxPendingRequests
	if maxPendingRequests == 0 {
		maxPendingRequests = defaultMaxPendingRequests
	}
	if maxPendingRequests < 0 {
		return fmt.Errorf("framework: max pending requests cannot be negative")
	}

	scanner := bufio.NewScanner(s.Input)
	scanner.Buffer(make([]byte, 64*1024), MaxRequestBytes)
	dispatchCtx, cancelDispatch := context.WithCancel(ctx)
	defer cancelDispatch()
	encoder := newServerResponseEncoder(s.Output, cancelDispatch)
	requests := newServerRequestRegistry()
	jobs := make(chan *serverRequest, maxPendingRequests)
	var workers sync.WaitGroup
	for range maxConcurrentRequests {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for request := range jobs {
				if request.Context.Err() == nil {
					_ = encoder.Encode(s.Router.Dispatch(request.Context, request.Request))
				}
				requests.Complete(request)
			}
		}()
	}

scan:
	for scanner.Scan() {
		if dispatchCtx.Err() != nil {
			break
		}
		var request Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if s.Errors != nil {
				fmt.Fprintf(s.Errors, "sidecar: invalid JSON request: %v\n", err)
			}
			if encodeErr := encoder.Encode(Response{
				Error: NewError("invalid_json", "The request is not valid JSON."),
			}); encodeErr != nil {
				break
			}
			continue
		}
		if request.Method == rpcCancellationMethod {
			requests.Cancel(request.ID, request.Meta["token"])
			continue
		}

		registered, duplicate := requests.Register(dispatchCtx, request)
		if duplicate {
			if encodeErr := encoder.Encode(Response{
				ID: request.ID,
				Error: NewError(
					"duplicate_request",
					"Another request already uses this id.",
				),
			}); encodeErr != nil {
				break
			}
			continue
		}
		select {
		case jobs <- registered:
		case <-dispatchCtx.Done():
			requests.Complete(registered)
			break scan
		default:
			requests.Complete(registered)
			if encodeErr := encoder.Encode(Response{
				ID:    request.ID,
				Error: NewError("server_busy", "The Go sidecar request queue is full."),
			}); encodeErr != nil {
				break scan
			}
		}
	}

	scanError := scanner.Err()
	close(jobs)
	workers.Wait()
	return errors.Join(scanError, encoder.Err())
}

type serverRequest struct {
	Context context.Context
	Request Request
	cancel  context.CancelFunc
}

type serverRequestRegistry struct {
	mu       sync.Mutex
	requests map[string]*serverRequest
}

func newServerRequestRegistry() *serverRequestRegistry {
	return &serverRequestRegistry{requests: make(map[string]*serverRequest)}
}

func (r *serverRequestRegistry) Register(
	parent context.Context,
	request Request,
) (*serverRequest, bool) {
	requestCtx, cancel := context.WithCancel(parent)
	registered := &serverRequest{
		Context: requestCtx,
		Request: request,
		cancel:  cancel,
	}
	if request.ID == "" {
		return registered, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.requests[request.ID]; exists {
		cancel()
		return nil, true
	}
	r.requests[request.ID] = registered
	return registered, false
}

func (r *serverRequestRegistry) Cancel(id, token string) {
	if id == "" || token == "" {
		return
	}
	r.mu.Lock()
	request := r.requests[id]
	if request == nil || request.Request.Meta["token"] != token {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	request.cancel()
}

func (r *serverRequestRegistry) Complete(request *serverRequest) {
	if request == nil {
		return
	}
	if request.Request.ID != "" {
		r.mu.Lock()
		if r.requests[request.Request.ID] == request {
			delete(r.requests, request.Request.ID)
		}
		r.mu.Unlock()
	}
	request.cancel()
}

type serverResponseEncoder struct {
	mu      sync.Mutex
	encoder *json.Encoder
	cancel  context.CancelFunc
	err     error
}

func newServerResponseEncoder(
	output io.Writer,
	cancel context.CancelFunc,
) *serverResponseEncoder {
	return &serverResponseEncoder{
		encoder: json.NewEncoder(output),
		cancel:  cancel,
	}
}

func (e *serverResponseEncoder) Encode(response Response) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	if err := e.encoder.Encode(response); err != nil {
		e.err = err
		e.cancel()
	}
	return e.err
}

func (e *serverResponseEncoder) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}
