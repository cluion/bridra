package framework

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
)

type FileTransferHTTPHandler struct {
	Store         *FileTransferStore
	AllowedOrigin string
	Errors        io.Writer
}

func (handler *FileTransferHTTPHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if handler.Store == nil {
		handler.writeError(writer, http.StatusInternalServerError, "The file transfer store is not configured.")
		return
	}
	if !allowFileTransferOrigin(writer, request, handler.AllowedOrigin) {
		handler.writeError(writer, http.StatusForbidden, "The request origin is not allowed.")
		return
	}
	if request.Method == http.MethodOptions {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", "GET, OPTIONS")
		handler.writeError(writer, http.StatusMethodNotAllowed, "Use GET for file downloads.")
		return
	}

	id := path.Base(request.URL.Path)
	download, err := handler.Store.Take(id)
	if err != nil {
		if errors.Is(err, ErrFileTransferNotFound) ||
			errors.Is(err, ErrFileTransferExpired) {
			handler.writeError(writer, http.StatusNotFound, "The file is unavailable or has expired.")
			return
		}
		handler.logf("http file transfer: take: %v\n", err)
		handler.writeError(writer, http.StatusInternalServerError, "The file could not be opened.")
		return
	}
	defer func() {
		if err := download.Close(); err != nil {
			handler.logf("http file transfer: close: %v\n", err)
		}
	}()

	disposition := mime.FormatMediaType(
		"attachment",
		map[string]string{"filename": download.Reference.Name},
	)
	writer.Header().Set("Content-Disposition", disposition)
	writer.Header().Set("Content-Length", fmt.Sprint(download.Reference.Size))
	writer.Header().Set("Content-Type", download.Reference.MediaType)
	writer.WriteHeader(http.StatusOK)
	if _, err := io.Copy(writer, download); err != nil && request.Context().Err() == nil {
		handler.logf("http file transfer: stream: %v\n", err)
	}
}

func allowFileTransferOrigin(
	writer http.ResponseWriter,
	request *http.Request,
	allowedOrigin string,
) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	writer.Header().Add("Vary", "Origin")
	switch {
	case allowedOrigin == "*":
		writer.Header().Set("Access-Control-Allow-Origin", "*")
	case allowedOrigin == origin:
		writer.Header().Set("Access-Control-Allow-Origin", origin)
	default:
		return false
	}
	writer.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	return true
}

func (handler *FileTransferHTTPHandler) writeError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(map[string]string{"message": message}); err != nil {
		handler.logf("http file transfer: encode error: %v\n", err)
	}
}

func (handler *FileTransferHTTPHandler) logf(format string, arguments ...any) {
	if handler.Errors != nil {
		fmt.Fprintf(handler.Errors, format, arguments...)
	}
}
