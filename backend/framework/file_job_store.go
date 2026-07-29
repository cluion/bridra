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
	defaultFileJobStoreMaxJobs         = 10_000
	defaultFileJobStoreMaxPayloadBytes = 4 * 1024 * 1024
	fileJobStoreEventVersion           = 1
	fileJobStoreEventOverhead          = 64 * 1024
	fileJobStoreMaxErrorBytes          = 4 * 1024
	fileJobStoreMaxHandlerBytes        = 1024
	jobIdentifierBytes                 = 32
)

type FileJobStoreOptions struct {
	Path            string
	MaxJobs         int
	MaxPayloadBytes int
}

func DefaultFileJobStoreOptions(path string) FileJobStoreOptions {
	return FileJobStoreOptions{
		Path:            path,
		MaxJobs:         defaultFileJobStoreMaxJobs,
		MaxPayloadBytes: defaultFileJobStoreMaxPayloadBytes,
	}
}

type FileJobStore struct {
	path            string
	maxJobs         int
	maxPayloadBytes int
	file            *os.File
	jobs            map[string]fileStoredJob
	closed          bool
	mu              sync.Mutex
}

type fileStoredJob struct {
	job              StoredJob
	reservationToken string
	reservedUntil    time.Time
	failedAt         time.Time
	lastError        string
}

type fileJobStoreEvent struct {
	Version       int             `json:"version"`
	Type          string          `json:"type"`
	Job           *fileJobPayload `json:"job,omitempty"`
	ID            string          `json:"id,omitempty"`
	Token         string          `json:"token,omitempty"`
	ReservedUntil time.Time       `json:"reservedUntil,omitempty"`
	AvailableAt   time.Time       `json:"availableAt,omitempty"`
	Attempts      int             `json:"attempts,omitempty"`
	Error         string          `json:"error,omitempty"`
	FailedAt      time.Time       `json:"failedAt,omitempty"`
}

type fileJobPayload struct {
	ID          string          `json:"id"`
	Handler     string          `json:"handler"`
	Payload     json.RawMessage `json:"payload"`
	AvailableAt time.Time       `json:"availableAt"`
	EnqueuedAt  time.Time       `json:"enqueuedAt"`
	Attempts    int             `json:"attempts"`
}

func NewFileJobStore(options FileJobStoreOptions) (*FileJobStore, error) {
	normalized, err := normalizeFileJobStoreOptions(options)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(normalized.Path), 0o700); err != nil {
		return nil, fmt.Errorf("%w: create directory: %w", ErrJobStoreOperationFailed, err)
	}
	file, err := os.OpenFile(normalized.Path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: open log: %w", ErrJobStoreOperationFailed, err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: secure log: %w", ErrJobStoreOperationFailed, err)
	}
	store := &FileJobStore{
		path:            normalized.Path,
		maxJobs:         normalized.MaxJobs,
		maxPayloadBytes: normalized.MaxPayloadBytes,
		file:            file,
		jobs:            make(map[string]fileStoredJob),
	}
	if err := store.replay(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return store, nil
}

func normalizeFileJobStoreOptions(options FileJobStoreOptions) (FileJobStoreOptions, error) {
	options.Path = strings.TrimSpace(options.Path)
	if options.Path == "" || options.MaxJobs < 0 || options.MaxPayloadBytes < 0 {
		return FileJobStoreOptions{}, ErrInvalidFileJobStoreOptions
	}
	if options.MaxJobs == 0 {
		options.MaxJobs = defaultFileJobStoreMaxJobs
	}
	if options.MaxPayloadBytes == 0 {
		options.MaxPayloadBytes = defaultFileJobStoreMaxPayloadBytes
	}
	path, err := filepath.Abs(options.Path)
	if err != nil {
		return FileJobStoreOptions{}, fmt.Errorf("%w: path: %w", ErrInvalidFileJobStoreOptions, err)
	}
	options.Path = path
	return options, nil
}

func (store *FileJobStore) Path() string {
	if store == nil {
		return ""
	}
	return store.path
}

func (store *FileJobStore) Enqueue(ctx context.Context, job StoredJob) error {
	if store == nil {
		return ErrJobStoreUnavailable
	}
	if ctx == nil {
		return ErrJobContextUnavailable
	}
	if err := validateStoredJob(job, store.maxPayloadBytes); err != nil {
		return err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ready(ctx); err != nil {
		return err
	}
	if len(store.jobs) >= store.maxJobs {
		return ErrJobStoreFull
	}
	if _, exists := store.jobs[job.ID]; exists {
		return ErrJobStoreConflict
	}
	job = cloneStoredJob(job)
	event := fileJobStoreEvent{
		Version: fileJobStoreEventVersion,
		Type:    "enqueue",
		Job:     fileJobPayloadFromStored(job),
	}
	if err := store.append(event); err != nil {
		return err
	}
	store.jobs[job.ID] = fileStoredJob{job: job}
	return nil
}

func (store *FileJobStore) Reserve(
	ctx context.Context,
	now time.Time,
	lease time.Duration,
) (JobReservation, error) {
	if store == nil {
		return JobReservation{}, ErrJobStoreUnavailable
	}
	if ctx == nil {
		return JobReservation{}, ErrJobContextUnavailable
	}
	if now.IsZero() || lease <= 0 {
		return JobReservation{}, ErrJobReservationInvalid
	}
	now = now.UTC()

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ready(ctx); err != nil {
		return JobReservation{}, err
	}
	eligible := make([]fileStoredJob, 0)
	for _, record := range store.jobs {
		if !record.failedAt.IsZero() ||
			record.job.AvailableAt.After(now) ||
			(!record.reservationTokenIsEmpty() && record.reservedUntil.After(now)) {
			continue
		}
		eligible = append(eligible, record)
	}
	if len(eligible) == 0 {
		return JobReservation{}, ErrJobStoreEmpty
	}
	sort.Slice(eligible, func(left int, right int) bool {
		if !eligible[left].job.AvailableAt.Equal(eligible[right].job.AvailableAt) {
			return eligible[left].job.AvailableAt.Before(eligible[right].job.AvailableAt)
		}
		if !eligible[left].job.EnqueuedAt.Equal(eligible[right].job.EnqueuedAt) {
			return eligible[left].job.EnqueuedAt.Before(eligible[right].job.EnqueuedAt)
		}
		return eligible[left].job.ID < eligible[right].job.ID
	})
	record := eligible[0]
	token, err := newJobIdentifier()
	if err != nil {
		return JobReservation{}, err
	}
	record.job.Attempts++
	record.reservationToken = token
	record.reservedUntil = now.Add(lease)
	event := fileJobStoreEvent{
		Version:       fileJobStoreEventVersion,
		Type:          "reserve",
		ID:            record.job.ID,
		Token:         token,
		ReservedUntil: record.reservedUntil,
		Attempts:      record.job.Attempts,
	}
	if err := store.append(event); err != nil {
		return JobReservation{}, err
	}
	store.jobs[record.job.ID] = record
	return reservationFromFileRecord(record), nil
}

func (store *FileJobStore) Release(
	ctx context.Context,
	reservation JobReservation,
	availableAt time.Time,
	lastError string,
) error {
	if availableAt.IsZero() {
		return ErrJobReservationInvalid
	}
	lastError = normalizeFileJobStoreError(lastError)
	return store.finishReservation(
		ctx,
		reservation,
		fileJobStoreEvent{
			Version:     fileJobStoreEventVersion,
			Type:        "release",
			ID:          reservation.Job.ID,
			Token:       reservation.Token,
			AvailableAt: availableAt.UTC(),
			Error:       lastError,
		},
		func(record fileStoredJob) (fileStoredJob, bool) {
			record.job.AvailableAt = availableAt.UTC()
			record.reservationToken = ""
			record.reservedUntil = time.Time{}
			record.lastError = lastError
			return record, true
		},
	)
}

func (store *FileJobStore) Complete(
	ctx context.Context,
	reservation JobReservation,
) error {
	return store.finishReservation(
		ctx,
		reservation,
		fileJobStoreEvent{
			Version: fileJobStoreEventVersion,
			Type:    "complete",
			ID:      reservation.Job.ID,
			Token:   reservation.Token,
		},
		func(record fileStoredJob) (fileStoredJob, bool) {
			return record, false
		},
	)
}

func (store *FileJobStore) Fail(
	ctx context.Context,
	reservation JobReservation,
	lastError string,
) error {
	lastError = normalizeFileJobStoreError(lastError)
	failedAt := time.Now().UTC()
	return store.finishReservation(
		ctx,
		reservation,
		fileJobStoreEvent{
			Version:  fileJobStoreEventVersion,
			Type:     "fail",
			ID:       reservation.Job.ID,
			Token:    reservation.Token,
			Error:    lastError,
			FailedAt: failedAt,
		},
		func(record fileStoredJob) (fileStoredJob, bool) {
			record.reservationToken = ""
			record.reservedUntil = time.Time{}
			record.failedAt = failedAt
			record.lastError = lastError
			return record, true
		},
	)
}

func (store *FileJobStore) finishReservation(
	ctx context.Context,
	reservation JobReservation,
	event fileJobStoreEvent,
	apply func(fileStoredJob) (fileStoredJob, bool),
) error {
	if store == nil {
		return ErrJobStoreUnavailable
	}
	if ctx == nil {
		return ErrJobContextUnavailable
	}
	if reservation.Job.ID == "" || reservation.Token == "" {
		return ErrJobReservationInvalid
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ready(ctx); err != nil {
		return err
	}
	record, exists := store.jobs[reservation.Job.ID]
	if !exists || record.reservationToken != reservation.Token {
		return ErrJobReservationInvalid
	}
	if err := store.append(event); err != nil {
		return err
	}
	updated, keep := apply(record)
	if keep {
		store.jobs[record.job.ID] = updated
	} else {
		delete(store.jobs, record.job.ID)
	}
	return nil
}

func (store *FileJobStore) FailedJobs() []FailedStoredJob {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	failed := make([]FailedStoredJob, 0)
	for _, record := range store.jobs {
		if record.failedAt.IsZero() {
			continue
		}
		failed = append(failed, FailedStoredJob{
			Job:      cloneStoredJob(record.job),
			FailedAt: record.failedAt,
			Error:    record.lastError,
		})
	}
	sort.Slice(failed, func(left int, right int) bool {
		if !failed[left].FailedAt.Equal(failed[right].FailedAt) {
			return failed[left].FailedAt.Before(failed[right].FailedAt)
		}
		return failed[left].Job.ID < failed[right].Job.ID
	})
	return failed
}

func (store *FileJobStore) RetryFailed(
	ctx context.Context,
	id string,
	availableAt time.Time,
) error {
	if store == nil {
		return ErrJobStoreUnavailable
	}
	if ctx == nil {
		return ErrJobContextUnavailable
	}
	if id == "" || availableAt.IsZero() {
		return ErrJobStoreConflict
	}
	availableAt = availableAt.UTC()

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ready(ctx); err != nil {
		return err
	}
	record, exists := store.jobs[id]
	if !exists || record.failedAt.IsZero() {
		return ErrJobStoreConflict
	}
	event := fileJobStoreEvent{
		Version:     fileJobStoreEventVersion,
		Type:        "retry",
		ID:          id,
		AvailableAt: availableAt,
	}
	if err := store.append(event); err != nil {
		return err
	}
	record.job.Attempts = 0
	record.job.AvailableAt = availableAt
	record.failedAt = time.Time{}
	record.lastError = ""
	store.jobs[id] = record
	return nil
}

func (store *FileJobStore) ForgetFailed(ctx context.Context, id string) error {
	if store == nil {
		return ErrJobStoreUnavailable
	}
	if ctx == nil {
		return ErrJobContextUnavailable
	}
	if id == "" {
		return ErrJobStoreConflict
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ready(ctx); err != nil {
		return err
	}
	record, exists := store.jobs[id]
	if !exists || record.failedAt.IsZero() {
		return ErrJobStoreConflict
	}
	if err := store.append(fileJobStoreEvent{
		Version: fileJobStoreEventVersion,
		Type:    "forget",
		ID:      id,
	}); err != nil {
		return err
	}
	delete(store.jobs, id)
	return nil
}

func (store *FileJobStore) Close() error {
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
		return fmt.Errorf("%w: close log: %w", ErrJobStoreOperationFailed, err)
	}
	return nil
}

func (store *FileJobStore) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store.closed {
		return ErrJobStoreClosed
	}
	return nil
}

func (store *FileJobStore) append(event fileJobStoreEvent) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("%w: encode event: %w", ErrJobStoreOperationFailed, err)
	}
	encoded = append(encoded, '\n')
	if err := writeFileJobStoreAll(store.file, encoded); err != nil {
		return fmt.Errorf("%w: append event: %w", ErrJobStoreOperationFailed, err)
	}
	if err := store.file.Sync(); err != nil {
		return fmt.Errorf("%w: sync event: %w", ErrJobStoreOperationFailed, err)
	}
	return nil
}

func (store *FileJobStore) replay() error {
	if _, err := store.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("%w: seek log: %w", ErrJobStoreOperationFailed, err)
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
						ErrJobStoreOperationFailed,
						truncateErr,
					)
				}
				if syncErr := store.file.Sync(); syncErr != nil {
					return fmt.Errorf(
						"%w: sync repaired log: %w",
						ErrJobStoreOperationFailed,
						syncErr,
					)
				}
			}
			break
		}
		if err != nil {
			return fmt.Errorf("%w: read log: %w", ErrJobStoreOperationFailed, err)
		}
		validBytes += int64(len(line))
		if len(line) > store.maxPayloadBytes+fileJobStoreEventOverhead {
			return ErrFileJobStoreCorrupt
		}
		line = bytes.TrimSuffix(line, []byte{'\n'})
		var event fileJobStoreEvent
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&event); decodeErr != nil {
			return fmt.Errorf("%w: %w", ErrFileJobStoreCorrupt, decodeErr)
		}
		var extra any
		if decodeErr := decoder.Decode(&extra); decodeErr != io.EOF {
			return ErrFileJobStoreCorrupt
		}
		if applyErr := store.applyEvent(event); applyErr != nil {
			return applyErr
		}
	}
	if _, err := store.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("%w: seek log end: %w", ErrJobStoreOperationFailed, err)
	}
	return nil
}

func (store *FileJobStore) applyEvent(event fileJobStoreEvent) error {
	if event.Version != fileJobStoreEventVersion {
		return ErrFileJobStoreCorrupt
	}
	switch event.Type {
	case "enqueue":
		if event.Job == nil {
			return ErrFileJobStoreCorrupt
		}
		job := event.Job.stored()
		if err := validateStoredJob(job, store.maxPayloadBytes); err != nil {
			return fmt.Errorf("%w: %w", ErrFileJobStoreCorrupt, err)
		}
		if _, exists := store.jobs[job.ID]; exists {
			return ErrFileJobStoreCorrupt
		}
		store.jobs[job.ID] = fileStoredJob{job: job}
	case "reserve":
		record, exists := store.jobs[event.ID]
		if !exists ||
			event.Token == "" ||
			event.ReservedUntil.IsZero() ||
			event.Attempts != record.job.Attempts+1 {
			return ErrFileJobStoreCorrupt
		}
		record.job.Attempts = event.Attempts
		record.reservationToken = event.Token
		record.reservedUntil = event.ReservedUntil.UTC()
		store.jobs[event.ID] = record
	case "release":
		record, ok := store.replayReservation(event)
		if !ok || event.AvailableAt.IsZero() {
			return ErrFileJobStoreCorrupt
		}
		record.job.AvailableAt = event.AvailableAt.UTC()
		record.reservationToken = ""
		record.reservedUntil = time.Time{}
		record.lastError = event.Error
		store.jobs[event.ID] = record
	case "complete":
		if _, ok := store.replayReservation(event); !ok {
			return ErrFileJobStoreCorrupt
		}
		delete(store.jobs, event.ID)
	case "fail":
		record, ok := store.replayReservation(event)
		if !ok || event.FailedAt.IsZero() {
			return ErrFileJobStoreCorrupt
		}
		record.reservationToken = ""
		record.reservedUntil = time.Time{}
		record.failedAt = event.FailedAt.UTC()
		record.lastError = event.Error
		store.jobs[event.ID] = record
	case "retry":
		record, exists := store.jobs[event.ID]
		if !exists || record.failedAt.IsZero() || event.AvailableAt.IsZero() {
			return ErrFileJobStoreCorrupt
		}
		record.job.Attempts = 0
		record.job.AvailableAt = event.AvailableAt.UTC()
		record.failedAt = time.Time{}
		record.lastError = ""
		store.jobs[event.ID] = record
	case "forget":
		record, exists := store.jobs[event.ID]
		if !exists || record.failedAt.IsZero() {
			return ErrFileJobStoreCorrupt
		}
		delete(store.jobs, event.ID)
	default:
		return ErrFileJobStoreCorrupt
	}
	return nil
}

func (store *FileJobStore) replayReservation(
	event fileJobStoreEvent,
) (fileStoredJob, bool) {
	record, exists := store.jobs[event.ID]
	if !exists || event.Token == "" || record.reservationToken != event.Token {
		return fileStoredJob{}, false
	}
	return record, true
}

func (record fileStoredJob) reservationTokenIsEmpty() bool {
	return record.reservationToken == ""
}

func reservationFromFileRecord(record fileStoredJob) JobReservation {
	return JobReservation{
		Job:           cloneStoredJob(record.job),
		Token:         record.reservationToken,
		ReservedUntil: record.reservedUntil,
	}
}

func fileJobPayloadFromStored(job StoredJob) *fileJobPayload {
	return &fileJobPayload{
		ID:          job.ID,
		Handler:     job.Handler,
		Payload:     append(json.RawMessage(nil), job.Payload...),
		AvailableAt: job.AvailableAt.UTC(),
		EnqueuedAt:  job.EnqueuedAt.UTC(),
		Attempts:    job.Attempts,
	}
}

func (payload fileJobPayload) stored() StoredJob {
	return StoredJob{
		ID:          payload.ID,
		Handler:     payload.Handler,
		Payload:     append(json.RawMessage(nil), payload.Payload...),
		AvailableAt: payload.AvailableAt.UTC(),
		EnqueuedAt:  payload.EnqueuedAt.UTC(),
		Attempts:    payload.Attempts,
	}
}

func cloneStoredJob(job StoredJob) StoredJob {
	job.Payload = append(json.RawMessage(nil), job.Payload...)
	return job
}

func validateStoredJob(job StoredJob, maxPayloadBytes int) error {
	if !validJobIdentifier(job.ID) ||
		strings.TrimSpace(job.Handler) == "" ||
		len(job.Handler) > fileJobStoreMaxHandlerBytes ||
		len(job.Payload) == 0 ||
		len(job.Payload) > maxPayloadBytes ||
		!json.Valid(job.Payload) ||
		job.AvailableAt.IsZero() ||
		job.EnqueuedAt.IsZero() ||
		job.Attempts < 0 {
		return ErrJobStoreConflict
	}
	return nil
}

func newJobIdentifier() (string, error) {
	buffer := make([]byte, jobIdentifierBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("%w: random identifier: %w", ErrJobStoreOperationFailed, err)
	}
	return hex.EncodeToString(buffer), nil
}

func validJobIdentifier(value string) bool {
	if len(value) != jobIdentifierBytes*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func normalizeFileJobStoreError(value string) string {
	if len(value) <= fileJobStoreMaxErrorBytes {
		return value
	}
	value = value[:fileJobStoreMaxErrorBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func writeFileJobStoreAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[written:]
	}
	return nil
}
