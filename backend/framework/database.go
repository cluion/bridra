package framework

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrDatabaseUnavailable            = errors.New("framework: database is unavailable")
	ErrDatabaseContextUnavailable     = errors.New("framework: database context is unavailable")
	ErrDatabasePingFailed             = errors.New("framework: database ping failed")
	ErrDatabaseCloseFailed            = errors.New("framework: database close failed")
	ErrTransactionCallbackUnavailable = errors.New("framework: transaction callback is unavailable")
	ErrTransactionAlreadyActive       = errors.New("framework: transaction is already active")
	ErrTransactionBeginFailed         = errors.New("framework: transaction begin failed")
	ErrTransactionCommitFailed        = errors.New("framework: transaction commit failed")
	ErrTransactionRollbackFailed      = errors.New("framework: transaction rollback failed")
)

type SQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

var (
	_ SQLExecutor = (*sql.DB)(nil)
	_ SQLExecutor = (*sql.Tx)(nil)
)

type TransactionFunc func(context.Context) error

type Database struct {
	pool      *sql.DB
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
}

type transactionContextKey struct{}

type transactionContextValue struct {
	database    *Database
	transaction *sql.Tx
	parent      *transactionContextValue
}

func NewDatabase(pool *sql.DB) (*Database, error) {
	if pool == nil {
		return nil, ErrDatabaseUnavailable
	}
	return &Database{
		pool:      pool,
		closeDone: make(chan struct{}),
	}, nil
}

func (database *Database) Pool() *sql.DB {
	if database == nil {
		return nil
	}
	return database.pool
}

func (database *Database) Ping(ctx context.Context) error {
	if database == nil || database.pool == nil {
		return ErrDatabaseUnavailable
	}
	if ctx == nil {
		return ErrDatabaseContextUnavailable
	}
	if err := database.pool.PingContext(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrDatabasePingFailed, err)
	}
	return nil
}

func (database *Database) Executor(ctx context.Context) (SQLExecutor, error) {
	if database == nil || database.pool == nil {
		return nil, ErrDatabaseUnavailable
	}
	if ctx == nil {
		return nil, ErrDatabaseContextUnavailable
	}
	if transaction, ok := database.transactionFromContext(ctx); ok {
		return transaction, nil
	}
	return database.pool, nil
}

func (database *Database) WithinTransaction(
	ctx context.Context,
	options *sql.TxOptions,
	callback TransactionFunc,
) (err error) {
	if database == nil || database.pool == nil {
		return ErrDatabaseUnavailable
	}
	if ctx == nil {
		return ErrDatabaseContextUnavailable
	}
	if callback == nil {
		return ErrTransactionCallbackUnavailable
	}
	if _, active := database.transactionFromContext(ctx); active {
		return ErrTransactionAlreadyActive
	}

	transaction, err := database.pool.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTransactionBeginFailed, err)
	}
	parent, _ := ctx.Value(transactionContextKey{}).(*transactionContextValue)
	transactionContext := context.WithValue(ctx, transactionContextKey{}, &transactionContextValue{
		database:    database,
		transaction: transaction,
		parent:      parent,
	})

	finished := false
	defer func() {
		recovered := recover()
		if !finished {
			_ = transaction.Rollback()
		}
		if recovered != nil {
			panic(recovered)
		}
	}()

	if err := callback(transactionContext); err != nil {
		rollbackErr := transaction.Rollback()
		finished = true
		if rollbackErr != nil {
			return errors.Join(
				err,
				fmt.Errorf("%w: %w", ErrTransactionRollbackFailed, rollbackErr),
			)
		}
		return err
	}
	commitErr := transaction.Commit()
	finished = true
	if commitErr != nil {
		return fmt.Errorf("%w: %w", ErrTransactionCommitFailed, commitErr)
	}
	return nil
}

func (database *Database) transactionFromContext(ctx context.Context) (*sql.Tx, bool) {
	value, _ := ctx.Value(transactionContextKey{}).(*transactionContextValue)
	for value != nil {
		if value.database == database {
			return value.transaction, true
		}
		value = value.parent
	}
	return nil, false
}

func (database *Database) Close(ctx context.Context) error {
	if database == nil || database.pool == nil {
		return ErrDatabaseUnavailable
	}
	if ctx == nil {
		return ErrDatabaseContextUnavailable
	}

	database.closeOnce.Do(func() {
		go func() {
			if err := database.pool.Close(); err != nil {
				database.closeErr = fmt.Errorf("%w: %w", ErrDatabaseCloseFailed, err)
			}
			close(database.closeDone)
		}()
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-database.closeDone:
		return database.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
