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

func TestFileTransferStoreResumesAndConsumesVerifiedUpload(t *testing.T) {
	store, err := NewFileTransferStore(FileTransferOptions{
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
	content := []byte("resumable upload")
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
	first := int64(6)
	status, err = store.AppendUpload(
		context.Background(),
		status.Reference.ID,
		0,
		bytes.NewReader(content[:first]),
	)
	if err != nil {
		t.Fatalf("append first range: %v", err)
	}
	if status.Offset != first || status.Complete {
		t.Fatalf("first status = %#v", status)
	}
	if _, err := store.AppendUpload(
		context.Background(),
		status.Reference.ID,
		0,
		bytes.NewReader(content[first:]),
	); !errors.Is(err, ErrFileTransferOffset) {
		t.Fatalf("wrong offset error = %v", err)
	}
	status, err = store.AppendUpload(
		context.Background(),
		status.Reference.ID,
		first,
		bytes.NewReader(content[first:]),
	)
	if err != nil {
		t.Fatalf("append final range: %v", err)
	}
	if status.Offset != int64(len(content)) || !status.Complete {
		t.Fatalf("final status = %#v", status)
	}
	upload, err := store.ConsumeUpload(status.Reference)
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
	if _, err := store.ConsumeUpload(status.Reference); !errors.Is(err, ErrFileTransferInvalid) {
		t.Fatalf("second consume error = %v", err)
	}
}

func TestFileTransferStoreRejectsCorruptAndIncompleteUploads(t *testing.T) {
	store, err := NewFileTransferStore(FileTransferOptions{
		TTL:      time.Minute,
		MaxBytes: 4,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	if _, err := store.BeginUpload(
		"large.bin",
		"",
		5,
		strings.Repeat("0", 64),
	); !errors.Is(err, ErrFileTransferTooLarge) {
		t.Fatalf("large upload error = %v", err)
	}
	status, err := store.BeginUpload(
		"bad.bin",
		"",
		4,
		strings.Repeat("0", 64),
	)
	if err != nil {
		t.Fatalf("begin corrupt upload: %v", err)
	}
	if _, err := store.AppendUpload(
		context.Background(),
		status.Reference.ID,
		0,
		strings.NewReader("data"),
	); !errors.Is(err, ErrFileTransferChecksum) {
		t.Fatalf("checksum error = %v", err)
	}
	if _, err := store.UploadStatus(status.Reference.ID); !errors.Is(err, ErrFileTransferNotFound) {
		t.Fatalf("corrupt upload status error = %v", err)
	}

	sum := sha256.Sum256([]byte("data"))
	if _, err := store.ImportUpload(
		context.Background(),
		"short.bin",
		"",
		4,
		hex.EncodeToString(sum[:]),
		strings.NewReader("da"),
	); !errors.Is(err, ErrFileTransferIncomplete) {
		t.Fatalf("incomplete import error = %v", err)
	}
}

func TestFileTransferStoreRejectsInvalidAndBusyTransferState(t *testing.T) {
	if _, err := NewFileTransferStore(
		FileTransferOptions{TTL: -time.Second},
	); !errors.Is(err, ErrInvalidFileTransferOpts) {
		t.Fatalf("negative options error = %v", err)
	}
	store, err := NewFileTransferStore(FileTransferOptions{
		TTL:      time.Minute,
		MaxBytes: 8,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	for _, test := range []struct {
		name     string
		size     int64
		checksum string
	}{
		{name: "../bad", size: 0, checksum: emptyFileSHA256},
		{name: "bad.bin", size: -1, checksum: emptyFileSHA256},
		{name: "bad.bin", size: 0, checksum: "bad"},
	} {
		if _, err := store.BeginUpload(
			test.name,
			"",
			test.size,
			test.checksum,
		); !errors.Is(err, ErrFileTransferInvalid) {
			t.Errorf("begin invalid upload error = %v", err)
		}
	}
	empty, err := store.ImportUpload(
		context.Background(),
		"empty.bin",
		"",
		0,
		emptyFileSHA256,
		strings.NewReader(""),
	)
	if err != nil {
		t.Fatalf("import empty upload: %v", err)
	}
	emptyUpload, err := store.ConsumeUpload(empty)
	if err != nil {
		t.Fatalf("consume empty upload: %v", err)
	}
	if err := emptyUpload.Close(); err != nil {
		t.Fatalf("close empty upload: %v", err)
	}
	if _, err := store.ImportUpload(
		context.Background(),
		"oversized-empty.bin",
		"",
		0,
		emptyFileSHA256,
		strings.NewReader("x"),
	); !errors.Is(err, ErrFileTransferTooLarge) {
		t.Fatalf("oversized empty upload error = %v", err)
	}
	if _, err := store.Take("bad"); !errors.Is(err, ErrFileTransferNotFound) {
		t.Fatalf("invalid take error = %v", err)
	}
	incomplete, err := store.BeginUpload(
		"incomplete.bin",
		"",
		4,
		hex.EncodeToString(sha256.New().Sum(nil)),
	)
	if err != nil {
		t.Fatalf("begin incomplete upload: %v", err)
	}
	if _, err := store.Take(incomplete.Reference.ID); !errors.Is(err, ErrFileTransferIncomplete) {
		t.Fatalf("incomplete take error = %v", err)
	}

	reference, err := store.Stage(
		context.Background(),
		"download.bin",
		"",
		strings.NewReader("1234"),
	)
	if err != nil {
		t.Fatalf("stage download: %v", err)
	}
	download, err := store.OpenDownload(reference.ID, 0)
	if err != nil {
		t.Fatalf("open download: %v", err)
	}
	if _, err := store.OpenDownload(reference.ID, 0); !errors.Is(err, ErrFileTransferBusy) {
		t.Fatalf("busy download error = %v", err)
	}
	if err := download.Close(); err != nil {
		t.Fatalf("close download: %v", err)
	}
	if _, err := store.OpenDownload(reference.ID, reference.Size+1); !errors.Is(err, ErrFileTransferOffset) {
		t.Fatalf("download offset error = %v", err)
	}

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	status, err := store.BeginUpload(
		"expires.bin",
		"",
		4,
		hex.EncodeToString(sha256.New().Sum(nil)),
	)
	if err != nil {
		t.Fatalf("begin expiring upload: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := store.UploadStatus(status.Reference.ID); !errors.Is(err, ErrFileTransferExpired) {
		t.Fatalf("expired upload status error = %v", err)
	}
	if _, err := store.OpenDownload("bad", 0); !errors.Is(err, ErrFileTransferNotFound) {
		t.Fatalf("invalid download id error = %v", err)
	}
	if _, err := store.AppendUpload(
		nil,
		strings.Repeat("a", 64),
		0,
		strings.NewReader("x"),
	); !errors.Is(err, ErrFileTransferInvalid) {
		t.Fatalf("invalid append error = %v", err)
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
