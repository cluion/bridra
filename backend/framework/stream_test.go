package framework

import (
	"context"
	"testing"
)

func TestRouterDispatchStreamEmitsDataProgressAndCompletion(t *testing.T) {
	router := NewRouter()
	router.Use(Traced("trace", func(next Handler) Handler { return next }))
	router.Handle("reports.build", func(ctx *Context) (any, error) {
		return ProduceStream(ctx, func(stream *StreamWriter) error {
			if err := stream.Report(Progress{
				Completed: 1,
				Total:     2,
				Message:   "Preparing",
				Unit:      "steps",
			}); err != nil {
				return err
			}
			return stream.Send(map[string]any{"page": 1})
		})
	})

	var responses []Response
	err := router.DispatchStream(
		context.Background(),
		Request{ID: "stream-1", Method: "reports.build"},
		func(response Response) error {
			responses = append(responses, response)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("dispatch stream: %v", err)
	}
	if len(responses) != 3 {
		t.Fatalf("responses = %d, want 3", len(responses))
	}
	if frame := responses[0].Stream; frame == nil ||
		frame.Sequence != 1 ||
		frame.Kind != streamProgressKind ||
		frame.Progress == nil ||
		frame.Progress.Completed != 1 {
		t.Fatalf("progress response = %#v", responses[0])
	}
	if frame := responses[1].Stream; frame == nil ||
		frame.Sequence != 2 ||
		frame.Kind != streamDataKind {
		t.Fatalf("data response = %#v", responses[1])
	}
	if result, ok := responses[1].Result.(map[string]any); !ok || result["page"] != 1 {
		t.Fatalf("data result = %#v", responses[1].Result)
	}
	if frame := responses[2].Stream; frame == nil ||
		frame.Sequence != 3 ||
		frame.Kind != streamCompleteKind {
		t.Fatalf("complete response = %#v", responses[2])
	}
	if got := responses[2].Meta["pipeline"]; len(got.([]string)) != 2 {
		t.Fatalf("completion pipeline = %#v", got)
	}
}

func TestStreamingHandlerRejectsUnaryDispatch(t *testing.T) {
	router := NewRouter()
	router.Handle("reports.build", func(ctx *Context) (any, error) {
		return ProduceStream(ctx, func(*StreamWriter) error { return nil })
	})

	response := router.Dispatch(
		context.Background(),
		Request{ID: "1", Method: "reports.build"},
	)
	if response.Error == nil || response.Error.Code != "streaming_required" {
		t.Fatalf("response = %#v", response)
	}
}

func TestRouterDispatchStreamRendersProgressValidationErrors(t *testing.T) {
	router := NewRouter()
	router.Handle("reports.build", func(ctx *Context) (any, error) {
		return ProduceStream(ctx, func(stream *StreamWriter) error {
			return stream.Report(Progress{Completed: 2, Total: 1})
		})
	})

	var responses []Response
	err := router.DispatchStream(
		context.Background(),
		Request{ID: "1", Method: "reports.build"},
		func(response Response) error {
			responses = append(responses, response)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("dispatch stream: %v", err)
	}
	if len(responses) != 1 ||
		responses[0].Error == nil ||
		responses[0].Error.Code != "invalid_progress" ||
		responses[0].Stream == nil ||
		responses[0].Stream.Kind != streamCompleteKind {
		t.Fatalf("responses = %#v", responses)
	}
}
