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

const defaultSQLJobStoreTable = "bridra_jobs"

type SQLJobStoreOptions struct {
	Table            string
	PlaceholderStyle SQLPlaceholderStyle
	MaxPayloadBytes  int
}

func DefaultSQLJobStoreOptions() SQLJobStoreOptions {
	return SQLJobStoreOptions{
		Table:            defaultSQLJobStoreTable,
		PlaceholderStyle: SQLPlaceholderQuestionMark,
		MaxPayloadBytes:  defaultFileJobStoreMaxPayloadBytes,
	}
}

type SQLJobStore struct {
	pool             *sql.DB
	table            string
	placeholderStyle SQLPlaceholderStyle
	maxPayloadBytes  int
}

var _ JobStore = (*SQLJobStore)(nil)

func NewSQLJobStore(pool *sql.DB, options SQLJobStoreOptions) (*SQLJobStore, error) {
	if pool == nil {
		return nil, ErrJobStoreUnavailable
	}
	normalized, err := normalizeSQLJobStoreOptions(options)
	if err != nil {
		return nil, err
	}
	return &SQLJobStore{
		pool:             pool,
		table:            normalized.Table,
		placeholderStyle: normalized.PlaceholderStyle,
		maxPayloadBytes:  normalized.MaxPayloadBytes,
	}, nil
}

func normalizeSQLJobStoreOptions(options SQLJobStoreOptions) (SQLJobStoreOptions, error) {
	defaults := DefaultSQLJobStoreOptions()
	options.Table = strings.TrimSpace(options.Table)
	if options.Table == "" {
		options.Table = defaults.Table
	}
	if options.PlaceholderStyle == "" {
		options.PlaceholderStyle = defaults.PlaceholderStyle
	}
	if options.MaxPayloadBytes == 0 {
		options.MaxPayloadBytes = defaults.MaxPayloadBytes
	}
	if !validSQLIdentifier(options.Table) ||
		(options.PlaceholderStyle != SQLPlaceholderQuestionMark &&
			options.PlaceholderStyle != SQLPlaceholderDollar) ||
		options.MaxPayloadBytes < 0 {
		return SQLJobStoreOptions{}, ErrInvalidSQLJobStoreOptions
	}
	return options, nil
}

func (store *SQLJobStore) Table() string {
	if store == nil {
		return ""
	}
	return store.table
}

func (store *SQLJobStore) Ensure(ctx context.Context) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	_, err := store.pool.ExecContext(ctx, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s ("+
			"id VARCHAR(64) PRIMARY KEY, "+
			"handler VARCHAR(1024) NOT NULL, "+
			"payload JSON NOT NULL, "+
			"available_at BIGINT NOT NULL, "+
			"enqueued_at BIGINT NOT NULL, "+
			"attempts BIGINT NOT NULL, "+
			"reservation_token VARCHAR(64), "+
			"reserved_until BIGINT, "+
			"failed_at BIGINT, "+
			"last_error VARCHAR(4096), "+
			"UNIQUE (failed_at, available_at, reserved_until, enqueued_at, id)"+
			")",
		store.table,
	))
	if err != nil {
		return sqlJobStoreError("ensure schema", err)
	}
	return nil
}

func (store *SQLJobStore) Enqueue(ctx context.Context, job StoredJob) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if err := validateStoredJob(job, store.maxPayloadBytes); err != nil {
		return err
	}
	exists, err := store.jobExists(ctx, job.ID)
	if err != nil {
		return sqlJobStoreError("check enqueue conflict", err)
	}
	if exists {
		return ErrJobStoreConflict
	}

	_, err = store.pool.ExecContext(ctx, fmt.Sprintf(
		"INSERT INTO %s ("+
			"id, handler, payload, available_at, enqueued_at, attempts"+
			") VALUES (%s, %s, %s, %s, %s, %s)",
		store.table,
		store.placeholder(1),
		store.placeholder(2),
		store.placeholder(3),
		store.placeholder(4),
		store.placeholder(5),
		store.placeholder(6),
	),
		job.ID,
		job.Handler,
		string(job.Payload),
		sqlJobStoreTime(job.AvailableAt),
		sqlJobStoreTime(job.EnqueuedAt),
		int64(job.Attempts),
	)
	if err == nil {
		return nil
	}
	exists, conflictErr := store.jobExists(ctx, job.ID)
	if conflictErr == nil && exists {
		return ErrJobStoreConflict
	}
	if conflictErr != nil {
		return errors.Join(
			sqlJobStoreError("enqueue", err),
			sqlJobStoreError("check enqueue result", conflictErr),
		)
	}
	return sqlJobStoreError("enqueue", err)
}

func (store *SQLJobStore) Reserve(
	ctx context.Context,
	now time.Time,
	lease time.Duration,
) (JobReservation, error) {
	if err := store.ready(ctx); err != nil {
		return JobReservation{}, err
	}
	if now.IsZero() || lease <= 0 {
		return JobReservation{}, ErrJobReservationInvalid
	}
	now = now.UTC()
	reservedUntil := now.Add(lease)

	for {
		if err := ctx.Err(); err != nil {
			return JobReservation{}, err
		}
		job, err := store.readyJob(ctx, now)
		if errors.Is(err, sql.ErrNoRows) {
			return JobReservation{}, ErrJobStoreEmpty
		}
		if err != nil {
			return JobReservation{}, sqlJobStoreError("select ready job", err)
		}
		token, err := newJobIdentifier()
		if err != nil {
			return JobReservation{}, err
		}
		result, err := store.pool.ExecContext(ctx, fmt.Sprintf(
			"UPDATE %s SET reservation_token = %s, reserved_until = %s, "+
				"attempts = attempts + 1 "+
				"WHERE id = %s AND attempts = %s AND failed_at IS NULL "+
				"AND available_at <= %s "+
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
			sqlJobStoreTime(reservedUntil),
			job.ID,
			int64(job.Attempts),
			sqlJobStoreTime(now),
			sqlJobStoreTime(now),
		)
		if err != nil {
			return JobReservation{}, sqlJobStoreError("reserve job", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return JobReservation{}, sqlJobStoreError("inspect reservation", err)
		}
		if updated == 0 {
			continue
		}
		if updated != 1 {
			return JobReservation{}, sqlJobStoreError(
				"reserve job",
				fmt.Errorf("updated %d records", updated),
			)
		}
		job.Attempts++
		return JobReservation{
			Job:           job,
			Token:         token,
			ReservedUntil: reservedUntil,
		}, nil
	}
}

func (store *SQLJobStore) Release(
	ctx context.Context,
	reservation JobReservation,
	availableAt time.Time,
	lastError string,
) error {
	if availableAt.IsZero() {
		return ErrJobReservationInvalid
	}
	if store == nil {
		return ErrJobStoreUnavailable
	}
	return store.finishReservation(
		ctx,
		reservation,
		fmt.Sprintf(
			"UPDATE %s SET available_at = %s, reservation_token = NULL, "+
				"reserved_until = NULL, last_error = %s "+
				"WHERE id = %s AND reservation_token = %s",
			store.table,
			store.placeholder(1),
			store.placeholder(2),
			store.placeholder(3),
			store.placeholder(4),
		),
		[]any{
			sqlJobStoreTime(availableAt.UTC()),
			normalizeFileJobStoreError(lastError),
			reservation.Job.ID,
			reservation.Token,
		},
		"release job",
	)
}

func (store *SQLJobStore) Complete(
	ctx context.Context,
	reservation JobReservation,
) error {
	if store == nil {
		return ErrJobStoreUnavailable
	}
	return store.finishReservation(
		ctx,
		reservation,
		fmt.Sprintf(
			"DELETE FROM %s WHERE id = %s AND reservation_token = %s",
			store.table,
			store.placeholder(1),
			store.placeholder(2),
		),
		[]any{reservation.Job.ID, reservation.Token},
		"complete job",
	)
}

func (store *SQLJobStore) Fail(
	ctx context.Context,
	reservation JobReservation,
	lastError string,
) error {
	if store == nil {
		return ErrJobStoreUnavailable
	}
	return store.finishReservation(
		ctx,
		reservation,
		fmt.Sprintf(
			"UPDATE %s SET reservation_token = NULL, reserved_until = NULL, "+
				"failed_at = %s, last_error = %s "+
				"WHERE id = %s AND reservation_token = %s",
			store.table,
			store.placeholder(1),
			store.placeholder(2),
			store.placeholder(3),
			store.placeholder(4),
		),
		[]any{
			sqlJobStoreTime(time.Now().UTC()),
			normalizeFileJobStoreError(lastError),
			reservation.Job.ID,
			reservation.Token,
		},
		"fail job",
	)
}

func (store *SQLJobStore) FailedJobs(
	ctx context.Context,
) ([]FailedStoredJob, error) {
	if err := store.ready(ctx); err != nil {
		return nil, err
	}
	rows, err := store.pool.QueryContext(ctx, fmt.Sprintf(
		"SELECT id, handler, payload, available_at, enqueued_at, attempts, "+
			"failed_at, last_error FROM %s "+
			"WHERE failed_at IS NOT NULL ORDER BY failed_at ASC, id ASC",
		store.table,
	))
	if err != nil {
		return nil, sqlJobStoreError("query failed jobs", err)
	}
	defer rows.Close()

	failed := make([]FailedStoredJob, 0)
	for rows.Next() {
		job, failedAt, lastError, err := scanSQLFailedJob(rows, store.maxPayloadBytes)
		if err != nil {
			return nil, sqlJobStoreError("scan failed job", err)
		}
		failed = append(failed, FailedStoredJob{
			Job:      job,
			FailedAt: failedAt,
			Error:    lastError,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, sqlJobStoreError("iterate failed jobs", err)
	}
	if err := rows.Close(); err != nil {
		return nil, sqlJobStoreError("close failed jobs", err)
	}
	return failed, nil
}

func (store *SQLJobStore) RetryFailed(
	ctx context.Context,
	id string,
	availableAt time.Time,
) error {
	if id == "" || availableAt.IsZero() {
		return ErrJobStoreConflict
	}
	if err := store.ready(ctx); err != nil {
		return err
	}
	result, err := store.pool.ExecContext(ctx, fmt.Sprintf(
		"UPDATE %s SET attempts = 0, available_at = %s, "+
			"reservation_token = NULL, reserved_until = NULL, "+
			"failed_at = NULL, last_error = NULL "+
			"WHERE id = %s AND failed_at IS NOT NULL",
		store.table,
		store.placeholder(1),
		store.placeholder(2),
	), sqlJobStoreTime(availableAt.UTC()), id)
	if err != nil {
		return sqlJobStoreError("retry failed job", err)
	}
	return store.requireOneAffected(result, "retry failed job", ErrJobStoreConflict)
}

func (store *SQLJobStore) ForgetFailed(ctx context.Context, id string) error {
	if id == "" {
		return ErrJobStoreConflict
	}
	if err := store.ready(ctx); err != nil {
		return err
	}
	result, err := store.pool.ExecContext(ctx, fmt.Sprintf(
		"DELETE FROM %s WHERE id = %s AND failed_at IS NOT NULL",
		store.table,
		store.placeholder(1),
	), id)
	if err != nil {
		return sqlJobStoreError("forget failed job", err)
	}
	return store.requireOneAffected(result, "forget failed job", ErrJobStoreConflict)
}

func (store *SQLJobStore) readyJob(
	ctx context.Context,
	now time.Time,
) (StoredJob, error) {
	row := store.pool.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT id, handler, payload, available_at, enqueued_at, attempts "+
			"FROM %s WHERE failed_at IS NULL AND available_at <= %s "+
			"AND (reservation_token IS NULL OR reserved_until <= %s) "+
			"ORDER BY available_at ASC, enqueued_at ASC, id ASC LIMIT 1",
		store.table,
		store.placeholder(1),
		store.placeholder(2),
	), sqlJobStoreTime(now), sqlJobStoreTime(now))
	return scanSQLStoredJob(row, store.maxPayloadBytes)
}

func (store *SQLJobStore) finishReservation(
	ctx context.Context,
	reservation JobReservation,
	query string,
	args []any,
	operation string,
) error {
	if reservation.Job.ID == "" || reservation.Token == "" {
		return ErrJobReservationInvalid
	}
	if err := store.ready(ctx); err != nil {
		return err
	}
	result, err := store.pool.ExecContext(ctx, query, args...)
	if err != nil {
		return sqlJobStoreError(operation, err)
	}
	return store.requireOneAffected(result, operation, ErrJobReservationInvalid)
}

func (store *SQLJobStore) requireOneAffected(
	result sql.Result,
	operation string,
	zeroError error,
) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return sqlJobStoreError("inspect "+operation, err)
	}
	if affected == 0 {
		return zeroError
	}
	if affected != 1 {
		return sqlJobStoreError(
			operation,
			fmt.Errorf("updated %d records", affected),
		)
	}
	return nil
}

func (store *SQLJobStore) jobExists(ctx context.Context, id string) (bool, error) {
	var marker int
	err := store.pool.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT 1 FROM %s WHERE id = %s",
		store.table,
		store.placeholder(1),
	), id).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (store *SQLJobStore) ready(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return ErrJobStoreUnavailable
	}
	if ctx == nil {
		return ErrJobContextUnavailable
	}
	return ctx.Err()
}

func (store *SQLJobStore) placeholder(index int) string {
	if store.placeholderStyle == SQLPlaceholderDollar {
		return "$" + strconv.Itoa(index)
	}
	return "?"
}

func sqlJobStoreTime(value time.Time) int64 {
	return value.UTC().UnixMicro()
}

type sqlJobScanner interface {
	Scan(...any) error
}

func scanSQLStoredJob(
	scanner sqlJobScanner,
	maxPayloadBytes int,
) (StoredJob, error) {
	var job StoredJob
	var payload []byte
	var availableAt int64
	var enqueuedAt int64
	var attempts int64
	if err := scanner.Scan(
		&job.ID,
		&job.Handler,
		&payload,
		&availableAt,
		&enqueuedAt,
		&attempts,
	); err != nil {
		return StoredJob{}, err
	}
	if attempts < 0 || uint64(attempts) > uint64(^uint(0)>>1) {
		return StoredJob{}, errors.New("invalid SQL job attempt count")
	}
	job.Payload = append(job.Payload, payload...)
	job.AvailableAt = time.UnixMicro(availableAt).UTC()
	job.EnqueuedAt = time.UnixMicro(enqueuedAt).UTC()
	job.Attempts = int(attempts)
	if err := validateStoredJob(job, maxPayloadBytes); err != nil {
		return StoredJob{}, err
	}
	return job, nil
}

func scanSQLFailedJob(
	scanner sqlJobScanner,
	maxPayloadBytes int,
) (StoredJob, time.Time, string, error) {
	var job StoredJob
	var payload []byte
	var availableAt int64
	var enqueuedAt int64
	var attempts int64
	var failedAt sql.NullInt64
	var lastError sql.NullString
	if err := scanner.Scan(
		&job.ID,
		&job.Handler,
		&payload,
		&availableAt,
		&enqueuedAt,
		&attempts,
		&failedAt,
		&lastError,
	); err != nil {
		return StoredJob{}, time.Time{}, "", err
	}
	if !failedAt.Valid ||
		attempts < 0 ||
		uint64(attempts) > uint64(^uint(0)>>1) {
		return StoredJob{}, time.Time{}, "", errors.New("invalid failed SQL job")
	}
	job.Payload = append(job.Payload, payload...)
	job.AvailableAt = time.UnixMicro(availableAt).UTC()
	job.EnqueuedAt = time.UnixMicro(enqueuedAt).UTC()
	job.Attempts = int(attempts)
	if err := validateStoredJob(job, maxPayloadBytes); err != nil {
		return StoredJob{}, time.Time{}, "", err
	}
	return job, time.UnixMicro(failedAt.Int64).UTC(), lastError.String, nil
}

func sqlJobStoreError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrJobStoreOperationFailed, operation, err)
}
