package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestBuildApplicationLoadsTokenFromEnvironment(t *testing.T) {
	t.Setenv("BRIDRA_BACKEND_TOKEN", "environment-token")

	application, err := buildApplication("", false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdownApplication(application); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	response := application.Router().Dispatch(context.Background(), framework.Request{
		ID: "1", Method: "system.health", Meta: map[string]string{"token": "environment-token"},
	})
	if response.Error != nil {
		t.Fatalf("response error: %v", response.Error)
	}
}

func TestBuildApplicationExplicitTokenOverridesEnvironment(t *testing.T) {
	t.Setenv("BRIDRA_BACKEND_TOKEN", "environment-token")

	application, err := buildApplication("runtime-token", true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdownApplication(application); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	response := application.Router().Dispatch(context.Background(), framework.Request{
		ID: "1", Method: "system.health", Meta: map[string]string{"token": "runtime-token"},
	})
	if response.Error != nil {
		t.Fatalf("response error: %v", response.Error)
	}
}

func TestBuildApplicationReportsMissingTokenAsConfigError(t *testing.T) {
	t.Setenv("BRIDRA_BACKEND_TOKEN", "")

	_, err := buildApplication("", false)
	var loadErrors *framework.ConfigLoadErrors
	if !errors.As(err, &loadErrors) {
		t.Fatalf("error = %v, want ConfigLoadErrors", err)
	}
}

func TestSmokeStreamEmitsOrderedProgressAndData(t *testing.T) {
	router := framework.NewRouter()
	registerSmokeStream(router)

	var responses []framework.Response
	err := router.DispatchStream(
		context.Background(),
		framework.Request{ID: "1", Method: smokeStreamMethod},
		func(response framework.Response) error {
			responses = append(responses, response)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("dispatch stream: %v", err)
	}
	if len(responses) != 6 {
		t.Fatalf("responses = %#v", responses)
	}
	if responses[0].Stream == nil || responses[0].Stream.Progress == nil ||
		responses[0].Stream.Progress.Completed != 0 ||
		responses[1].Stream == nil || responses[1].Result == nil ||
		responses[4].Stream == nil || responses[4].Stream.Progress == nil ||
		responses[4].Stream.Progress.Completed != 2 ||
		responses[5].Stream == nil || responses[5].Stream.Kind != "complete" {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestSmokeStreamUsesApplicationAuthentication(t *testing.T) {
	application, err := buildApplication("secret", true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdownApplication(application); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	registerSmokeStream(application.Router())

	var responses []framework.Response
	err = application.Router().DispatchStream(
		context.Background(),
		framework.Request{
			ID:     "1",
			Method: smokeStreamMethod,
			Meta:   map[string]string{"token": "wrong"},
		},
		func(response framework.Response) error {
			responses = append(responses, response)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("dispatch stream: %v", err)
	}
	if len(responses) != 1 || responses[0].Error == nil ||
		responses[0].Error.Code != "unauthorized" ||
		responses[0].Stream == nil || responses[0].Stream.Kind != "complete" {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestSmokeDownloadStagesKnownManagedFile(t *testing.T) {
	store, err := framework.NewFileTransferStore(
		framework.DefaultFileTransferOptions(),
	)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	router := framework.NewRouter()
	registerSmokeDownload(router, store)

	response := router.Dispatch(context.Background(), framework.Request{
		ID: "1", Method: smokeDownloadMethod,
	})
	if response.Error != nil {
		t.Fatalf("response error: %v", response.Error)
	}
	reference, ok := response.Result.(framework.FileReference)
	if !ok {
		t.Fatalf("result = %#v", response.Result)
	}
	if reference.Name != smokeDownloadName ||
		reference.MediaType != smokeDownloadMediaType ||
		reference.Size != 61440 ||
		reference.SHA256 != "1d07674b0ad481aad8ca8bfb35963b10d765add489fc10c8e4645958023da950" ||
		reference.LocalPath != "" {
		t.Fatalf("reference = %#v", reference)
	}

	download, err := store.Take(reference.ID)
	if err != nil {
		t.Fatalf("take download: %v", err)
	}
	content, readErr := io.ReadAll(download)
	closeErr := download.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatalf("read download: %v", err)
	}
	want := strings.Repeat(smokeDownloadBlock, smokeDownloadBlockCount)
	if string(content) != want {
		t.Fatalf("downloaded %d unexpected bytes", len(content))
	}
}

func TestSmokeDownloadUsesApplicationAuthentication(t *testing.T) {
	application, err := buildApplication("secret", true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdownApplication(application); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	store, err := framework.Resolve(
		application.Container(),
		framework.FileTransferStoreKey,
	)
	if err != nil {
		t.Fatalf("resolve store: %v", err)
	}
	registerSmokeDownload(application.Router(), store)

	response := application.Router().Dispatch(
		context.Background(),
		framework.Request{
			ID:     "1",
			Method: smokeDownloadMethod,
			Meta:   map[string]string{"token": "wrong"},
		},
	)
	if response.Error == nil || response.Error.Code != "unauthorized" {
		t.Fatalf("response = %#v", response)
	}
}

func TestSmokeDownloadResumeHandlerInterruptsAndContinuesWithRange(t *testing.T) {
	store, err := framework.NewFileTransferStore(
		framework.DefaultFileTransferOptions(),
	)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	content := []byte(strings.Repeat(smokeDownloadBlock, smokeDownloadBlockCount))
	reference, err := store.Stage(
		context.Background(),
		smokeDownloadName,
		smokeDownloadMediaType,
		bytes.NewReader(content),
	)
	if err != nil {
		t.Fatalf("stage download: %v", err)
	}
	var logs bytes.Buffer
	handler := &smokeDownloadResumeHandler{
		handler: &framework.FileTransferHTTPHandler{
			Store:  store,
			Errors: &logs,
		},
		errorOutput: &logs,
	}

	download := func(byteRange string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(
			http.MethodGet,
			"/rpc/files/"+reference.ID,
			nil,
		)
		if byteRange != "" {
			request.Header.Set("Range", byteRange)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	first := download("")
	if first.Code != http.StatusOK ||
		first.Body.Len() != int(smokeDownloadInterruptAt) {
		t.Fatalf("interrupted response = %d, %d bytes", first.Code, first.Body.Len())
	}
	second := download(fmt.Sprintf("bytes=%d-", smokeDownloadInterruptAt))
	if second.Code != http.StatusPartialContent ||
		second.Body.Len() != len(content)-int(smokeDownloadInterruptAt) {
		t.Fatalf("resumed response = %d, %d bytes", second.Code, second.Body.Len())
	}
	combined := append(first.Body.Bytes(), second.Body.Bytes()...)
	if !bytes.Equal(combined, content) {
		t.Fatalf("combined download = %d unexpected bytes", len(combined))
	}
	if _, err := store.OpenDownload(reference.ID, 0); !errors.Is(
		err,
		framework.ErrFileTransferNotFound,
	) {
		t.Fatalf("consumed download error = %v", err)
	}
	if !strings.Contains(logs.String(), "smoke download interrupted at offset 32768") ||
		!strings.Contains(logs.String(), "smoke download resumed at offset 32768") {
		t.Fatalf("logs = %s", logs.String())
	}
}

func TestSmokeUploadResumeHandlerInterruptsAndContinuesAtStoredOffset(t *testing.T) {
	store, err := framework.NewFileTransferStore(
		framework.DefaultFileTransferOptions(),
	)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	content := []byte(strings.Repeat("bridra-managed-upload-smoke|", 4096))
	sum := sha256.Sum256(content)
	status, err := store.BeginUpload(
		"bridra-upload-smoke.bin",
		"application/octet-stream",
		int64(len(content)),
		hex.EncodeToString(sum[:]),
	)
	if err != nil {
		t.Fatalf("begin upload: %v", err)
	}
	var logs bytes.Buffer
	handler := &smokeUploadResumeHandler{
		handler: &framework.FileTransferHTTPHandler{
			Store:  store,
			Errors: &logs,
		},
		errorOutput: &logs,
	}

	appendUpload := func(offset int64, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(
			http.MethodPatch,
			"/rpc/files/"+status.Reference.ID,
			bytes.NewReader(body),
		)
		request.Header.Set("Content-Type", "application/offset+octet-stream")
		request.Header.Set("Upload-Offset", fmt.Sprint(offset))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	first := appendUpload(0, content)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("interrupted response = %d, body %s", first.Code, first.Body.String())
	}
	status, err = store.UploadStatus(status.Reference.ID)
	if err != nil {
		t.Fatalf("upload status: %v", err)
	}
	if status.Offset != smokeUploadInterruptAt || status.Complete {
		t.Fatalf("interrupted status = %#v", status)
	}

	resumed := appendUpload(status.Offset, content[status.Offset:])
	if resumed.Code != http.StatusOK {
		t.Fatalf("resumed response = %d, body %s", resumed.Code, resumed.Body.String())
	}
	status, err = store.UploadStatus(status.Reference.ID)
	if err != nil {
		t.Fatalf("completed upload status: %v", err)
	}
	if status.Offset != int64(len(content)) || !status.Complete {
		t.Fatalf("completed status = %#v", status)
	}
	if !strings.Contains(logs.String(), "smoke upload interrupted at offset 32768") ||
		!strings.Contains(logs.String(), "smoke upload resumed at offset 32768") {
		t.Fatalf("logs = %s", logs.String())
	}
}

func TestSmokeUploadVerificationConsumesAndHashesManagedUpload(t *testing.T) {
	store, err := framework.NewFileTransferStore(
		framework.DefaultFileTransferOptions(),
	)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	content := []byte("verified managed upload")
	sum := sha256.Sum256(content)
	status, err := store.BeginUpload(
		"upload.bin",
		"application/octet-stream",
		int64(len(content)),
		hex.EncodeToString(sum[:]),
	)
	if err != nil {
		t.Fatalf("begin upload: %v", err)
	}
	status, err = store.AppendUpload(
		context.Background(),
		status.Reference.ID,
		0,
		bytes.NewReader(content),
	)
	if err != nil {
		t.Fatalf("append upload: %v", err)
	}
	params, err := json.Marshal(map[string]any{"file": status.Reference})
	if err != nil {
		t.Fatalf("encode params: %v", err)
	}
	router := framework.NewRouter()
	registerSmokeUploadVerification(router, store)
	response := router.Dispatch(context.Background(), framework.Request{
		ID: "1", Method: smokeUploadVerifyMethod, Params: params,
	})
	if response.Error != nil {
		t.Fatalf("response error: %v", response.Error)
	}
	result, ok := response.Result.(map[string]any)
	if !ok || result["name"] != status.Reference.Name ||
		result["size"] != int64(len(content)) ||
		result["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("result = %#v", response.Result)
	}
	if _, err := store.ConsumeUpload(status.Reference); err == nil {
		t.Fatal("verified upload was not consumed")
	}
}
