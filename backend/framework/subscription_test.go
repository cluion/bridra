package framework

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSubscriptionBufferReplayExpiryAndEpoch(t *testing.T) {
	b, err := NewSubscriptionBuffer[int](SubscriptionBufferOptions{Capacity: 2})
	if err != nil {
		t.Fatal(err)
	}
	start := b.Cursor()
	first, _ := b.Publish(1)
	second, _ := b.Publish(2)
	item, err := b.Next(context.Background(), first)
	if err != nil || item.Cursor != second || item.Value != 2 {
		t.Fatalf("item=%+v error=%v", item, err)
	}
	third, _ := b.Publish(3)
	_, err = b.Next(context.Background(), start)
	requireSubscriptionError(t, err, "subscription_cursor_expired")
	item, err = b.Next(context.Background(), "")
	if err != nil || item.Cursor != second {
		t.Fatalf("initial replay=%+v %v", item, err)
	}
	item, err = b.Next(context.Background(), second)
	if err != nil || item.Cursor != third {
		t.Fatalf("resume=%+v %v", item, err)
	}
	other, _ := NewSubscriptionBuffer[int](SubscriptionBufferOptions{})
	_, err = other.Next(context.Background(), third)
	requireSubscriptionError(t, err, "subscription_cursor_expired")
	for _, cursor := range []string{"bad", b.epoch + ":-1", b.epoch + ":01", b.epoch + ":100"} {
		_, err = b.Next(context.Background(), cursor)
		requireSubscriptionError(t, err, "subscription_cursor_invalid")
	}
}

func TestSubscriptionBufferBoundsAndImmutableHistory(t *testing.T) {
	for _, options := range []SubscriptionBufferOptions{{Capacity: -1}, {Capacity: 4097}, {MaxEventBytes: -1}, {MaxEventBytes: MaxRequestBytes + 1}} {
		if _, err := NewSubscriptionBuffer[int](options); err == nil {
			t.Fatalf("accepted %+v", options)
		}
	}
	b, _ := NewSubscriptionBuffer[map[string]int](SubscriptionBufferOptions{MaxEventBytes: 16})
	value := map[string]int{"a": 1}
	cursor, err := b.Publish(value)
	if err != nil {
		t.Fatal(err)
	}
	value["a"] = 2
	first, _ := b.Next(context.Background(), "")
	first.Value["a"] = 3
	second, _ := b.Next(context.Background(), "")
	if second.Value["a"] != 1 {
		t.Fatalf("history changed: %+v", second)
	}
	if _, err := b.Publish(map[string]int{"very-long-key-for-size": 1}); err == nil {
		t.Fatal("accepted oversized event")
	}
	if b.Cursor() != cursor {
		t.Fatal("rejected event advanced cursor")
	}
	b.Close()
	b.Close()
	if _, err := b.Publish(value); !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatal(err)
	}
	if _, err := b.Next(context.Background(), cursor); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestSubscriptionBufferCancellationAndConcurrentReaders(t *testing.T) {
	b, _ := NewSubscriptionBuffer[int](SubscriptionBufferOptions{})
	start := b.Cursor()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Next(ctx, start); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, err := b.Next(ctx, start)
			if err != nil || item.Value != 42 {
				t.Errorf("item=%+v error=%v", item, err)
			}
		}()
	}
	if _, err := b.Publish(42); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	closed := make(chan error, 1)
	go func() { _, err := b.Next(ctx, b.Cursor()); closed <- err }()
	b.Close()
	if err := <-closed; !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestSubscriptionSlowConsumerGetsExplicitGapAfterRetentionOverflows(t *testing.T) {
	buffer, _ := NewSubscriptionBuffer[int](SubscriptionBufferOptions{Capacity: 2})
	_, _ = buffer.Publish(1)
	firstSent := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	router := NewRouter()
	router.Handle("logs.watch", func(ctx *Context) (any, error) {
		return ProduceSubscription(ctx, SubscriptionOptions{}, buffer.Next)
	})
	var responses []Response
	done := make(chan error, 1)
	go func() {
		done <- router.DispatchStream(context.Background(), Request{ID: "1", Method: "logs.watch"}, func(response Response) error {
			responses = append(responses, response)
			if len(responses) == 1 {
				close(firstSent)
				<-release
			}
			return nil
		})
	}()
	<-firstSent
	for _, value := range []int{2, 3, 4} {
		if _, err := buffer.Publish(value); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(responses) != 2 || responses[1].Error == nil || responses[1].Error.Code != "subscription_cursor_expired" {
		t.Fatalf("slow consumer responses=%+v", responses)
	}
	if len(buffer.records) != 2 {
		t.Fatalf("retention grew: %d", len(buffer.records))
	}
}

func TestProduceSubscriptionHeartbeatReplayAndCancellation(t *testing.T) {
	b, _ := NewSubscriptionBuffer[int](SubscriptionBufferOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	router := NewRouter()
	router.Handle("logs.watch", func(ctx *Context) (any, error) {
		return ProduceSubscription(ctx, SubscriptionOptions{HeartbeatInterval: time.Millisecond}, b.Next)
	})
	var kinds []string
	err := router.DispatchStream(ctx, Request{ID: "watch", Method: "logs.watch"}, func(response Response) error {
		envelope := response.Result.(map[string]any)
		kind := envelope["kind"].(string)
		kinds = append(kinds, kind)
		if kind == "heartbeat" {
			if envelope["cursor"] != "" {
				t.Fatal("heartbeat advanced cursor")
			}
			_, _ = b.Publish(42)
		} else {
			if envelope["value"] != 42 {
				t.Fatal(envelope)
			}
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || strings.Join(kinds, ",") != "heartbeat,data" {
		t.Fatalf("kinds=%v error=%v", kinds, err)
	}
}

func TestProduceSubscriptionRejectsInvalidSourceAndPreservesErrors(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		options    SubscriptionOptions
		next       SubscriptionNext[int]
	}{
		{"nil", "invalid_subscription", SubscriptionOptions{}, nil},
		{"negative", "invalid_subscription", SubscriptionOptions{HeartbeatInterval: -1}, func(context.Context, string) (SubscriptionItem[int], error) { return SubscriptionItem[int]{}, nil }},
		{"cursor", "subscription_cursor_invalid", SubscriptionOptions{}, func(context.Context, string) (SubscriptionItem[int], error) { return SubscriptionItem[int]{}, nil }},
		{"repeated-cursor", "subscription_cursor_invalid", SubscriptionOptions{AfterCursor: "same"}, func(context.Context, string) (SubscriptionItem[int], error) {
			return SubscriptionItem[int]{Cursor: "same"}, nil
		}},
		{"large-cursor", "subscription_cursor_invalid", SubscriptionOptions{AfterCursor: strings.Repeat("x", 4097)}, func(context.Context, string) (SubscriptionItem[int], error) {
			return SubscriptionItem[int]{Cursor: "next"}, nil
		}},
		{"expired", "subscription_cursor_expired", SubscriptionOptions{}, func(context.Context, string) (SubscriptionItem[int], error) {
			return SubscriptionItem[int]{}, NewError("subscription_cursor_expired", "resync")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := NewRouter()
			router.Handle("logs.watch", func(ctx *Context) (any, error) { return ProduceSubscription(ctx, tc.options, tc.next) })
			err := router.DispatchStream(context.Background(), Request{ID: "1", Method: "logs.watch"}, func(r Response) error {
				if r.Error == nil || r.Error.Code != tc.code {
					t.Fatalf("response=%+v", r)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	b, _ := NewSubscriptionBuffer[int](SubscriptionBufferOptions{})
	router := NewRouter()
	router.Handle("logs.watch", func(ctx *Context) (any, error) { return ProduceSubscription(ctx, SubscriptionOptions{}, b.Next) })
	if r := router.Dispatch(context.Background(), Request{Method: "logs.watch"}); r.Error == nil || r.Error.Code != "streaming_required" {
		t.Fatal(r)
	}
}

func TestSubscriptionTransportParity(t *testing.T) {
	for _, transport := range []string{"sidecar", "http", "embedded"} {
		t.Run(transport, func(t *testing.T) {
			b, _ := NewSubscriptionBuffer[int](SubscriptionBufferOptions{})
			first, _ := b.Publish(1)
			second, _ := b.Publish(2)
			b.Close()
			router := NewRouter()
			router.Handle("logs.watch", func(ctx *Context) (any, error) {
				return ProduceSubscription(ctx, SubscriptionOptions{AfterCursor: first}, b.Next)
			})
			request := Request{ID: "watch", Method: "logs.watch", Meta: map[string]string{"stream": "1", "stream_window": "1"}}
			responses := readSubscriptionTransport(t, transport, router, request)
			if len(responses) != 2 || responses[0].Stream.Kind != "data" || responses[1].Stream.Kind != "complete" {
				t.Fatalf("responses=%+v", responses)
			}
			encoded, _ := json.Marshal(responses[0].Result)
			var envelope struct {
				Kind, Cursor string
				Value        int
			}
			if err := json.Unmarshal(encoded, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Kind != "data" || envelope.Cursor != second || envelope.Value != 2 {
				t.Fatalf("envelope=%+v", envelope)
			}
		})
	}
}

func readSubscriptionTransport(t *testing.T, transport string, router *Router, request Request) []Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var reader io.Reader
	var next func() (string, error)
	var input *io.PipeWriter
	var served chan error
	encoded, _ := json.Marshal(request)
	switch transport {
	case "http":
		recorder := httptest.NewRecorder()
		handler := &HTTPHandler{Router: router}
		httpRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(string(encoded))).WithContext(ctx)
		httpRequest.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, httpRequest)
		if recorder.Code != 200 {
			t.Fatalf("http=%d %s", recorder.Code, recorder.Body.String())
		}
		reader = recorder.Body
	case "embedded":
		runtime, err := NewEmbeddedRuntime(router, func(context.Context) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close(context.Background()) })
		stream, err := runtime.StreamJSON(string(encoded))
		if err != nil {
			t.Fatal(err)
		}
		next = stream.NextJSON
	case "sidecar":
		inRead, inWrite := io.Pipe()
		outRead, outWrite := io.Pipe()
		t.Cleanup(func() { _ = inWrite.Close(); _ = inRead.Close(); _ = outWrite.Close(); _ = outRead.Close() })
		input = inWrite
		reader = outRead
		served = make(chan error, 1)
		go func() { served <- (&Server{Router: router, Input: inRead, Output: outWrite}).Serve(ctx) }()
		if err := json.NewEncoder(input).Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	var responses []Response
	var decoder *json.Decoder
	if reader != nil {
		decoder = json.NewDecoder(reader)
	}
	for {
		var response Response
		var err error
		if next != nil {
			var raw string
			raw, err = next()
			if raw == "" && err == nil {
				break
			}
			if err == nil {
				err = json.Unmarshal([]byte(raw), &response)
			}
		} else {
			err = decoder.Decode(&response)
		}
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
		if response.Stream == nil {
			t.Fatalf("missing frame: %+v", response)
		}
		if response.Stream.Sequence != int64(len(responses)) {
			t.Fatal("non-contiguous frame sequence")
		}
		if response.Stream.Kind == "complete" {
			break
		}
		if input != nil {
			if err := json.NewEncoder(input).Encode(Request{ID: request.ID, Method: "rpc.stream_ack", Params: json.RawMessage(`{"sequence":` + strconv.Itoa(len(responses)) + `}`)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if input != nil {
		_ = input.Close()
		if err := <-served; err != nil {
			t.Fatal(err)
		}
	}
	return responses
}

func requireSubscriptionError(t *testing.T, err error, code string) {
	t.Helper()
	var rpc *RPCError
	if !errors.As(err, &rpc) || rpc.Code != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
}
