package framework

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func FuzzSidecarServerInput(f *testing.F) {
	const token = "fuzz-sidecar-secret"
	for _, seed := range [][]byte{
		[]byte("not-json\n"),
		[]byte("{}\n"),
		[]byte(`{"id":"1","method":"echo","params":{"value":1},"meta":{"token":"fuzz-sidecar-secret"}}` + "\n"),
		[]byte(`{"id":"1","method":"rpc.cancel","meta":{"token":"secret"}}` + "\n"),
		[]byte("{\"id\":\"broken\"\n{\"id\":\"2\",\"method\":\"missing\"}\n"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 64*1024 || bytes.Count(input, []byte{'\n'}) > 256 {
			return
		}
		router := NewRouter()
		router.Handle("echo", func(*Context) (any, error) {
			return "ok", nil
		})
		var output bytes.Buffer
		var errors bytes.Buffer
		server := &Server{
			Router: router,
			Input:  bytes.NewReader(input),
			Output: &output,
			Errors: &errors,
			Token:  token,
		}
		if err := server.Serve(context.Background()); err != nil {
			t.Fatalf("serve fuzz input: %v", err)
		}
		if bytes.Contains(output.Bytes(), []byte(token)) ||
			bytes.Contains(errors.Bytes(), []byte(token)) {
			t.Fatal("Sidecar output leaked its token")
		}
		scanner := bufio.NewScanner(&output)
		for scanner.Scan() {
			var response Response
			if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
				t.Fatalf("invalid response JSON %q: %v", scanner.Text(), err)
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("scan responses: %v", err)
		}
	})
}

func FuzzHTTPRPCInput(f *testing.F) {
	const token = "fuzz-http-secret"
	seeds := []struct {
		body          []byte
		contentType   string
		authorization string
		origin        string
	}{
		{[]byte(`{"id":"1","method":"echo"}`), "application/json", "Bearer " + token, ""},
		{[]byte("not-json"), "application/json", "Bearer " + token, "https://app.example"},
		{[]byte(`{"id":"1"} trailing`), "application/json", "Bearer " + token, ""},
		{[]byte(`{}`), "text/plain", "Bearer fuzz-http-secret", ""},
		{[]byte(`{}`), "application/json", "broken", ""},
		{[]byte(`{}`), "application/json", "Bearer fuzz-http-secret", "https://evil.example"},
	}
	for _, seed := range seeds {
		f.Add(seed.body, seed.contentType, seed.authorization, seed.origin)
	}

	f.Fuzz(func(
		t *testing.T,
		body []byte,
		contentType string,
		authorization string,
		origin string,
	) {
		if len(body) > 64*1024 ||
			len(contentType) > 1024 ||
			len(authorization) > 4096 ||
			len(origin) > 4096 {
			return
		}
		principal, err := NewPrincipal("fuzz-user")
		if err != nil {
			t.Fatalf("principal: %v", err)
		}
		authenticator, err := NewStaticTokenAuthenticator(
			token,
			principal,
		)
		if err != nil {
			t.Fatalf("authenticator: %v", err)
		}
		router := NewRouter()
		router.Handle("echo", func(*Context) (any, error) {
			return "ok", nil
		})
		var errors bytes.Buffer
		handler := &HTTPHandler{
			Router:        router,
			Authenticator: authenticator,
			AllowedOrigin: "https://app.example",
			Errors:        &errors,
		}
		request := httptest.NewRequest(
			http.MethodPost,
			"http://backend.example/rpc",
			bytes.NewReader(body),
		)
		request.Header.Set("Content-Type", contentType)
		request.Header.Set("Authorization", authorization)
		request.Header.Set("Origin", origin)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code < 100 || recorder.Code > 599 {
			t.Fatalf("invalid HTTP status %d", recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), token) ||
			strings.Contains(errors.String(), token) {
			t.Fatal("HTTP output leaked its credential")
		}
		var response Response
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf(
				"invalid HTTP response JSON for status %d: %v",
				recorder.Code,
				err,
			)
		}
	})
}
