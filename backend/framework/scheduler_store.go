package framework

import (
	"context"
	"errors"
	"time"
)

var (
	ErrSchedulerStoreUnavailable        = errors.New("framework: scheduler store is unavailable")
	ErrSchedulerStoreClosed             = errors.New("framework: scheduler store is closed")
	ErrSchedulerStoreFull               = errors.New("framework: scheduler store is full")
	ErrSchedulerStoreConflict           = errors.New("framework: scheduler store record conflicts")
	ErrScheduledTaskStateNotFound       = errors.New("framework: scheduled task state is not found")
	ErrScheduledTaskNotDue              = errors.New("framework: scheduled task is not due")
	ErrScheduledTaskReserved            = errors.New("framework: scheduled task is reserved")
	ErrScheduledTaskReservationInvalid  = errors.New("framework: scheduled task reservation is invalid")
	ErrSchedulerStoreOperationFailed    = errors.New("framework: scheduler store operation failed")
	ErrInvalidFileSchedulerStoreOptions = errors.New("framework: file scheduler store options are invalid")
	ErrFileSchedulerStoreCorrupt        = errors.New("framework: file scheduler store log is corrupt")
)

type StoredScheduledTask struct {
	Name            string
	NextRunAt       time.Time
	LastScheduledAt time.Time
	LastCompletedAt time.Time
	LastError       string
	ReservedUntil   time.Time
}

type ScheduledTaskReservation struct {
	Task          StoredScheduledTask
	Token         string
	ReservedUntil time.Time
}

type SchedulerStore interface {
	Initialize(context.Context, string, time.Time) error
	State(context.Context, string) (StoredScheduledTask, error)
	Reserve(
		context.Context,
		string,
		time.Time,
		time.Duration,
	) (ScheduledTaskReservation, error)
	Complete(
		context.Context,
		ScheduledTaskReservation,
		time.Time,
		time.Time,
		string,
	) error
}
