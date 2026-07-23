package framework

import (
	"context"
	"io"
	"reflect"
	"testing"
)

func TestRouterRunsLaravelStylePipelineInOrder(t *testing.T) {
	router := NewRouter()
	router.Use(
		Traced("logging", LogRequests(io.Discard)),
		Traced("recovery", Recovery()),
		Traced("request-id", RequireRequestID()),
		Traced("auth", Authenticate("secret")),
	)
	router.Handle("test", func(*Context) (any, error) {
		return "controller result", nil
	})

	response := router.Dispatch(context.Background(), Request{
		ID: "request-1", Method: "test", Meta: map[string]string{"token": "secret"},
	})

	if response.Error != nil {
		t.Fatalf("unexpected error: %v", response.Error)
	}
	want := []string{
		"logging:before",
		"recovery:before",
		"request-id:before",
		"auth:before",
		"auth:after",
		"request-id:after",
		"recovery:after",
		"logging:after",
	}
	if got := response.Meta["pipeline"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("pipeline = %#v, want %#v", got, want)
	}
}

func TestRouterReturnsMethodNotFound(t *testing.T) {
	router := NewRouter()

	response := router.Dispatch(context.Background(), Request{ID: "1", Method: "missing"})

	if response.Error == nil || response.Error.Code != "method_not_found" {
		t.Fatalf("error = %#v, want method_not_found", response.Error)
	}
}

func TestRouterRejectsInvalidTokenBeforeController(t *testing.T) {
	router := NewRouter()
	router.Use(Authenticate("secret"))
	called := false
	router.Handle("test", func(*Context) (any, error) {
		called = true
		return nil, nil
	})

	response := router.Dispatch(context.Background(), Request{ID: "1", Method: "test"})
	if response.Error == nil || response.Error.Code != "unauthorized" {
		t.Fatalf("error = %#v, want unauthorized", response.Error)
	}
	if called {
		t.Fatal("controller was called after authentication failed")
	}
}

func TestRecoveryConvertsPanicToRPCError(t *testing.T) {
	router := NewRouter()
	router.Use(Recovery())
	router.Handle("panic", func(*Context) (any, error) {
		panic("boom")
	})

	response := router.Dispatch(context.Background(), Request{ID: "1", Method: "panic"})
	if response.Error == nil || response.Error.Code != "internal_error" {
		t.Fatalf("error = %#v, want internal_error", response.Error)
	}
}
