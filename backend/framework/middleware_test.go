package framework

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLoggingIncludesRecoveredPanics(t *testing.T) {
	var logs bytes.Buffer
	router := NewRouter()
	router.Use(LogRequests(&logs), Recovery())
	router.Handle("panic", func(*Context) (any, error) { panic("boom") })

	response := router.Dispatch(context.Background(), Request{ID: "1", Method: "panic"})

	if response.Error == nil || response.Error.Code != "internal_error" {
		t.Fatalf("response = %#v", response)
	}
	if !strings.Contains(logs.String(), "internal_error") {
		t.Fatalf("logs = %q", logs.String())
	}
}

func TestLoggingIncludesRejectedRequests(t *testing.T) {
	var logs bytes.Buffer
	router := NewRouter()
	router.Use(LogRequests(&logs), Authenticate("secret"))
	router.Handle("test", func(*Context) (any, error) { return nil, nil })

	router.Dispatch(context.Background(), Request{ID: "1", Method: "test"})

	if !strings.Contains(logs.String(), "unauthorized") {
		t.Fatalf("logs = %q", logs.String())
	}
}

func TestAuthenticateAcceptsTrustedPrincipalOrSidecarToken(t *testing.T) {
	principal, err := NewPrincipal("user-1")
	if err != nil {
		t.Fatalf("new principal: %v", err)
	}
	router := NewRouter()
	router.Use(Authenticate("sidecar-secret"))
	router.Handle("test", func(*Context) (any, error) { return "ok", nil })

	fromSidecar := router.Dispatch(context.Background(), Request{
		ID: "1", Method: "test", Meta: map[string]string{"token": "sidecar-secret"},
	})
	if fromSidecar.Error != nil || fromSidecar.Result != "ok" {
		t.Fatalf("sidecar response = %#v", fromSidecar)
	}

	fromHTTPIdentity := router.Dispatch(
		ContextWithPrincipal(context.Background(), principal),
		Request{ID: "2", Method: "test"},
	)
	if fromHTTPIdentity.Error != nil || fromHTTPIdentity.Result != "ok" {
		t.Fatalf("principal response = %#v", fromHTTPIdentity)
	}
}
