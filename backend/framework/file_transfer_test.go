package framework

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFileTransferStoreStagesAndConsumesManagedFile(t *testing.T) {
	store, err := NewFileTransferStore(FileTransferOptions{
		TTL:             time.Minute,
		MaxBytes:        1024,
		ExposeLocalPath: true,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	content := []byte("large report")
	sum := sha256.Sum256(content)

	reference, err := store.Stage(
		context.Background(),
		"report.txt",
		"text/plain; charset=utf-8",
		bytes.NewReader(content),
	)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if !validFileTransferID(reference.ID) ||
		reference.Name != "report.txt" ||
		reference.MediaType != "text/plain; charset=utf-8" ||
		reference.Size != int64(len(content)) ||
		reference.SHA256 != hex.EncodeToString(sum[:]) ||
		reference.LocalPath == "" {
		t.Fatalf("reference = %#v", reference)
	}
	if filepath.Dir(reference.LocalPath) != store.root {
		t.Fatalf("local path = %q, root = %q", reference.LocalPath, store.root)
	}
	info, err := os.Stat(reference.LocalPath)
	if err != nil {
		t.Fatalf("stat staged file: %v", err)
	}
	if permissions := info.Mode().Perm(); runtime.GOOS != "windows" && permissions != 0o600 {
		t.Fatalf("permissions = %o", permissions)
	}

	download, err := store.Take(reference.ID)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if download.Reference.LocalPath != "" {
		t.Fatalf("HTTP download exposed local path %q", download.Reference.LocalPath)
	}
	got, err := io.ReadAll(download)
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content = %q", got)
	}
	if err := download.Close(); err != nil {
		t.Fatalf("close download: %v", err)
	}
	if _, err := os.Stat(reference.LocalPath); !os.IsNotExist(err) {
		t.Fatalf("staged file still exists: %v", err)
	}
	if _, err := store.Take(reference.ID); !errors.Is(err, ErrFileTransferNotFound) {
		t.Fatalf("second take error = %v", err)
	}
}

func TestFileTransferStoreEnforcesMetadataSizeExpiryAndClose(t *testing.T) {
	store, err := NewFileTransferStore(FileTransferOptions{
		TTL:      time.Minute,
		MaxBytes: 4,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	for _, name := range []string{"", "../secret", `nested\secret`, "bad\nname"} {
		if _, err := store.Stage(
			context.Background(),
			name,
			"text/plain",
			strings.NewReader("ok"),
		); !errors.Is(err, ErrFileTransferInvalid) {
			t.Errorf("stage name %q error = %v", name, err)
		}
	}
	if _, err := store.Stage(
		context.Background(),
		"report.txt",
		"invalid media type",
		strings.NewReader("ok"),
	); !errors.Is(err, ErrFileTransferInvalid) {
		t.Fatalf("invalid media type error = %v", err)
	}
	if _, err := store.Stage(
		context.Background(),
		"report.txt",
		"text/plain",
		strings.NewReader("oversized"),
	); !errors.Is(err, ErrFileTransferTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	reference, err := store.Stage(
		context.Background(),
		"expires.bin",
		"",
		strings.NewReader("1234"),
	)
	if err != nil {
		t.Fatalf("stage expiring file: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := store.Take(reference.ID); !errors.Is(err, ErrFileTransferExpired) {
		t.Fatalf("expired take error = %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := store.Stage(
		context.Background(),
		"closed.bin",
		"",
		strings.NewReader("data"),
	); !errors.Is(err, ErrFileTransferClosed) {
		t.Fatalf("stage after close error = %v", err)
	}
}

func TestFileTransferStoreHonorsCancellationAndOwnedRootCleanup(t *testing.T) {
	store, err := NewFileTransferStore(DefaultFileTransferOptions())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	root := store.root
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Stage(
		ctx,
		"cancelled.bin",
		"",
		strings.NewReader("data"),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stage error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("owned root still exists: %v", err)
	}
}

func TestFileTransferServiceProviderRegistersAndCleansStore(t *testing.T) {
	application := NewApplication(NewConfig())
	provider := NewFileTransferServiceProvider(DefaultFileTransferOptions())
	if err := application.Register(provider); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot application: %v", err)
	}
	store, err := Resolve(application.Container(), FileTransferStoreKey)
	if err != nil {
		t.Fatalf("resolve store: %v", err)
	}
	root := store.root

	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown application: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("provider did not remove root: %v", err)
	}
}
