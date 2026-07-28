package framework

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestServerImportsVerifiedSidecarFileUpload(t *testing.T) {
	store, err := NewFileTransferStore(FileTransferOptions{
		TTL:      time.Minute,
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	content := []byte("sidecar upload")
	sum := sha256.Sum256(content)
	sourcePath := filepath.Join(t.TempDir(), "upload.bin")
	if err := os.WriteFile(sourcePath, content, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	params, err := json.Marshal(map[string]any{
		"path":      sourcePath,
		"name":      "upload.bin",
		"mediaType": "application/octet-stream",
		"size":      len(content),
		"sha256":    hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatalf("encode params: %v", err)
	}
	input := fmt.Sprintf(
		"{\"id\":\"upload\",\"method\":\"rpc.file_upload\",\"params\":%s,\"meta\":{\"token\":\"secret\"}}\n",
		params,
	)
	var output bytes.Buffer
	server := &Server{
		Router:        NewRouter(),
		Input:         strings.NewReader(input),
		Output:        &output,
		FileTransfers: store,
		Token:         "secret",
	}

	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var response struct {
		ID     string        `json:"id"`
		Result FileReference `json:"result"`
		Error  *RPCError     `json:"error"`
	}
	if err := json.NewDecoder(&output).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "upload" || response.Error != nil {
		t.Fatalf("response = %#v", response)
	}
	upload, err := store.ConsumeUpload(response.Result)
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

func TestServerRejectsInvalidSidecarFileUploads(t *testing.T) {
	store, err := NewFileTransferStore(FileTransferOptions{
		TTL:      time.Minute,
		MaxBytes: 4,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	sourcePath := filepath.Join(t.TempDir(), "upload.bin")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	validParams := func(overrides map[string]any) json.RawMessage {
		t.Helper()
		values := map[string]any{
			"path":      sourcePath,
			"name":      "upload.bin",
			"mediaType": "application/octet-stream",
			"size":      4,
			"sha256":    "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7",
		}
		for key, value := range overrides {
			values[key] = value
		}
		params, err := json.Marshal(values)
		if err != nil {
			t.Fatalf("encode params: %v", err)
		}
		return params
	}
	tests := []struct {
		name    string
		server  *Server
		ctx     context.Context
		request Request
		code    string
	}{
		{
			name:   "store",
			server: &Server{Token: "secret"},
			ctx:    context.Background(),
			request: Request{
				ID: "1", Params: validParams(nil), Meta: map[string]string{"token": "secret"},
			},
			code: "file_upload_unavailable",
		},
		{
			name:   "token",
			server: &Server{FileTransfers: store, Token: "secret"},
			ctx:    context.Background(),
			request: Request{
				ID: "1", Params: validParams(nil), Meta: map[string]string{"token": "wrong"},
			},
			code: "unauthenticated",
		},
		{
			name:   "json",
			server: &Server{FileTransfers: store, Token: "secret"},
			ctx:    context.Background(),
			request: Request{
				ID: "1", Params: json.RawMessage("{"), Meta: map[string]string{"token": "secret"},
			},
			code: "invalid_params",
		},
		{
			name:   "missing path",
			server: &Server{FileTransfers: store, Token: "secret"},
			ctx:    context.Background(),
			request: Request{
				ID:     "1",
				Params: validParams(map[string]any{"path": ""}),
				Meta:   map[string]string{"token": "secret"},
			},
			code: "invalid_params",
		},
		{
			name:   "source",
			server: &Server{FileTransfers: store, Token: "secret"},
			ctx:    context.Background(),
			request: Request{
				ID:     "1",
				Params: validParams(map[string]any{"path": sourcePath + ".missing"}),
				Meta:   map[string]string{"token": "secret"},
			},
			code: "file_upload_unavailable",
		},
		{
			name:   "too large",
			server: &Server{FileTransfers: store, Token: "secret"},
			ctx:    context.Background(),
			request: Request{
				ID:     "1",
				Params: validParams(map[string]any{"size": 5}),
				Meta:   map[string]string{"token": "secret"},
			},
			code: "file_upload_too_large",
		},
		{
			name:   "checksum",
			server: &Server{FileTransfers: store, Token: "secret"},
			ctx:    context.Background(),
			request: Request{
				ID:     "1",
				Params: validParams(map[string]any{"sha256": strings.Repeat("0", 64)}),
				Meta:   map[string]string{"token": "secret"},
			},
			code: "file_upload_checksum_mismatch",
		},
		{
			name:   "invalid metadata",
			server: &Server{FileTransfers: store, Token: "secret"},
			ctx:    context.Background(),
			request: Request{
				ID:     "1",
				Params: validParams(map[string]any{"name": "../bad"}),
				Meta:   map[string]string{"token": "secret"},
			},
			code: "file_upload_invalid",
		},
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests = append(tests, struct {
		name    string
		server  *Server
		ctx     context.Context
		request Request
		code    string
	}{
		name:   "cancelled",
		server: &Server{FileTransfers: store, Token: "secret"},
		ctx:    cancelled,
		request: Request{
			ID: "1", Params: validParams(nil), Meta: map[string]string{"token": "secret"},
		},
		code: "request_cancelled",
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := test.server.importSidecarUpload(test.ctx, test.request)
			if response.Error == nil || response.Error.Code != test.code {
				t.Fatalf("response = %#v, want %q", response, test.code)
			}
		})
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

func TestServerCancellationReachesActiveAndPendingRequests(t *testing.T) {
	router := NewRouter()
	activeStarted := make(chan struct{})
	activeCancelled := make(chan struct{})
	pendingRan := make(chan struct{}, 1)
	router.Handle("wait", func(ctx *Context) (any, error) {
		switch ctx.Request.ID {
		case "active":
			close(activeStarted)
			<-ctx.Done()
			close(activeCancelled)
			return nil, ctx.Err()
		case "pending":
			pendingRan <- struct{}{}
		}
		return "ok", nil
	})

	inputReader, inputWriter := io.Pipe()
	var output bytes.Buffer
	server := &Server{
		Router:                router,
		Input:                 inputReader,
		Output:                &output,
		MaxConcurrentRequests: 1,
		MaxPendingRequests:    2,
	}
	result := make(chan error, 1)
	go func() { result <- server.Serve(context.Background()) }()

	writeServerRequest(t, inputWriter, `{"id":"active","method":"wait","meta":{"token":"secret"}}`)
	select {
	case <-activeStarted:
	case <-time.After(time.Second):
		t.Fatal("active request did not start")
	}
	writeServerRequest(t, inputWriter, `{"id":"pending","method":"wait","meta":{"token":"secret"}}`)
	writeServerRequest(t, inputWriter, `{"id":"pending","method":"rpc.cancel","meta":{"token":"secret"}}`)
	writeServerRequest(t, inputWriter, `{"id":"active","method":"rpc.cancel","meta":{"token":"wrong"}}`)
	select {
	case <-activeCancelled:
		t.Fatal("mismatched token cancelled the active request")
	case <-time.After(25 * time.Millisecond):
	}
	writeServerRequest(t, inputWriter, `{"id":"active","method":"rpc.cancel","meta":{"token":"secret"}}`)
	select {
	case <-activeCancelled:
	case <-time.After(time.Second):
		t.Fatal("active request context was not cancelled")
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("serve: %v", err)
	}
	select {
	case <-pendingRan:
		t.Fatal("cancelled pending request reached the router")
	default:
	}
}

func TestServerRejectsRequestsBeyondPendingLimit(t *testing.T) {
	router := NewRouter()
	started := make(chan struct{})
	release := make(chan struct{})
	router.Handle("wait", func(ctx *Context) (any, error) {
		if ctx.Request.ID == "active" {
			close(started)
			<-release
		}
		return "ok", nil
	})

	inputReader, inputWriter := io.Pipe()
	output := newNotifyingBuffer()
	server := &Server{
		Router:                router,
		Input:                 inputReader,
		Output:                output,
		MaxConcurrentRequests: 1,
		MaxPendingRequests:    1,
	}
	result := make(chan error, 1)
	go func() { result <- server.Serve(context.Background()) }()

	writeServerRequest(t, inputWriter, `{"id":"active","method":"wait"}`)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("active request did not start")
	}
	writeServerRequest(t, inputWriter, `{"id":"pending","method":"wait"}`)
	writeServerRequest(t, inputWriter, `{"id":"overflow","method":"wait"}`)
	select {
	case <-output.written:
	case <-time.After(time.Second):
		t.Fatal("server did not report the overflow request")
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := output.String()
	decoder := json.NewDecoder(strings.NewReader(responses))
	var foundBusy bool
	for decoder.More() {
		var response Response
		if err := decoder.Decode(&response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.ID == "overflow" &&
			response.Error != nil &&
			response.Error.Code == "server_busy" {
			foundBusy = true
		}
	}
	if !foundBusy {
		t.Fatalf("responses = %s, want overflow server_busy error", responses)
	}
}

type notifyingBuffer struct {
	buffer  bytes.Buffer
	written chan struct{}
	mu      sync.Mutex
}

func newNotifyingBuffer() *notifyingBuffer {
	return &notifyingBuffer{written: make(chan struct{}, 1)}
}

func (buffer *notifyingBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	written, err := buffer.buffer.Write(value)
	buffer.mu.Unlock()
	select {
	case buffer.written <- struct{}{}:
	default:
	}
	return written, err
}

func (buffer *notifyingBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func TestServerRejectsNegativePendingRequestLimit(t *testing.T) {
	server := &Server{
		Router:             NewRouter(),
		Input:              strings.NewReader(""),
		Output:             io.Discard,
		MaxPendingRequests: -1,
	}

	err := server.Serve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("error = %v, want invalid pending request limit", err)
	}
}

func TestServerStreamWindowAppliesBackpressureUntilAcknowledged(t *testing.T) {
	router := NewRouter()
	router.Handle("numbers.list", func(ctx *Context) (any, error) {
		return ProduceStream(ctx, func(stream *StreamWriter) error {
			if err := stream.Send(1); err != nil {
				return err
			}
			return stream.Send(2)
		})
	})

	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	server := &Server{
		Router: router,
		Input:  inputReader,
		Output: outputWriter,
	}
	result := make(chan error, 1)
	go func() {
		err := server.Serve(context.Background())
		_ = outputWriter.CloseWithError(err)
		result <- err
	}()

	writeServerRequest(
		t,
		inputWriter,
		`{"id":"stream","method":"numbers.list","meta":{"token":"secret","stream":"1","stream_window":"1"}}`,
	)
	decoder := json.NewDecoder(outputReader)
	var first Response
	if err := decoder.Decode(&first); err != nil {
		t.Fatalf("decode first event: %v", err)
	}
	if first.Stream == nil || first.Stream.Sequence != 1 || first.Result != float64(1) {
		t.Fatalf("first response = %#v", first)
	}

	secondResult := make(chan Response, 1)
	secondError := make(chan error, 1)
	go func() {
		var second Response
		if err := decoder.Decode(&second); err != nil {
			secondError <- err
			return
		}
		secondResult <- second
	}()
	select {
	case response := <-secondResult:
		t.Fatalf("second event bypassed backpressure: %#v", response)
	case err := <-secondError:
		t.Fatalf("decode second event: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	writeServerRequest(
		t,
		inputWriter,
		`{"id":"stream","method":"rpc.stream_ack","params":{"sequence":1},"meta":{"token":"secret"}}`,
	)
	select {
	case second := <-secondResult:
		if second.Stream == nil ||
			second.Stream.Sequence != 2 ||
			second.Result != float64(2) {
			t.Fatalf("second response = %#v", second)
		}
	case err := <-secondError:
		t.Fatalf("decode second event: %v", err)
	case <-time.After(time.Second):
		t.Fatal("acknowledgement did not release stream backpressure")
	}

	var complete Response
	if err := decoder.Decode(&complete); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if complete.Stream == nil || complete.Stream.Kind != streamCompleteKind {
		t.Fatalf("complete response = %#v", complete)
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

func TestServerCancelsBackpressuredStreamsWhenInputCloses(t *testing.T) {
	router := NewRouter()
	firstSent := make(chan struct{})
	router.Handle("numbers.list", func(ctx *Context) (any, error) {
		return ProduceStream(ctx, func(stream *StreamWriter) error {
			if err := stream.Send(1); err != nil {
				return err
			}
			close(firstSent)
			return stream.Send(2)
		})
	})

	inputReader, inputWriter := io.Pipe()
	server := &Server{
		Router: router,
		Input:  inputReader,
		Output: io.Discard,
	}
	result := make(chan error, 1)
	go func() { result <- server.Serve(context.Background()) }()
	writeServerRequest(
		t,
		inputWriter,
		`{"id":"stream","method":"numbers.list","meta":{"stream":"1","stream_window":"1"}}`,
	)
	select {
	case <-firstSent:
	case <-time.After(time.Second):
		t.Fatal("first stream event was not sent")
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not cancel the backpressured stream on input EOF")
	}
}

func writeServerRequest(t *testing.T, output io.Writer, request string) {
	t.Helper()
	if _, err := fmt.Fprintln(output, request); err != nil {
		t.Fatalf("write request: %v", err)
	}
}

var errWriteFailed = errors.New("write failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errWriteFailed
}
