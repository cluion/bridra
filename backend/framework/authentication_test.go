package framework

import (
	"context"
	"errors"
	"testing"
)

func TestPrincipalValidatesAndChecksExactPermissions(t *testing.T) {
	if _, err := NewPrincipal(""); !errors.Is(err, ErrInvalidPrincipal) {
		t.Fatalf("empty subject error = %v", err)
	}
	if _, err := NewPrincipal("user-1", ""); !errors.Is(err, ErrInvalidPrincipal) {
		t.Fatalf("empty permission error = %v", err)
	}

	principal, err := NewPrincipal(" user-1 ", "reports.read", "reports.read")
	if err != nil {
		t.Fatalf("new principal: %v", err)
	}
	if principal.Subject() != "user-1" {
		t.Fatalf("subject = %q", principal.Subject())
	}
	if !principal.HasPermission("reports.read") || principal.HasPermission("reports.write") {
		t.Fatalf("permissions did not use exact matching")
	}
}

func TestStaticTokenAuthenticator(t *testing.T) {
	principal, err := NewPrincipal("service")
	if err != nil {
		t.Fatalf("new principal: %v", err)
	}
	if _, err := NewStaticTokenAuthenticator("", principal); !errors.Is(err, ErrInvalidAuthenticator) {
		t.Fatalf("empty token error = %v", err)
	}
	authenticator, err := NewStaticTokenAuthenticator("secret", principal)
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}

	authenticated, err := authenticator.Authenticate(context.Background(), "secret")
	if err != nil || authenticated.Subject() != "service" {
		t.Fatalf("authenticated = %#v, error = %v", authenticated, err)
	}
	if _, err := authenticator.Authenticate(context.Background(), "wrong"); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("wrong token error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := authenticator.Authenticate(cancelled, "secret"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
}

func TestRequirePermissionUsesContextPrincipal(t *testing.T) {
	principal, err := NewPrincipal("user-1", "reports.read")
	if err != nil {
		t.Fatalf("new principal: %v", err)
	}
	router := NewRouter()
	controllerCalls := 0
	router.HandleWithPolicies(
		"reports.read",
		func(ctx *Context) (any, error) {
			controllerCalls++
			authenticated, exists := PrincipalFromContext(ctx)
			if !exists {
				return nil, errors.New("expected principal")
			}
			return authenticated.Subject() + ":" + ctx.Request.Method, nil
		},
		RequirePermission("reports.read"),
	)

	unauthenticated := router.Dispatch(context.Background(), Request{ID: "1", Method: "reports.read"})
	if unauthenticated.Error == nil || unauthenticated.Error.Code != "unauthenticated" {
		t.Fatalf("unauthenticated response = %#v", unauthenticated)
	}

	deniedPrincipal, err := NewPrincipal("user-2", "reports.write")
	if err != nil {
		t.Fatalf("new denied principal: %v", err)
	}
	denied := router.Dispatch(
		ContextWithPrincipal(context.Background(), deniedPrincipal),
		Request{ID: "2", Method: "reports.read"},
	)
	if denied.Error == nil || denied.Error.Code != "forbidden" {
		t.Fatalf("denied response = %#v", denied)
	}

	allowed := router.Dispatch(
		ContextWithPrincipal(context.Background(), principal),
		Request{ID: "3", Method: "reports.read"},
	)
	if allowed.Error != nil || allowed.Result != "user-1:reports.read" {
		t.Fatalf("allowed response = %#v", allowed)
	}
	if controllerCalls != 1 {
		t.Fatalf("controller calls = %d", controllerCalls)
	}
}

func TestContextWithPrincipalRejectsInvalidPrincipal(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil || !errors.Is(recovered.(error), ErrInvalidPrincipal) {
			t.Fatalf("panic = %v", recovered)
		}
	}()
	ContextWithPrincipal(context.Background(), Principal{})
}
