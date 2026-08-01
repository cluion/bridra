package framework

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

func TestFileTransferHTTPHandlerResumesDownloads(t *testing.T) {
	store, err := NewFileTransferStore(FileTransferOptions{
		TTL:      time.Minute,
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	content := []byte("resumable report")
	reference, err := store.Stage(
		context.Background(),
		"report.txt",
		"text/plain",
		bytes.NewReader(content),
	)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	interrupted, err := store.OpenDownload(reference.ID, 0)
	if err != nil {
		t.Fatalf("open interrupted download: %v", err)
	}
	buffer := make([]byte, 5)
	if _, err := io.ReadFull(interrupted, buffer); err != nil {
		t.Fatalf("read interrupted range: %v", err)
	}
	if err := interrupted.Close(); err != nil {
		t.Fatalf("close interrupted download: %v", err)
	}
	invalidRequest := httptest.NewRequest(
		http.MethodGet,
		"/rpc/files/"+reference.ID,
		nil,
	)
	invalidRequest.Header.Set("Range", fmt.Sprintf("bytes=%d-", len(content)))
	invalidRecorder := httptest.NewRecorder()
	(&FileTransferHTTPHandler{Store: store}).ServeHTTP(
		invalidRecorder,
		invalidRequest,
	)
	if invalidRecorder.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("invalid range status = %d", invalidRecorder.Code)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"/rpc/files/"+reference.ID,
		nil,
	)
	request.Header.Set("Range", "bytes=5-")
	recorder := httptest.NewRecorder()
	(&FileTransferHTTPHandler{Store: store}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Equal(recorder.Body.Bytes(), content[5:]) {
		t.Fatalf("range body = %q", recorder.Body.Bytes())
	}
	if got := recorder.Header().Get("Content-Range"); got != "bytes 5-15/16" {
		t.Fatalf("content range = %q", got)
	}
	second := httptest.NewRecorder()
	(&FileTransferHTTPHandler{Store: store}).ServeHTTP(
		second,
		httptest.NewRequest(http.MethodGet, "/rpc/files/"+reference.ID, nil),
	)
	if second.Code != http.StatusNotFound {
		t.Fatalf("consumed status = %d", second.Code)
	}
}

func TestFileTransferHTTPHandlerResumesVerifiedUploads(t *testing.T) {
	store, err := NewFileTransferStore(FileTransferOptions{
		TTL:      time.Minute,
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	handler := &FileTransferHTTPHandler{Store: store, Token: "secret"}
	content := []byte("resumable upload")
	sum := sha256.Sum256(content)
	metadata := fmt.Sprintf(
		`{"name":"upload.bin","mediaType":"application/octet-stream","size":%d,"sha256":"%s"}`,
		len(content),
		hex.EncodeToString(sum[:]),
	)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(
		unauthorized,
		httptest.NewRequest(http.MethodPost, "/rpc/files/", strings.NewReader(metadata)),
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/rpc/files/",
		strings.NewReader(metadata),
	)
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set(fileTransferTokenHeader, "secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, createRequest)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var status FileUploadStatus
	if err := json.NewDecoder(created.Body).Decode(&status); err != nil {
		t.Fatalf("decode created status: %v", err)
	}

	first := int64(6)
	appendRange := func(offset int64, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(
			http.MethodPatch,
			"/rpc/files/"+status.Reference.ID,
			bytes.NewReader(body),
		)
		request.Header.Set("Content-Type", "application/offset+octet-stream")
		request.Header.Set("Upload-Offset", fmt.Sprint(offset))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	firstResponse := appendRange(0, content[:first])
	if firstResponse.Code != http.StatusOK ||
		firstResponse.Header().Get("Upload-Offset") != fmt.Sprint(first) {
		t.Fatalf(
			"first response = %d, offset %q",
			firstResponse.Code,
			firstResponse.Header().Get("Upload-Offset"),
		)
	}
	headRequest := httptest.NewRequest(
		http.MethodHead,
		"/rpc/files/"+status.Reference.ID,
		nil,
	)
	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, headRequest)
	if headResponse.Code != http.StatusNoContent ||
		headResponse.Header().Get("Upload-Offset") != fmt.Sprint(first) {
		t.Fatalf("head response = %d, headers %#v", headResponse.Code, headResponse.Header())
	}
	wrongOffset := appendRange(0, content[first:])
	if wrongOffset.Code != http.StatusConflict ||
		wrongOffset.Header().Get("Upload-Offset") != fmt.Sprint(first) {
		t.Fatalf("wrong offset response = %d, headers %#v", wrongOffset.Code, wrongOffset.Header())
	}
	finalResponse := appendRange(first, content[first:])
	if finalResponse.Code != http.StatusOK {
		t.Fatalf("final response = %d, body = %s", finalResponse.Code, finalResponse.Body.String())
	}
	if err := json.NewDecoder(finalResponse.Body).Decode(&status); err != nil {
		t.Fatalf("decode final status: %v", err)
	}
	if !status.Complete || status.Offset != int64(len(content)) {
		t.Fatalf("final status = %#v", status)
	}
	upload, err := store.ConsumeUpload(status.Reference)
	if err != nil {
		t.Fatalf("consume upload: %v", err)
	}
	got, err := io.ReadAll(upload)
	if err != nil {
		t.Fatalf("read upload: %v", err)
	}
	if err := upload.Close(); err != nil {
		t.Fatalf("close upload: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("upload = %q", got)
	}
}

func TestFileTransferHTTPHandlerRejectsInvalidTransferProtocol(t *testing.T) {
	store, err := NewFileTransferStore(FileTransferOptions{
		TTL:      time.Minute,
		MaxBytes: 4,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	handler := &FileTransferHTTPHandler{
		Store:  store,
		Token:  "secret",
		Errors: io.Discard,
	}
	request := func(
		method string,
		target string,
		body string,
		headers map[string]string,
	) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	jsonHeaders := map[string]string{
		"Content-Type":          "application/json",
		fileTransferTokenHeader: "secret",
	}
	tests := []struct {
		name    string
		method  string
		target  string
		body    string
		headers map[string]string
		status  int
	}{
		{
			name:   "collection method",
			method: http.MethodGet,
			target: "/rpc/files/",
			status: http.StatusMethodNotAllowed,
		},
		{
			name:   "metadata content type",
			method: http.MethodPost,
			target: "/rpc/files/",
			headers: map[string]string{
				fileTransferTokenHeader: "secret",
			},
			status: http.StatusUnsupportedMediaType,
		},
		{
			name:    "malformed metadata",
			method:  http.MethodPost,
			target:  "/rpc/files/",
			body:    "{",
			headers: jsonHeaders,
			status:  http.StatusBadRequest,
		},
		{
			name:    "extra metadata",
			method:  http.MethodPost,
			target:  "/rpc/files/",
			body:    `{"name":"x","mediaType":"text/plain","size":0,"sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"} {}`,
			headers: jsonHeaders,
			status:  http.StatusBadRequest,
		},
		{
			name:    "oversized metadata",
			method:  http.MethodPost,
			target:  "/rpc/files/",
			body:    `{"name":"x","mediaType":"text/plain","size":5,"sha256":"0000000000000000000000000000000000000000000000000000000000000000"}`,
			headers: jsonHeaders,
			status:  http.StatusRequestEntityTooLarge,
		},
		{
			name:   "append content type",
			method: http.MethodPatch,
			target: "/rpc/files/" + strings.Repeat("a", 64),
			status: http.StatusUnsupportedMediaType,
		},
		{
			name:   "append offset",
			method: http.MethodPatch,
			target: "/rpc/files/" + strings.Repeat("a", 64),
			headers: map[string]string{
				"Content-Type": "application/offset+octet-stream",
			},
			status: http.StatusBadRequest,
		},
		{
			name:   "missing upload",
			method: http.MethodPatch,
			target: "/rpc/files/" + strings.Repeat("a", 64),
			headers: map[string]string{
				"Content-Type":  "application/offset+octet-stream",
				"Upload-Offset": "0",
			},
			status: http.StatusNotFound,
		},
		{
			name:   "missing upload status",
			method: http.MethodHead,
			target: "/rpc/files/" + strings.Repeat("a", 64),
			status: http.StatusNotFound,
		},
		{
			name:   "invalid download range",
			method: http.MethodGet,
			target: "/rpc/files/" + strings.Repeat("a", 64),
			headers: map[string]string{
				"Range": "bytes=-5",
			},
			status: http.StatusRequestedRangeNotSatisfiable,
		},
		{
			name:   "resource method",
			method: http.MethodDelete,
			target: "/rpc/files/" + strings.Repeat("a", 64),
			status: http.StatusMethodNotAllowed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := request(
				test.method,
				test.target,
				test.body,
				test.headers,
			)
			if recorder.Code != test.status {
				t.Fatalf(
					"status = %d, want %d, body = %s",
					recorder.Code,
					test.status,
					recorder.Body.String(),
				)
			}
		})
	}

	content := []byte("data")
	status, err := store.BeginUpload(
		"bad.bin",
		"application/octet-stream",
		int64(len(content)),
		strings.Repeat("0", 64),
	)
	if err != nil {
		t.Fatalf("begin corrupt upload: %v", err)
	}
	corrupt := request(
		http.MethodPatch,
		"/rpc/files/"+status.Reference.ID,
		string(content),
		map[string]string{
			"Content-Type":  "application/offset+octet-stream",
			"Upload-Offset": "0",
		},
	)
	if corrupt.Code != http.StatusUnprocessableEntity {
		t.Fatalf("corrupt status = %d, body = %s", corrupt.Code, corrupt.Body.String())
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
	if got := recorder.Header().Get("Access-Control-Allow-Methods"); got != "GET, HEAD, POST, PATCH, OPTIONS" {
		t.Fatalf("allowed methods = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "X-Request-ID") {
		t.Fatalf("exposed headers = %q", got)
	}
}
