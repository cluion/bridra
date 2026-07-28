package framework_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cluion/bridra/backend/framework"
)

func TestPublicFileTransferStagesAndServesManagedDownload(t *testing.T) {
	store, err := framework.NewFileTransferStore(framework.FileTransferOptions{
		TTL:      time.Minute,
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	reference, err := store.Stage(
		context.Background(),
		"public.txt",
		"text/plain",
		strings.NewReader("public data"),
	)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	handler := &framework.FileTransferHTTPHandler{Store: store}
	request := httptest.NewRequest(
		http.MethodGet,
		"/rpc/files/"+reference.ID,
		nil,
	)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Body.String() != "public data" {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
}

func TestPublicFileTransferImportsAndConsumesManagedUpload(t *testing.T) {
	store, err := framework.NewFileTransferStore(framework.FileTransferOptions{
		TTL:      time.Minute,
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	content := []byte("public upload")
	sum := sha256.Sum256(content)
	reference, err := store.ImportUpload(
		context.Background(),
		"upload.bin",
		"application/octet-stream",
		int64(len(content)),
		hex.EncodeToString(sum[:]),
		bytes.NewReader(content),
	)
	if err != nil {
		t.Fatalf("import upload: %v", err)
	}
	upload, err := store.ConsumeUpload(reference)
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
