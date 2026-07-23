package framework

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryMigrationStore struct {
	ensureErr  error
	appliedErr error
	recordErr  error
	removeErr  error
	mu         sync.Mutex
	records    []AppliedMigration
	ensures    int
	recordsRun int
	removes    int
	recordTx   []bool
	removeTx   []bool
}

func (store *memoryMigrationStore) Ensure(context.Context, SQLExecutor) error {
	store.mu.Lock()
	store.ensures++
	err := store.ensureErr
	store.mu.Unlock()
	return err
}

func (store *memoryMigrationStore) Applied(
	context.Context,
	SQLExecutor,
) ([]AppliedMigration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.appliedErr != nil {
		return nil, store.appliedErr
	}
	return append([]AppliedMigration(nil), store.records...), nil
}

func (store *memoryMigrationStore) Record(
	_ context.Context,
	executor SQLExecutor,
	migration AppliedMigration,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.recordsRun++
	_, transactional := executor.(*sql.Tx)
	store.recordTx = append(store.recordTx, transactional)
	if store.recordErr != nil {
		return store.recordErr
	}
	store.records = append(store.records, migration)
	return nil
}

func (store *memoryMigrationStore) Remove(
	_ context.Context,
	executor SQLExecutor,
	version string,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.removes++
	_, transactional := executor.(*sql.Tx)
	store.removeTx = append(store.removeTx, transactional)
	if store.removeErr != nil {
		return store.removeErr
	}
	remaining := store.records[:0]
	for _, record := range store.records {
		if record.Version != version {
			remaining = append(remaining, record)
		}
	}
	store.records = remaining
	return nil
}

func (store *memoryMigrationStore) snapshot() []AppliedMigration {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]AppliedMigration(nil), store.records...)
}

func newMigrationTestRunner(
	t *testing.T,
	store MigrationStore,
	state *databaseTestState,
) (*MigrationRunner, *Database) {
	t.Helper()
	database, err := NewDatabase(newDatabaseTestPool(t, state))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	runner, err := NewMigrationRunner(database, store)
	if err != nil {
		t.Fatalf("new migration runner: %v", err)
	}
	return runner, database
}

func testMigration(version string, name string) Migration {
	return Migration{
		Version: version,
		Name:    name,
		Up:      func(context.Context, SQLExecutor) error { return nil },
		Down:    func(context.Context, SQLExecutor) error { return nil },
	}
}

func TestMigrationRunnerRequiresDependencies(t *testing.T) {
	store := &memoryMigrationStore{}
	if _, err := NewMigrationRunner(nil, store); !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("database error = %v", err)
	}
	database, err := NewDatabase(newDatabaseTestPool(t, &databaseTestState{}))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	if _, err := NewMigrationRunner(database, nil); !errors.Is(err, ErrMigrationStoreUnavailable) {
		t.Fatalf("store error = %v", err)
	}

	var runner *MigrationRunner
	if err := runner.Register(testMigration("1", "first")); !errors.Is(err, ErrMigrationRunnerUnavailable) {
		t.Fatalf("register error = %v", err)
	}
	if runner.Registered() != nil {
		t.Fatal("nil runner returned migrations")
	}
	if _, err := runner.Status(context.Background()); !errors.Is(err, ErrMigrationRunnerUnavailable) {
		t.Fatalf("status error = %v", err)
	}
	if _, err := runner.Migrate(context.Background()); !errors.Is(err, ErrMigrationRunnerUnavailable) {
		t.Fatalf("migrate error = %v", err)
	}
	if _, err := runner.Rollback(context.Background()); !errors.Is(err, ErrMigrationRunnerUnavailable) {
		t.Fatalf("rollback error = %v", err)
	}
}

func TestMigrationRegistrationIsValidatedAtomicallyAndSorted(t *testing.T) {
	runner, _ := newMigrationTestRunner(t, &memoryMigrationStore{}, &databaseTestState{})
	invalid := []Migration{
		{},
		{Version: "1", Name: "first"},
		{Version: "1", Name: "", Up: func(context.Context, SQLExecutor) error { return nil }},
		{Version: strings.Repeat("1", 256), Name: "long", Up: func(context.Context, SQLExecutor) error { return nil }},
		{Version: "1", Name: strings.Repeat("n", 256), Up: func(context.Context, SQLExecutor) error { return nil }},
	}
	for index, migration := range invalid {
		if err := runner.Register(migration); !errors.Is(err, ErrInvalidMigration) {
			t.Fatalf("invalid migration %d error = %v", index, err)
		}
	}

	if err := runner.Register(
		testMigration("3", "third"),
		Migration{Version: "4", Name: "invalid"},
	); !errors.Is(err, ErrInvalidMigration) {
		t.Fatalf("atomic registration error = %v", err)
	}
	if len(runner.Registered()) != 0 {
		t.Fatalf("partial registration = %#v", runner.Registered())
	}
	if err := runner.Register(
		testMigration(" 2 ", " second "),
		testMigration("1", "first"),
	); err != nil {
		t.Fatalf("register migrations: %v", err)
	}
	if err := runner.Register(testMigration("1", "duplicate")); !errors.Is(err, ErrMigrationAlreadyDefined) {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := runner.Register(
		testMigration("4", "fourth"),
		testMigration("4", "duplicate fourth"),
	); !errors.Is(err, ErrMigrationAlreadyDefined) {
		t.Fatalf("batch duplicate error = %v", err)
	}

	registered := runner.Registered()
	if len(registered) != 2 || registered[0].Version != "1" || registered[1].Version != "2" || registered[1].Name != "second" {
		t.Fatalf("registered = %#v", registered)
	}
}

func TestMigrationStatusDoesNotCloseRegistration(t *testing.T) {
	store := &memoryMigrationStore{records: []AppliedMigration{{Version: "1", Name: "first", Batch: 2}}}
	runner, _ := newMigrationTestRunner(t, store, &databaseTestState{})
	if err := runner.Register(testMigration("1", "first"), testMigration("2", "second")); err != nil {
		t.Fatalf("register: %v", err)
	}
	statuses, err := runner.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	expected := []MigrationStatus{
		{Version: "1", Name: "first", Applied: true, Batch: 2},
		{Version: "2", Name: "second"},
	}
	if !reflect.DeepEqual(statuses, expected) {
		t.Fatalf("statuses = %#v", statuses)
	}
	if err := runner.Register(testMigration("3", "third")); err != nil {
		t.Fatalf("register after status: %v", err)
	}
}

func TestMigrationRunnerAppliesPendingMigrationsInVersionOrder(t *testing.T) {
	store := &memoryMigrationStore{}
	state := &databaseTestState{}
	runner, _ := newMigrationTestRunner(t, store, state)
	var order []string
	var orderMu sync.Mutex
	first := testMigration("202607220001", "create_accounts")
	first.Up = func(ctx context.Context, executor SQLExecutor) error {
		orderMu.Lock()
		order = append(order, "first")
		orderMu.Unlock()
		_, err := executor.ExecContext(ctx, "CREATE TABLE accounts (id BIGINT)")
		return err
	}
	second := testMigration("202607220002", "add_account_name")
	second.Up = func(context.Context, SQLExecutor) error {
		orderMu.Lock()
		order = append(order, "second")
		orderMu.Unlock()
		return nil
	}
	if err := runner.Register(second, first); err != nil {
		t.Fatalf("register: %v", err)
	}

	result, err := runner.Migrate(context.Background())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if result.Batch != 1 || len(result.Applied) != 2 || result.Applied[0].Version != first.Version || result.Applied[1].Version != second.Version {
		t.Fatalf("result = %#v", result)
	}
	if !reflect.DeepEqual(order, []string{"first", "second"}) {
		t.Fatalf("order = %#v", order)
	}
	if !reflect.DeepEqual(store.recordTx, []bool{true, true}) {
		t.Fatalf("transaction records = %#v", store.recordTx)
	}
	snapshot := state.snapshot()
	if snapshot.begins != 2 || snapshot.commits != 2 || snapshot.rollbacks != 0 {
		t.Fatalf("database state = %#v", snapshot)
	}

	secondResult, err := runner.Migrate(context.Background())
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if secondResult.Batch != 0 || len(secondResult.Applied) != 0 {
		t.Fatalf("second result = %#v", secondResult)
	}
	if err := runner.Register(testMigration("202607220003", "late")); !errors.Is(err, ErrMigrationRegistrationClosed) {
		t.Fatalf("late registration error = %v", err)
	}
}

func TestMigrationRunnerUsesNextBatchAndSkipsApplied(t *testing.T) {
	store := &memoryMigrationStore{records: []AppliedMigration{{Version: "1", Name: "first", Batch: 2}}}
	runner, _ := newMigrationTestRunner(t, store, &databaseTestState{})
	var firstRuns atomic.Int32
	first := testMigration("1", "first")
	first.Up = func(context.Context, SQLExecutor) error {
		firstRuns.Add(1)
		return nil
	}
	if err := runner.Register(first, testMigration("3", "third"), testMigration("2", "second")); err != nil {
		t.Fatalf("register: %v", err)
	}
	result, err := runner.Migrate(context.Background())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if firstRuns.Load() != 0 || result.Batch != 3 || len(result.Applied) != 2 || result.Applied[0].Version != "2" || result.Applied[1].Version != "3" {
		t.Fatalf("result = %#v, first runs = %d", result, firstRuns.Load())
	}
}

func TestMigrationRunnerRejectsBatchOverflow(t *testing.T) {
	store := &memoryMigrationStore{records: []AppliedMigration{{
		Version: "1",
		Name:    "first",
		Batch:   math.MaxInt64,
	}}}
	state := &databaseTestState{}
	runner, _ := newMigrationTestRunner(t, store, state)
	if err := runner.Register(testMigration("1", "first"), testMigration("2", "second")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := runner.Migrate(context.Background()); !errors.Is(err, ErrMigrationHistoryInvalid) {
		t.Fatalf("migrate error = %v", err)
	}
	if state.snapshot().begins != 0 {
		t.Fatalf("database state = %#v", state.snapshot())
	}
}

func TestMigrationRunnerSerializesConcurrentRuns(t *testing.T) {
	store := &memoryMigrationStore{}
	runner, _ := newMigrationTestRunner(t, store, &databaseTestState{})
	started := make(chan struct{})
	release := make(chan struct{})
	var runs atomic.Int32
	migration := testMigration("1", "first")
	migration.Up = func(context.Context, SQLExecutor) error {
		if runs.Add(1) == 1 {
			close(started)
		}
		<-release
		return nil
	}
	if err := runner.Register(migration); err != nil {
		t.Fatalf("register: %v", err)
	}

	type outcome struct {
		result MigrationRunResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	go func() {
		result, err := runner.Migrate(context.Background())
		outcomes <- outcome{result: result, err: err}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first migration did not start")
	}
	go func() {
		result, err := runner.Migrate(context.Background())
		outcomes <- outcome{result: result, err: err}
	}()
	close(release)

	totalApplied := 0
	for range 2 {
		select {
		case outcome := <-outcomes:
			if outcome.err != nil {
				t.Fatalf("migrate: %v", outcome.err)
			}
			totalApplied += len(outcome.result.Applied)
		case <-time.After(time.Second):
			t.Fatal("concurrent migration did not finish")
		}
	}
	if runs.Load() != 1 || totalApplied != 1 || len(store.snapshot()) != 1 {
		t.Fatalf(
			"runs = %d, applied = %d, records = %#v",
			runs.Load(),
			totalApplied,
			store.snapshot(),
		)
	}
}

func TestMigrationRunnerRejectsHistoryDrift(t *testing.T) {
	tests := []struct {
		name       string
		records    []AppliedMigration
		migrations []Migration
		target     error
	}{
		{
			name:       "missing definition",
			records:    []AppliedMigration{{Version: "1", Name: "missing", Batch: 1}},
			migrations: []Migration{testMigration("2", "second")},
			target:     ErrMigrationDefinitionMissing,
		},
		{
			name:       "name mismatch",
			records:    []AppliedMigration{{Version: "1", Name: "old_name", Batch: 1}},
			migrations: []Migration{testMigration("1", "new_name")},
			target:     ErrMigrationNameMismatch,
		},
		{
			name: "duplicate version",
			records: []AppliedMigration{
				{Version: "1", Name: "first", Batch: 1},
				{Version: "1", Name: "first", Batch: 1},
			},
			migrations: []Migration{testMigration("1", "first")},
			target:     ErrMigrationHistoryInvalid,
		},
		{
			name:       "invalid batch",
			records:    []AppliedMigration{{Version: "1", Name: "first", Batch: 0}},
			migrations: []Migration{testMigration("1", "first")},
			target:     ErrMigrationHistoryInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, _ := newMigrationTestRunner(
				t,
				&memoryMigrationStore{records: test.records},
				&databaseTestState{},
			)
			if err := runner.Register(test.migrations...); err != nil {
				t.Fatalf("register: %v", err)
			}
			if _, err := runner.Migrate(context.Background()); !errors.Is(err, test.target) {
				t.Fatalf("migrate error = %v", err)
			}
		})
	}
}

func TestMigrationRunnerStopsAfterFailureAndPreservesPartialResult(t *testing.T) {
	store := &memoryMigrationStore{}
	state := &databaseTestState{}
	runner, _ := newMigrationTestRunner(t, store, state)
	cause := errors.New("alter table rejected")
	second := testMigration("2", "second")
	second.Up = func(context.Context, SQLExecutor) error { return cause }
	if err := runner.Register(testMigration("1", "first"), second, testMigration("3", "third")); err != nil {
		t.Fatalf("register: %v", err)
	}
	result, err := runner.Migrate(context.Background())
	if !errors.Is(err, ErrMigrationExecutionFailed) || !errors.Is(err, cause) {
		t.Fatalf("migrate error = %v", err)
	}
	var failure *MigrationFailure
	if !errors.As(err, &failure) || failure.Direction != MigrationDirectionUp || failure.Migration.Version != "2" {
		t.Fatalf("failure = %#v", failure)
	}
	if len(result.Applied) != 1 || result.Applied[0].Version != "1" {
		t.Fatalf("result = %#v", result)
	}
	if records := store.snapshot(); len(records) != 1 || records[0].Version != "1" {
		t.Fatalf("records = %#v", records)
	}
	snapshot := state.snapshot()
	if snapshot.commits != 1 || snapshot.rollbacks != 1 {
		t.Fatalf("database state = %#v", snapshot)
	}
}

func TestMigrationRunnerRollsBackStoreFailure(t *testing.T) {
	cause := errors.New("record insert failed")
	store := &memoryMigrationStore{recordErr: cause}
	state := &databaseTestState{}
	runner, _ := newMigrationTestRunner(t, store, state)
	if err := runner.Register(testMigration("1", "first")); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := runner.Migrate(context.Background())
	if !errors.Is(err, ErrMigrationExecutionFailed) || !errors.Is(err, ErrMigrationStoreFailed) || !errors.Is(err, cause) {
		t.Fatalf("migrate error = %v", err)
	}
	if snapshot := state.snapshot(); snapshot.commits != 0 || snapshot.rollbacks != 1 {
		t.Fatalf("database state = %#v", snapshot)
	}
}

func TestMigrationRunnerRecoversCallbackPanic(t *testing.T) {
	state := &databaseTestState{}
	runner, _ := newMigrationTestRunner(t, &memoryMigrationStore{}, state)
	migration := testMigration("1", "panics")
	cause := errors.New("migration panic")
	migration.Up = func(context.Context, SQLExecutor) error {
		panic(cause)
	}
	if err := runner.Register(migration); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := runner.Migrate(context.Background())
	if !errors.Is(err, ErrMigrationExecutionFailed) || !errors.Is(err, ErrMigrationPanicked) || !errors.Is(err, cause) {
		t.Fatalf("migrate error = %v", err)
	}
	if snapshot := state.snapshot(); snapshot.rollbacks != 1 {
		t.Fatalf("database state = %#v", snapshot)
	}
}

func TestMigrationRunnerSupportsExplicitNonTransactionalMigration(t *testing.T) {
	store := &memoryMigrationStore{}
	state := &databaseTestState{}
	runner, _ := newMigrationTestRunner(t, store, state)
	migration := testMigration("1", "mysql_ddl")
	migration.DisableTransaction = true
	if err := runner.Register(migration); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := runner.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !reflect.DeepEqual(store.recordTx, []bool{false}) {
		t.Fatalf("transaction records = %#v", store.recordTx)
	}
	if snapshot := state.snapshot(); snapshot.begins != 0 {
		t.Fatalf("database state = %#v", snapshot)
	}
}

func TestMigrationRunnerRollsBackLatestBatchInReverseOrder(t *testing.T) {
	store := &memoryMigrationStore{records: []AppliedMigration{
		{Version: "1", Name: "first", Batch: 1},
		{Version: "2", Name: "second", Batch: 2},
		{Version: "3", Name: "third", Batch: 2},
	}}
	state := &databaseTestState{}
	runner, _ := newMigrationTestRunner(t, store, state)
	var order []string
	migrations := []Migration{
		testMigration("1", "first"),
		testMigration("2", "second"),
		testMigration("3", "third"),
	}
	for index := range migrations {
		version := migrations[index].Version
		migrations[index].Down = func(context.Context, SQLExecutor) error {
			order = append(order, version)
			return nil
		}
	}
	if err := runner.Register(migrations...); err != nil {
		t.Fatalf("register: %v", err)
	}
	result, err := runner.Rollback(context.Background())
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if result.Batch != 2 || len(result.RolledBack) != 2 || result.RolledBack[0].Version != "3" || result.RolledBack[1].Version != "2" {
		t.Fatalf("result = %#v", result)
	}
	if !reflect.DeepEqual(order, []string{"3", "2"}) {
		t.Fatalf("order = %#v", order)
	}
	if records := store.snapshot(); len(records) != 1 || records[0].Version != "1" {
		t.Fatalf("records = %#v", records)
	}
	if !reflect.DeepEqual(store.removeTx, []bool{true, true}) {
		t.Fatalf("transaction removes = %#v", store.removeTx)
	}
	if snapshot := state.snapshot(); snapshot.commits != 2 || snapshot.rollbacks != 0 {
		t.Fatalf("database state = %#v", snapshot)
	}
}

func TestMigrationRollbackValidatesWholeBatchBeforeMutation(t *testing.T) {
	store := &memoryMigrationStore{records: []AppliedMigration{
		{Version: "1", Name: "first", Batch: 1},
		{Version: "2", Name: "second", Batch: 1},
	}}
	state := &databaseTestState{}
	runner, _ := newMigrationTestRunner(t, store, state)
	second := testMigration("2", "second")
	second.Down = nil
	if err := runner.Register(testMigration("1", "first"), second); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := runner.Rollback(context.Background()); !errors.Is(err, ErrMigrationDownUnavailable) {
		t.Fatalf("rollback error = %v", err)
	}
	if store.removes != 0 || state.snapshot().begins != 0 {
		t.Fatalf("store removes = %d, state = %#v", store.removes, state.snapshot())
	}
}

func TestMigrationRollbackStopsAfterFailure(t *testing.T) {
	store := &memoryMigrationStore{records: []AppliedMigration{
		{Version: "1", Name: "first", Batch: 1},
		{Version: "2", Name: "second", Batch: 1},
		{Version: "3", Name: "third", Batch: 1},
	}}
	state := &databaseTestState{}
	runner, _ := newMigrationTestRunner(t, store, state)
	cause := errors.New("drop rejected")
	second := testMigration("2", "second")
	second.Down = func(context.Context, SQLExecutor) error { return cause }
	if err := runner.Register(testMigration("1", "first"), second, testMigration("3", "third")); err != nil {
		t.Fatalf("register: %v", err)
	}
	result, err := runner.Rollback(context.Background())
	if !errors.Is(err, ErrMigrationRevertFailed) || !errors.Is(err, cause) {
		t.Fatalf("rollback error = %v", err)
	}
	if len(result.RolledBack) != 1 || result.RolledBack[0].Version != "3" {
		t.Fatalf("result = %#v", result)
	}
	if records := store.snapshot(); len(records) != 2 || records[1].Version != "2" {
		t.Fatalf("records = %#v", records)
	}
	if snapshot := state.snapshot(); snapshot.commits != 1 || snapshot.rollbacks != 1 {
		t.Fatalf("database state = %#v", snapshot)
	}
}

func TestMigrationRollbackRollsBackStoreFailure(t *testing.T) {
	cause := errors.New("delete history failed")
	store := &memoryMigrationStore{
		removeErr: cause,
		records:   []AppliedMigration{{Version: "1", Name: "first", Batch: 1}},
	}
	state := &databaseTestState{}
	runner, _ := newMigrationTestRunner(t, store, state)
	if err := runner.Register(testMigration("1", "first")); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := runner.Rollback(context.Background())
	if !errors.Is(err, ErrMigrationRevertFailed) || !errors.Is(err, ErrMigrationStoreFailed) || !errors.Is(err, cause) {
		t.Fatalf("rollback error = %v", err)
	}
	if snapshot := state.snapshot(); snapshot.commits != 0 || snapshot.rollbacks != 1 {
		t.Fatalf("database state = %#v", snapshot)
	}
	if records := store.snapshot(); len(records) != 1 {
		t.Fatalf("records = %#v", records)
	}
}

func TestMigrationRunnerValidatesContextBeforeStoreMutation(t *testing.T) {
	store := &memoryMigrationStore{}
	runner, database := newMigrationTestRunner(t, store, &databaseTestState{})
	if err := runner.Register(testMigration("1", "first")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := runner.Migrate(nil); !errors.Is(err, ErrMigrationContextUnavailable) {
		t.Fatalf("nil context error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.Migrate(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error = %v", err)
	}
	var nestedError error
	if err := database.WithinTransaction(context.Background(), nil, func(ctx context.Context) error {
		_, nestedError = runner.Migrate(ctx)
		return nil
	}); err != nil {
		t.Fatalf("outer transaction: %v", err)
	}
	if !errors.Is(nestedError, ErrTransactionAlreadyActive) {
		t.Fatalf("nested error = %v", nestedError)
	}
	if store.ensures != 0 {
		t.Fatalf("ensure calls = %d", store.ensures)
	}
}

func TestMigrationRunnerWrapsStoreLoadFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		store *memoryMigrationStore
		cause error
	}{
		{
			name:  "ensure",
			cause: errors.New("create table failed"),
		},
		{
			name:  "applied",
			cause: errors.New("select failed"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &memoryMigrationStore{}
			if test.name == "ensure" {
				store.ensureErr = test.cause
			} else {
				store.appliedErr = test.cause
			}
			runner, _ := newMigrationTestRunner(t, store, &databaseTestState{})
			if err := runner.Register(testMigration("1", "first")); err != nil {
				t.Fatalf("register: %v", err)
			}
			if _, err := runner.Migrate(context.Background()); !errors.Is(err, ErrMigrationStoreFailed) || !errors.Is(err, test.cause) {
				t.Fatalf("migrate error = %v", err)
			}
		})
	}
}

func TestMigrationServiceProviderRegistersRunnerAfterDatabase(t *testing.T) {
	application := NewApplication(nil)
	databaseProvider := NewDatabaseServiceProvider(
		newDatabaseTestPool(t, &databaseTestState{}),
		DefaultDatabaseProviderOptions(),
	)
	migrationProvider := NewMigrationServiceProvider(&memoryMigrationStore{})
	if migrationProvider.ProviderName() != "framework.migrations" {
		t.Fatalf("provider name = %q", migrationProvider.ProviderName())
	}
	if err := application.Register(databaseProvider, migrationProvider); err != nil {
		t.Fatalf("register: %v", err)
	}
	runner, err := Resolve(application.Container(), MigrationRunnerKey)
	if err != nil || runner == nil {
		t.Fatalf("resolve runner = %v, %v", runner, err)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestMigrationServiceProviderRequiresDatabaseFirst(t *testing.T) {
	application := NewApplication(nil)
	err := application.Register(NewMigrationServiceProvider(&memoryMigrationStore{}))
	if !errors.Is(err, ErrApplicationFailed) || !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("register error = %v", err)
	}
}

func TestSQLMigrationStorePersistsRecordsAndSupportsPlaceholderStyles(t *testing.T) {
	state := &databaseTestState{}
	database, err := NewDatabase(newDatabaseTestPool(t, state))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	executor, err := database.Executor(context.Background())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	store, err := NewSQLMigrationStore(SQLMigrationStoreOptions{})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if store.Table() != "bridra_migrations" {
		t.Fatalf("table = %q", store.Table())
	}
	if err := store.Ensure(context.Background(), executor); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	record := AppliedMigration{Version: "1", Name: "first", Batch: 1}
	if err := store.Record(context.Background(), executor, record); err != nil {
		t.Fatalf("record: %v", err)
	}
	applied, err := store.Applied(context.Background(), executor)
	if err != nil {
		t.Fatalf("applied: %v", err)
	}
	if !reflect.DeepEqual(applied, []AppliedMigration{record}) {
		t.Fatalf("applied = %#v", applied)
	}
	if err := store.Remove(context.Background(), executor, "1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	state.mu.Lock()
	queries := append([]string(nil), state.queries...)
	state.mu.Unlock()
	if !strings.Contains(strings.Join(queries, "\n"), "VALUES (?, ?, ?)") {
		t.Fatalf("queries = %#v", queries)
	}

	dollarStore, err := NewSQLMigrationStore(SQLMigrationStoreOptions{
		Table:            "custom_migrations",
		PlaceholderStyle: SQLPlaceholderDollar,
	})
	if err != nil {
		t.Fatalf("new dollar store: %v", err)
	}
	if err := dollarStore.Record(context.Background(), executor, record); err != nil {
		t.Fatalf("dollar record: %v", err)
	}
	state.mu.Lock()
	lastQuery := state.queries[len(state.queries)-1]
	state.mu.Unlock()
	if !strings.Contains(lastQuery, "VALUES ($1, $2, $3)") {
		t.Fatalf("dollar query = %q", lastQuery)
	}
}

func TestMigrationRunnerUsesSQLStoreEndToEnd(t *testing.T) {
	state := &databaseTestState{}
	database, err := NewDatabase(newDatabaseTestPool(t, state))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	store, err := NewSQLMigrationStore(DefaultSQLMigrationStoreOptions())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	runner, err := NewMigrationRunner(database, store)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	if err := runner.Register(testMigration("1", "first")); err != nil {
		t.Fatalf("register: %v", err)
	}
	result, err := runner.Migrate(context.Background())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0].Batch != 1 {
		t.Fatalf("result = %#v", result)
	}
	statuses, err := runner.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(statuses) != 1 || !statuses[0].Applied {
		t.Fatalf("statuses = %#v", statuses)
	}
	rollback, err := runner.Rollback(context.Background())
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if len(rollback.RolledBack) != 1 || rollback.RolledBack[0].Version != "1" {
		t.Fatalf("rollback = %#v", rollback)
	}
	state.mu.Lock()
	records := append([]AppliedMigration(nil), state.records...)
	queries := strings.Join(state.queries, "\n")
	state.mu.Unlock()
	if len(records) != 0 || !strings.Contains(queries, "INSERT INTO bridra_migrations") ||
		!strings.Contains(queries, "DELETE FROM bridra_migrations") {
		t.Fatalf("records = %#v, queries = %q", records, queries)
	}
	if snapshot := state.snapshot(); snapshot.begins != 2 || snapshot.commits != 2 {
		t.Fatalf("database state = %#v", snapshot)
	}
}

func TestSQLMigrationStoreValidatesOptionsAndDependencies(t *testing.T) {
	for _, options := range []SQLMigrationStoreOptions{
		{Table: "invalid table"},
		{Table: "1invalid"},
		{PlaceholderStyle: "named"},
	} {
		if _, err := NewSQLMigrationStore(options); !errors.Is(err, ErrInvalidSQLMigrationStoreOptions) {
			t.Fatalf("options %#v error = %v", options, err)
		}
	}
	store, err := NewSQLMigrationStore(DefaultSQLMigrationStoreOptions())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Ensure(context.Background(), nil); !errors.Is(err, ErrMigrationStoreUnavailable) {
		t.Fatalf("nil executor error = %v", err)
	}
	database, err := NewDatabase(newDatabaseTestPool(t, &databaseTestState{}))
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	executor, err := database.Executor(context.Background())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	if err := store.Ensure(nil, executor); !errors.Is(err, ErrMigrationContextUnavailable) {
		t.Fatalf("nil context error = %v", err)
	}

	var unavailable *SQLMigrationStore
	if unavailable.Table() != "" {
		t.Fatalf("nil store table = %q", unavailable.Table())
	}
	if _, err := unavailable.Applied(context.Background(), executor); !errors.Is(err, ErrMigrationStoreUnavailable) {
		t.Fatalf("nil store error = %v", err)
	}
}

func TestMigrationFailureFormattingAndUnknownDirection(t *testing.T) {
	failure := &MigrationFailure{
		Direction: "unknown",
		Migration: AppliedMigration{Version: "1", Name: "first", Batch: 1},
		Err:       errors.New("failure"),
	}
	if errors.Is(failure, ErrMigrationExecutionFailed) || failure.Error() == "" || failure.Unwrap() == nil {
		t.Fatalf("failure = %#v", failure)
	}
	var unavailable *MigrationFailure
	if unavailable.Error() == "" || unavailable.Unwrap() != nil || unavailable.Is(ErrMigrationExecutionFailed) {
		t.Fatalf("nil failure behavior is invalid")
	}
}
