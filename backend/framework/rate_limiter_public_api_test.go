package framework_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cluion/bridra/backend/framework"
)

func TestPublicRateLimiterAPI(t *testing.T) {
	options := framework.DefaultMemoryRateLimiterOptions()
	options.Requests = 2
	options.Window = time.Minute
	options.MaxKeys = 10
	limiter, err := framework.NewMemoryRateLimiter(options)
	if err != nil {
		t.Fatalf("new rate limiter: %v", err)
	}
	decision, err := limiter.Allow(context.Background(), "tenant:one")
	if err != nil || !decision.Allowed || decision.Remaining != 1 {
		t.Fatalf("decision = %#v, error = %v", decision, err)
	}

	principal, err := framework.NewPrincipal("user-1")
	if err != nil {
		t.Fatalf("new principal: %v", err)
	}
	request := httptest.NewRequest("POST", "/rpc", nil)
	principalKey, err := framework.DefaultHTTPRateLimitKey(request, principal)
	if err != nil || principalKey == "" || strings.Contains(principalKey, "user-1") {
		t.Fatalf("principal key = %q, error = %v", principalKey, err)
	}
	request.RemoteAddr = "192.0.2.10:4321"
	ipKey, err := framework.DefaultHTTPRateLimitKey(request, framework.Principal{})
	if err != nil || ipKey == "" || ipKey == principalKey || strings.Contains(ipKey, "192.0.2.10") {
		t.Fatalf("IP key = %q, error = %v", ipKey, err)
	}
}
