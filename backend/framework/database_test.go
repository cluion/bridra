package framework

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const databaseTestDriverName = "bridra_framework_database_test"

var (
	databaseTestSequence atomic.Uint64
	databaseTestStates   sync.Map
)

func init() {
	sql.Register(databaseTestDriverName, databaseTestDriver{})
}

type databaseTestState struct {
	pingErr      error
	beginErr     error
	commitErr    error
	rollbackErr  error
	closeErr     error
	pingBlock    <-chan struct{}
	closeBlock   <-chan struct{}
	closeStarted chan struct{}
	closeOnce    sync.Once
	mu           sync.Mutex
	opens        int
	pings        int
	begins       int
	commits      int
	rollbacks    int
	closes       int
	executions   int
	queries      []string
	records      []AppliedMigration
}

type databaseTestSnapshot struct {
	opens      int
	pings      int
	begins     int
	commits    int
	rollbacks  int
	closes     int
	executions int
}

func (state *databaseTestState) snapshot() databaseTestSnapshot {
	state.mu.Lock()
	defer state.mu.Unlock()
	return databaseTestSnapshot{
		opens:      state.opens,
		pings:      state.pings,
		begins:     state.begins,
		commits:    state.commits,
		rollbacks:  state.rollbacks,
		closes:     state.closes,
		executions: state.executions,
	}
}

type databaseTestDriver struct{}

func (databaseTestDriver) Open(name string) (driver.Conn, error) {
	value, exists := databaseTestStates.Load(name)
	if !exists {
		return nil, fmt.Errorf("unknown database test state %q", name)
	}
	state := value.(*databaseTestState)
	state.mu.Lock()
	state.opens++
	state.mu.Unlock()
	return &databaseTestConnection{state: state}, nil
}

type databaseTestConnection struct {
	state *databaseTestState
}

func (connection *databaseTestConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by the database test driver")
}

func (connection *databaseTestConnection) Close() error {
	state := connection.state
	state.closeOnce.Do(func() {
		if state.closeStarted != nil {
			close(state.closeStarted)
		}
	})
	if state.closeBlock != nil {
		<-state.closeBlock
	}
	state.mu.Lock()
	state.closes++
	state.mu.Unlock()
	return state.closeErr
}

func (connection *databaseTestConnection) Begin() (driver.Tx, error) {
	return connection.BeginTx(context.Background(), driver.TxOptions{})
}

func (connection *databaseTestConnection) BeginTx(
	ctx context.Context,
	_ driver.TxOptions,
) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := connection.state
	state.mu.Lock()
	state.begins++
	err := state.beginErr
	state.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &databaseTestTransaction{state: state}, nil
}

func (connection *databaseTestConnection) Ping(ctx context.Context) error {
	state := connection.state
	state.mu.Lock()
	state.pings++
	err := state.pingErr
	block := state.pingBlock
	state.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (connection *databaseTestConnection) ExecContext(
	ctx context.Context,
	query string,
	arguments []driver.NamedValue,
) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	connection.state.mu.Lock()
	connection.state.executions++
	connection.state.queries = append(connection.state.queries, query)
	if strings.HasPrefix(query, "INSERT INTO ") && strings.Contains(query, "(version, name, batch)") {
		if len(arguments) != 3 {
			connection.state.mu.Unlock()
			return nil, fmt.Errorf("migration insert arguments = %d", len(arguments))
		}
		version, versionOK := arguments[0].Value.(string)
		name, nameOK := arguments[1].Value.(string)
		batch, batchOK := arguments[2].Value.(int64)
		if !versionOK || !nameOK || !batchOK {
			connection.state.mu.Unlock()
			return nil, errors.New("invalid migration insert arguments")
		}
		connection.state.records = append(connection.state.records, AppliedMigration{
			Version: version,
			Name:    name,
			Batch:   batch,
		})
	}
	if strings.HasPrefix(query, "DELETE FROM ") && strings.Contains(query, "WHERE version =") {
		if len(arguments) != 1 {
			connection.state.mu.Unlock()
			return nil, fmt.Errorf("migration delete arguments = %d", len(arguments))
		}
		version, ok := arguments[0].Value.(string)
		if !ok {
			connection.state.mu.Unlock()
			return nil, errors.New("invalid migration delete version")
		}
		remaining := connection.state.records[:0]
		for _, record := range connection.state.records {
			if record.Version != version {
				remaining = append(remaining, record)
			}
		}
		connection.state.records = remaining
	}
	connection.state.mu.Unlock()
	return driver.RowsAffected(1), nil
}

func (connection *databaseTestConnection) QueryContext(
	ctx context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	connection.state.mu.Lock()
	connection.state.queries = append(connection.state.queries, query)
	records := append([]AppliedMigration(nil), connection.state.records...)
	connection.state.mu.Unlock()
	if !strings.HasPrefix(query, "SELECT version, name, batch FROM ") {
		return nil, fmt.Errorf("unsupported test query %q", query)
	}
	return &databaseTestRows{records: records}, nil
}

type databaseTestRows struct {
	records []AppliedMigration
	index   int
}

func (rows *databaseTestRows) Columns() []string {
	return []string{"version", "name", "batch"}
}

func (rows *databaseTestRows) Close() error {
	return nil
}

func (rows *databaseTestRows) Next(values []driver.Value) error {
	if rows.index >= len(rows.records) {
		return io.EOF
	}
	record := rows.records[rows.index]
	rows.index++
	values[0] = record.Version
	values[1] = record.Name
	values[2] = record.Batch
	return nil
}

type databaseTestTransaction struct {
	state *databaseTestState
}

func (transaction *databaseTestTransaction) Commit() error {
	transaction.state.mu.Lock()
	transaction.state.commits++
	err := transaction.state.commitErr
	transaction.state.mu.Unlock()
	return err
}

func (transaction *databaseTestTransaction) Rollback() error {
	transaction.state.mu.Lock()
	transaction.state.rollbacks++
	err := transaction.state.rollbackErr
	transaction.state.mu.Unlock()
	return err
}

func newDatabaseTestPool(t *testing.T, state *databaseTestState) *sql.DB {
	t.Helper()
	name := strconv.FormatUint(databaseTestSequence.Add(1), 10)
	databaseTestStates.Store(name, state)
	pool, err := sql.Open(databaseTestDriverName, name)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		_ = pool.Close()
		databaseTestStates.Delete(name)
	})
	return pool
}

func TestDatabaseRequiresPoolAndContext(t *testing.T) {
	if _, err := NewDatabase(nil); !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("new database error = %v", err)
	}

	var unavailable *Database
	if unavailable.Pool() != nil {
		t.Fatal("nil database returned a pool")
	}
	if err := unavailable.Ping(context.Background()); !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("ping error = %v", err)
	}
	if _, err := unavailable.Executor(context.Background()); !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("executor error = %v", err)
	}
	if err := unavailable.WithinTransaction(
		context.Background(),
		nil,
		func(context.Context) error { return nil },
	); !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("transaction error = %v", err)
	}
	if err := unavailable.Close(context.Background()); !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("close error = %v", err)
	}

	database, err := NewDatabase(newDatabaseTestPool(t, &databaseTestState{}))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	if err := database.Ping(nil); !errors.Is(err, ErrDatabaseContextUnavailable) {
		t.Fatalf("nil ping context error = %v", err)
	}
	if _, err := database.Executor(nil); !errors.Is(err, ErrDatabaseContextUnavailable) {
		t.Fatalf("nil executor context error = %v", err)
	}
	if err := database.WithinTransaction(nil, nil, func(context.Context) error { return nil }); !errors.Is(
		err,
		ErrDatabaseContextUnavailable,
	) {
		t.Fatalf("nil transaction context error = %v", err)
	}
	if err := database.WithinTransaction(context.Background(), nil, nil); !errors.Is(
		err,
		ErrTransactionCallbackUnavailable,
	) {
		t.Fatalf("nil callback error = %v", err)
	}
	if err := database.Close(nil); !errors.Is(err, ErrDatabaseContextUnavailable) {
		t.Fatalf("nil close context error = %v", err)
	}
}

func TestDatabasePingPreservesCause(t *testing.T) {
	cause := errors.New("database is offline")
	database, err := NewDatabase(newDatabaseTestPool(t, &databaseTestState{pingErr: cause}))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	if err := database.Ping(context.Background()); !errors.Is(err, ErrDatabasePingFailed) || !errors.Is(err, cause) {
		t.Fatalf("ping error = %v", err)
	}
}

func TestDatabaseExecutorUsesTransactionFromContext(t *testing.T) {
	state := &databaseTestState{}
	pool := newDatabaseTestPool(t, state)
	database, err := NewDatabase(pool)
	if err != nil {
		t.Fatalf("new database: %v", err)
	}

	executor, err := database.Executor(context.Background())
	if err != nil {
		t.Fatalf("root executor: %v", err)
	}
	if executor != pool {
		t.Fatalf("root executor = %T", executor)
	}

	err = database.WithinTransaction(context.Background(), nil, func(ctx context.Context) error {
		executor, err := database.Executor(ctx)
		if err != nil {
			return err
		}
		if _, ok := executor.(*sql.Tx); !ok {
			return fmt.Errorf("transaction executor = %T", executor)
		}
		_, err = executor.ExecContext(ctx, "INSERT INTO examples (name) VALUES (?)", "Bridra")
		return err
	})
	if err != nil {
		t.Fatalf("within transaction: %v", err)
	}
	snapshot := state.snapshot()
	if snapshot.begins != 1 || snapshot.commits != 1 || snapshot.rollbacks != 0 || snapshot.executions != 1 {
		t.Fatalf("state = %#v", snapshot)
	}
}

func TestDatabaseTransactionContextIsScopedToOwningDatabase(t *testing.T) {
	firstState := &databaseTestState{}
	first, err := NewDatabase(newDatabaseTestPool(t, firstState))
	if err != nil {
		t.Fatalf("new first database: %v", err)
	}
	secondState := &databaseTestState{}
	secondPool := newDatabaseTestPool(t, secondState)
	second, err := NewDatabase(secondPool)
	if err != nil {
		t.Fatalf("new second database: %v", err)
	}

	err = first.WithinTransaction(context.Background(), nil, func(firstCtx context.Context) error {
		executor, err := second.Executor(firstCtx)
		if err != nil {
			return err
		}
		if executor != secondPool {
			return fmt.Errorf("second root executor = %T", executor)
		}
		return second.WithinTransaction(firstCtx, nil, func(secondCtx context.Context) error {
			firstExecutor, err := first.Executor(secondCtx)
			if err != nil {
				return err
			}
			secondExecutor, err := second.Executor(secondCtx)
			if err != nil {
				return err
			}
			if _, ok := firstExecutor.(*sql.Tx); !ok {
				return fmt.Errorf("first nested executor = %T", firstExecutor)
			}
			if _, ok := secondExecutor.(*sql.Tx); !ok {
				return fmt.Errorf("second nested executor = %T", secondExecutor)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("nested databases: %v", err)
	}
	if snapshot := firstState.snapshot(); snapshot.begins != 1 || snapshot.commits != 1 {
		t.Fatalf("first state = %#v", snapshot)
	}
	if snapshot := secondState.snapshot(); snapshot.begins != 1 || snapshot.commits != 1 {
		t.Fatalf("second state = %#v", snapshot)
	}
}

func TestDatabaseTransactionRollsBackCallbackError(t *testing.T) {
	state := &databaseTestState{}
	database, err := NewDatabase(newDatabaseTestPool(t, state))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	cause := errors.New("save failed")
	err = database.WithinTransaction(context.Background(), nil, func(context.Context) error {
		return cause
	})
	if err != cause {
		t.Fatalf("transaction error = %v", err)
	}
	snapshot := state.snapshot()
	if snapshot.begins != 1 || snapshot.commits != 0 || snapshot.rollbacks != 1 {
		t.Fatalf("state = %#v", snapshot)
	}
}

func TestDatabaseTransactionPreservesRollbackFailure(t *testing.T) {
	rollbackCause := errors.New("rollback connection failure")
	state := &databaseTestState{rollbackErr: rollbackCause}
	database, err := NewDatabase(newDatabaseTestPool(t, state))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	callbackCause := errors.New("operation failed")
	err = database.WithinTransaction(context.Background(), nil, func(context.Context) error {
		return callbackCause
	})
	if !errors.Is(err, callbackCause) || !errors.Is(err, ErrTransactionRollbackFailed) || !errors.Is(err, rollbackCause) {
		t.Fatalf("transaction error = %v", err)
	}
}

func TestDatabaseTransactionPreservesBeginAndCommitFailures(t *testing.T) {
	t.Run("begin", func(t *testing.T) {
		cause := errors.New("begin failed")
		database, err := NewDatabase(newDatabaseTestPool(t, &databaseTestState{beginErr: cause}))
		if err != nil {
			t.Fatalf("new database: %v", err)
		}
		err = database.WithinTransaction(context.Background(), nil, func(context.Context) error {
			return nil
		})
		if !errors.Is(err, ErrTransactionBeginFailed) || !errors.Is(err, cause) {
			t.Fatalf("transaction error = %v", err)
		}
	})

	t.Run("commit", func(t *testing.T) {
		cause := errors.New("commit failed")
		database, err := NewDatabase(newDatabaseTestPool(t, &databaseTestState{commitErr: cause}))
		if err != nil {
			t.Fatalf("new database: %v", err)
		}
		err = database.WithinTransaction(context.Background(), nil, func(context.Context) error {
			return nil
		})
		if !errors.Is(err, ErrTransactionCommitFailed) || !errors.Is(err, cause) {
			t.Fatalf("transaction error = %v", err)
		}
	})
}

func TestDatabaseTransactionRejectsNesting(t *testing.T) {
	state := &databaseTestState{}
	database, err := NewDatabase(newDatabaseTestPool(t, state))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	err = database.WithinTransaction(context.Background(), nil, func(ctx context.Context) error {
		return database.WithinTransaction(ctx, nil, func(context.Context) error {
			return nil
		})
	})
	if !errors.Is(err, ErrTransactionAlreadyActive) {
		t.Fatalf("transaction error = %v", err)
	}
	snapshot := state.snapshot()
	if snapshot.begins != 1 || snapshot.commits != 0 || snapshot.rollbacks != 1 {
		t.Fatalf("state = %#v", snapshot)
	}
}

func TestDatabaseTransactionRollsBackAndPreservesPanic(t *testing.T) {
	state := &databaseTestState{}
	database, err := NewDatabase(newDatabaseTestPool(t, state))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	marker := &struct{ message string }{message: "repository panic"}

	func() {
		defer func() {
			if recovered := recover(); recovered != marker {
				t.Fatalf("recovered = %#v", recovered)
			}
		}()
		_ = database.WithinTransaction(context.Background(), nil, func(context.Context) error {
			panic(marker)
		})
	}()

	snapshot := state.snapshot()
	if snapshot.begins != 1 || snapshot.commits != 0 || snapshot.rollbacks != 1 {
		t.Fatalf("state = %#v", snapshot)
	}
}

func TestDatabaseCloseContinuesAfterCallerContextEnds(t *testing.T) {
	closeBlock := make(chan struct{})
	closeStarted := make(chan struct{})
	state := &databaseTestState{
		closeBlock:   closeBlock,
		closeStarted: closeStarted,
	}
	database, err := NewDatabase(newDatabaseTestPool(t, state))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	if err := database.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := database.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close error = %v", err)
	}
	select {
	case <-closeStarted:
	case <-time.After(time.Second):
		t.Fatal("pool close did not start")
	}
	close(closeBlock)
	if err := database.Close(context.Background()); err != nil {
		t.Fatalf("wait for close: %v", err)
	}
	if snapshot := state.snapshot(); snapshot.closes != 1 {
		t.Fatalf("state = %#v", snapshot)
	}
}

func TestDatabaseClosePreservesDriverFailure(t *testing.T) {
	cause := errors.New("driver close failed")
	state := &databaseTestState{closeErr: cause}
	database, err := NewDatabase(newDatabaseTestPool(t, state))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	if err := database.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := database.Close(context.Background()); !errors.Is(err, ErrDatabaseCloseFailed) || !errors.Is(err, cause) {
		t.Fatalf("close error = %v", err)
	}
}

func TestDatabaseServiceProviderLifecycle(t *testing.T) {
	state := &databaseTestState{}
	pool := newDatabaseTestPool(t, state)
	provider := NewDatabaseServiceProvider(pool, DefaultDatabaseProviderOptions())
	if provider.ProviderName() != "framework.database" {
		t.Fatalf("provider name = %q", provider.ProviderName())
	}
	application := NewApplication(nil)
	if err := application.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	database, err := Resolve(application.Container(), DatabaseKey)
	if err != nil {
		t.Fatalf("resolve database: %v", err)
	}
	if database.Pool() != pool {
		t.Fatal("provider registered a different pool")
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	snapshot := state.snapshot()
	if snapshot.pings != 1 || snapshot.closes != 1 {
		t.Fatalf("state = %#v", snapshot)
	}
}

func TestDatabaseServiceProviderRejectsInvalidOptionsAndCleansUp(t *testing.T) {
	state := &databaseTestState{}
	pool := newDatabaseTestPool(t, state)
	if err := pool.PingContext(context.Background()); err != nil {
		t.Fatalf("open pool connection: %v", err)
	}
	provider := NewDatabaseServiceProvider(pool, DatabaseProviderOptions{PingTimeout: -time.Second})
	application := NewApplication(nil)
	err := application.Register(provider)
	if !errors.Is(err, ErrApplicationFailed) || !errors.Is(err, ErrInvalidDatabaseProviderOptions) {
		t.Fatalf("register error = %v", err)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := pool.PingContext(context.Background()); err == nil {
		t.Fatal("pool remained open after cleanup")
	}
	if snapshot := state.snapshot(); snapshot.closes != 1 {
		t.Fatalf("state = %#v", snapshot)
	}
}

func TestDatabaseServiceProviderPingFailureIsTerminal(t *testing.T) {
	cause := errors.New("ping rejected")
	state := &databaseTestState{pingErr: cause}
	provider := NewDatabaseServiceProvider(
		newDatabaseTestPool(t, state),
		DefaultDatabaseProviderOptions(),
	)
	application := NewApplication(nil)
	if err := application.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := application.Boot()
	if !errors.Is(err, ErrApplicationFailed) || !errors.Is(err, ErrDatabasePingFailed) || !errors.Is(err, cause) {
		t.Fatalf("boot error = %v", err)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if snapshot := state.snapshot(); snapshot.closes != 1 {
		t.Fatalf("state = %#v", snapshot)
	}
}

func TestDatabaseServiceProviderAppliesPingTimeout(t *testing.T) {
	provider := NewDatabaseServiceProvider(
		newDatabaseTestPool(t, &databaseTestState{pingBlock: make(chan struct{})}),
		DatabaseProviderOptions{PingTimeout: 5 * time.Millisecond},
	)
	application := NewApplication(nil)
	if err := application.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := application.Boot()
	if !errors.Is(err, ErrDatabasePingFailed) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("boot error = %v", err)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
