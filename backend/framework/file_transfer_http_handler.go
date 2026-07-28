package framework

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

const fileTransferTokenHeader = "X-Bridra-Token"

type FileTransferHTTPHandler struct {
	Store         *FileTransferStore
	AllowedOrigin string
	Token         string
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

	trimmedPath := strings.TrimSuffix(request.URL.Path, "/")
	id := path.Base(trimmedPath)
	if id == "files" {
		handler.serveUploadCreation(writer, request)
		return
	}
	switch request.Method {
	case http.MethodGet:
		handler.serveDownload(writer, request, id)
	case http.MethodHead:
		handler.serveUploadStatus(writer, id)
	case http.MethodPatch:
		handler.serveUploadAppend(writer, request, id)
	default:
		writer.Header().Set("Allow", "GET, HEAD, PATCH, OPTIONS")
		handler.writeError(writer, http.StatusMethodNotAllowed, "Use GET for downloads or HEAD/PATCH for uploads.")
	}
}

func (handler *FileTransferHTTPHandler) serveUploadCreation(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", "POST, OPTIONS")
		handler.writeError(writer, http.StatusMethodNotAllowed, "Use POST to create a file upload.")
		return
	}
	if !handler.authenticated(request) {
		handler.writeError(writer, http.StatusUnauthorized, "The upload token is invalid.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		handler.writeError(writer, http.StatusUnsupportedMediaType, "Content-Type must be application/json.")
		return
	}
	body := http.MaxBytesReader(writer, request.Body, 16*1024)
	defer body.Close()
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	var metadata struct {
		Name      string `json:"name"`
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
		SHA256    string `json:"sha256"`
	}
	if err := decoder.Decode(&metadata); err != nil {
		handler.writeError(writer, http.StatusBadRequest, "The upload metadata is invalid.")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		handler.writeError(writer, http.StatusBadRequest, "The upload metadata must contain one JSON object.")
		return
	}
	status, err := handler.Store.BeginUpload(
		metadata.Name,
		metadata.MediaType,
		metadata.Size,
		metadata.SHA256,
	)
	if err != nil {
		handler.writeTransferError(writer, err, FileUploadStatus{})
		return
	}
	handler.writeUploadStatus(writer, http.StatusCreated, status)
}

func (handler *FileTransferHTTPHandler) serveUploadStatus(
	writer http.ResponseWriter,
	id string,
) {
	status, err := handler.Store.UploadStatus(id)
	if err != nil {
		handler.writeTransferError(writer, err, status)
		return
	}
	writeUploadHeaders(writer, status)
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *FileTransferHTTPHandler) serveUploadAppend(
	writer http.ResponseWriter,
	request *http.Request,
	id string,
) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/offset+octet-stream" {
		handler.writeError(
			writer,
			http.StatusUnsupportedMediaType,
			"Content-Type must be application/offset+octet-stream.",
		)
		return
	}
	offset, err := strconv.ParseInt(request.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || offset < 0 {
		handler.writeError(writer, http.StatusBadRequest, "Upload-Offset must be a non-negative integer.")
		return
	}
	status, err := handler.Store.AppendUpload(request.Context(), id, offset, request.Body)
	if err != nil {
		if request.Context().Err() == nil {
			handler.writeTransferError(writer, err, status)
		}
		return
	}
	handler.writeUploadStatus(writer, http.StatusOK, status)
}

func (handler *FileTransferHTTPHandler) serveDownload(
	writer http.ResponseWriter,
	request *http.Request,
	id string,
) {
	offset, ranged, err := parseFileRange(request.Header.Get("Range"))
	if err != nil {
		handler.writeError(writer, http.StatusRequestedRangeNotSatisfiable, "The requested file range is invalid.")
		return
	}
	download, err := handler.Store.OpenDownload(id, offset)
	if err != nil {
		if errors.Is(err, ErrFileTransferOffset) {
			handler.writeError(writer, http.StatusRequestedRangeNotSatisfiable, "The requested file range is invalid.")
			return
		}
		handler.writeDownloadError(writer, err)
		return
	}
	defer func() {
		if err := download.Close(); err != nil {
			handler.logf("http file transfer: close: %v\n", err)
		}
	}()
	if ranged && offset >= download.Reference.Size {
		writer.Header().Set(
			"Content-Range",
			fmt.Sprintf("bytes */%d", download.Reference.Size),
		)
		handler.writeError(writer, http.StatusRequestedRangeNotSatisfiable, "The requested file range is invalid.")
		return
	}

	disposition := mime.FormatMediaType(
		"attachment",
		map[string]string{"filename": download.Reference.Name},
	)
	writer.Header().Set("Accept-Ranges", "bytes")
	writer.Header().Set("Content-Disposition", disposition)
	writer.Header().Set("Content-Length", fmt.Sprint(download.Reference.Size-offset))
	writer.Header().Set("Content-Type", download.Reference.MediaType)
	writer.Header().Set("ETag", `"`+download.Reference.SHA256+`"`)
	responseStatus := http.StatusOK
	if ranged {
		responseStatus = http.StatusPartialContent
		writer.Header().Set(
			"Content-Range",
			fmt.Sprintf(
				"bytes %d-%d/%d",
				offset,
				download.Reference.Size-1,
				download.Reference.Size,
			),
		)
	}
	writer.WriteHeader(responseStatus)
	if _, err := io.Copy(writer, download); err != nil {
		if request.Context().Err() == nil {
			handler.logf("http file transfer: stream: %v\n", err)
		}
		return
	}
	if err := download.Commit(); err != nil {
		handler.logf("http file transfer: commit: %v\n", err)
	}
}

func (handler *FileTransferHTTPHandler) authenticated(request *http.Request) bool {
	if handler.Token == "" {
		return true
	}
	provided := request.Header.Get(fileTransferTokenHeader)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(handler.Token)) == 1
}

func (handler *FileTransferHTTPHandler) writeDownloadError(
	writer http.ResponseWriter,
	err error,
) {
	switch {
	case errors.Is(err, ErrFileTransferNotFound),
		errors.Is(err, ErrFileTransferExpired):
		handler.writeError(writer, http.StatusNotFound, "The file is unavailable or has expired.")
	case errors.Is(err, ErrFileTransferBusy):
		handler.writeError(writer, http.StatusConflict, "The file is already being transferred.")
	default:
		handler.logf("http file transfer: open: %v\n", err)
		handler.writeError(writer, http.StatusInternalServerError, "The file could not be opened.")
	}
}

func (handler *FileTransferHTTPHandler) writeTransferError(
	writer http.ResponseWriter,
	err error,
	status FileUploadStatus,
) {
	if status.Reference.ID != "" {
		writeUploadHeaders(writer, status)
	}
	switch {
	case errors.Is(err, ErrFileTransferNotFound),
		errors.Is(err, ErrFileTransferExpired):
		handler.writeError(writer, http.StatusNotFound, "The upload is unavailable or has expired.")
	case errors.Is(err, ErrFileTransferOffset),
		errors.Is(err, ErrFileTransferBusy),
		errors.Is(err, ErrFileTransferIncomplete):
		handler.writeError(writer, http.StatusConflict, "The upload offset or state does not match.")
	case errors.Is(err, ErrFileTransferTooLarge):
		handler.writeError(writer, http.StatusRequestEntityTooLarge, "The upload exceeds its declared or configured size.")
	case errors.Is(err, ErrFileTransferChecksum):
		handler.writeError(writer, http.StatusUnprocessableEntity, "The upload failed SHA-256 verification.")
	case errors.Is(err, ErrFileTransferInvalid):
		handler.writeError(writer, http.StatusBadRequest, "The upload is invalid.")
	default:
		handler.logf("http file transfer: upload: %v\n", err)
		handler.writeError(writer, http.StatusInternalServerError, "The upload could not be stored.")
	}
}

func (handler *FileTransferHTTPHandler) writeUploadStatus(
	writer http.ResponseWriter,
	code int,
	status FileUploadStatus,
) {
	writeUploadHeaders(writer, status)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(code)
	if err := json.NewEncoder(writer).Encode(status); err != nil {
		handler.logf("http file transfer: encode upload status: %v\n", err)
	}
}

func writeUploadHeaders(writer http.ResponseWriter, status FileUploadStatus) {
	writer.Header().Set("Upload-Offset", strconv.FormatInt(status.Offset, 10))
	writer.Header().Set("Upload-Length", strconv.FormatInt(status.Reference.Size, 10))
	writer.Header().Set(
		"Upload-Expires-At",
		status.Reference.ExpiresAt.UTC().Format(time.RFC3339Nano),
	)
	if status.Complete {
		writer.Header().Set("Upload-Complete", "true")
	} else {
		writer.Header().Set("Upload-Complete", "false")
	}
}

func parseFileRange(value string) (int64, bool, error) {
	if value == "" {
		return 0, false, nil
	}
	if !strings.HasPrefix(value, "bytes=") ||
		strings.Contains(value, ",") ||
		!strings.HasSuffix(value, "-") {
		return 0, false, ErrFileTransferOffset
	}
	offset, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(value, "bytes="), "-"), 10, 64)
	if err != nil || offset < 0 {
		return 0, false, ErrFileTransferOffset
	}
	return offset, true, nil
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
	writer.Header().Set(
		"Access-Control-Allow-Headers",
		"Content-Type, Range, Upload-Offset, X-Bridra-Token",
	)
	writer.Header().Set(
		"Access-Control-Allow-Methods",
		"GET, HEAD, POST, PATCH, OPTIONS",
	)
	writer.Header().Set(
		"Access-Control-Expose-Headers",
		"Accept-Ranges, Content-Range, Upload-Complete, Upload-Expires-At, Upload-Length, Upload-Offset",
	)
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
