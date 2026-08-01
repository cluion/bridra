package framework

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type HTTPHandler struct {
	Router        *Router
	Authenticator Authenticator
	RateLimiter   RateLimiter
	RateLimitKey  HTTPRateLimitKeyFunc
	AllowedOrigin string
	Errors        io.Writer
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Router == nil {
		h.writeError(w, http.StatusInternalServerError, "configuration_error", "The HTTP backend is not configured.")
		return
	}
	if !h.allowOrigin(w, r) {
		h.writeError(w, http.StatusForbidden, "origin_not_allowed", "The request origin is not allowed.")
		return
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for RPC requests.")
		return
	}
	if h.Authenticator != nil || h.RateLimiter != nil {
		w.Header().Set("Cache-Control", "no-store")
	}
	if h.Authenticator != nil {
		if authenticatorIsNil(h.Authenticator) {
			h.writeError(w, http.StatusInternalServerError, "configuration_error", "The HTTP authenticator is not configured.")
			return
		}
		credential, valid := bearerCredential(r.Header.Values("Authorization"))
		if !valid {
			h.writeUnauthenticated(w)
			return
		}
		principal, err := h.Authenticator.Authenticate(r.Context(), credential)
		if err != nil {
			if errors.Is(err, ErrAuthenticationFailed) {
				h.writeUnauthenticated(w)
				return
			}
			h.logf("http backend: authenticate request: %v\n", err)
			h.writeError(w, http.StatusServiceUnavailable, "authentication_unavailable", "Authentication is temporarily unavailable.")
			return
		}
		if !principal.valid() {
			h.logf("http backend: authenticator returned an invalid principal\n")
			h.writeError(w, http.StatusInternalServerError, "authentication_error", "Authentication could not complete.")
			return
		}
		r = r.WithContext(ContextWithPrincipal(r.Context(), principal))
	}
	if h.RateLimiter != nil {
		if rateLimiterIsNil(h.RateLimiter) {
			h.writeError(w, http.StatusInternalServerError, "configuration_error", "The HTTP rate limiter is not configured.")
			return
		}
		principal, _ := PrincipalFromContext(r.Context())
		keyFunc := h.RateLimitKey
		if keyFunc == nil {
			keyFunc = DefaultHTTPRateLimitKey
		}
		key, err := keyFunc(r, principal)
		key = strings.TrimSpace(key)
		if err != nil || key == "" || len(key) > maxRateLimitKeyBytes {
			h.logf("http backend: resolve rate limit key: %v\n", err)
			h.writeError(w, http.StatusInternalServerError, "rate_limit_error", "The request rate limit could not be evaluated.")
			return
		}
		decision, err := h.RateLimiter.Allow(r.Context(), key)
		if err != nil {
			h.logf("http backend: evaluate rate limit: %v\n", err)
			h.writeError(w, http.StatusServiceUnavailable, "rate_limit_unavailable", "Rate limiting is temporarily unavailable.")
			return
		}
		if !decision.Allowed {
			w.Header().Set("Retry-After", retryAfterSeconds(decision.RetryAfter))
			h.writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests were sent. Retry later.")
			return
		}
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		h.writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json.")
		return
	}

	body := http.MaxBytesReader(w, r.Body, MaxRequestBytes)
	defer body.Close()
	decoder := json.NewDecoder(body)
	var request Request
	if err := decoder.Decode(&request); err != nil {
		h.logf("http backend: invalid JSON request: %v\n", err)
		status := http.StatusBadRequest
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			status = http.StatusRequestEntityTooLarge
		}
		h.writeError(w, status, "invalid_json", "The request is not valid JSON.")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		h.writeError(w, http.StatusBadRequest, "invalid_json", "The request must contain one JSON object.")
		return
	}

	if request.Meta[streamRequestMeta] == "1" {
		h.writeStream(w, r, request)
		return
	}
	h.writeJSON(w, http.StatusOK, h.Router.Dispatch(r.Context(), request))
}

func (h *HTTPHandler) writeStream(
	w http.ResponseWriter,
	r *http.Request,
	request Request,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.writeError(
			w,
			http.StatusInternalServerError,
			"streaming_not_supported",
			"The HTTP response writer does not support streaming.",
		)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(w)
	err := h.Router.DispatchStream(
		r.Context(),
		request,
		func(response Response) error {
			if err := encoder.Encode(response); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		},
	)
	if err != nil && r.Context().Err() == nil {
		h.logf("http backend: stream response: %v\n", err)
	}
}

func (h *HTTPHandler) allowOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	w.Header().Add("Vary", "Origin")
	if h.AllowedOrigin == "" {
		return false
	}
	if h.AllowedOrigin == "*" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else if origin == h.AllowedOrigin {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	} else {
		return false
	}
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Expose-Headers", "Retry-After, WWW-Authenticate")
	return true
}

func (h *HTTPHandler) writeUnauthenticated(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	h.writeError(w, http.StatusUnauthorized, "unauthenticated", "A valid Bearer token is required.")
}

func bearerCredential(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func authenticatorIsNil(authenticator Authenticator) bool {
	value := reflect.ValueOf(authenticator)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func rateLimiterIsNil(limiter RateLimiter) bool {
	value := reflect.ValueOf(limiter)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func retryAfterSeconds(retryAfter time.Duration) string {
	seconds := int64(retryAfter / time.Second)
	if retryAfter%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(seconds, 10)
}

func (h *HTTPHandler) writeError(w http.ResponseWriter, status int, code, message string) {
	h.writeJSON(w, status, Response{Error: NewError(code, message)})
}

func (h *HTTPHandler) writeJSON(w http.ResponseWriter, status int, response Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logf("http backend: encode response: %v\n", err)
	}
}

func (h *HTTPHandler) logf(format string, args ...any) {
	if h.Errors != nil {
		fmt.Fprintf(h.Errors, format, args...)
	}
}
