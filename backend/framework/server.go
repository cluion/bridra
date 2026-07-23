package framework

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

type Server struct {
	Router *Router
	Input  io.Reader
	Output io.Writer
	Errors io.Writer
}

func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil || s.Router == nil || s.Input == nil || s.Output == nil {
		return fmt.Errorf("framework: context, router, input, and output are required")
	}

	scanner := bufio.NewScanner(s.Input)
	scanner.Buffer(make([]byte, 64*1024), MaxRequestBytes)
	encoder := json.NewEncoder(s.Output)

	for scanner.Scan() {
		var request Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if s.Errors != nil {
				fmt.Fprintf(s.Errors, "sidecar: invalid JSON request: %v\n", err)
			}
			if encodeErr := encoder.Encode(Response{
				Error: NewError("invalid_json", "The request is not valid JSON."),
			}); encodeErr != nil {
				return encodeErr
			}
			continue
		}

		response := s.Router.Dispatch(ctx, request)
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}

	return scanner.Err()
}
