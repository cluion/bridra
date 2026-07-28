package framework

import (
	"bytes"
	"context"
	"encoding/json"
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
	var response Response
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "1" || response.Error != nil {
		t.Fatalf("response = %#v", response)
	}
}

func TestHTTPHandlerSupportsCORSPreflight(t *testing.T) {
	handler := &HTTPHandler{Router: NewRouter(), AllowedOrigin: "http://localhost:3000"}
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
