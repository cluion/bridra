package mobilebridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestRuntimeDispatchesApplicationRPCWithoutHTTP(t *testing.T) {
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close(5000)
	})

	responseJSON, err := runtime.CallJSON(
		`{"id":"health-1","method":"system.health","meta":{"token":"embedded-test-token"}}`,
	)
	if err != nil {
		t.Fatalf("CallJSON() error = %v", err)
	}
	var response struct {
		ID     string `json:"id"`
		Result struct {
			Status  string `json:"status"`
			Runtime string `json:"runtime"`
		} `json:"result"`
		Error *framework.RPCError `json:"error"`
	}
	if err := json.Unmarshal([]byte(responseJSON), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "health-1" || response.Error != nil ||
		response.Result.Status != "ok" || response.Result.Runtime != embeddedRuntimeName {
		t.Fatalf("response = %#v", response)
	}
}

func TestRuntimeStreamsApplicationRPCWithoutHTTP(t *testing.T) {
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(5000) })

	stream, err := runtime.StreamJSON(
		`{"id":"health-stream","method":"system.health","meta":{"token":"embedded-test-token","stream":"1"}}`,
	)
	if err != nil {
		t.Fatalf("StreamJSON() error = %v", err)
	}
	var kinds []string
	for {
		encoded, nextErr := stream.NextJSON()
		if nextErr != nil {
			t.Fatalf("NextJSON() error = %v", nextErr)
		}
		if encoded == "" {
			break
		}
		var response framework.Response
		if err := json.Unmarshal([]byte(encoded), &response); err != nil {
			t.Fatalf("decode frame: %v", err)
		}
		if response.Stream == nil {
			t.Fatalf("response = %#v", response)
		}
		kinds = append(kinds, response.Stream.Kind)
	}
	if len(kinds) != 2 || kinds[0] != "data" || kinds[1] != "complete" {
		t.Fatalf("stream kinds = %#v", kinds)
	}
}

func TestRuntimePreservesRPCAuthentication(t *testing.T) {
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close(5000)
	})

	responseJSON, err := runtime.CallJSON(
		`{"id":"health-1","method":"system.health","meta":{"token":"wrong"}}`,
	)
	if err != nil {
		t.Fatalf("CallJSON() error = %v", err)
	}
	var response framework.Response
	if err := json.Unmarshal([]byte(responseJSON), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error == nil || response.Error.Code != "unauthorized" {
		t.Fatalf("response = %#v", response)
	}
}

func TestRuntimeValidatesCloseTimeout(t *testing.T) {
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Close(0); !errors.Is(err, ErrInvalidCloseTimeout) {
		t.Fatalf("Close() error = %v", err)
	}
	if err := runtime.Close(5000); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRuntimeCancelRejectsUnknownRequest(t *testing.T) {
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if runtime.Cancel("") || runtime.Cancel("missing") {
		t.Fatal("Cancel() accepted an empty or unknown request id")
	}
	if err := runtime.Close(5000); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	var nilRuntime *Runtime
	if nilRuntime.Cancel("request-1") {
		t.Fatal("nil Runtime.Cancel() returned true")
	}
}

func TestRuntimeDownloadsWithResumeAndConsumesOnlyOnCommit(t *testing.T) {
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(5000) })
	content := []byte("bounded embedded file download")
	reference, err := runtime.transfers.Stage(
		context.Background(), "result.txt", "text/plain", bytes.NewReader(content),
	)
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}

	first, err := runtime.OpenDownload(reference.ID, 0)
	if err != nil {
		t.Fatalf("OpenDownload() error = %v", err)
	}
	chunk, err := first.NextChunk(8)
	if err != nil || !bytes.Equal(chunk, content[:8]) {
		t.Fatalf("NextChunk() = %q, %v", chunk, err)
	}
	if err := first.Close(false); err != nil {
		t.Fatalf("Close(false) error = %v", err)
	}

	resumed, err := runtime.OpenDownload(reference.ID, int64(len(chunk)))
	if err != nil {
		t.Fatalf("resume OpenDownload() error = %v", err)
	}
	rest, err := io.ReadAll(downloadReader{download: resumed, maxBytes: 7})
	if err != nil || !bytes.Equal(append(chunk, rest...), content) {
		t.Fatalf("resumed download = %q, %v", append(chunk, rest...), err)
	}
	if err := resumed.Close(true); err != nil {
		t.Fatalf("Close(true) error = %v", err)
	}
	if _, err := runtime.OpenDownload(reference.ID, 0); !errors.Is(err, framework.ErrFileTransferNotFound) {
		t.Fatalf("consumed OpenDownload() error = %v", err)
	}
}

func TestRuntimeUploadsBoundedChunksWithStatusResume(t *testing.T) {
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(5000) })
	content := []byte("bounded embedded file upload")
	checksum := sha256.Sum256(content)
	createdJSON, err := runtime.BeginUploadJSON(
		"input.txt", "text/plain", int64(len(content)), formatChecksum(checksum),
	)
	if err != nil {
		t.Fatalf("BeginUploadJSON() error = %v", err)
	}
	created := decodeUploadStatus(t, createdJSON)
	if created.Offset != 0 || created.Complete {
		t.Fatalf("created status = %#v", created)
	}

	partialJSON, err := runtime.AppendUploadJSON(created.Reference.ID, 0, content[:9])
	if err != nil {
		t.Fatalf("AppendUploadJSON(partial) error = %v", err)
	}
	partial := decodeUploadStatus(t, partialJSON)
	if partial.Offset != 9 || partial.Complete {
		t.Fatalf("partial status = %#v", partial)
	}
	statusJSON, err := runtime.UploadStatusJSON(created.Reference.ID)
	if err != nil {
		t.Fatalf("UploadStatusJSON() error = %v", err)
	}
	if status := decodeUploadStatus(t, statusJSON); status.Offset != 9 {
		t.Fatalf("resumed status = %#v", status)
	}
	completeJSON, err := runtime.AppendUploadJSON(created.Reference.ID, 9, content[9:])
	if err != nil {
		t.Fatalf("AppendUploadJSON(complete) error = %v", err)
	}
	complete := decodeUploadStatus(t, completeJSON)
	if !complete.Complete || complete.Offset != int64(len(content)) {
		t.Fatalf("complete status = %#v", complete)
	}
	upload, err := runtime.transfers.ConsumeUpload(complete.Reference)
	if err != nil {
		t.Fatalf("ConsumeUpload() error = %v", err)
	}
	got, readErr := io.ReadAll(upload)
	closeErr := upload.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(got, content) {
		t.Fatalf("uploaded content = %q, errors %v/%v", got, readErr, closeErr)
	}
}

func TestRuntimeRejectsUnboundedFileChunks(t *testing.T) {
	if _, err := (&Download{}).NextChunk(maxFileTransferChunkBytes + 1); !errors.Is(err, ErrInvalidChunkSize) {
		t.Fatalf("NextChunk() error = %v", err)
	}
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(5000) })
	if _, err := runtime.AppendUploadJSON("invalid", 0, nil); !errors.Is(err, ErrInvalidChunkSize) {
		t.Fatalf("AppendUploadJSON() error = %v", err)
	}
}

func TestRuntimeGrantsOpaqueDirectoryCapabilities(t *testing.T) {
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(5000) })
	directory := filepath.Clean(t.TempDir())

	capability, err := runtime.GrantResourcePath(directory)
	if err != nil {
		t.Fatalf("GrantResourcePath() error = %v", err)
	}
	if len(capability) != 96 || capability == directory {
		t.Fatalf("capability shape is invalid")
	}
	resolved, err := runtime.resources.ResolvePath(framework.ResourceCapability(capability))
	if err != nil || resolved != directory {
		t.Fatalf("ResolvePath() = [REDACTED], %v", err)
	}
	if err := runtime.ReleaseResource(capability); err != nil {
		t.Fatalf("ReleaseResource() error = %v", err)
	}
	if err := runtime.ReleaseResource(capability); err != nil {
		t.Fatalf("duplicate ReleaseResource() error = %v", err)
	}
	if _, err := runtime.resources.ResolvePath(framework.ResourceCapability(capability)); !errors.Is(err, framework.ErrResourceCapabilityNotFound) {
		t.Fatalf("released ResolvePath() error = %v", err)
	}
}

func TestRuntimeRejectsInvalidResourcePaths(t *testing.T) {
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(5000) })
	for _, path := range []string{"relative", filepath.Join(t.TempDir(), "missing")} {
		if _, err := runtime.GrantResourcePath(path); err == nil {
			t.Fatal("GrantResourcePath() accepted an invalid path")
		}
	}
}

type downloadReader struct {
	download *Download
	maxBytes int
}

func (reader downloadReader) Read(buffer []byte) (int, error) {
	chunk, err := reader.download.NextChunk(min(len(buffer), reader.maxBytes))
	if err != nil {
		return 0, err
	}
	if chunk == nil {
		return 0, io.EOF
	}
	return copy(buffer, chunk), nil
}

func decodeUploadStatus(t *testing.T, encoded string) framework.FileUploadStatus {
	t.Helper()
	var status framework.FileUploadStatus
	if err := json.Unmarshal([]byte(encoded), &status); err != nil {
		t.Fatalf("decode upload status: %v", err)
	}
	return status
}

func formatChecksum(checksum [sha256.Size]byte) string {
	const hexadecimal = "0123456789abcdef"
	encoded := make([]byte, sha256.Size*2)
	for index, value := range checksum {
		encoded[index*2] = hexadecimal[value>>4]
		encoded[index*2+1] = hexadecimal[value&0x0f]
	}
	return string(encoded)
}
