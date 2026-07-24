package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestServerDispatchesRequestsConcurrentlyAndDrains(t *testing.T) {
	router := NewRouter()
	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	router.Handle("slow", func(*Context) (any, error) {
		close(slowStarted)
		<-releaseSlow
		return "slow", nil
	})
	router.Handle("fast", func(*Context) (any, error) {
		<-slowStarted
		return "fast", nil
	})

	outputReader, outputWriter := io.Pipe()
	server := &Server{
		Router: router,
		Input: strings.NewReader(
			"{\"id\":\"slow\",\"method\":\"slow\"}\n" +
				"{\"id\":\"fast\",\"method\":\"fast\"}\n",
		),
		Output: outputWriter,
	}
	result := make(chan error, 1)
	go func() {
		err := server.Serve(context.Background())
		_ = outputWriter.CloseWithError(err)
		result <- err
	}()

	decoder := json.NewDecoder(outputReader)
	var fast Response
	decodeResult := make(chan error, 1)
	go func() { decodeResult <- decoder.Decode(&fast) }()
	select {
	case err := <-decodeResult:
		if err != nil {
			t.Fatalf("decode fast response: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("fast request was blocked by slow request")
	}
	if fast.ID != "fast" || fast.Result != "fast" {
		t.Fatalf("first response = %#v, want fast response", fast)
	}
	select {
	case err := <-result:
		t.Fatalf("server returned before draining slow request: %v", err)
	default:
	}

	close(releaseSlow)
	var slow Response
	if err := decoder.Decode(&slow); err != nil {
		t.Fatalf("decode slow response: %v", err)
	}
	if slow.ID != "slow" || slow.Result != "slow" {
		t.Fatalf("second response = %#v, want slow response", slow)
	}
	if err := <-result; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

func TestServerBoundsConcurrentRequests(t *testing.T) {
	const requestCount = 24
	const maximum = 3

	router := NewRouter()
	release := make(chan struct{})
	started := make(chan struct{}, requestCount)
	var active atomic.Int32
	var peak atomic.Int32
	router.Handle("wait", func(*Context) (any, error) {
		current := active.Add(1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return "ok", nil
	})

	var input strings.Builder
	for index := 0; index < requestCount; index++ {
		fmt.Fprintf(&input, "{\"id\":\"%d\",\"method\":\"wait\"}\n", index)
	}
	var output bytes.Buffer
	server := &Server{
		Router:                router,
		Input:                 strings.NewReader(input.String()),
		Output:                &output,
		MaxConcurrentRequests: maximum,
	}
	result := make(chan error, 1)
	go func() { result <- server.Serve(context.Background()) }()

	for index := 0; index < maximum; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("server did not fill the configured request capacity")
		}
	}
	select {
	case <-started:
		t.Fatal("server exceeded the configured request capacity")
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	if err := <-result; err != nil {
		t.Fatalf("serve: %v", err)
	}
	if got := peak.Load(); got != maximum {
		t.Fatalf("peak concurrency = %d, want %d", got, maximum)
	}

	decoder := json.NewDecoder(&output)
	responses := make(map[string]bool, requestCount)
	for index := 0; index < requestCount; index++ {
		var response Response
		if err := decoder.Decode(&response); err != nil {
			t.Fatalf("decode response %d: %v", index, err)
		}
		responses[response.ID] = true
	}
	if len(responses) != requestCount {
		t.Fatalf("response ids = %d, want %d", len(responses), requestCount)
	}
}

func TestServerRejectsNegativeConcurrentRequestLimit(t *testing.T) {
	server := &Server{
		Router:                NewRouter(),
		Input:                 strings.NewReader(""),
		Output:                io.Discard,
		MaxConcurrentRequests: -1,
	}

	err := server.Serve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("error = %v, want invalid concurrency limit", err)
	}
}

var errWriteFailed = errors.New("write failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errWriteFailed
}
