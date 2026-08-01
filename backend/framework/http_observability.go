package framework

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"time"
)

const maxHTTPObservationValueBytes = 256

var ErrInvalidHTTPObserver = errors.New("framework: HTTP observer is invalid")

type HTTPOutcome string

const (
	HTTPOutcomeSuccess     HTTPOutcome = "success"
	HTTPOutcomeRPCError    HTTPOutcome = "rpc_error"
	HTTPOutcomeClientError HTTPOutcome = "client_error"
	HTTPOutcomeServerError HTTPOutcome = "server_error"
	HTTPOutcomeCanceled    HTTPOutcome = "canceled"
)

type HTTPRequestStart struct {
	RequestID  string
	HTTPMethod string
	ClientIP   string
}

type HTTPRequestObservation struct {
	HTTPRequestStart
	Surface       string
	RPCMethod     string
	Principal     string
	StatusCode    int
	ErrorCode     string
	ResponseBytes int64
	Duration      time.Duration
	Outcome       HTTPOutcome
}

type HTTPObserver interface {
	BeginHTTP(context.Context, HTTPRequestStart) context.Context
	EndHTTP(context.Context, HTTPRequestObservation)
}

type HTTPObserverFuncs struct {
	BeginFunc func(context.Context, HTTPRequestStart) context.Context
	EndFunc   func(context.Context, HTTPRequestObservation)
}

func (observer HTTPObserverFuncs) BeginHTTP(
	ctx context.Context,
	request HTTPRequestStart,
) context.Context {
	if observer.BeginFunc == nil {
		return ctx
	}
	return observer.BeginFunc(ctx, request)
}

func (observer HTTPObserverFuncs) EndHTTP(
	ctx context.Context,
	observation HTTPRequestObservation,
) {
	if observer.EndFunc != nil {
		observer.EndFunc(ctx, observation)
	}
}

type httpObserverGroup []HTTPObserver

func NewHTTPObserverGroup(observers ...HTTPObserver) HTTPObserver {
	group := make(httpObserverGroup, 0, len(observers))
	for _, observer := range observers {
		if observer != nil && !httpObserverIsNil(observer) {
			if nested, ok := observer.(httpObserverGroup); ok {
				group = append(group, nested...)
			} else {
				group = append(group, observer)
			}
		}
	}
	return group
}

func (group httpObserverGroup) BeginHTTP(
	ctx context.Context,
	request HTTPRequestStart,
) context.Context {
	for _, observer := range group {
		if next := observer.BeginHTTP(ctx, request); next != nil {
			ctx = next
		}
	}
	return ctx
}

func (group httpObserverGroup) EndHTTP(
	ctx context.Context,
	observation HTTPRequestObservation,
) {
	for index := len(group) - 1; index >= 0; index-- {
		group[index].EndHTTP(ctx, observation)
	}
}

type HTTPObservationHandler struct {
	Handler  http.Handler
	Observer HTTPObserver
	Errors   io.Writer

	now       func() time.Time
	requestID func() string
}

func (handler *HTTPObservationHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if handler.Handler == nil {
		http.Error(writer, "The HTTP handler is not configured.", http.StatusInternalServerError)
		return
	}
	startedAt := handler.currentTime()
	requestID := handler.newRequestID()
	writer.Header().Set("X-Request-ID", requestID)

	state := &httpObservationState{}
	ctx := context.WithValue(request.Context(), httpRequestIDContextKey{}, requestID)
	ctx = context.WithValue(ctx, httpObservationContextKey{}, state)
	start := HTTPRequestStart{
		RequestID:  requestID,
		HTTPMethod: boundedHTTPObservationValue(request.Method),
		ClientIP:   directHTTPClientIP(request.RemoteAddr),
	}
	ctx = handler.beginObservation(ctx, start)
	ctx = context.WithValue(ctx, httpRequestIDContextKey{}, requestID)
	ctx = context.WithValue(ctx, httpObservationContextKey{}, state)
	request = request.WithContext(ctx)

	recorder := &httpObservationResponseWriter{ResponseWriter: writer, state: state}
	var observedWriter http.ResponseWriter = recorder
	if flusher, ok := writer.(http.Flusher); ok {
		observedWriter = &httpObservationFlushingResponseWriter{
			httpObservationResponseWriter: recorder,
			flusher:                       flusher,
		}
	}
	defer func() {
		finishedAt := handler.currentTime()
		duration := finishedAt.Sub(startedAt)
		if duration < 0 {
			duration = 0
		}
		status := recorder.statusCode
		if status == 0 && request.Context().Err() == nil {
			status = http.StatusOK
		}
		observation := HTTPRequestObservation{
			HTTPRequestStart: start,
			Surface:          state.surface,
			RPCMethod:        state.rpcMethod,
			Principal:        state.principal,
			StatusCode:       status,
			ErrorCode:        state.errorCode,
			ResponseBytes:    recorder.responseBytes,
			Duration:         duration,
		}
		observation.Outcome = httpObservationOutcome(observation, request.Context().Err())
		handler.endObservation(request.Context(), observation)
	}()
	handler.Handler.ServeHTTP(observedWriter, request)
}

func (handler *HTTPObservationHandler) currentTime() time.Time {
	if handler.now != nil {
		return handler.now()
	}
	return time.Now()
}

func (handler *HTTPObservationHandler) newRequestID() string {
	if handler.requestID != nil {
		if requestID := boundedHTTPObservationValue(handler.requestID()); requestID != "" {
			return requestID
		}
	}
	return newHTTPRequestID()
}

func (handler *HTTPObservationHandler) beginObservation(
	ctx context.Context,
	request HTTPRequestStart,
) context.Context {
	if handler.Observer == nil || httpObserverIsNil(handler.Observer) {
		return ctx
	}
	observers := httpObserverGroup{handler.Observer}
	if group, ok := handler.Observer.(httpObserverGroup); ok {
		observers = group
	}
	for _, observer := range observers {
		ctx = handler.beginObserver(ctx, request, observer)
	}
	return ctx
}

func (handler *HTTPObservationHandler) beginObserver(
	ctx context.Context,
	request HTTPRequestStart,
	observer HTTPObserver,
) (observed context.Context) {
	observed = ctx
	defer func() {
		if recovered := recover(); recovered != nil {
			handler.logf("http observer: begin: %v\n", recovered)
			observed = ctx
		}
	}()
	if next := observer.BeginHTTP(ctx, request); next != nil {
		observed = next
	}
	return observed
}

func (handler *HTTPObservationHandler) endObservation(
	ctx context.Context,
	observation HTTPRequestObservation,
) {
	if handler.Observer == nil || httpObserverIsNil(handler.Observer) {
		return
	}
	observers := httpObserverGroup{handler.Observer}
	if group, ok := handler.Observer.(httpObserverGroup); ok {
		observers = group
	}
	for index := len(observers) - 1; index >= 0; index-- {
		handler.endObserver(ctx, observation, observers[index])
	}
}

func (handler *HTTPObservationHandler) endObserver(
	ctx context.Context,
	observation HTTPRequestObservation,
	observer HTTPObserver,
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			handler.logf("http observer: end: %v\n", recovered)
		}
	}()
	observer.EndHTTP(ctx, observation)
}

func (handler *HTTPObservationHandler) logf(format string, arguments ...any) {
	if handler.Errors != nil {
		fmt.Fprintf(handler.Errors, format, arguments...)
	}
}

type httpObservationState struct {
	surface   string
	rpcMethod string
	principal string
	errorCode string
}

type httpRequestIDContextKey struct{}
type httpObservationContextKey struct{}

func HTTPRequestIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	requestID, exists := ctx.Value(httpRequestIDContextKey{}).(string)
	return requestID, exists && requestID != ""
}

func markHTTPObservationSurface(ctx context.Context, surface string) {
	if state := httpObservationStateFromContext(ctx); state != nil {
		state.surface = boundedHTTPObservationValue(surface)
	}
}

func markHTTPObservationRPCMethod(ctx context.Context, method string) {
	if state := httpObservationStateFromContext(ctx); state != nil {
		state.rpcMethod = boundedHTTPObservationValue(method)
	}
}

func markHTTPObservationPrincipal(ctx context.Context, principal string) {
	if state := httpObservationStateFromContext(ctx); state != nil {
		state.principal = boundedHTTPObservationValue(principal)
	}
}

func httpObservationStateFromContext(ctx context.Context) *httpObservationState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(httpObservationContextKey{}).(*httpObservationState)
	return state
}

type httpObservationWriter interface {
	setHTTPObservationError(string)
}

func markHTTPObservationError(writer http.ResponseWriter, code string) {
	if observed, ok := writer.(httpObservationWriter); ok {
		observed.setHTTPObservationError(code)
	}
}

type httpObservationResponseWriter struct {
	http.ResponseWriter
	state         *httpObservationState
	statusCode    int
	responseBytes int64
}

func (writer *httpObservationResponseWriter) WriteHeader(status int) {
	if writer.statusCode != 0 {
		return
	}
	writer.statusCode = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *httpObservationResponseWriter) Write(body []byte) (int, error) {
	if writer.statusCode == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	written, err := writer.ResponseWriter.Write(body)
	writer.responseBytes += int64(written)
	return written, err
}

func (writer *httpObservationResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *httpObservationResponseWriter) setHTTPObservationError(code string) {
	writer.state.errorCode = boundedHTTPObservationValue(code)
}

type httpObservationFlushingResponseWriter struct {
	*httpObservationResponseWriter
	flusher http.Flusher
}

func (writer *httpObservationFlushingResponseWriter) Flush() {
	if writer.statusCode == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	writer.flusher.Flush()
}

func NewJSONHTTPObserver(writer io.Writer) (HTTPObserver, error) {
	if writer == nil || httpObservationValueIsNil(writer) {
		return nil, ErrInvalidHTTPObserver
	}
	logger := slog.New(slog.NewJSONHandler(writer, nil))
	return HTTPObserverFuncs{
		EndFunc: func(ctx context.Context, observation HTTPRequestObservation) {
			level := slog.LevelInfo
			if observation.Outcome == HTTPOutcomeServerError {
				level = slog.LevelError
			} else if observation.Outcome != HTTPOutcomeSuccess {
				level = slog.LevelWarn
			}
			attributes := []slog.Attr{
				slog.String("event", "bridra_http_request"),
				slog.String("request_id", observation.RequestID),
				slog.String("http_method", observation.HTTPMethod),
				slog.String("client_ip", observation.ClientIP),
				slog.String("surface", observation.Surface),
				slog.String("rpc_method", observation.RPCMethod),
				slog.Int("status", observation.StatusCode),
				slog.String("error_code", observation.ErrorCode),
				slog.String("outcome", string(observation.Outcome)),
				slog.Int64("response_bytes", observation.ResponseBytes),
				slog.Int64("duration_micros", observation.Duration.Microseconds()),
			}
			if observation.Principal != "" {
				attributes = append(
					attributes,
					slog.String("principal_sha256", hashHTTPObservationValue(observation.Principal)),
				)
			}
			logger.LogAttrs(ctx, level, "HTTP request completed", attributes...)
		},
	}, nil
}

type HTTPMetricsSnapshot struct {
	Active        int64
	Total         uint64
	Success       uint64
	RPCErrors     uint64
	ClientErrors  uint64
	ServerErrors  uint64
	Canceled      uint64
	Unauthorized  uint64
	Forbidden     uint64
	RateLimited   uint64
	TotalDuration time.Duration
	MaxDuration   time.Duration
}

type HTTPMetrics struct {
	active        atomic.Int64
	total         atomic.Uint64
	success       atomic.Uint64
	rpcErrors     atomic.Uint64
	clientErrors  atomic.Uint64
	serverErrors  atomic.Uint64
	canceled      atomic.Uint64
	unauthorized  atomic.Uint64
	forbidden     atomic.Uint64
	rateLimited   atomic.Uint64
	totalDuration atomic.Uint64
	maxDuration   atomic.Uint64
}

func NewHTTPMetrics() *HTTPMetrics {
	return &HTTPMetrics{}
}

func (metrics *HTTPMetrics) BeginHTTP(
	ctx context.Context,
	_ HTTPRequestStart,
) context.Context {
	metrics.active.Add(1)
	return ctx
}

func (metrics *HTTPMetrics) EndHTTP(
	_ context.Context,
	observation HTTPRequestObservation,
) {
	metrics.active.Add(-1)
	metrics.total.Add(1)
	switch observation.Outcome {
	case HTTPOutcomeSuccess:
		metrics.success.Add(1)
	case HTTPOutcomeRPCError:
		metrics.rpcErrors.Add(1)
	case HTTPOutcomeClientError:
		metrics.clientErrors.Add(1)
	case HTTPOutcomeServerError:
		metrics.serverErrors.Add(1)
	case HTTPOutcomeCanceled:
		metrics.canceled.Add(1)
	}
	switch observation.ErrorCode {
	case "unauthenticated":
		metrics.unauthorized.Add(1)
	case "forbidden":
		metrics.forbidden.Add(1)
	case "rate_limited":
		metrics.rateLimited.Add(1)
	}
	duration := uint64(max(observation.Duration.Nanoseconds(), 0))
	metrics.totalDuration.Add(duration)
	for current := metrics.maxDuration.Load(); duration > current; current = metrics.maxDuration.Load() {
		if metrics.maxDuration.CompareAndSwap(current, duration) {
			break
		}
	}
}

func (metrics *HTTPMetrics) Snapshot() HTTPMetricsSnapshot {
	return HTTPMetricsSnapshot{
		Active:        metrics.active.Load(),
		Total:         metrics.total.Load(),
		Success:       metrics.success.Load(),
		RPCErrors:     metrics.rpcErrors.Load(),
		ClientErrors:  metrics.clientErrors.Load(),
		ServerErrors:  metrics.serverErrors.Load(),
		Canceled:      metrics.canceled.Load(),
		Unauthorized:  metrics.unauthorized.Load(),
		Forbidden:     metrics.forbidden.Load(),
		RateLimited:   metrics.rateLimited.Load(),
		TotalDuration: time.Duration(metrics.totalDuration.Load()),
		MaxDuration:   time.Duration(metrics.maxDuration.Load()),
	}
}

func httpObservationOutcome(
	observation HTTPRequestObservation,
	requestError error,
) HTTPOutcome {
	if requestError != nil {
		return HTTPOutcomeCanceled
	}
	if observation.StatusCode >= http.StatusInternalServerError || observation.StatusCode == 0 {
		return HTTPOutcomeServerError
	}
	if observation.StatusCode >= http.StatusBadRequest {
		return HTTPOutcomeClientError
	}
	if observation.ErrorCode != "" {
		return HTTPOutcomeRPCError
	}
	return HTTPOutcomeSuccess
}

func newHTTPRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	fallback := fmt.Sprintf("%d:%d", time.Now().UnixNano(), httpRequestIDFallback.Add(1))
	return hashHTTPObservationValue(fallback)[:32]
}

var httpRequestIDFallback atomic.Uint64

func directHTTPClientIP(remoteAddress string) string {
	remoteAddress = strings.TrimSpace(remoteAddress)
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		if net.ParseIP(remoteAddress) == nil {
			return ""
		}
		host = remoteAddress
	}
	return boundedHTTPObservationValue(strings.TrimSpace(host))
}

func boundedHTTPObservationValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxHTTPObservationValueBytes {
		return value
	}
	return value[:maxHTTPObservationValueBytes]
}

func hashHTTPObservationValue(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func httpObserverIsNil(observer HTTPObserver) bool {
	return httpObservationValueIsNil(observer)
}

func httpObservationValueIsNil(value any) bool {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
