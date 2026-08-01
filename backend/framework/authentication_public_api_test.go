package framework_test

import (
	"context"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestPublicAuthenticationAndAuthorizationAPI(t *testing.T) {
	principal, err := framework.NewPrincipal("user-1", "reports.read")
	if err != nil {
		t.Fatalf("new principal: %v", err)
	}
	authenticator, err := framework.NewStaticTokenAuthenticator("secret", principal)
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}
	authenticated, err := authenticator.Authenticate(context.Background(), "secret")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	router := framework.NewRouter()
	router.HandleWithPolicies(
		"reports.read",
		func(ctx *framework.Context) (any, error) {
			requestPrincipal, exists := framework.PrincipalFromContext(ctx)
			if !exists {
				t.Fatal("principal was not propagated")
			}
			return requestPrincipal.Subject(), nil
		},
		framework.RequirePermission("reports.read"),
	)
	response := router.Dispatch(
		framework.ContextWithPrincipal(context.Background(), authenticated),
		framework.Request{ID: "1", Method: "reports.read"},
	)
	if response.Error != nil || response.Result != "user-1" {
		t.Fatalf("response = %#v", response)
	}
}
