package framework

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

var (
	ErrMigrationRunnerUnavailable  = errors.New("framework: migration runner is unavailable")
	ErrMigrationContextUnavailable = errors.New("framework: migration context is unavailable")
	ErrMigrationStoreUnavailable   = errors.New("framework: migration store is unavailable")
	ErrInvalidMigration            = errors.New("framework: migration is invalid")
	ErrMigrationAlreadyDefined     = errors.New("framework: migration is already defined")
	ErrMigrationRegistrationClosed = errors.New("framework: migration registration is closed")
	ErrMigrationStoreFailed        = errors.New("framework: migration store operation failed")
	ErrMigrationHistoryInvalid     = errors.New("framework: migration history is invalid")
	ErrMigrationDefinitionMissing  = errors.New("framework: applied migration definition is missing")
	ErrMigrationNameMismatch       = errors.New("framework: applied migration name does not match")
	ErrMigrationDownUnavailable    = errors.New("framework: migration down callback is unavailable")
	ErrMigrationExecutionFailed    = errors.New("framework: migration execution failed")
	ErrMigrationRevertFailed       = errors.New("framework: migration revert failed")
	ErrMigrationPanicked           = errors.New("framework: migration callback panicked")
)

type MigrationFunc func(context.Context, SQLExecutor) error

type Migration struct {
	Version            string
	Name               string
	Up                 MigrationFunc
	Down               MigrationFunc
	DisableTransaction bool
}

type AppliedMigration struct {
	Version string
	Name    string
	Batch   int64
}

type MigrationRunResult struct {
	Batch   int64
	Applied []AppliedMigration
}

type MigrationRollbackResult struct {
	Batch      int64
	RolledBack []AppliedMigration
}

type MigrationStatus struct {
	Version string
	Name    string
	Applied bool
	Batch   int64
}

type MigrationDirection string

const (
	MigrationDirectionUp   MigrationDirection = "up"
	MigrationDirectionDown MigrationDirection = "down"
)

type MigrationFailure struct {
	Direction MigrationDirection
	Migration AppliedMigration
	Err       error
}

func (failure *MigrationFailure) Error() string {
	if failure == nil {
		return "migration failed"
	}
	return fmt.Sprintf(
		"migration %s failed for %q (%s): %v",
		failure.Direction,
		failure.Migration.Name,
		failure.Migration.Version,
		failure.Err,
	)
}

func (failure *MigrationFailure) Is(target error) bool {
	if failure == nil {
		return false
	}
	switch failure.Direction {
	case MigrationDirectionUp:
		return target == ErrMigrationExecutionFailed
	case MigrationDirectionDown:
		return target == ErrMigrationRevertFailed
	default:
		return false
	}
}

func (failure *MigrationFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

type MigrationStore interface {
	Ensure(context.Context, SQLExecutor) error
	Applied(context.Context, SQLExecutor) ([]AppliedMigration, error)
	Record(context.Context, SQLExecutor, AppliedMigration) error
	Remove(context.Context, SQLExecutor, string) error
}

type MigrationRunner struct {
	database           *Database
	store              MigrationStore
	migrations         map[string]Migration
	registrationClosed bool
	mu                 sync.Mutex
	operationMu        sync.Mutex
}

func NewMigrationRunner(database *Database, store MigrationStore) (*MigrationRunner, error) {
	if database == nil || database.pool == nil {
		return nil, ErrDatabaseUnavailable
	}
	if store == nil {
		return nil, ErrMigrationStoreUnavailable
	}
	return &MigrationRunner{
		database:   database,
		store:      store,
		migrations: make(map[string]Migration),
	}, nil
}

func (runner *MigrationRunner) Register(migrations ...Migration) error {
	if runner == nil {
		return ErrMigrationRunnerUnavailable
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.registrationClosed {
		return ErrMigrationRegistrationClosed
	}

	normalized := make([]Migration, 0, len(migrations))
	seen := make(map[string]struct{}, len(migrations))
	for _, migration := range migrations {
		migration.Version = strings.TrimSpace(migration.Version)
		migration.Name = strings.TrimSpace(migration.Name)
		if migration.Version == "" || len(migration.Version) > 255 ||
			migration.Name == "" || len(migration.Name) > 255 || migration.Up == nil {
			return ErrInvalidMigration
		}
		if _, exists := runner.migrations[migration.Version]; exists {
			return fmt.Errorf("%w: %s", ErrMigrationAlreadyDefined, migration.Version)
		}
		if _, exists := seen[migration.Version]; exists {
			return fmt.Errorf("%w: %s", ErrMigrationAlreadyDefined, migration.Version)
		}
		seen[migration.Version] = struct{}{}
		normalized = append(normalized, migration)
	}
	for _, migration := range normalized {
		runner.migrations[migration.Version] = migration
	}
	return nil
}

func (runner *MigrationRunner) Registered() []Migration {
	if runner == nil {
		return nil
	}
	runner.mu.Lock()
	migrations := runner.migrationSnapshotLocked()
	runner.mu.Unlock()
	return migrations
}

func (runner *MigrationRunner) Status(ctx context.Context) ([]MigrationStatus, error) {
	if runner == nil {
		return nil, ErrMigrationRunnerUnavailable
	}
	if err := runner.validateContext(ctx); err != nil {
		return nil, err
	}
	runner.operationMu.Lock()
	defer runner.operationMu.Unlock()

	migrations := runner.snapshot(false)
	applied, err := runner.loadApplied(ctx, migrations)
	if err != nil {
		return nil, err
	}
	byVersion := make(map[string]AppliedMigration, len(applied))
	for _, record := range applied {
		byVersion[record.Version] = record
	}
	statuses := make([]MigrationStatus, 0, len(migrations))
	for _, migration := range migrations {
		record, exists := byVersion[migration.Version]
		statuses = append(statuses, MigrationStatus{
			Version: migration.Version,
			Name:    migration.Name,
			Applied: exists,
			Batch:   record.Batch,
		})
	}
	return statuses, nil
}

func (runner *MigrationRunner) Migrate(ctx context.Context) (MigrationRunResult, error) {
	if runner == nil {
		return MigrationRunResult{}, ErrMigrationRunnerUnavailable
	}
	if err := runner.validateContext(ctx); err != nil {
		return MigrationRunResult{}, err
	}
	runner.operationMu.Lock()
	defer runner.operationMu.Unlock()

	migrations := runner.snapshot(true)
	applied, err := runner.loadApplied(ctx, migrations)
	if err != nil {
		return MigrationRunResult{}, err
	}
	appliedVersions := make(map[string]struct{}, len(applied))
	var latestBatch int64
	for _, record := range applied {
		appliedVersions[record.Version] = struct{}{}
		if record.Batch > latestBatch {
			latestBatch = record.Batch
		}
	}
	if latestBatch == math.MaxInt64 {
		return MigrationRunResult{}, fmt.Errorf(
			"%w: migration batch overflow",
			ErrMigrationHistoryInvalid,
		)
	}

	pending := make([]Migration, 0, len(migrations))
	for _, migration := range migrations {
		if _, exists := appliedVersions[migration.Version]; !exists {
			pending = append(pending, migration)
		}
	}
	if len(pending) == 0 {
		return MigrationRunResult{}, nil
	}

	result := MigrationRunResult{Batch: latestBatch + 1}
	for _, migration := range pending {
		record := AppliedMigration{
			Version: migration.Version,
			Name:    migration.Name,
			Batch:   result.Batch,
		}
		err := runner.execute(ctx, migration.DisableTransaction, func(
			operationContext context.Context,
			executor SQLExecutor,
		) error {
			if err := invokeMigration(operationContext, executor, migration.Up); err != nil {
				return err
			}
			if err := runner.store.Record(operationContext, executor, record); err != nil {
				return migrationStoreError("record", err)
			}
			return nil
		})
		if err != nil {
			return result, &MigrationFailure{
				Direction: MigrationDirectionUp,
				Migration: record,
				Err:       err,
			}
		}
		result.Applied = append(result.Applied, record)
	}
	return result, nil
}

func (runner *MigrationRunner) Rollback(ctx context.Context) (MigrationRollbackResult, error) {
	if runner == nil {
		return MigrationRollbackResult{}, ErrMigrationRunnerUnavailable
	}
	if err := runner.validateContext(ctx); err != nil {
		return MigrationRollbackResult{}, err
	}
	runner.operationMu.Lock()
	defer runner.operationMu.Unlock()

	migrations := runner.snapshot(true)
	applied, err := runner.loadApplied(ctx, migrations)
	if err != nil {
		return MigrationRollbackResult{}, err
	}
	var latestBatch int64
	for _, record := range applied {
		if record.Batch > latestBatch {
			latestBatch = record.Batch
		}
	}
	if latestBatch == 0 {
		return MigrationRollbackResult{}, nil
	}

	definitions := make(map[string]Migration, len(migrations))
	for _, migration := range migrations {
		definitions[migration.Version] = migration
	}
	batch := make([]AppliedMigration, 0)
	for index := len(applied) - 1; index >= 0; index-- {
		record := applied[index]
		if record.Batch != latestBatch {
			continue
		}
		migration := definitions[record.Version]
		if migration.Down == nil {
			return MigrationRollbackResult{}, fmt.Errorf(
				"%w: %s",
				ErrMigrationDownUnavailable,
				record.Version,
			)
		}
		batch = append(batch, record)
	}

	result := MigrationRollbackResult{Batch: latestBatch}
	for _, record := range batch {
		migration := definitions[record.Version]
		err := runner.execute(ctx, migration.DisableTransaction, func(
			operationContext context.Context,
			executor SQLExecutor,
		) error {
			if err := invokeMigration(operationContext, executor, migration.Down); err != nil {
				return err
			}
			if err := runner.store.Remove(operationContext, executor, record.Version); err != nil {
				return migrationStoreError("remove", err)
			}
			return nil
		})
		if err != nil {
			return result, &MigrationFailure{
				Direction: MigrationDirectionDown,
				Migration: record,
				Err:       err,
			}
		}
		result.RolledBack = append(result.RolledBack, record)
	}
	return result, nil
}

func (runner *MigrationRunner) validateContext(ctx context.Context) error {
	if ctx == nil {
		return ErrMigrationContextUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, active := runner.database.transactionFromContext(ctx); active {
		return ErrTransactionAlreadyActive
	}
	return nil
}

func (runner *MigrationRunner) snapshot(closeRegistration bool) []Migration {
	runner.mu.Lock()
	if closeRegistration {
		runner.registrationClosed = true
	}
	migrations := runner.migrationSnapshotLocked()
	runner.mu.Unlock()
	return migrations
}

func (runner *MigrationRunner) migrationSnapshotLocked() []Migration {
	migrations := make([]Migration, 0, len(runner.migrations))
	for _, migration := range runner.migrations {
		migrations = append(migrations, migration)
	}
	sort.Slice(migrations, func(left int, right int) bool {
		return migrations[left].Version < migrations[right].Version
	})
	return migrations
}

func (runner *MigrationRunner) loadApplied(
	ctx context.Context,
	migrations []Migration,
) ([]AppliedMigration, error) {
	executor, err := runner.database.Executor(ctx)
	if err != nil {
		return nil, err
	}
	if err := runner.store.Ensure(ctx, executor); err != nil {
		return nil, migrationStoreError("ensure", err)
	}
	applied, err := runner.store.Applied(ctx, executor)
	if err != nil {
		return nil, migrationStoreError("list applied", err)
	}
	definitions := make(map[string]Migration, len(migrations))
	for _, migration := range migrations {
		definitions[migration.Version] = migration
	}
	seen := make(map[string]struct{}, len(applied))
	for _, record := range applied {
		if strings.TrimSpace(record.Version) == "" || strings.TrimSpace(record.Name) == "" || record.Batch <= 0 {
			return nil, fmt.Errorf("%w: malformed applied migration", ErrMigrationHistoryInvalid)
		}
		if _, exists := seen[record.Version]; exists {
			return nil, fmt.Errorf(
				"%w: duplicate version %s",
				ErrMigrationHistoryInvalid,
				record.Version,
			)
		}
		seen[record.Version] = struct{}{}
		migration, exists := definitions[record.Version]
		if !exists {
			return nil, fmt.Errorf("%w: %s", ErrMigrationDefinitionMissing, record.Version)
		}
		if migration.Name != record.Name {
			return nil, fmt.Errorf(
				"%w: %s has stored name %q and registered name %q",
				ErrMigrationNameMismatch,
				record.Version,
				record.Name,
				migration.Name,
			)
		}
	}
	sort.Slice(applied, func(left int, right int) bool {
		return applied[left].Version < applied[right].Version
	})
	return applied, nil
}

func (runner *MigrationRunner) execute(
	ctx context.Context,
	disableTransaction bool,
	operation func(context.Context, SQLExecutor) error,
) error {
	if disableTransaction {
		executor, err := runner.database.Executor(ctx)
		if err != nil {
			return err
		}
		return operation(ctx, executor)
	}
	return runner.database.WithinTransaction(ctx, nil, func(transactionContext context.Context) error {
		executor, err := runner.database.Executor(transactionContext)
		if err != nil {
			return err
		}
		return operation(transactionContext, executor)
	})
}

func invokeMigration(ctx context.Context, executor SQLExecutor, callback MigrationFunc) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if cause, ok := recovered.(error); ok {
				err = fmt.Errorf("%w: %w", ErrMigrationPanicked, cause)
				return
			}
			err = fmt.Errorf("%w: %v", ErrMigrationPanicked, recovered)
		}
	}()
	return callback(ctx, executor)
}

func migrationStoreError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrMigrationStoreFailed, operation, err)
}
