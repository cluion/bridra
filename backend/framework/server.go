package framework

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
)

const defaultMaxConcurrentRequests = 8
const defaultMaxPendingRequests = 64
const defaultStreamWindow = 16
const maxStreamWindow = 256
const rpcCancellationMethod = "rpc.cancel"
const rpcFileUploadMethod = "rpc.file_upload"
const rpcResourceGrantMethod = "rpc.resource_grant"
const rpcResourceReleaseMethod = "rpc.resource_release"
const rpcStreamAckMethod = "rpc.stream_ack"

type Server struct {
	Router                *Router
	Input                 io.Reader
	Output                io.Writer
	Errors                io.Writer
	FileTransfers         *FileTransferStore
	Resources             *ResourceBroker
	Token                 string
	MaxConcurrentRequests int
	MaxPendingRequests    int
}

func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil || s.Router == nil || s.Input == nil || s.Output == nil {
		return fmt.Errorf("framework: context, router, input, and output are required")
	}
	maxConcurrentRequests := s.MaxConcurrentRequests
	if maxConcurrentRequests == 0 {
		maxConcurrentRequests = defaultMaxConcurrentRequests
	}
	if maxConcurrentRequests < 0 {
		return fmt.Errorf("framework: max concurrent requests cannot be negative")
	}
	maxPendingRequests := s.MaxPendingRequests
	if maxPendingRequests == 0 {
		maxPendingRequests = defaultMaxPendingRequests
	}
	if maxPendingRequests < 0 {
		return fmt.Errorf("framework: max pending requests cannot be negative")
	}

	scanner := bufio.NewScanner(s.Input)
	scanner.Buffer(make([]byte, 64*1024), MaxRequestBytes)
	dispatchCtx, cancelDispatch := context.WithCancel(ctx)
	defer cancelDispatch()
	encoder := newServerResponseEncoder(s.Output, cancelDispatch)
	requests := newServerRequestRegistry()
	jobs := make(chan *serverRequest, maxPendingRequests)
	var workers sync.WaitGroup
	for range maxConcurrentRequests {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for request := range jobs {
				if request.Context.Err() == nil {
					if request.flow == nil {
						response := Response{}
						switch request.Request.Method {
						case rpcFileUploadMethod:
							response = s.importSidecarUpload(
								request.Context,
								request.Request,
							)
						case rpcResourceGrantMethod:
							response = s.grantSidecarResource(request.Request)
						case rpcResourceReleaseMethod:
							response = s.releaseSidecarResource(request.Request)
						default:
							response = s.Router.Dispatch(
								request.Context,
								request.Request,
							)
						}
						_ = encoder.Encode(response)
					} else {
						_ = s.Router.DispatchStream(
							request.Context,
							request.Request,
							func(response Response) error {
								if response.Stream != nil &&
									response.Stream.Kind != streamCompleteKind {
									if err := request.flow.Acquire(
										request.Context,
										response.Stream.Sequence,
									); err != nil {
										return err
									}
								}
								return encoder.Encode(response)
							},
						)
					}
				}
				requests.Complete(request)
			}
		}()
	}

scan:
	for scanner.Scan() {
		if dispatchCtx.Err() != nil {
			break
		}
		var request Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if s.Errors != nil {
				fmt.Fprintf(s.Errors, "sidecar: invalid JSON request: %v\n", err)
			}
			if encodeErr := encoder.Encode(Response{
				Error: NewError("invalid_json", "The request is not valid JSON."),
			}); encodeErr != nil {
				break
			}
			continue
		}
		if request.Method == rpcCancellationMethod {
			requests.Cancel(request.ID, request.Meta["token"])
			continue
		}
		if request.Method == rpcStreamAckMethod {
			requests.Acknowledge(request.ID, request.Meta["token"], request.Params)
			continue
		}

		registered, duplicate := requests.Register(dispatchCtx, request)
		if duplicate {
			if encodeErr := encoder.Encode(Response{
				ID: request.ID,
				Error: NewError(
					"duplicate_request",
					"Another request already uses this id.",
				),
			}); encodeErr != nil {
				break
			}
			continue
		}
		select {
		case jobs <- registered:
		case <-dispatchCtx.Done():
			requests.Complete(registered)
			break scan
		default:
			requests.Complete(registered)
			if encodeErr := encoder.Encode(Response{
				ID:    request.ID,
				Error: NewError("server_busy", "The Go sidecar request queue is full."),
			}); encodeErr != nil {
				break scan
			}
		}
	}

	scanError := scanner.Err()
	requests.CancelStreams()
	close(jobs)
	workers.Wait()
	return errors.Join(scanError, encoder.Err())
}

func (s *Server) grantSidecarResource(request Request) Response {
	response := Response{ID: request.ID}
	if s.Resources == nil {
		response.Error = NewError(
			"resource_unavailable",
			"Native Sidecar resources are not configured.",
		)
		return response
	}
	if !s.authenticatesSidecarControl(request) {
		response.Error = NewError("unauthenticated", "The request token is invalid.")
		return response
	}
	var params struct {
		Bookmark string                `json:"bookmark"`
		Scope    ResourceBookmarkScope `json:"scope"`
	}
	if err := decodeSidecarControlParams(request.Params, &params); err != nil ||
		params.Bookmark == "" || !validResourceBookmarkScope(params.Scope) {
		response.Error = NewError(
			"resource_invalid",
			"The resource grant parameters are invalid.",
		)
		return response
	}
	if len(params.Bookmark) > base64.StdEncoding.EncodedLen(s.Resources.maxBookmarkBytes) {
		response.Error = NewError(
			"resource_too_large",
			"The resource bookmark exceeds the configured size.",
		)
		return response
	}
	bookmark, err := base64.StdEncoding.DecodeString(params.Bookmark)
	if err != nil {
		response.Error = NewError(
			"resource_invalid",
			"The resource grant parameters are invalid.",
		)
		return response
	}
	capability, err := s.Resources.Grant(bookmark, params.Scope)
	if err != nil {
		response.Error = renderSidecarResourceError(err)
		return response
	}
	response.Result = map[string]string{"capability": string(capability)}
	return response
}

func (s *Server) releaseSidecarResource(request Request) Response {
	response := Response{ID: request.ID}
	if s.Resources == nil {
		response.Error = NewError(
			"resource_unavailable",
			"Native Sidecar resources are not configured.",
		)
		return response
	}
	if !s.authenticatesSidecarControl(request) {
		response.Error = NewError("unauthenticated", "The request token is invalid.")
		return response
	}
	var params struct {
		Capability ResourceCapability `json:"capability"`
	}
	if err := decodeSidecarControlParams(request.Params, &params); err != nil ||
		!validResourceCapability(params.Capability) {
		response.Error = NewError(
			"resource_invalid",
			"The resource release parameters are invalid.",
		)
		return response
	}
	if err := s.Resources.Release(params.Capability); err != nil {
		response.Error = renderSidecarResourceError(err)
		return response
	}
	response.Result = map[string]bool{"released": true}
	return response
}

func (s *Server) authenticatesSidecarControl(request Request) bool {
	return s.Token != "" && request.Meta["token"] == s.Token
}

func decodeSidecarControlParams(params json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("sidecar control parameters contain trailing data")
	}
	return nil
}

func renderSidecarResourceError(err error) *RPCError {
	switch {
	case errors.Is(err, ErrResourceBookmarkUnavailable),
		errors.Is(err, ErrResourceBrokerClosed):
		return NewError("resource_unavailable", "Native Sidecar resources are unavailable.")
	case errors.Is(err, ErrResourceBookmarkInvalid),
		errors.Is(err, ErrResourceCapabilityInvalid),
		errors.Is(err, ErrResourceLeaseInvalid):
		return NewError("resource_invalid", "The native resource grant is invalid.")
	case errors.Is(err, ErrResourceBookmarkTooLarge):
		return NewError("resource_too_large", "The resource bookmark exceeds the configured size.")
	case errors.Is(err, ErrResourceBookmarkStale):
		return NewError("resource_stale", "The resource bookmark is stale and must be recreated.")
	case errors.Is(err, ErrResourceBookmarkAccess):
		return NewError("resource_access_denied", "The Sidecar could not access the resource.")
	case errors.Is(err, ErrResourceGrantLimit):
		return NewError("resource_limit", "The active native resource limit was reached.")
	case errors.Is(err, ErrResourceCapabilityNotFound):
		return NewError("resource_not_found", "The native resource capability was not found.")
	default:
		return NewError("resource_failed", "The native resource operation failed.")
	}
}

func (s *Server) importSidecarUpload(
	ctx context.Context,
	request Request,
) Response {
	response := Response{ID: request.ID}
	if s.FileTransfers == nil {
		response.Error = NewError(
			"file_upload_unavailable",
			"The Sidecar file transfer store is not configured.",
		)
		return response
	}
	if s.Token == "" || request.Meta["token"] != s.Token {
		response.Error = NewError(
			"unauthenticated",
			"The request token is invalid.",
		)
		return response
	}
	var params struct {
		Path      string `json:"path"`
		Name      string `json:"name"`
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
		SHA256    string `json:"sha256"`
	}
	decoder := json.NewDecoder(bytes.NewReader(request.Params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		response.Error = NewError(
			"invalid_params",
			"The file upload parameters are invalid.",
		)
		return response
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF || params.Path == "" {
		response.Error = NewError(
			"invalid_params",
			"The file upload parameters are invalid.",
		)
		return response
	}
	source, err := os.Open(params.Path)
	if err != nil {
		response.Error = NewError(
			"file_upload_unavailable",
			"The Sidecar could not open the upload source.",
		)
		return response
	}
	defer source.Close()
	reference, err := s.FileTransfers.ImportUpload(
		ctx,
		params.Name,
		params.MediaType,
		params.Size,
		params.SHA256,
		source,
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrFileTransferTooLarge):
			response.Error = NewError(
				"file_upload_too_large",
				"The file upload exceeds its declared or configured size.",
			)
		case errors.Is(err, ErrFileTransferChecksum):
			response.Error = NewError(
				"file_upload_checksum_mismatch",
				"The file upload failed SHA-256 verification.",
			)
		case errors.Is(err, ErrFileTransferInvalid),
			errors.Is(err, ErrFileTransferIncomplete):
			response.Error = NewError(
				"file_upload_invalid",
				"The file upload is invalid.",
			)
		case errors.Is(err, context.Canceled):
			response.Error = NewError(
				"request_cancelled",
				"The request was cancelled.",
			)
		default:
			if s.Errors != nil {
				fmt.Fprintf(s.Errors, "sidecar: file upload: %v\n", err)
			}
			response.Error = NewError(
				"file_upload_failed",
				"The Sidecar could not store the file upload.",
			)
		}
		return response
	}
	response.Result = reference
	return response
}

type serverRequest struct {
	Context context.Context
	Request Request
	cancel  context.CancelFunc
	flow    *streamFlow
}

type serverRequestRegistry struct {
	mu       sync.Mutex
	requests map[string]*serverRequest
}

func newServerRequestRegistry() *serverRequestRegistry {
	return &serverRequestRegistry{requests: make(map[string]*serverRequest)}
}

func (r *serverRequestRegistry) Register(
	parent context.Context,
	request Request,
) (*serverRequest, bool) {
	requestCtx, cancel := context.WithCancel(parent)
	registered := &serverRequest{
		Context: requestCtx,
		Request: request,
		cancel:  cancel,
	}
	if requestsStream(request) {
		registered.flow = newStreamFlow(parseStreamWindow(request.Meta[streamWindowMeta]))
	}
	if request.ID == "" {
		return registered, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.requests[request.ID]; exists {
		cancel()
		return nil, true
	}
	r.requests[request.ID] = registered
	return registered, false
}

func (r *serverRequestRegistry) Acknowledge(
	id string,
	token string,
	params json.RawMessage,
) {
	if id == "" || token == "" {
		return
	}
	var acknowledgement struct {
		Sequence int64 `json:"sequence"`
	}
	if err := json.Unmarshal(params, &acknowledgement); err != nil {
		return
	}

	r.mu.Lock()
	request := r.requests[id]
	if request == nil ||
		request.flow == nil ||
		request.Request.Meta["token"] != token {
		r.mu.Unlock()
		return
	}
	flow := request.flow
	r.mu.Unlock()
	flow.Acknowledge(acknowledgement.Sequence)
}

func (r *serverRequestRegistry) Cancel(id, token string) {
	if id == "" || token == "" {
		return
	}
	r.mu.Lock()
	request := r.requests[id]
	if request == nil || request.Request.Meta["token"] != token {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	request.cancel()
}

func (r *serverRequestRegistry) Complete(request *serverRequest) {
	if request == nil {
		return
	}
	if request.Request.ID != "" {
		r.mu.Lock()
		if r.requests[request.Request.ID] == request {
			delete(r.requests, request.Request.ID)
		}
		r.mu.Unlock()
	}
	request.cancel()
}

func (r *serverRequestRegistry) CancelStreams() {
	r.mu.Lock()
	var cancels []context.CancelFunc
	for _, request := range r.requests {
		if request.flow != nil {
			cancels = append(cancels, request.cancel)
		}
	}
	r.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

type streamFlow struct {
	credits chan struct{}
	mu      sync.Mutex
	sent    int64
	acked   int64
}

func newStreamFlow(window int) *streamFlow {
	flow := &streamFlow{credits: make(chan struct{}, window)}
	for range window {
		flow.credits <- struct{}{}
	}
	return flow
}

func (f *streamFlow) Acquire(ctx context.Context, sequence int64) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.credits:
	}
	f.mu.Lock()
	if sequence > f.sent {
		f.sent = sequence
	}
	f.mu.Unlock()
	return nil
}

func (f *streamFlow) Acknowledge(sequence int64) {
	f.mu.Lock()
	if sequence <= f.acked || sequence > f.sent {
		f.mu.Unlock()
		return
	}
	release := sequence - f.acked
	f.acked = sequence
	f.mu.Unlock()

	for range release {
		select {
		case f.credits <- struct{}{}:
		default:
			return
		}
	}
}

func parseStreamWindow(value string) int {
	if value == "" {
		return defaultStreamWindow
	}
	window, err := strconv.Atoi(value)
	if err != nil ||
		window < 1 ||
		window > maxStreamWindow {
		return defaultStreamWindow
	}
	return window
}

type serverResponseEncoder struct {
	mu      sync.Mutex
	encoder *json.Encoder
	cancel  context.CancelFunc
	err     error
}

func newServerResponseEncoder(
	output io.Writer,
	cancel context.CancelFunc,
) *serverResponseEncoder {
	return &serverResponseEncoder{
		encoder: json.NewEncoder(output),
		cancel:  cancel,
	}
}

func (e *serverResponseEncoder) Encode(response Response) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	if err := e.encoder.Encode(response); err != nil {
		e.err = err
		e.cancel()
	}
	return e.err
}

func (e *serverResponseEncoder) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}
