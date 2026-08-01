package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type httpTraceContextKey struct{}

func TestHTTPObservationHandlerRecordsRPCSecurityOutcome(t *testing.T) {
	principal, err := NewPrincipal("user-1")
	if err != nil {
		t.Fatalf("new principal: %v", err)
	}
	router := NewRouter()
	router.HandleWithPolicies(
		"reports.read",
		func(*Context) (any, error) { return "unreachable", nil },
		RequirePermission("reports.read"),
	)
	var observation HTTPRequestObservation
	observer := HTTPObserverFuncs{
		BeginFunc: func(ctx context.Context, start HTTPRequestStart) context.Context {
			if requestID, ok := HTTPRequestIDFromContext(ctx); !ok || requestID != "request-1" {
				t.Fatalf("begin request id = %q, %v", requestID, ok)
			}
			if start.ClientIP != "192.0.2.10" {
				t.Fatalf("client ip = %q", start.ClientIP)
			}
			return context.WithValue(ctx, httpTraceContextKey{}, "trace-1")
		},
		EndFunc: func(ctx context.Context, result HTTPRequestObservation) {
			if trace, _ := ctx.Value(httpTraceContextKey{}).(string); trace != "trace-1" {
				t.Fatalf("trace context = %q", trace)
			}
			observation = result
		},
	}
	times := []time.Time{
		time.Unix(100, 0),
		time.Unix(100, int64(25*time.Millisecond)),
	}
	observed := &HTTPObservationHandler{
		Handler: &HTTPHandler{
			Router: router,
			Authenticator: AuthenticatorFunc(
				func(context.Context, string) (Principal, error) { return principal, nil },
			),
		},
		Observer:  observer,
		now:       func() time.Time { value := times[0]; times = times[1:]; return value },
		requestID: func() string { return "request-1" },
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"https://backend.example/rpc",
		strings.NewReader(`{"id":"1","method":"reports.read","params":{"secret":"hidden"}}`),
	)
	request.RemoteAddr = "192.0.2.10:4321"
	request.Header.Set("Authorization", "Bearer top-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "198.51.100.20")
	recorder := httptest.NewRecorder()

	observed.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if requestID := recorder.Header().Get("X-Request-ID"); requestID != "request-1" {
		t.Fatalf("response request id = %q", requestID)
	}
	if observation.RequestID != "request-1" ||
		observation.HTTPMethod != http.MethodPost ||
		observation.ClientIP != "192.0.2.10" ||
		observation.Surface != "rpc" ||
		observation.RPCMethod != "reports.read" ||
		observation.Principal != "user-1" ||
		observation.StatusCode != http.StatusOK ||
		observation.ErrorCode != "forbidden" ||
		observation.Outcome != HTTPOutcomeRPCError ||
		observation.ResponseBytes == 0 ||
		observation.Duration != 25*time.Millisecond {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestJSONHTTPObserverDoesNotLogSecretsOrCapabilityPaths(t *testing.T) {
	principal, err := NewPrincipal("user@example.com")
	if err != nil {
		t.Fatalf("new principal: %v", err)
	}
	observerOutput := &bytes.Buffer{}
	observer, err := NewJSONHTTPObserver(observerOutput)
	if err != nil {
		t.Fatalf("new observer: %v", err)
	}
	router := NewRouter()
	router.Handle("echo", func(*Context) (any, error) { return "ok", nil })
	handler := &HTTPObservationHandler{
		Handler: &HTTPHandler{
			Router: router,
			Authenticator: AuthenticatorFunc(
				func(context.Context, string) (Principal, error) { return principal, nil },
			),
		},
		Observer:  observer,
		requestID: func() string { return "audit-1" },
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"https://backend.example/rpc/files/capability-secret",
		strings.NewReader(`{"id":"1","method":"echo","params":{"password":"body-secret"}}`),
	)
	request.Header.Set("Authorization", "Bearer bearer-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "forwarded-secret")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	logged := observerOutput.String()
	for _, secret := range []string{
		"capability-secret",
		"body-secret",
		"bearer-secret",
		"forwarded-secret",
		"user@example.com",
	} {
		if strings.Contains(logged, secret) {
			t.Fatalf("audit log contains %q: %s", secret, logged)
		}
	}
	var event map[string]any
	if err := json.Unmarshal(observerOutput.Bytes(), &event); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	if event["event"] != "bridra_http_request" || event["request_id"] != "audit-1" {
		t.Fatalf("event = %#v", event)
	}
	if event["principal_sha256"] != hashHTTPObservationValue("user@example.com") {
		t.Fatalf("principal hash = %#v", event["principal_sha256"])
	}
}

func TestHTTPObservationHandlerPreservesStreamingFlush(t *testing.T) {
	router := NewRouter()
	router.Handle("reports.build", func(ctx *Context) (any, error) {
		return ProduceStream(ctx, func(stream *StreamWriter) error {
			return stream.Send("page-1")
		})
	})
	var observation HTTPRequestObservation
	handler := &HTTPObservationHandler{
		Handler: &HTTPHandler{Router: router},
		Observer: HTTPObserverFuncs{EndFunc: func(_ context.Context, result HTTPRequestObservation) {
			observation = result
		}},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/rpc",
		strings.NewReader(`{"id":"1","method":"reports.build","meta":{"stream":"1"}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || !recorder.Flushed {
		t.Fatalf("status = %d, flushed = %v", recorder.Code, recorder.Flushed)
	}
	if observation.ResponseBytes == 0 || observation.RPCMethod != "reports.build" {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestHTTPObservationHandlerClassifiesFileTransferWithoutLoggingCapability(t *testing.T) {
	var observation HTTPRequestObservation
	handler := &HTTPObservationHandler{
		Handler: &FileTransferHTTPHandler{},
		Observer: HTTPObserverFuncs{EndFunc: func(_ context.Context, result HTTPRequestObservation) {
			observation = result
		}},
	}
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/rpc/files/capability-secret", nil),
	)

	if recorder.Code != http.StatusInternalServerError ||
		observation.Surface != "file_transfer" ||
		observation.ErrorCode != "file_transfer_error" ||
		observation.Outcome != HTTPOutcomeServerError {
		t.Fatalf("status = %d, observation = %#v", recorder.Code, observation)
	}
}

func TestHTTPObservationHandlerContainsObserverPanics(t *testing.T) {
	var errorsOutput bytes.Buffer
	metrics := NewHTTPMetrics()
	handler := &HTTPObservationHandler{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}),
		Observer: NewHTTPObserverGroup(
			metrics,
			HTTPObserverFuncs{
				BeginFunc: func(context.Context, HTTPRequestStart) context.Context {
					panic("begin panic")
				},
				EndFunc: func(context.Context, HTTPRequestObservation) {
					panic("end panic")
				},
			},
		),
		Errors: &errorsOutput,
	}
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	logged := errorsOutput.String()
	if !strings.Contains(logged, "begin panic") || !strings.Contains(logged, "end panic") {
		t.Fatalf("observer errors = %q", logged)
	}
	if snapshot := metrics.Snapshot(); snapshot.Active != 0 || snapshot.Total != 1 {
		t.Fatalf("metrics after observer panic = %#v", snapshot)
	}
}

func TestHTTPMetricsRecordsBoundedConcurrentOutcomes(t *testing.T) {
	metrics := NewHTTPMetrics()
	const requests = 100
	var wait sync.WaitGroup
	wait.Add(requests)
	for index := range requests {
		go func() {
			defer wait.Done()
			ctx := metrics.BeginHTTP(context.Background(), HTTPRequestStart{})
			observation := HTTPRequestObservation{
				StatusCode: http.StatusOK,
				Duration:   time.Duration(index+1) * time.Millisecond,
				Outcome:    HTTPOutcomeSuccess,
			}
			switch index % 5 {
			case 1:
				observation.Outcome = HTTPOutcomeRPCError
				observation.ErrorCode = "forbidden"
			case 2:
				observation.Outcome = HTTPOutcomeClientError
				observation.StatusCode = http.StatusUnauthorized
				observation.ErrorCode = "unauthenticated"
			case 3:
				observation.Outcome = HTTPOutcomeClientError
				observation.StatusCode = http.StatusTooManyRequests
				observation.ErrorCode = "rate_limited"
			case 4:
				observation.Outcome = HTTPOutcomeServerError
				observation.StatusCode = http.StatusInternalServerError
			}
			metrics.EndHTTP(ctx, observation)
		}()
	}
	wait.Wait()

	snapshot := metrics.Snapshot()
	if snapshot.Active != 0 || snapshot.Total != requests ||
		snapshot.Success != 20 || snapshot.RPCErrors != 20 ||
		snapshot.ClientErrors != 40 || snapshot.ServerErrors != 20 ||
		snapshot.Canceled != 0 || snapshot.Unauthorized != 20 ||
		snapshot.Forbidden != 20 || snapshot.RateLimited != 20 ||
		snapshot.MaxDuration != 100*time.Millisecond {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestHTTPObservabilityValidatesAndBoundsInputs(t *testing.T) {
	var nilWriter *bytes.Buffer
	if _, err := NewJSONHTTPObserver(nilWriter); !errors.Is(err, ErrInvalidHTTPObserver) {
		t.Fatalf("nil writer error = %v", err)
	}
	if _, ok := HTTPRequestIDFromContext(nil); ok {
		t.Fatal("nil context returned a request id")
	}
	if ip := directHTTPClientIP("not-an-address"); ip != "" {
		t.Fatalf("invalid client ip = %q", ip)
	}
	if value := boundedHTTPObservationValue(strings.Repeat("x", 300)); len(value) != 256 {
		t.Fatalf("bounded value length = %d", len(value))
	}

	var called int
	group := NewHTTPObserverGroup(
		HTTPObserverFuncs{EndFunc: func(context.Context, HTTPRequestObservation) { called++ }},
		nil,
	)
	ctx := group.BeginHTTP(context.Background(), HTTPRequestStart{})
	group.EndHTTP(ctx, HTTPRequestObservation{})
	if called != 1 {
		t.Fatalf("observer calls = %d", called)
	}

	recorder := httptest.NewRecorder()
	(&HTTPObservationHandler{}).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("missing handler status = %d", recorder.Code)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if outcome := httpObservationOutcome(HTTPRequestObservation{}, canceled.Err()); outcome != HTTPOutcomeCanceled {
		t.Fatalf("canceled outcome = %q", outcome)
	}
	if outcome := httpObservationOutcome(
		HTTPRequestObservation{StatusCode: http.StatusInternalServerError},
		nil,
	); outcome != HTTPOutcomeServerError {
		t.Fatalf("server outcome = %q", outcome)
	}

	if _, err := io.WriteString(io.Discard, newHTTPRequestID()); err != nil {
		t.Fatalf("write request id: %v", err)
	}
}
