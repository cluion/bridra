package framework

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultSQLSchedulerStoreTable = "bridra_scheduled_tasks"
	sqlSchedulerStoreMaxNameBytes = 255
)

type SQLSchedulerStoreOptions struct {
	Table            string
	PlaceholderStyle SQLPlaceholderStyle
}

func DefaultSQLSchedulerStoreOptions() SQLSchedulerStoreOptions {
	return SQLSchedulerStoreOptions{
		Table:            defaultSQLSchedulerStoreTable,
		PlaceholderStyle: SQLPlaceholderQuestionMark,
	}
}

type SQLSchedulerStore struct {
	pool             *sql.DB
	table            string
	placeholderStyle SQLPlaceholderStyle
}

var _ SchedulerStore = (*SQLSchedulerStore)(nil)

func NewSQLSchedulerStore(
	pool *sql.DB,
	options SQLSchedulerStoreOptions,
) (*SQLSchedulerStore, error) {
	if pool == nil {
		return nil, ErrSchedulerStoreUnavailable
	}
	normalized, err := normalizeSQLSchedulerStoreOptions(options)
	if err != nil {
		return nil, err
	}
	return &SQLSchedulerStore{
		pool:             pool,
		table:            normalized.Table,
		placeholderStyle: normalized.PlaceholderStyle,
	}, nil
}

func normalizeSQLSchedulerStoreOptions(
	options SQLSchedulerStoreOptions,
) (SQLSchedulerStoreOptions, error) {
	defaults := DefaultSQLSchedulerStoreOptions()
	options.Table = strings.TrimSpace(options.Table)
	if options.Table == "" {
		options.Table = defaults.Table
	}
	if options.PlaceholderStyle == "" {
		options.PlaceholderStyle = defaults.PlaceholderStyle
	}
	if !validSQLIdentifier(options.Table) ||
		(options.PlaceholderStyle != SQLPlaceholderQuestionMark &&
			options.PlaceholderStyle != SQLPlaceholderDollar) {
		return SQLSchedulerStoreOptions{}, ErrInvalidSQLSchedulerStoreOptions
	}
	return options, nil
}

func (store *SQLSchedulerStore) Table() string {
	if store == nil {
		return ""
	}
	return store.table
}

func (store *SQLSchedulerStore) Ensure(ctx context.Context) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	_, err := store.pool.ExecContext(ctx, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s ("+
			"name VARCHAR(255) PRIMARY KEY, "+
			"next_run_at BIGINT NOT NULL, "+
			"last_scheduled_at BIGINT, "+
			"last_completed_at BIGINT, "+
			"last_error VARCHAR(4096), "+
			"reservation_token VARCHAR(64), "+
			"reserved_until BIGINT"+
			")",
		store.table,
	))
	if err != nil {
		return sqlSchedulerStoreError("ensure schema", err)
	}
	return nil
}

func (store *SQLSchedulerStore) Initialize(
	ctx context.Context,
	name string,
	nextRunAt time.Time,
) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if !validSQLScheduledTaskName(name) || nextRunAt.IsZero() {
		return ErrSchedulerStoreConflict
	}
	exists, err := store.stateExists(ctx, name)
	if err != nil {
		return sqlSchedulerStoreError("check initialization conflict", err)
	}
	if exists {
		return nil
	}

	_, err = store.pool.ExecContext(ctx, fmt.Sprintf(
		"INSERT INTO %s (name, next_run_at) VALUES (%s, %s)",
		store.table,
		store.placeholder(1),
		store.placeholder(2),
	), name, sqlSchedulerStoreTime(nextRunAt))
	if err == nil {
		return nil
	}
	exists, conflictErr := store.stateExists(ctx, name)
	if conflictErr == nil && exists {
		return nil
	}
	if conflictErr != nil {
		return errors.Join(
			sqlSchedulerStoreError("initialize task", err),
			sqlSchedulerStoreError("check initialization result", conflictErr),
		)
	}
	return sqlSchedulerStoreError("initialize task", err)
}

func (store *SQLSchedulerStore) State(
	ctx context.Context,
	name string,
) (StoredScheduledTask, error) {
	if err := store.ready(ctx); err != nil {
		return StoredScheduledTask{}, err
	}
	if !validSQLScheduledTaskName(name) {
		return StoredScheduledTask{}, ErrSchedulerStoreConflict
	}
	record, err := store.storedTask(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredScheduledTask{}, ErrScheduledTaskStateNotFound
	}
	if err != nil {
		return StoredScheduledTask{}, sqlSchedulerStoreError("read task state", err)
	}
	return record.state, nil
}

func (store *SQLSchedulerStore) Reserve(
	ctx context.Context,
	name string,
	now time.Time,
	lease time.Duration,
) (ScheduledTaskReservation, error) {
	if err := store.ready(ctx); err != nil {
		return ScheduledTaskReservation{}, err
	}
	if !validSQLScheduledTaskName(name) || now.IsZero() || lease <= 0 {
		return ScheduledTaskReservation{}, ErrScheduledTaskReservationInvalid
	}
	now = now.UTC()
	reservedUntil := now.Add(lease)

	for {
		if err := ctx.Err(); err != nil {
			return ScheduledTaskReservation{}, err
		}
		record, err := store.storedTask(ctx, name)
		if errors.Is(err, sql.ErrNoRows) {
			return ScheduledTaskReservation{}, ErrScheduledTaskStateNotFound
		}
		if err != nil {
			return ScheduledTaskReservation{}, sqlSchedulerStoreError(
				"read reservation state",
				err,
			)
		}
		if record.reservationToken != "" &&
			record.state.ReservedUntil.After(now) {
			return ScheduledTaskReservation{}, ErrScheduledTaskReserved
		}
		if record.state.NextRunAt.After(now) {
			return ScheduledTaskReservation{}, ErrScheduledTaskNotDue
		}
		token, err := newSchedulerReservationToken()
		if err != nil {
			return ScheduledTaskReservation{}, err
		}
		result, err := store.pool.ExecContext(ctx, fmt.Sprintf(
			"UPDATE %s SET reservation_token = %s, reserved_until = %s "+
				"WHERE name = %s AND next_run_at = %s AND next_run_at <= %s "+
				"AND (reservation_token IS NULL OR reserved_until <= %s)",
			store.table,
			store.placeholder(1),
			store.placeholder(2),
			store.placeholder(3),
			store.placeholder(4),
			store.placeholder(5),
			store.placeholder(6),
		),
			token,
			sqlSchedulerStoreTime(reservedUntil),
			name,
			sqlSchedulerStoreTime(record.state.NextRunAt),
			sqlSchedulerStoreTime(now),
			sqlSchedulerStoreTime(now),
		)
		if err != nil {
			return ScheduledTaskReservation{}, sqlSchedulerStoreError(
				"reserve task",
				err,
			)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return ScheduledTaskReservation{}, sqlSchedulerStoreError(
				"inspect reservation",
				err,
			)
		}
		if affected == 0 {
			continue
		}
		if affected != 1 {
			return ScheduledTaskReservation{}, sqlSchedulerStoreError(
				"reserve task",
				fmt.Errorf("updated %d records", affected),
			)
		}
		record.state.ReservedUntil = reservedUntil
		return ScheduledTaskReservation{
			Task:          record.state,
			Token:         token,
			ReservedUntil: reservedUntil,
		}, nil
	}
}

func (store *SQLSchedulerStore) Complete(
	ctx context.Context,
	reservation ScheduledTaskReservation,
	nextRunAt time.Time,
	completedAt time.Time,
	lastError string,
) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if !validSQLScheduledTaskName(reservation.Task.Name) ||
		reservation.Token == "" ||
		reservation.Task.NextRunAt.IsZero() ||
		nextRunAt.IsZero() ||
		completedAt.IsZero() ||
		!nextRunAt.After(completedAt) {
		return ErrScheduledTaskReservationInvalid
	}
	result, err := store.pool.ExecContext(ctx, fmt.Sprintf(
		"UPDATE %s SET next_run_at = %s, last_scheduled_at = %s, "+
			"last_completed_at = %s, last_error = %s, "+
			"reservation_token = NULL, reserved_until = NULL "+
			"WHERE name = %s AND reservation_token = %s AND next_run_at = %s",
		store.table,
		store.placeholder(1),
		store.placeholder(2),
		store.placeholder(3),
		store.placeholder(4),
		store.placeholder(5),
		store.placeholder(6),
		store.placeholder(7),
	),
		sqlSchedulerStoreTime(nextRunAt),
		sqlSchedulerStoreTime(reservation.Task.NextRunAt),
		sqlSchedulerStoreTime(completedAt),
		normalizeFileSchedulerStoreError(lastError),
		reservation.Task.Name,
		reservation.Token,
		sqlSchedulerStoreTime(reservation.Task.NextRunAt),
	)
	if err != nil {
		return sqlSchedulerStoreError("complete task", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return sqlSchedulerStoreError("inspect completion", err)
	}
	if affected == 0 {
		return ErrScheduledTaskReservationInvalid
	}
	if affected != 1 {
		return sqlSchedulerStoreError(
			"complete task",
			fmt.Errorf("updated %d records", affected),
		)
	}
	return nil
}

type sqlStoredScheduledTask struct {
	state            StoredScheduledTask
	reservationToken string
}

func (store *SQLSchedulerStore) storedTask(
	ctx context.Context,
	name string,
) (sqlStoredScheduledTask, error) {
	row := store.pool.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT name, next_run_at, last_scheduled_at, last_completed_at, "+
			"last_error, reservation_token, reserved_until "+
			"FROM %s WHERE name = %s",
		store.table,
		store.placeholder(1),
	), name)
	return scanSQLStoredScheduledTask(row)
}

func scanSQLStoredScheduledTask(
	scanner sqlJobScanner,
) (sqlStoredScheduledTask, error) {
	var record sqlStoredScheduledTask
	var nextRunAt int64
	var lastScheduledAt sql.NullInt64
	var lastCompletedAt sql.NullInt64
	var lastError sql.NullString
	var reservationToken sql.NullString
	var reservedUntil sql.NullInt64
	if err := scanner.Scan(
		&record.state.Name,
		&nextRunAt,
		&lastScheduledAt,
		&lastCompletedAt,
		&lastError,
		&reservationToken,
		&reservedUntil,
	); err != nil {
		return sqlStoredScheduledTask{}, err
	}
	if !validSQLScheduledTaskName(record.state.Name) ||
		lastScheduledAt.Valid != lastCompletedAt.Valid ||
		reservationToken.Valid != reservedUntil.Valid ||
		(reservationToken.Valid && reservationToken.String == "") {
		return sqlStoredScheduledTask{}, errors.New("invalid SQL scheduled task")
	}
	record.state.NextRunAt = time.UnixMicro(nextRunAt).UTC()
	if lastScheduledAt.Valid {
		record.state.LastScheduledAt = time.UnixMicro(lastScheduledAt.Int64).UTC()
		record.state.LastCompletedAt = time.UnixMicro(lastCompletedAt.Int64).UTC()
	}
	record.state.LastError = lastError.String
	if reservationToken.Valid {
		record.reservationToken = reservationToken.String
		record.state.ReservedUntil = time.UnixMicro(reservedUntil.Int64).UTC()
	}
	return record, nil
}

func (store *SQLSchedulerStore) stateExists(
	ctx context.Context,
	name string,
) (bool, error) {
	var marker int
	err := store.pool.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT 1 FROM %s WHERE name = %s",
		store.table,
		store.placeholder(1),
	), name).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func validSQLScheduledTaskName(name string) bool {
	return validStoredScheduledTaskName(name) &&
		len(name) <= sqlSchedulerStoreMaxNameBytes
}

func (store *SQLSchedulerStore) ready(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return ErrSchedulerStoreUnavailable
	}
	if ctx == nil {
		return ErrSchedulerContextUnavailable
	}
	return ctx.Err()
}

func (store *SQLSchedulerStore) placeholder(index int) string {
	if store.placeholderStyle == SQLPlaceholderDollar {
		return "$" + strconv.Itoa(index)
	}
	return "?"
}

func sqlSchedulerStoreTime(value time.Time) int64 {
	return value.UTC().UnixMicro()
}

func sqlSchedulerStoreError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrSchedulerStoreOperationFailed, operation, err)
}
