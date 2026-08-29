package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPHandlerDispatchesRPCAndAllowsConfiguredOrigin(t *testing.T) {
	router := NewRouter()
	router.Handle("echo", func(ctx *Context) (any, error) {
		return map[string]any{"id": ctx.Request.ID}, nil
	})
	handler := &HTTPHandler{Router: router, AllowedOrigin: "*"}
	request := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(`{"id":"1","method":"echo"}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Origin", "http://localhost:54321")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if origin := recorder.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Fatalf("allow origin = %q", origin)
	}
	if cacheControl := recorder.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("cache control = %q", cacheControl)
	}
	if contentTypeOptions := recorder.Header().Get("X-Content-Type-Options"); contentTypeOptions != "nosniff" {
		t.Fatalf("content type options = %q", contentTypeOptions)
	}
	var response Response
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "1" || response.Error != nil {
		t.Fatalf("response = %#v", response)
	}
}

func TestHTTPHandlerSupportsCORSPreflight(t *testing.T) {
	handler := &HTTPHandler{
		Router: NewRouter(),
		Authenticator: AuthenticatorFunc(func(context.Context, string) (Principal, error) {
			t.Fatal("preflight called the authenticator")
			return Principal{}, nil
		}),
		RateLimiter: RateLimiterFunc(func(context.Context, string) (RateLimitDecision, error) {
			t.Fatal("preflight called the rate limiter")
			return RateLimitDecision{}, nil
		}),
		AllowedOrigin: "http://localhost:3000",
	}
	request := httptest.NewRequest(http.MethodOptions, "/rpc", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	if methods := recorder.Header().Get("Access-Control-Allow-Methods"); methods != "POST, OPTIONS" {
		t.Fatalf("allow methods = %q", methods)
	}
	if headers := recorder.Header().Get("Access-Control-Allow-Headers"); headers != "Authorization, Content-Type" {
		t.Fatalf("allow headers = %q", headers)
	}
	if headers := recorder.Header().Get("Access-Control-Expose-Headers"); headers != "Retry-After, WWW-Authenticate, X-Request-ID" {
		t.Fatalf("expose headers = %q", headers)
	}
}

func TestHTTPHandlerAuthenticatesBearerPrincipal(t *testing.T) {
	principal, err := NewPrincipal("user-1", "reports.read")
	if err != nil {
		t.Fatalf("new principal: %v", err)
	}
	router := NewRouter()
	router.HandleWithPolicies(
		"reports.read",
		func(ctx *Context) (any, error) {
			authenticated, exists := PrincipalFromContext(ctx)
			if !exists {
				t.Fatal("controller did not receive principal")
			}
			return authenticated.Subject(), nil
		},
		RequirePermission("reports.read"),
	)
	var credential string
	handler := &HTTPHandler{
		Router: router,
		Authenticator: AuthenticatorFunc(func(_ context.Context, provided string) (Principal, error) {
			credential = provided
			return principal, nil
		}),
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/rpc",
		strings.NewReader(`{"id":"1","method":"reports.read"}`),
	)
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || credential != "access-token" {
		t.Fatalf("status = %d, credential = %q, body = %s", recorder.Code, credential, recorder.Body.String())
	}
	if cacheControl := recorder.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("cache control = %q", cacheControl)
	}
	var response Response
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error != nil || response.Result != "user-1" {
		t.Fatalf("response = %#v", response)
	}
}

func TestHTTPHandlerRejectsInvalidBearerCredentials(t *testing.T) {
	principal, err := NewPrincipal("user-1")
	if err != nil {
		t.Fatalf("new principal: %v", err)
	}
	authenticator := AuthenticatorFunc(func(_ context.Context, credential string) (Principal, error) {
		if credential != "valid" {
			return Principal{}, ErrAuthenticationFailed
		}
		return principal, nil
	})
	tests := []struct {
		name          string
		authorization string
	}{
		{name: "missing"},
		{name: "wrong scheme", authorization: "Basic valid"},
		{name: "missing token", authorization: "Bearer"},
		{name: "invalid token", authorization: "Bearer invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &HTTPHandler{Router: NewRouter(), Authenticator: authenticator}
			request := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			if challenge := recorder.Header().Get("WWW-Authenticate"); challenge != "Bearer" {
				t.Fatalf("challenge = %q", challenge)
			}
			var response Response
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Error == nil || response.Error.Code != "unauthenticated" {
				t.Fatalf("response = %#v", response)
			}
		})
	}
}

func TestHTTPHandlerHidesAuthenticatorFailure(t *testing.T) {
	var logs bytes.Buffer
	handler := &HTTPHandler{
		Router: NewRouter(),
		Authenticator: AuthenticatorFunc(func(context.Context, string) (Principal, error) {
			return Principal{}, errors.New("identity provider database password leaked")
		}),
		Errors: &logs,
	}
	request := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "database password") {
		t.Fatalf("response leaked authenticator error: %s", recorder.Body.String())
	}
	if !strings.Contains(logs.String(), "database password") {
		t.Fatalf("logs = %q", logs.String())
	}
}

func TestHTTPHandlerKeepsPermissionDenialInRPCEnvelope(t *testing.T) {
	principal, err := NewPrincipal("user-1")
	if err != nil {
		t.Fatalf("new principal: %v", err)
	}
	controllerCalled := false
	router := NewRouter()
	router.HandleWithPolicies(
		"reports.read",
		func(*Context) (any, error) {
			controllerCalled = true
			return "report", nil
		},
		RequirePermission("reports.read"),
	)
	handler := &HTTPHandler{
		Router: router,
		Authenticator: AuthenticatorFunc(func(context.Context, string) (Principal, error) {
			return principal, nil
		}),
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/rpc",
		strings.NewReader(`{"id":"1","method":"reports.read"}`),
	)
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response Response
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error == nil || response.Error.Code != "forbidden" {
		t.Fatalf("response = %#v", response)
	}
	if controllerCalled {
		t.Fatal("controller ran after permission denial")
	}
}

func TestHTTPHandlerRateLimitsAuthenticatedPrincipals(t *testing.T) {
	alice, err := NewPrincipal("alice")
	if err != nil {
		t.Fatalf("new Alice principal: %v", err)
	}
	bob, err := NewPrincipal("bob")
	if err != nil {
		t.Fatalf("new Bob principal: %v", err)
	}
	limiter, err := NewMemoryRateLimiter(MemoryRateLimiterOptions{
		Requests: 1,
		Window:   time.Minute,
		MaxKeys:  2,
	})
	if err != nil {
		t.Fatalf("new rate limiter: %v", err)
	}
	fixed := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return fixed }
	router := NewRouter()
	router.Handle("echo", func(*Context) (any, error) { return "ok", nil })
	handler := &HTTPHandler{
		Router: router,
		Authenticator: AuthenticatorFunc(func(_ context.Context, credential string) (Principal, error) {
			if credential == "alice-token" {
				return alice, nil
			}
			return bob, nil
		}),
		RateLimiter: limiter,
	}

	request := func(token string) *http.Request {
		value := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(`{"id":"1","method":"echo"}`))
		value.Header.Set("Authorization", "Bearer "+token)
		value.Header.Set("Content-Type", "application/json")
		return value
	}
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, request("alice-token"))
	if first.Code != http.StatusOK {
		t.Fatalf("first Alice status = %d, body = %s", first.Code, first.Body.String())
	}
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, request("alice-token"))
	if denied.Code != http.StatusTooManyRequests {
		t.Fatalf("denied status = %d, body = %s", denied.Code, denied.Body.String())
	}
	if retryAfter := denied.Header().Get("Retry-After"); retryAfter != "60" {
		t.Fatalf("retry after = %q", retryAfter)
	}
	var response Response
	if err := json.NewDecoder(denied.Body).Decode(&response); err != nil {
		t.Fatalf("decode denied response: %v", err)
	}
	if response.Error == nil || response.Error.Code != "rate_limited" {
		t.Fatalf("denied response = %#v", response)
	}
	bobRequest := httptest.NewRecorder()
	handler.ServeHTTP(bobRequest, request("bob-token"))
	if bobRequest.Code != http.StatusOK {
		t.Fatalf("Bob status = %d, body = %s", bobRequest.Code, bobRequest.Body.String())
	}
}

func TestHTTPHandlerRateLimitsRemoteIPWithoutTrustingForwardedHeaders(t *testing.T) {
	limiter, err := NewMemoryRateLimiter(MemoryRateLimiterOptions{
		Requests: 1,
		Window:   time.Minute,
		MaxKeys:  2,
	})
	if err != nil {
		t.Fatalf("new rate limiter: %v", err)
	}
	fixed := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return fixed }
	router := NewRouter()
	router.Handle("echo", func(*Context) (any, error) { return "ok", nil })
	handler := &HTTPHandler{Router: router, RateLimiter: limiter}
	request := func(remoteAddress, forwardedFor string) *http.Request {
		value := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(`{"id":"1","method":"echo"}`))
		value.RemoteAddr = remoteAddress
		value.Header.Set("Content-Type", "application/json")
		value.Header.Set("X-Forwarded-For", forwardedFor)
		return value
	}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, request("192.0.2.10:1234", "198.51.100.1"))
	spoofed := httptest.NewRecorder()
	handler.ServeHTTP(spoofed, request("192.0.2.10:5678", "198.51.100.2"))
	differentIP := httptest.NewRecorder()
	handler.ServeHTTP(differentIP, request("192.0.2.11:1234", "198.51.100.1"))

	if first.Code != http.StatusOK || spoofed.Code != http.StatusTooManyRequests || differentIP.Code != http.StatusOK {
		t.Fatalf("statuses = %d, %d, %d", first.Code, spoofed.Code, differentIP.Code)
	}
}

func TestHTTPHandlerHidesRateLimiterFailure(t *testing.T) {
	var logs bytes.Buffer
	handler := &HTTPHandler{
		Router: NewRouter(),
		RateLimiter: RateLimiterFunc(func(context.Context, string) (RateLimitDecision, error) {
			return RateLimitDecision{}, errors.New("Redis password leaked")
		}),
		Errors: &logs,
	}
	request := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "Redis password") {
		t.Fatalf("response leaked rate limiter error: %s", recorder.Body.String())
	}
	if !strings.Contains(logs.String(), "Redis password") {
		t.Fatalf("logs = %q", logs.String())
	}
}

func TestRetryAfterSecondsRoundsUpAndHasSafeMinimum(t *testing.T) {
	tests := map[time.Duration]string{
		0:                       "1",
		time.Nanosecond:         "1",
		time.Second:             "1",
		time.Second + 1:         "2",
		1500 * time.Millisecond: "2",
	}
	for duration, expected := range tests {
		if actual := retryAfterSeconds(duration); actual != expected {
			t.Fatalf("retry after %s = %q, want %q", duration, actual, expected)
		}
	}
}

func TestHTTPHandlerRejectsDisallowedOrigin(t *testing.T) {
	handler := &HTTPHandler{Router: NewRouter(), AllowedOrigin: "https://example.com"}
	request := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://attacker.example")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHTTPHandlerValidatesMethodContentTypeAndBody(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		contentType string
		body        string
		wantStatus  int
	}{
		{name: "method", method: http.MethodGet, wantStatus: http.StatusMethodNotAllowed},
		{name: "content type", method: http.MethodPost, contentType: "text/plain", body: `{}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "json", method: http.MethodPost, contentType: "application/json", body: `not-json`, wantStatus: http.StatusBadRequest},
		{name: "multiple values", method: http.MethodPost, contentType: "application/json", body: `{} {}`, wantStatus: http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &HTTPHandler{Router: NewRouter()}
			request := httptest.NewRequest(test.method, "/rpc", strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
		})
	}
}

func TestHTTPHandlerRejectsOversizedBody(t *testing.T) {
	handler := &HTTPHandler{Router: NewRouter()}
	body := bytes.NewBufferString(`{"value":"` + strings.Repeat("x", MaxRequestBytes) + `"}`)
	request := httptest.NewRequest(http.MethodPost, "/rpc", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestHTTPHandlerRequiresRouter(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	(&HTTPHandler{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHTTPHandlerPropagatesRequestCancellation(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	router := NewRouter()
	router.Handle("wait", func(ctx *Context) (any, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	handler := &HTTPHandler{Router: router}
	request := httptest.NewRequest(
		http.MethodPost,
		"/rpc",
		strings.NewReader(`{"id":"1","method":"wait"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	requestCtx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(requestCtx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not reach the router")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not reach the router context")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("HTTP handler did not finish after request cancellation")
	}
}

func TestHTTPHandlerStreamsNDJSONAndFlushesCompletion(t *testing.T) {
	router := NewRouter()
	router.Handle("reports.build", func(ctx *Context) (any, error) {
		return ProduceStream(ctx, func(stream *StreamWriter) error {
			if err := stream.Report(Progress{Completed: 1, Total: 2}); err != nil {
				return err
			}
			return stream.Send(map[string]any{"page": 1})
		})
	})
	handler := &HTTPHandler{Router: router}
	request := httptest.NewRequest(
		http.MethodPost,
		"/rpc",
		strings.NewReader(
			`{"id":"1","method":"reports.build","meta":{"stream":"1"}}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/x-ndjson")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/x-ndjson" {
		t.Fatalf("content type = %q", contentType)
	}
	decoder := json.NewDecoder(recorder.Body)
	for sequence, kind := range []string{
		streamProgressKind,
		streamDataKind,
		streamCompleteKind,
	} {
		var response Response
		if err := decoder.Decode(&response); err != nil {
			t.Fatalf("decode response %d: %v", sequence+1, err)
		}
		if response.Stream == nil ||
			response.Stream.Sequence != int64(sequence+1) ||
			response.Stream.Kind != kind {
			t.Fatalf("response %d = %#v", sequence+1, response)
		}
	}
}

func TestHTTPHandlerTreatsNonCanonicalStreamMetadataAsUnary(t *testing.T) {
	router := NewRouter()
	router.Handle("reports.build", func(ctx *Context) (any, error) {
		return ProduceStream(ctx, func(stream *StreamWriter) error {
			return stream.Send(map[string]any{"page": 1})
		})
	})
	handler := &HTTPHandler{Router: router}
	request := httptest.NewRequest(
		http.MethodPost,
		"/rpc",
		strings.NewReader(
			`{"id":"1","method":"reports.build","meta":{"stream":"true"}}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/x-ndjson")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("content type = %q", contentType)
	}
	var response Response
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "1" ||
		response.Stream != nil ||
		response.Error == nil ||
		response.Error.Code != "streaming_required" {
		t.Fatalf("response = %#v", response)
	}
}
