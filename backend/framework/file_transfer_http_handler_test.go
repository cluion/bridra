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

func TestFileTransferHTTPHandlerStreamsOneTimeDownload(t *testing.T) {
	store, err := NewFileTransferStore(FileTransferOptions{
		TTL:      time.Minute,
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	content := []byte("report contents")
	reference, err := store.Stage(
		context.Background(),
		"monthly report.txt",
		"text/plain",
		bytes.NewReader(content),
	)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	handler := &FileTransferHTTPHandler{
		Store:         store,
		AllowedOrigin: "https://app.example",
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/rpc/files/"+reference.ID,
		nil,
	)
	request.Header.Set("Origin", "https://app.example")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Equal(recorder.Body.Bytes(), content) {
		t.Fatalf("body = %q", recorder.Body.Bytes())
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/plain" {
		t.Fatalf("content type = %q", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); !strings.Contains(got, "monthly report.txt") {
		t.Fatalf("content disposition = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("allowed origin = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q", got)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(
		second,
		httptest.NewRequest(http.MethodGet, "/rpc/files/"+reference.ID, nil),
	)
	if second.Code != http.StatusNotFound {
		t.Fatalf("second status = %d", second.Code)
	}
}

func TestFileTransferHTTPHandlerRejectsInvalidRequests(t *testing.T) {
	store, err := NewFileTransferStore(DefaultFileTransferOptions())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	handler := &FileTransferHTTPHandler{
		Store:         store,
		AllowedOrigin: "https://app.example",
	}
	tests := []struct {
		name   string
		method string
		origin string
		status int
	}{
		{name: "method", method: http.MethodPost, status: http.StatusMethodNotAllowed},
		{name: "missing", method: http.MethodGet, status: http.StatusNotFound},
		{
			name:   "origin",
			method: http.MethodGet,
			origin: "https://attacker.example",
			status: http.StatusForbidden,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/rpc/files/missing", nil)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			var body map[string]string
			if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body["message"] == "" {
				t.Fatalf("body = %#v", body)
			}
		})
	}
}

func TestFileTransferHTTPHandlerSupportsPreflightAndRequiresStore(t *testing.T) {
	handler := &FileTransferHTTPHandler{AllowedOrigin: "*"}
	missingStore := httptest.NewRecorder()
	handler.ServeHTTP(
		missingStore,
		httptest.NewRequest(http.MethodGet, "/rpc/files/id", nil),
	)
	if missingStore.Code != http.StatusInternalServerError {
		t.Fatalf("missing store status = %d", missingStore.Code)
	}

	store, err := NewFileTransferStore(DefaultFileTransferOptions())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	handler.Store = store
	request := httptest.NewRequest(http.MethodOptions, "/rpc/files/id", nil)
	request.Header.Set("Origin", "https://app.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Methods"); got != "GET, OPTIONS" {
		t.Fatalf("allowed methods = %q", got)
	}
}
