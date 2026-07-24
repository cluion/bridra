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

type Server struct {
	Router                *Router
	Input                 io.Reader
	Output                io.Writer
	Errors                io.Writer
	MaxConcurrentRequests int
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

	scanner := bufio.NewScanner(s.Input)
	scanner.Buffer(make([]byte, 64*1024), MaxRequestBytes)
	dispatchCtx, cancelDispatch := context.WithCancel(ctx)
	defer cancelDispatch()
	encoder := newServerResponseEncoder(s.Output, cancelDispatch)
	available := make(chan struct{}, maxConcurrentRequests)
	var active sync.WaitGroup

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

		select {
		case available <- struct{}{}:
		case <-dispatchCtx.Done():
			break scan
		}
		active.Add(1)
		go func() {
			defer active.Done()
			defer func() { <-available }()
			_ = encoder.Encode(s.Router.Dispatch(dispatchCtx, request))
		}()
	}

	scanError := scanner.Err()
	active.Wait()
	return errors.Join(scanError, encoder.Err())
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
