package framework_test

import (
	"context"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestPublicServerStreamingAPI(t *testing.T) {
	router := framework.NewRouter()
	router.Handle("public.stream", func(ctx *framework.Context) (any, error) {
		return framework.ProduceStream(
			ctx,
			func(stream *framework.StreamWriter) error {
				if stream.Context() == nil {
					t.Fatal("stream context is nil")
				}
				if err := stream.Report(framework.Progress{
					Completed: 1,
					Total:     1,
					Unit:      "items",
				}); err != nil {
					return err
				}
				return stream.Send("done")
			},
		)
	})

	var responses []framework.Response
	err := router.DispatchStream(
		context.Background(),
		framework.Request{ID: "1", Method: "public.stream"},
		func(response framework.Response) error {
			responses = append(responses, response)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("dispatch stream: %v", err)
	}
	if len(responses) != 3 ||
		responses[0].Stream == nil ||
		responses[0].Stream.Progress == nil ||
		responses[1].Result != "done" ||
		responses[2].Stream == nil {
		t.Fatalf("responses = %#v", responses)
	}
}
