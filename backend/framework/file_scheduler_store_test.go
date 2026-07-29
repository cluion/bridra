package framework

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFileSchedulerStorePersistsStateAndCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduler", "tasks.log")
	store := newTestFileSchedulerStore(t, path, FileSchedulerStoreOptions{})
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	firstRun := now.Add(time.Hour)
	if err := store.Initialize(context.Background(), "reports.daily", firstRun); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := store.Initialize(
		context.Background(),
		"reports.daily",
		now.Add(2*time.Hour),
	); err != nil {
		t.Fatalf("initialize existing: %v", err)
	}
	if err := store.Initialize(
		context.Background(),
		"cleanup.hourly",
		firstRun,
	); err != nil {
		t.Fatalf("initialize second: %v", err)
	}
	if states := store.States(); len(states) != 2 ||
		states[0].Name != "cleanup.hourly" ||
		states[1].Name != "reports.daily" {
		t.Fatalf("states = %#v", states)
	}
	if _, err := store.Reserve(
		context.Background(),
		"reports.daily",
		now,
		time.Minute,
	); !errors.Is(err, ErrScheduledTaskNotDue) {
		t.Fatalf("early reserve error = %v", err)
	}
	reservation, err := store.Reserve(
		context.Background(),
		"reports.daily",
		firstRun,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if reservation.Task.NextRunAt != firstRun ||
		reservation.Token == "" ||
		reservation.ReservedUntil != firstRun.Add(time.Minute) {
		t.Fatalf("reservation = %#v", reservation)
	}
	if _, err := store.Reserve(
		context.Background(),
		"reports.daily",
		firstRun.Add(30*time.Second),
		time.Minute,
	); !errors.Is(err, ErrScheduledTaskReserved) {
		t.Fatalf("active reserve error = %v", err)
	}

	completedAt := firstRun.Add(10 * time.Second)
	nextRun := completedAt.Add(time.Hour)
	longError := strings.Repeat("界", fileSchedulerStoreMaxErrorBytes)
	if err := store.Complete(
		context.Background(),
		reservation,
		nextRun,
		completedAt,
		longError,
	); err != nil {
		t.Fatalf("complete: %v", err)
	}
	state, err := store.State(context.Background(), "reports.daily")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.NextRunAt != nextRun ||
		state.LastScheduledAt != firstRun ||
		state.LastCompletedAt != completedAt ||
		!state.ReservedUntil.IsZero() ||
		len(state.LastError) > fileSchedulerStoreMaxErrorBytes ||
		!utf8.ValidString(state.LastError) {
		t.Fatalf("state = %#v", state)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	store = newTestFileSchedulerStore(t, path, FileSchedulerStoreOptions{})
	state, err = store.State(context.Background(), "reports.daily")
	if err != nil {
		t.Fatalf("replayed state: %v", err)
	}
	if state.NextRunAt != nextRun ||
		state.LastScheduledAt != firstRun ||
		state.LastCompletedAt != completedAt ||
		state.LastError == "" {
		t.Fatalf("replayed state = %#v", state)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close replayed: %v", err)
	}
}

func TestFileSchedulerStoreRecoversExpiredReservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.log")
	store := newTestFileSchedulerStore(t, path, FileSchedulerStoreOptions{})
	due := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	if err := store.Initialize(context.Background(), "recover", due); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	first, err := store.Reserve(context.Background(), "recover", due, time.Minute)
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	store = newTestFileSchedulerStore(t, path, FileSchedulerStoreOptions{})
	if _, err := store.Reserve(
		context.Background(),
		"recover",
		due.Add(30*time.Second),
		time.Minute,
	); !errors.Is(err, ErrScheduledTaskReserved) {
		t.Fatalf("active replayed reservation error = %v", err)
	}
	recovered, err := store.Reserve(
		context.Background(),
		"recover",
		due.Add(2*time.Minute),
		time.Minute,
	)
	if err != nil {
		t.Fatalf("reserve recovered: %v", err)
	}
	if recovered.Token == first.Token || recovered.Task.NextRunAt != due {
		t.Fatalf("recovered reservation = %#v", recovered)
	}
	if err := store.Complete(
		context.Background(),
		first,
		due.Add(time.Hour),
		due.Add(time.Minute),
		"",
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("stale completion error = %v", err)
	}
	if err := store.Complete(
		context.Background(),
		recovered,
		due.Add(3*time.Hour),
		due.Add(2*time.Minute),
		"",
	); err != nil {
		t.Fatalf("complete recovered: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close recovered: %v", err)
	}
}

func TestFileSchedulerStoreBoundsValidationAndClosedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.log")
	store := newTestFileSchedulerStore(t, path, FileSchedulerStoreOptions{
		MaxTasks: 1,
	})
	now := time.Now().UTC()
	if store.Path() != path {
		t.Fatalf("path = %q, want %q", store.Path(), path)
	}
	if err := store.Initialize(context.Background(), "first", now); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := store.Initialize(
		context.Background(),
		"second",
		now,
	); !errors.Is(err, ErrSchedulerStoreFull) {
		t.Fatalf("full initialize error = %v", err)
	}
	if err := store.Initialize(
		context.Background(),
		strings.Repeat("x", fileSchedulerStoreMaxNameBytes+1),
		now,
	); !errors.Is(err, ErrSchedulerStoreConflict) {
		t.Fatalf("long name error = %v", err)
	}
	if _, err := store.State(
		context.Background(),
		"missing",
	); !errors.Is(err, ErrScheduledTaskStateNotFound) {
		t.Fatalf("missing state error = %v", err)
	}
	if _, err := store.Reserve(
		context.Background(),
		"first",
		time.Time{},
		time.Second,
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("invalid reserve error = %v", err)
	}
	reservation, err := store.Reserve(context.Background(), "first", now, time.Minute)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	invalid := reservation
	invalid.Token = "wrong"
	if err := store.Complete(
		context.Background(),
		invalid,
		now.Add(time.Hour),
		now,
		"",
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("invalid complete error = %v", err)
	}
	if err := store.Complete(
		context.Background(),
		reservation,
		now,
		now,
		"",
	); !errors.Is(err, ErrScheduledTaskReservationInvalid) {
		t.Fatalf("non-future next run error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := store.State(
		context.Background(),
		"first",
	); !errors.Is(err, ErrSchedulerStoreClosed) {
		t.Fatalf("closed state error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestFileSchedulerStoreRepairsIncompleteTailAndRejectsCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.log")
	store := newTestFileSchedulerStore(t, path, FileSchedulerStoreOptions{})
	now := time.Now().UTC()
	if err := store.Initialize(context.Background(), "persisted", now); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before partial: %v", err)
	}
	appendTestFile(t, path, `{"version":1`)

	store = newTestFileSchedulerStore(t, path, FileSchedulerStoreOptions{})
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after partial: %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("repaired size = %d, want %d", after.Size(), before.Size())
	}
	if _, err := store.State(context.Background(), "persisted"); err != nil {
		t.Fatalf("state after repair: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close repaired: %v", err)
	}

	appendTestFile(t, path, "{broken}\n")
	if _, err := NewFileSchedulerStore(
		DefaultFileSchedulerStoreOptions(path),
	); !errors.Is(err, ErrFileSchedulerStoreCorrupt) {
		t.Fatalf("corrupt open error = %v", err)
	}
}

func TestFileSchedulerStoreRejectsInvalidCompletedEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.log")
	store := newTestFileSchedulerStore(t, path, FileSchedulerStoreOptions{})
	due := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	if err := store.Initialize(context.Background(), "invalid.complete", due); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	reservation, err := store.Reserve(
		context.Background(),
		"invalid.complete",
		due,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	encoded := `{"version":1,"type":"complete","name":"invalid.complete",` +
		`"token":"` + reservation.Token + `","scheduledAt":"` +
		due.Format(time.RFC3339Nano) + `","nextRunAt":"` +
		due.Format(time.RFC3339Nano) + `","lastCompletedAt":"` +
		due.Format(time.RFC3339Nano) + `"}` + "\n"
	appendTestFile(t, path, encoded)
	if _, err := NewFileSchedulerStore(
		DefaultFileSchedulerStoreOptions(path),
	); !errors.Is(err, ErrFileSchedulerStoreCorrupt) {
		t.Fatalf("invalid completed event error = %v", err)
	}
}

func TestFileSchedulerStoreRejectsInvalidOptionsContextAndNilReceiver(t *testing.T) {
	for _, options := range []FileSchedulerStoreOptions{
		{},
		{Path: "tasks.log", MaxTasks: -1},
	} {
		if _, err := NewFileSchedulerStore(options); !errors.Is(
			err,
			ErrInvalidFileSchedulerStoreOptions,
		) {
			t.Fatalf("options %#v error = %v", options, err)
		}
	}
	store := newTestFileSchedulerStore(
		t,
		filepath.Join(t.TempDir(), "tasks.log"),
		FileSchedulerStoreOptions{},
	)
	if err := store.Initialize(
		nil,
		"task",
		time.Now(),
	); !errors.Is(err, ErrSchedulerContextUnavailable) {
		t.Fatalf("nil initialize context error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Initialize(
		cancelled,
		"task",
		time.Now(),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled initialize error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var nilStore *FileSchedulerStore
	if nilStore.Path() != "" || nilStore.States() != nil {
		t.Fatal("nil store should expose empty read-only values")
	}
	if err := nilStore.Initialize(
		context.Background(),
		"task",
		time.Now(),
	); !errors.Is(err, ErrSchedulerStoreUnavailable) {
		t.Fatalf("nil initialize error = %v", err)
	}
	if _, err := nilStore.State(
		context.Background(),
		"task",
	); !errors.Is(err, ErrSchedulerStoreUnavailable) {
		t.Fatalf("nil state error = %v", err)
	}
	if _, err := nilStore.Reserve(
		context.Background(),
		"task",
		time.Now(),
		time.Minute,
	); !errors.Is(err, ErrSchedulerStoreUnavailable) {
		t.Fatalf("nil reserve error = %v", err)
	}
	if err := nilStore.Complete(
		context.Background(),
		ScheduledTaskReservation{},
		time.Now(),
		time.Now(),
		"",
	); !errors.Is(err, ErrSchedulerStoreUnavailable) {
		t.Fatalf("nil complete error = %v", err)
	}
	if err := nilStore.Close(); err != nil {
		t.Fatalf("nil close error = %v", err)
	}
}

func newTestFileSchedulerStore(
	t *testing.T,
	path string,
	overrides FileSchedulerStoreOptions,
) *FileSchedulerStore {
	t.Helper()
	options := DefaultFileSchedulerStoreOptions(path)
	if overrides.MaxTasks != 0 {
		options.MaxTasks = overrides.MaxTasks
	}
	store, err := NewFileSchedulerStore(options)
	if err != nil {
		t.Fatalf("new file scheduler store: %v", err)
	}
	return store
}
