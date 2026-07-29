package framework

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultFileSchedulerStoreMaxTasks = 1_000
	fileSchedulerStoreEventVersion    = 1
	fileSchedulerStoreMaxEventBytes   = 64 * 1024
	fileSchedulerStoreMaxNameBytes    = 1024
	fileSchedulerStoreMaxErrorBytes   = 4 * 1024
	schedulerReservationTokenBytes    = 32
)

type FileSchedulerStoreOptions struct {
	Path     string
	MaxTasks int
}

func DefaultFileSchedulerStoreOptions(path string) FileSchedulerStoreOptions {
	return FileSchedulerStoreOptions{
		Path:     path,
		MaxTasks: defaultFileSchedulerStoreMaxTasks,
	}
}

type FileSchedulerStore struct {
	path     string
	maxTasks int
	file     *os.File
	tasks    map[string]fileStoredScheduledTask
	closed   bool
	mu       sync.Mutex
}

type fileStoredScheduledTask struct {
	state            StoredScheduledTask
	reservationToken string
}

type fileSchedulerStoreEvent struct {
	Version         int       `json:"version"`
	Type            string    `json:"type"`
	Name            string    `json:"name"`
	Token           string    `json:"token,omitempty"`
	ScheduledAt     time.Time `json:"scheduledAt,omitempty"`
	ReservedUntil   time.Time `json:"reservedUntil,omitempty"`
	NextRunAt       time.Time `json:"nextRunAt,omitempty"`
	LastCompletedAt time.Time `json:"lastCompletedAt,omitempty"`
	Error           string    `json:"error,omitempty"`
}

func NewFileSchedulerStore(
	options FileSchedulerStoreOptions,
) (*FileSchedulerStore, error) {
	normalized, err := normalizeFileSchedulerStoreOptions(options)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(normalized.Path), 0o700); err != nil {
		return nil, fmt.Errorf(
			"%w: create directory: %w",
			ErrSchedulerStoreOperationFailed,
			err,
		)
	}
	file, err := os.OpenFile(normalized.Path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: open log: %w", ErrSchedulerStoreOperationFailed, err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: secure log: %w", ErrSchedulerStoreOperationFailed, err)
	}
	store := &FileSchedulerStore{
		path:     normalized.Path,
		maxTasks: normalized.MaxTasks,
		file:     file,
		tasks:    make(map[string]fileStoredScheduledTask),
	}
	if err := store.replay(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return store, nil
}

func normalizeFileSchedulerStoreOptions(
	options FileSchedulerStoreOptions,
) (FileSchedulerStoreOptions, error) {
	options.Path = strings.TrimSpace(options.Path)
	if options.Path == "" || options.MaxTasks < 0 {
		return FileSchedulerStoreOptions{}, ErrInvalidFileSchedulerStoreOptions
	}
	if options.MaxTasks == 0 {
		options.MaxTasks = defaultFileSchedulerStoreMaxTasks
	}
	path, err := filepath.Abs(options.Path)
	if err != nil {
		return FileSchedulerStoreOptions{}, fmt.Errorf(
			"%w: path: %w",
			ErrInvalidFileSchedulerStoreOptions,
			err,
		)
	}
	options.Path = path
	return options, nil
}

func (store *FileSchedulerStore) Path() string {
	if store == nil {
		return ""
	}
	return store.path
}

func (store *FileSchedulerStore) Initialize(
	ctx context.Context,
	name string,
	nextRunAt time.Time,
) error {
	if store == nil {
		return ErrSchedulerStoreUnavailable
	}
	if ctx == nil {
		return ErrSchedulerContextUnavailable
	}
	if !validStoredScheduledTaskName(name) || nextRunAt.IsZero() {
		return ErrSchedulerStoreConflict
	}
	nextRunAt = nextRunAt.UTC()

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ready(ctx); err != nil {
		return err
	}
	if _, exists := store.tasks[name]; exists {
		return nil
	}
	if len(store.tasks) >= store.maxTasks {
		return ErrSchedulerStoreFull
	}
	event := fileSchedulerStoreEvent{
		Version:   fileSchedulerStoreEventVersion,
		Type:      "initialize",
		Name:      name,
		NextRunAt: nextRunAt,
	}
	if err := store.append(event); err != nil {
		return err
	}
	store.tasks[name] = fileStoredScheduledTask{
		state: StoredScheduledTask{
			Name:      name,
			NextRunAt: nextRunAt,
		},
	}
	return nil
}

func (store *FileSchedulerStore) State(
	ctx context.Context,
	name string,
) (StoredScheduledTask, error) {
	if store == nil {
		return StoredScheduledTask{}, ErrSchedulerStoreUnavailable
	}
	if ctx == nil {
		return StoredScheduledTask{}, ErrSchedulerContextUnavailable
	}
	if !validStoredScheduledTaskName(name) {
		return StoredScheduledTask{}, ErrSchedulerStoreConflict
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ready(ctx); err != nil {
		return StoredScheduledTask{}, err
	}
	record, exists := store.tasks[name]
	if !exists {
		return StoredScheduledTask{}, ErrScheduledTaskStateNotFound
	}
	return record.state, nil
}

func (store *FileSchedulerStore) Reserve(
	ctx context.Context,
	name string,
	now time.Time,
	lease time.Duration,
) (ScheduledTaskReservation, error) {
	if store == nil {
		return ScheduledTaskReservation{}, ErrSchedulerStoreUnavailable
	}
	if ctx == nil {
		return ScheduledTaskReservation{}, ErrSchedulerContextUnavailable
	}
	if !validStoredScheduledTaskName(name) || now.IsZero() || lease <= 0 {
		return ScheduledTaskReservation{}, ErrScheduledTaskReservationInvalid
	}
	now = now.UTC()

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ready(ctx); err != nil {
		return ScheduledTaskReservation{}, err
	}
	record, exists := store.tasks[name]
	if !exists {
		return ScheduledTaskReservation{}, ErrScheduledTaskStateNotFound
	}
	if record.reservationToken != "" && record.state.ReservedUntil.After(now) {
		return ScheduledTaskReservation{}, ErrScheduledTaskReserved
	}
	if record.state.NextRunAt.After(now) {
		return ScheduledTaskReservation{}, ErrScheduledTaskNotDue
	}
	token, err := newSchedulerReservationToken()
	if err != nil {
		return ScheduledTaskReservation{}, err
	}
	reservedUntil := now.Add(lease)
	event := fileSchedulerStoreEvent{
		Version:       fileSchedulerStoreEventVersion,
		Type:          "reserve",
		Name:          name,
		Token:         token,
		ScheduledAt:   record.state.NextRunAt,
		ReservedUntil: reservedUntil,
	}
	if err := store.append(event); err != nil {
		return ScheduledTaskReservation{}, err
	}
	record.reservationToken = token
	record.state.ReservedUntil = reservedUntil
	store.tasks[name] = record
	return ScheduledTaskReservation{
		Task:          record.state,
		Token:         token,
		ReservedUntil: reservedUntil,
	}, nil
}

func (store *FileSchedulerStore) Complete(
	ctx context.Context,
	reservation ScheduledTaskReservation,
	nextRunAt time.Time,
	completedAt time.Time,
	lastError string,
) error {
	if store == nil {
		return ErrSchedulerStoreUnavailable
	}
	if ctx == nil {
		return ErrSchedulerContextUnavailable
	}
	if !validStoredScheduledTaskName(reservation.Task.Name) ||
		reservation.Token == "" ||
		reservation.Task.NextRunAt.IsZero() ||
		nextRunAt.IsZero() ||
		completedAt.IsZero() ||
		!nextRunAt.After(completedAt) {
		return ErrScheduledTaskReservationInvalid
	}
	nextRunAt = nextRunAt.UTC()
	completedAt = completedAt.UTC()
	lastError = normalizeFileSchedulerStoreError(lastError)

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ready(ctx); err != nil {
		return err
	}
	record, exists := store.tasks[reservation.Task.Name]
	if !exists ||
		record.reservationToken != reservation.Token ||
		!record.state.NextRunAt.Equal(reservation.Task.NextRunAt) {
		return ErrScheduledTaskReservationInvalid
	}
	event := fileSchedulerStoreEvent{
		Version:         fileSchedulerStoreEventVersion,
		Type:            "complete",
		Name:            reservation.Task.Name,
		Token:           reservation.Token,
		ScheduledAt:     reservation.Task.NextRunAt.UTC(),
		NextRunAt:       nextRunAt,
		LastCompletedAt: completedAt,
		Error:           lastError,
	}
	if err := store.append(event); err != nil {
		return err
	}
	record.reservationToken = ""
	record.state.NextRunAt = nextRunAt
	record.state.LastScheduledAt = reservation.Task.NextRunAt.UTC()
	record.state.LastCompletedAt = completedAt
	record.state.LastError = lastError
	record.state.ReservedUntil = time.Time{}
	store.tasks[record.state.Name] = record
	return nil
}

func (store *FileSchedulerStore) States() []StoredScheduledTask {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	states := make([]StoredScheduledTask, 0, len(store.tasks))
	for _, record := range store.tasks {
		states = append(states, record.state)
	}
	sort.Slice(states, func(left int, right int) bool {
		return states[left].Name < states[right].Name
	})
	return states
}

func (store *FileSchedulerStore) Close() error {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return nil
	}
	store.closed = true
	if err := store.file.Close(); err != nil {
		return fmt.Errorf("%w: close log: %w", ErrSchedulerStoreOperationFailed, err)
	}
	return nil
}

func (store *FileSchedulerStore) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store.closed {
		return ErrSchedulerStoreClosed
	}
	return nil
}

func (store *FileSchedulerStore) append(event fileSchedulerStoreEvent) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("%w: encode event: %w", ErrSchedulerStoreOperationFailed, err)
	}
	encoded = append(encoded, '\n')
	if err := writeFileJobStoreAll(store.file, encoded); err != nil {
		return fmt.Errorf("%w: append event: %w", ErrSchedulerStoreOperationFailed, err)
	}
	if err := store.file.Sync(); err != nil {
		return fmt.Errorf("%w: sync event: %w", ErrSchedulerStoreOperationFailed, err)
	}
	return nil
}

func (store *FileSchedulerStore) replay() error {
	if _, err := store.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("%w: seek log: %w", ErrSchedulerStoreOperationFailed, err)
	}
	reader := bufio.NewReader(store.file)
	var validBytes int64
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if len(line) != 0 {
				if truncateErr := store.file.Truncate(validBytes); truncateErr != nil {
					return fmt.Errorf(
						"%w: discard incomplete event: %w",
						ErrSchedulerStoreOperationFailed,
						truncateErr,
					)
				}
				if syncErr := store.file.Sync(); syncErr != nil {
					return fmt.Errorf(
						"%w: sync repaired log: %w",
						ErrSchedulerStoreOperationFailed,
						syncErr,
					)
				}
			}
			break
		}
		if err != nil {
			return fmt.Errorf("%w: read log: %w", ErrSchedulerStoreOperationFailed, err)
		}
		validBytes += int64(len(line))
		if len(line) > fileSchedulerStoreMaxEventBytes {
			return ErrFileSchedulerStoreCorrupt
		}
		line = bytes.TrimSuffix(line, []byte{'\n'})
		var event fileSchedulerStoreEvent
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&event); decodeErr != nil {
			return fmt.Errorf("%w: %w", ErrFileSchedulerStoreCorrupt, decodeErr)
		}
		var extra any
		if decodeErr := decoder.Decode(&extra); decodeErr != io.EOF {
			return ErrFileSchedulerStoreCorrupt
		}
		if applyErr := store.applyEvent(event); applyErr != nil {
			return applyErr
		}
	}
	if _, err := store.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("%w: seek log end: %w", ErrSchedulerStoreOperationFailed, err)
	}
	return nil
}

func (store *FileSchedulerStore) applyEvent(event fileSchedulerStoreEvent) error {
	if event.Version != fileSchedulerStoreEventVersion ||
		!validStoredScheduledTaskName(event.Name) {
		return ErrFileSchedulerStoreCorrupt
	}
	switch event.Type {
	case "initialize":
		if event.NextRunAt.IsZero() {
			return ErrFileSchedulerStoreCorrupt
		}
		if _, exists := store.tasks[event.Name]; exists {
			return ErrFileSchedulerStoreCorrupt
		}
		store.tasks[event.Name] = fileStoredScheduledTask{
			state: StoredScheduledTask{
				Name:      event.Name,
				NextRunAt: event.NextRunAt.UTC(),
			},
		}
	case "reserve":
		record, exists := store.tasks[event.Name]
		if !exists ||
			event.Token == "" ||
			event.ScheduledAt.IsZero() ||
			!event.ScheduledAt.Equal(record.state.NextRunAt) ||
			event.ReservedUntil.IsZero() {
			return ErrFileSchedulerStoreCorrupt
		}
		record.reservationToken = event.Token
		record.state.ReservedUntil = event.ReservedUntil.UTC()
		store.tasks[event.Name] = record
	case "complete":
		record, exists := store.tasks[event.Name]
		if !exists ||
			event.Token == "" ||
			record.reservationToken != event.Token ||
			event.ScheduledAt.IsZero() ||
			!event.ScheduledAt.Equal(record.state.NextRunAt) ||
			event.NextRunAt.IsZero() ||
			event.LastCompletedAt.IsZero() ||
			!event.NextRunAt.After(event.LastCompletedAt) {
			return ErrFileSchedulerStoreCorrupt
		}
		record.reservationToken = ""
		record.state.NextRunAt = event.NextRunAt.UTC()
		record.state.LastScheduledAt = event.ScheduledAt.UTC()
		record.state.LastCompletedAt = event.LastCompletedAt.UTC()
		record.state.LastError = event.Error
		record.state.ReservedUntil = time.Time{}
		store.tasks[event.Name] = record
	default:
		return ErrFileSchedulerStoreCorrupt
	}
	return nil
}

func validStoredScheduledTaskName(name string) bool {
	return strings.TrimSpace(name) != "" && len(name) <= fileSchedulerStoreMaxNameBytes
}

func newSchedulerReservationToken() (string, error) {
	buffer := make([]byte, schedulerReservationTokenBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf(
			"%w: random reservation token: %w",
			ErrSchedulerStoreOperationFailed,
			err,
		)
	}
	return hex.EncodeToString(buffer), nil
}

func normalizeFileSchedulerStoreError(value string) string {
	if len(value) <= fileSchedulerStoreMaxErrorBytes {
		return value
	}
	value = value[:fileSchedulerStoreMaxErrorBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
