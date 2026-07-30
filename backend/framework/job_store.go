package framework

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrJobStoreUnavailable         = errors.New("framework: job store is unavailable")
	ErrJobStoreClosed              = errors.New("framework: job store is closed")
	ErrJobStoreEmpty               = errors.New("framework: job store has no ready jobs")
	ErrJobStoreFull                = errors.New("framework: job store is full")
	ErrJobStoreConflict            = errors.New("framework: job store record conflicts")
	ErrJobReservationInvalid       = errors.New("framework: job reservation is invalid")
	ErrJobPayloadEncodingFailed    = errors.New("framework: job payload encoding failed")
	ErrJobPayloadDecodingFailed    = errors.New("framework: job payload decoding failed")
	ErrJobStoreOperationFailed     = errors.New("framework: job store operation failed")
	ErrInvalidFileJobStoreOptions  = errors.New("framework: file job store options are invalid")
	ErrFileJobStoreCorrupt         = errors.New("framework: file job store log is corrupt")
	ErrInvalidSQLJobStoreOptions   = errors.New("framework: SQL job store options are invalid")
	ErrInvalidRedisJobStoreOptions = errors.New(
		"framework: Redis job store options are invalid",
	)
)

type StoredJob struct {
	ID          string
	Handler     string
	Payload     json.RawMessage
	AvailableAt time.Time
	EnqueuedAt  time.Time
	Attempts    int
}

type JobReservation struct {
	Job           StoredJob
	Token         string
	ReservedUntil time.Time
}

type JobStore interface {
	Enqueue(context.Context, StoredJob) error
	Reserve(context.Context, time.Time, time.Duration) (JobReservation, error)
	Release(context.Context, JobReservation, time.Time, string) error
	Complete(context.Context, JobReservation) error
	Fail(context.Context, JobReservation, string) error
}

type FailedStoredJob struct {
	Job      StoredJob
	FailedAt time.Time
	Error    string
}
