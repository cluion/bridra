package framework

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sync"
)

const testSQLSchedulerDriverName = "bridra-sql-scheduler-test"

var testSQLSchedulerDatabases = struct {
	states map[string]*testSQLSchedulerState
	mu     sync.Mutex
}{
	states: make(map[string]*testSQLSchedulerState),
}

func init() {
	sql.Register(testSQLSchedulerDriverName, testSQLSchedulerDriver{})
}

type testSQLSchedulerDriver struct{}

func (testSQLSchedulerDriver) Open(name string) (driver.Conn, error) {
	testSQLSchedulerDatabases.mu.Lock()
	state := testSQLSchedulerDatabases.states[name]
	if state == nil {
		state = &testSQLSchedulerState{
			tasks: make(map[string]testSQLSchedulerRecord),
		}
		testSQLSchedulerDatabases.states[name] = state
	}
	testSQLSchedulerDatabases.mu.Unlock()
	return &testSQLSchedulerConnection{state: state}, nil
}

type testSQLSchedulerConnection struct {
	state *testSQLSchedulerState
}

var (
	_ driver.Conn           = (*testSQLSchedulerConnection)(nil)
	_ driver.ExecerContext  = (*testSQLSchedulerConnection)(nil)
	_ driver.QueryerContext = (*testSQLSchedulerConnection)(nil)
	_ driver.Pinger         = (*testSQLSchedulerConnection)(nil)
)

func (connection *testSQLSchedulerConnection) Prepare(string) (driver.Stmt, error) {
	return nil, driver.ErrSkip
}

func (connection *testSQLSchedulerConnection) Close() error {
	return nil
}

func (connection *testSQLSchedulerConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (connection *testSQLSchedulerConnection) Ping(context.Context) error {
	if connection == nil || connection.state == nil {
		return errors.New("database is unavailable")
	}
	return nil
}

func (connection *testSQLSchedulerConnection) ExecContext(
	ctx context.Context,
	query string,
	arguments []driver.NamedValue,
) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values := testSQLSchedulerValues(arguments)
	connection.state.mu.Lock()
	defer connection.state.mu.Unlock()

	switch {
	case hasSQLPrefix(query, "CREATE TABLE IF NOT EXISTS "):
		connection.state.ensured = true
		return driver.RowsAffected(0), nil
	case hasSQLPrefix(query, "INSERT INTO "):
		return connection.state.initialize(values)
	case containsSQL(query, "SET reservation_token ="):
		return connection.state.reserve(values)
	case containsSQL(query, "SET next_run_at ="):
		return connection.state.complete(values)
	default:
		return nil, fmt.Errorf("unexpected exec query: %s", query)
	}
}

func (connection *testSQLSchedulerConnection) QueryContext(
	ctx context.Context,
	query string,
	arguments []driver.NamedValue,
) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values := testSQLSchedulerValues(arguments)
	connection.state.mu.Lock()
	defer connection.state.mu.Unlock()
	if err := connection.state.ready(); err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("query arguments = %d", len(values))
	}
	name := testSQLSchedulerString(values[0])
	record, exists := connection.state.tasks[name]

	switch {
	case hasSQLPrefix(query, "SELECT 1 FROM "):
		rows := &testSQLSchedulerRows{columns: []string{"exists"}}
		if exists {
			rows.values = [][]driver.Value{{int64(1)}}
		}
		return rows, nil
	case hasSQLPrefix(query, "SELECT name, next_run_at, "):
		rows := &testSQLSchedulerRows{
			columns: []string{
				"name",
				"next_run_at",
				"last_scheduled_at",
				"last_completed_at",
				"last_error",
				"reservation_token",
				"reserved_until",
			},
		}
		if exists {
			rows.values = [][]driver.Value{record.values()}
		}
		return rows, nil
	default:
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
}

type testSQLSchedulerState struct {
	tasks   map[string]testSQLSchedulerRecord
	ensured bool
	mu      sync.Mutex
}

func (state *testSQLSchedulerState) ready() error {
	if !state.ensured {
		return errors.New("scheduled task table does not exist")
	}
	return nil
}

func (state *testSQLSchedulerState) initialize(
	values []driver.Value,
) (driver.Result, error) {
	if err := state.ready(); err != nil {
		return nil, err
	}
	if len(values) != 2 {
		return nil, fmt.Errorf("initialize arguments = %d", len(values))
	}
	name := testSQLSchedulerString(values[0])
	if _, exists := state.tasks[name]; exists {
		return nil, errors.New("duplicate primary key")
	}
	state.tasks[name] = testSQLSchedulerRecord{
		name:      name,
		nextRunAt: testSQLSchedulerInt64(values[1]),
	}
	return driver.RowsAffected(1), nil
}

func (state *testSQLSchedulerState) reserve(
	values []driver.Value,
) (driver.Result, error) {
	if err := state.ready(); err != nil {
		return nil, err
	}
	if len(values) != 6 {
		return nil, fmt.Errorf("reserve arguments = %d", len(values))
	}
	token := testSQLSchedulerString(values[0])
	reservedUntil := testSQLSchedulerInt64(values[1])
	name := testSQLSchedulerString(values[2])
	expectedNextRunAt := testSQLSchedulerInt64(values[3])
	dueAt := testSQLSchedulerInt64(values[4])
	leaseAt := testSQLSchedulerInt64(values[5])
	record, exists := state.tasks[name]
	if !exists ||
		record.nextRunAt != expectedNextRunAt ||
		record.nextRunAt > dueAt ||
		(record.reservationTokenValid && record.reservedUntil > leaseAt) {
		return driver.RowsAffected(0), nil
	}
	record.reservationToken = token
	record.reservationTokenValid = true
	record.reservedUntil = reservedUntil
	record.reservedUntilValid = true
	state.tasks[name] = record
	return driver.RowsAffected(1), nil
}

func (state *testSQLSchedulerState) complete(
	values []driver.Value,
) (driver.Result, error) {
	if err := state.ready(); err != nil {
		return nil, err
	}
	if len(values) != 7 {
		return nil, fmt.Errorf("complete arguments = %d", len(values))
	}
	name := testSQLSchedulerString(values[4])
	token := testSQLSchedulerString(values[5])
	expectedNextRunAt := testSQLSchedulerInt64(values[6])
	record, exists := state.tasks[name]
	if !exists ||
		!record.reservationTokenValid ||
		record.reservationToken != token ||
		record.nextRunAt != expectedNextRunAt {
		return driver.RowsAffected(0), nil
	}
	record.nextRunAt = testSQLSchedulerInt64(values[0])
	record.lastScheduledAt = testSQLSchedulerInt64(values[1])
	record.lastScheduledAtValid = true
	record.lastCompletedAt = testSQLSchedulerInt64(values[2])
	record.lastCompletedAtValid = true
	record.lastError = testSQLSchedulerString(values[3])
	record.lastErrorValid = true
	record.reservationToken = ""
	record.reservationTokenValid = false
	record.reservedUntil = 0
	record.reservedUntilValid = false
	state.tasks[name] = record
	return driver.RowsAffected(1), nil
}

type testSQLSchedulerRecord struct {
	name                  string
	nextRunAt             int64
	lastScheduledAt       int64
	lastScheduledAtValid  bool
	lastCompletedAt       int64
	lastCompletedAtValid  bool
	lastError             string
	lastErrorValid        bool
	reservationToken      string
	reservationTokenValid bool
	reservedUntil         int64
	reservedUntilValid    bool
}

func (record testSQLSchedulerRecord) values() []driver.Value {
	return []driver.Value{
		record.name,
		record.nextRunAt,
		testSQLSchedulerNullableInt64(
			record.lastScheduledAt,
			record.lastScheduledAtValid,
		),
		testSQLSchedulerNullableInt64(
			record.lastCompletedAt,
			record.lastCompletedAtValid,
		),
		testSQLSchedulerNullableString(record.lastError, record.lastErrorValid),
		testSQLSchedulerNullableString(
			record.reservationToken,
			record.reservationTokenValid,
		),
		testSQLSchedulerNullableInt64(
			record.reservedUntil,
			record.reservedUntilValid,
		),
	}
}

type testSQLSchedulerRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (rows *testSQLSchedulerRows) Columns() []string {
	return rows.columns
}

func (rows *testSQLSchedulerRows) Close() error {
	return nil
}

func (rows *testSQLSchedulerRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(destination, rows.values[rows.index])
	rows.index++
	return nil
}

func testSQLSchedulerValues(values []driver.NamedValue) []driver.Value {
	converted := make([]driver.Value, len(values))
	for index, value := range values {
		converted[index] = value.Value
	}
	return converted
}

func testSQLSchedulerString(value driver.Value) string {
	converted, _ := value.(string)
	return converted
}

func testSQLSchedulerInt64(value driver.Value) int64 {
	converted, _ := value.(int64)
	return converted
}

func testSQLSchedulerNullableString(value string, valid bool) driver.Value {
	if !valid {
		return nil
	}
	return value
}

func testSQLSchedulerNullableInt64(value int64, valid bool) driver.Value {
	if !valid {
		return nil
	}
	return value
}

func hasSQLPrefix(value string, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}

func containsSQL(value string, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
