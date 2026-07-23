package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	appevents "github.com/cluion/bridra/backend/app/events"
	"github.com/cluion/bridra/backend/app/responses"
	"github.com/cluion/bridra/backend/app/services"
	"github.com/cluion/bridra/backend/framework"
)

func TestGreetingControllerDelegatesToService(t *testing.T) {
	fixedTime := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	controller := NewGreetingController(services.NewGreetingServiceWithClock(func() time.Time {
		return fixedTime
	}))
	params, _ := json.Marshal(map[string]string{"name": "Codex"})
	ctx := framework.NewContext(context.Background(), framework.Request{Params: params})

	result, err := controller.Hello(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	greeting := result.(responses.GreetingResponse)
	if greeting.Message != "Hello, Codex!" {
		t.Fatalf("message = %q", greeting.Message)
	}
	if greeting.Timestamp != "2026-07-20T12:00:00Z" {
		t.Fatalf("timestamp = %q", greeting.Timestamp)
	}
}

func TestGreetingControllerDispatchesCreatedEvent(t *testing.T) {
	dispatcher := framework.NewEventDispatcher()
	var captured appevents.GreetingCreated
	if err := framework.Listen(
		dispatcher,
		"test.capture",
		func(_ context.Context, event appevents.GreetingCreated) error {
			captured = event
			return nil
		},
	); err != nil {
		t.Fatalf("listen: %v", err)
	}
	controller := NewGreetingControllerWithEvents(
		services.NewGreetingService(),
		dispatcher,
	)
	ctx := framework.NewContext(context.Background(), framework.Request{
		Params: json.RawMessage(`{"name":"Codex"}`),
	})

	if _, err := controller.Hello(ctx); err != nil {
		t.Fatalf("hello: %v", err)
	}
	if captured.Greeting.Message != "Hello, Codex!" {
		t.Fatalf("event = %#v", captured)
	}
}

func TestGreetingControllerPropagatesListenerFailures(t *testing.T) {
	dispatcher := framework.NewEventDispatcher()
	want := errors.New("audit unavailable")
	if err := framework.Listen(
		dispatcher,
		"test.fail",
		func(context.Context, appevents.GreetingCreated) error { return want },
	); err != nil {
		t.Fatalf("listen: %v", err)
	}
	controller := NewGreetingControllerWithEvents(
		services.NewGreetingService(),
		dispatcher,
	)
	ctx := framework.NewContext(context.Background(), framework.Request{
		Params: json.RawMessage(`{"name":"Codex"}`),
	})

	_, err := controller.Hello(ctx)
	if !errors.Is(err, framework.ErrEventDispatchFailed) || !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}

func TestGreetingControllerRejectsLongNamesWithDetails(t *testing.T) {
	controller := NewGreetingController(services.NewGreetingService())
	params, _ := json.Marshal(map[string]string{"name": string(make([]rune, 65))})
	ctx := framework.NewContext(context.Background(), framework.Request{Params: params})

	_, err := controller.Hello(ctx)
	rpcError := framework.AsRPCError(err)
	if rpcError.Code != "validation_error" {
		t.Fatalf("error = %#v, want validation_error", rpcError)
	}
	violations, ok := rpcError.Data["violations"].([]framework.FieldViolation)
	if !ok || len(violations) != 1 {
		t.Fatalf("violations = %#v", rpcError.Data["violations"])
	}
	if violations[0].Field != "name" || violations[0].Parameters["max"] != 64 {
		t.Fatalf("violation = %#v", violations[0])
	}
}

func TestGreetingControllerRejectsUnknownParameters(t *testing.T) {
	controller := NewGreetingController(services.NewGreetingService())
	params := json.RawMessage(`{"name":"Codex","unknown":true}`)
	ctx := framework.NewContext(context.Background(), framework.Request{Params: params})

	_, err := controller.Hello(ctx)
	rpcError, ok := err.(*framework.RPCError)
	if !ok || rpcError.Code != "invalid_params" {
		t.Fatalf("error = %#v, want invalid_params", err)
	}
}
