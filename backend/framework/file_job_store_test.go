package framework

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFileJobStorePersistsOrderingReleaseAndCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue", "jobs.log")
	store := newTestFileJobStore(t, path, FileJobStoreOptions{})
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	first := testStoredJob(t, "first", now, now)
	second := testStoredJob(t, "second", now.Add(time.Minute), now.Add(time.Second))
	if err := store.Enqueue(context.Background(), second); err != nil {
		t.Fatalf("enqueue second: %v", err)
	}
	if err := store.Enqueue(context.Background(), first); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}

	reservation, err := store.Reserve(context.Background(), now, time.Minute)
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	if reservation.Job.ID != first.ID || reservation.Job.Attempts != 1 {
		t.Fatalf("first reservation = %#v", reservation)
	}
	retryAt := now.Add(30 * time.Second)
	if err := store.Release(
		context.Background(),
		reservation,
		retryAt,
		"temporary",
	); err != nil {
		t.Fatalf("release first: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	store = newTestFileJobStore(t, path, FileJobStoreOptions{})
	if _, err := store.Reserve(
		context.Background(),
		now.Add(15*time.Second),
		time.Minute,
	); !errors.Is(err, ErrJobStoreEmpty) {
		t.Fatalf("early reserve error = %v", err)
	}
	reservation, err = store.Reserve(
		context.Background(),
		retryAt,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("reserve released job: %v", err)
	}
	if reservation.Job.ID != first.ID || reservation.Job.Attempts != 2 {
		t.Fatalf("released reservation = %#v", reservation)
	}
	if err := store.Complete(context.Background(), reservation); err != nil {
		t.Fatalf("complete first: %v", err)
	}

	reservation, err = store.Reserve(
		context.Background(),
		now.Add(time.Minute),
		time.Minute,
	)
	if err != nil {
		t.Fatalf("reserve second: %v", err)
	}
	if reservation.Job.ID != second.ID {
		t.Fatalf("second reservation = %#v", reservation)
	}
	if err := store.Complete(context.Background(), reservation); err != nil {
		t.Fatalf("complete second: %v", err)
	}
	if _, err := store.Reserve(
		context.Background(),
		now.Add(2*time.Minute),
		time.Minute,
	); !errors.Is(err, ErrJobStoreEmpty) {
		t.Fatalf("empty reserve error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close reopened store: %v", err)
	}
}

func TestFileJobStoreRecoversExpiredLeaseAndRetainsFailedJobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.log")
	store := newTestFileJobStore(t, path, FileJobStoreOptions{})
	now := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	job := testStoredJob(t, "recover", now, now)
	if err := store.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	first, err := store.Reserve(context.Background(), now, time.Minute)
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close crashed store: %v", err)
	}

	store = newTestFileJobStore(t, path, FileJobStoreOptions{})
	if _, err := store.Reserve(
		context.Background(),
		now.Add(30*time.Second),
		time.Minute,
	); !errors.Is(err, ErrJobStoreEmpty) {
		t.Fatalf("active lease reserve error = %v", err)
	}
	recovered, err := store.Reserve(
		context.Background(),
		now.Add(2*time.Minute),
		time.Minute,
	)
	if err != nil {
		t.Fatalf("reserve recovered: %v", err)
	}
	if recovered.Job.ID != first.Job.ID ||
		recovered.Token == first.Token ||
		recovered.Job.Attempts != 2 {
		t.Fatalf("recovered reservation = %#v", recovered)
	}
	if err := store.Fail(context.Background(), recovered, "permanent"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	failed := store.FailedJobs()
	if len(failed) != 1 ||
		failed[0].Job.ID != job.ID ||
		failed[0].Job.Attempts != 2 ||
		failed[0].Error != "permanent" ||
		failed[0].FailedAt.IsZero() {
		t.Fatalf("failed jobs = %#v", failed)
	}
	if _, err := store.Reserve(
		context.Background(),
		now.Add(3*time.Minute),
		time.Minute,
	); !errors.Is(err, ErrJobStoreEmpty) {
		t.Fatalf("failed job reserve error = %v", err)
	}

	retryAt := now.Add(4 * time.Minute)
	if err := store.RetryFailed(context.Background(), job.ID, retryAt); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	retried, err := store.Reserve(context.Background(), retryAt, time.Minute)
	if err != nil {
		t.Fatalf("reserve retried: %v", err)
	}
	if retried.Job.Attempts != 1 {
		t.Fatalf("retried attempts = %d", retried.Job.Attempts)
	}
	longError := strings.Repeat("界", fileJobStoreMaxErrorBytes)
	if err := store.Fail(context.Background(), retried, longError); err != nil {
		t.Fatalf("fail retried: %v", err)
	}
	failed = store.FailedJobs()
	if len(failed) != 1 ||
		len(failed[0].Error) > fileJobStoreMaxErrorBytes ||
		!utf8.ValidString(failed[0].Error) {
		t.Fatalf("truncated failed error = %#v", failed)
	}
	if err := store.ForgetFailed(context.Background(), job.ID); err != nil {
		t.Fatalf("forget failed: %v", err)
	}
	if failed := store.FailedJobs(); len(failed) != 0 {
		t.Fatalf("failed jobs after forget = %#v", failed)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	store = newTestFileJobStore(t, path, FileJobStoreOptions{})
	if failed := store.FailedJobs(); len(failed) != 0 {
		t.Fatalf("replayed failed jobs = %#v", failed)
	}
	if _, err := store.Reserve(
		context.Background(),
		now.Add(5*time.Minute),
		time.Minute,
	); !errors.Is(err, ErrJobStoreEmpty) {
		t.Fatalf("replayed empty reserve error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close replayed store: %v", err)
	}
}

func TestFileJobStoreBoundsValidatesAndRejectsInvalidReservations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.log")
	store := newTestFileJobStore(t, path, FileJobStoreOptions{
		MaxJobs:         1,
		MaxPayloadBytes: 16,
	})
	now := time.Now().UTC()
	job := testStoredJob(t, "bounded", now, now)
	if store.Path() != path {
		t.Fatalf("path = %q, want %q", store.Path(), path)
	}
	if err := store.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if failed := store.FailedJobs(); len(failed) != 0 {
		t.Fatalf("unexpected failed jobs = %#v", failed)
	}
	if err := store.RetryFailed(
		context.Background(),
		"",
		now,
	); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("empty retry identifier error = %v", err)
	}
	if err := store.RetryFailed(
		context.Background(),
		job.ID,
		now,
	); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("retry ready job error = %v", err)
	}
	if err := store.ForgetFailed(
		context.Background(),
		"",
	); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("empty forget identifier error = %v", err)
	}
	if err := store.ForgetFailed(
		context.Background(),
		job.ID,
	); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("forget ready job error = %v", err)
	}
	if err := store.Enqueue(
		context.Background(),
		testStoredJob(t, "full", now, now),
	); !errors.Is(err, ErrJobStoreFull) {
		t.Fatalf("full enqueue error = %v", err)
	}
	oversized := testStoredJob(t, "large", now, now)
	oversized.Payload = json.RawMessage(`{"payload":"too large"}`)
	if err := store.Enqueue(
		context.Background(),
		oversized,
	); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("oversized enqueue error = %v", err)
	}
	longHandler := testStoredJob(t, strings.Repeat("h", 1025), now, now)
	if err := store.Enqueue(
		context.Background(),
		longHandler,
	); !errors.Is(err, ErrJobStoreConflict) {
		t.Fatalf("long handler enqueue error = %v", err)
	}
	if _, err := store.Reserve(
		context.Background(),
		time.Time{},
		time.Minute,
	); !errors.Is(err, ErrJobReservationInvalid) {
		t.Fatalf("invalid reserve error = %v", err)
	}
	reservation, err := store.Reserve(context.Background(), now, time.Minute)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	invalid := reservation
	invalid.Token = "wrong"
	if err := store.Complete(
		context.Background(),
		invalid,
	); !errors.Is(err, ErrJobReservationInvalid) {
		t.Fatalf("invalid complete error = %v", err)
	}
	if err := store.Release(
		context.Background(),
		reservation,
		time.Time{},
		"",
	); !errors.Is(err, ErrJobReservationInvalid) {
		t.Fatalf("zero release time error = %v", err)
	}
	if err := store.Release(
		context.Background(),
		reservation,
		now,
		"",
	); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := store.Enqueue(
		context.Background(),
		job,
	); !errors.Is(err, ErrJobStoreClosed) {
		t.Fatalf("closed enqueue error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestFileJobStoreDiscardsIncompleteTailAndRejectsCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.log")
	store := newTestFileJobStore(t, path, FileJobStoreOptions{})
	now := time.Now().UTC()
	job := testStoredJob(t, "persisted", now, now)
	if err := store.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before partial: %v", err)
	}
	appendTestFile(t, path, `{"version":1`)

	store = newTestFileJobStore(t, path, FileJobStoreOptions{})
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after partial: %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("truncated size = %d, want %d", after.Size(), before.Size())
	}
	reservation, err := store.Reserve(context.Background(), now, time.Minute)
	if err != nil || reservation.Job.ID != job.ID {
		t.Fatalf("reservation after truncated tail = %#v, %v", reservation, err)
	}
	if err := store.Complete(context.Background(), reservation); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close reopened: %v", err)
	}

	appendTestFile(t, path, "{broken}\n")
	if _, err := NewFileJobStore(DefaultFileJobStoreOptions(path)); !errors.Is(
		err,
		ErrFileJobStoreCorrupt,
	) {
		t.Fatalf("corrupt open error = %v", err)
	}
}

func TestFileJobStoreRejectsInvalidOptionsAndContext(t *testing.T) {
	for _, options := range []FileJobStoreOptions{
		{},
		{Path: "jobs.log", MaxJobs: -1},
		{Path: "jobs.log", MaxPayloadBytes: -1},
	} {
		if _, err := NewFileJobStore(options); !errors.Is(
			err,
			ErrInvalidFileJobStoreOptions,
		) {
			t.Fatalf("options %#v error = %v", options, err)
		}
	}
	store := newTestFileJobStore(
		t,
		filepath.Join(t.TempDir(), "jobs.log"),
		FileJobStoreOptions{},
	)
	if err := store.Enqueue(nil, StoredJob{}); !errors.Is(
		err,
		ErrJobContextUnavailable,
	) {
		t.Fatalf("nil enqueue error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Enqueue(
		cancelled,
		testStoredJob(t, "cancelled", time.Now().UTC(), time.Now().UTC()),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled enqueue error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestNilFileJobStoreAPI(t *testing.T) {
	var store *FileJobStore
	ctx := context.Background()
	reservation := JobReservation{
		Job:   StoredJob{ID: strings.Repeat("0", jobIdentifierBytes*2)},
		Token: "token",
	}
	if store.Path() != "" || store.FailedJobs() != nil {
		t.Fatal("nil store should expose empty read-only values")
	}
	if err := store.Enqueue(ctx, StoredJob{}); !errors.Is(err, ErrJobStoreUnavailable) {
		t.Fatalf("nil enqueue error = %v", err)
	}
	if _, err := store.Reserve(
		ctx,
		time.Now(),
		time.Second,
	); !errors.Is(err, ErrJobStoreUnavailable) {
		t.Fatalf("nil reserve error = %v", err)
	}
	if err := store.Release(
		ctx,
		reservation,
		time.Now(),
		"",
	); !errors.Is(err, ErrJobStoreUnavailable) {
		t.Fatalf("nil release error = %v", err)
	}
	if err := store.Complete(
		ctx,
		reservation,
	); !errors.Is(err, ErrJobStoreUnavailable) {
		t.Fatalf("nil complete error = %v", err)
	}
	if err := store.Fail(
		ctx,
		reservation,
		"failed",
	); !errors.Is(err, ErrJobStoreUnavailable) {
		t.Fatalf("nil fail error = %v", err)
	}
	if err := store.RetryFailed(
		ctx,
		reservation.Job.ID,
		time.Now(),
	); !errors.Is(err, ErrJobStoreUnavailable) {
		t.Fatalf("nil retry error = %v", err)
	}
	if err := store.ForgetFailed(
		ctx,
		reservation.Job.ID,
	); !errors.Is(err, ErrJobStoreUnavailable) {
		t.Fatalf("nil forget error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("nil close error = %v", err)
	}
}

func newTestFileJobStore(
	t *testing.T,
	path string,
	overrides FileJobStoreOptions,
) *FileJobStore {
	t.Helper()
	options := DefaultFileJobStoreOptions(path)
	if overrides.MaxJobs != 0 {
		options.MaxJobs = overrides.MaxJobs
	}
	if overrides.MaxPayloadBytes != 0 {
		options.MaxPayloadBytes = overrides.MaxPayloadBytes
	}
	store, err := NewFileJobStore(options)
	if err != nil {
		t.Fatalf("new file job store: %v", err)
	}
	return store
}

func testStoredJob(
	t *testing.T,
	handler string,
	availableAt time.Time,
	enqueuedAt time.Time,
) StoredJob {
	t.Helper()
	id, err := newJobIdentifier()
	if err != nil {
		t.Fatalf("new job identifier: %v", err)
	}
	return StoredJob{
		ID:          id,
		Handler:     handler,
		Payload:     json.RawMessage(`{"value":"test"}`),
		AvailableAt: availableAt.UTC(),
		EnqueuedAt:  enqueuedAt.UTC(),
	}
}

func appendTestFile(t *testing.T, path string, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		t.Fatalf("append: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close append: %v", err)
	}
}
