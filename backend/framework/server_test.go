package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestServerContinuesAfterInvalidJSON(t *testing.T) {
	router := NewRouter()
	router.Handle("echo", func(*Context) (any, error) { return "ok", nil })
	input := strings.NewReader("not-json\n{\"id\":\"1\",\"method\":\"echo\"}\n")
	var output bytes.Buffer
	var logs bytes.Buffer
	server := &Server{Router: router, Input: input, Output: &output, Errors: &logs}

	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoder := json.NewDecoder(&output)
	var invalid Response
	if err := decoder.Decode(&invalid); err != nil {
		t.Fatalf("decode invalid response: %v", err)
	}
	if invalid.Error == nil || invalid.Error.Code != "invalid_json" {
		t.Fatalf("response = %#v", invalid)
	}

	var valid Response
	if err := decoder.Decode(&valid); err != nil {
		t.Fatalf("decode valid response: %v", err)
	}
	if valid.ID != "1" || valid.Result != "ok" {
		t.Fatalf("response = %#v", valid)
	}
	if !strings.Contains(logs.String(), "invalid JSON request") {
		t.Fatalf("logs = %q", logs.String())
	}
}

func TestServerRejectsOversizedRequests(t *testing.T) {
	server := &Server{
		Router: NewRouter(),
		Input:  strings.NewReader(strings.Repeat("x", 4*1024*1024+1)),
		Output: &bytes.Buffer{},
	}

	err := server.Serve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("error = %v, want token too long", err)
	}
}

func TestServerRequiresDependencies(t *testing.T) {
	server := &Server{}
	if err := server.Serve(context.Background()); err == nil {
		t.Fatal("expected configuration error")
	}
	if err := (&Server{Router: NewRouter(), Input: strings.NewReader("")}).Serve(nil); err == nil {
		t.Fatal("expected nil context error")
	}
}

func TestServerReturnsOutputErrors(t *testing.T) {
	router := NewRouter()
	router.Handle("test", func(*Context) (any, error) { return "ok", nil })
	server := &Server{
		Router: router,
		Input:  strings.NewReader(`{"id":"1","method":"test"}` + "\n"),
		Output: failingWriter{},
	}

	if err := server.Serve(context.Background()); !errors.Is(err, errWriteFailed) {
		t.Fatalf("error = %v", err)
	}
}

var errWriteFailed = errors.New("write failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errWriteFailed
}
