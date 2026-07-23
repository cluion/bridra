package framework

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
)

type HTTPHandler struct {
	Router        *Router
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

	h.writeJSON(w, http.StatusOK, h.Router.Dispatch(r.Context(), request))
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
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	return true
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
