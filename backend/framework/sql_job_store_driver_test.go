package framework

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

const testSQLJobDriverName = "bridra-sql-job-test"

var testSQLJobDatabases = struct {
	states map[string]*testSQLJobState
	mu     sync.Mutex
}{
	states: make(map[string]*testSQLJobState),
}

func init() {
	sql.Register(testSQLJobDriverName, testSQLJobDriver{})
}

type testSQLJobDriver struct{}

func (testSQLJobDriver) Open(name string) (driver.Conn, error) {
	testSQLJobDatabases.mu.Lock()
	state := testSQLJobDatabases.states[name]
	if state == nil {
		state = &testSQLJobState{jobs: make(map[string]testSQLJobRecord)}
		testSQLJobDatabases.states[name] = state
	}
	testSQLJobDatabases.mu.Unlock()
	return &testSQLJobConnection{state: state}, nil
}

type testSQLJobConnection struct {
	state *testSQLJobState
}

var (
	_ driver.Conn           = (*testSQLJobConnection)(nil)
	_ driver.ExecerContext  = (*testSQLJobConnection)(nil)
	_ driver.QueryerContext = (*testSQLJobConnection)(nil)
	_ driver.Pinger         = (*testSQLJobConnection)(nil)
)

func (connection *testSQLJobConnection) Prepare(string) (driver.Stmt, error) {
	return nil, driver.ErrSkip
}

func (connection *testSQLJobConnection) Close() error {
	return nil
}

func (connection *testSQLJobConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (connection *testSQLJobConnection) Ping(context.Context) error {
	if connection == nil || connection.state == nil {
		return errors.New("database is unavailable")
	}
	return nil
}

func (connection *testSQLJobConnection) ExecContext(
	ctx context.Context,
	query string,
	arguments []driver.NamedValue,
) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values := testSQLJobValues(arguments)
	connection.state.mu.Lock()
	defer connection.state.mu.Unlock()

	switch {
	case strings.HasPrefix(query, "CREATE TABLE IF NOT EXISTS "):
		connection.state.ensured = true
		return driver.RowsAffected(0), nil
	case strings.HasPrefix(query, "INSERT INTO "):
		if err := connection.state.ready(); err != nil {
			return nil, err
		}
		if len(values) != 6 {
			return nil, fmt.Errorf("insert arguments = %d", len(values))
		}
		id := testSQLJobString(values[0])
		if _, exists := connection.state.jobs[id]; exists {
			return nil, errors.New("duplicate primary key")
		}
		connection.state.jobs[id] = testSQLJobRecord{
			id:          id,
			handler:     testSQLJobString(values[1]),
			payload:     testSQLJobString(values[2]),
			availableAt: testSQLJobInt64(values[3]),
			enqueuedAt:  testSQLJobInt64(values[4]),
			attempts:    testSQLJobInt64(values[5]),
		}
		return driver.RowsAffected(1), nil
	case strings.Contains(query, "attempts = attempts + 1"):
		return connection.state.reserve(values)
	case strings.Contains(query, "SET attempts = 0"):
		return connection.state.retry(values)
	case strings.Contains(query, "SET available_at ="):
		return connection.state.release(values)
	case strings.Contains(query, "failed_at = ") &&
		!strings.Contains(query, "failed_at = NULL"):
		return connection.state.fail(values)
	case strings.HasPrefix(query, "DELETE FROM ") &&
		strings.Contains(query, "failed_at IS NOT NULL"):
		return connection.state.forget(values)
	case strings.HasPrefix(query, "DELETE FROM "):
		return connection.state.complete(values)
	default:
		return nil, fmt.Errorf("unexpected exec query: %s", query)
	}
}

func (connection *testSQLJobConnection) QueryContext(
	ctx context.Context,
	query string,
	arguments []driver.NamedValue,
) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values := testSQLJobValues(arguments)
	connection.state.mu.Lock()
	defer connection.state.mu.Unlock()
	if err := connection.state.ready(); err != nil {
		return nil, err
	}

	switch {
	case strings.HasPrefix(query, "SELECT 1 FROM "):
		record, exists := connection.state.jobs[testSQLJobString(values[0])]
		if !exists || record.id == "" {
			return &testSQLJobRows{columns: []string{"exists"}}, nil
		}
		return &testSQLJobRows{
			columns: []string{"exists"},
			values:  [][]driver.Value{{int64(1)}},
		}, nil
	case strings.Contains(query, "ORDER BY available_at ASC"):
		return connection.state.readyRows(values), nil
	case strings.Contains(query, "WHERE failed_at IS NOT NULL ORDER BY"):
		return connection.state.failedRows(), nil
	default:
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
}

type testSQLJobState struct {
	jobs    map[string]testSQLJobRecord
	ensured bool
	mu      sync.Mutex
}

func (state *testSQLJobState) ready() error {
	if !state.ensured {
		return errors.New("job table does not exist")
	}
	return nil
}

func (state *testSQLJobState) reserve(values []driver.Value) (driver.Result, error) {
	if err := state.ready(); err != nil {
		return nil, err
	}
	if len(values) != 6 {
		return nil, fmt.Errorf("reserve arguments = %d", len(values))
	}
	token := testSQLJobString(values[0])
	reservedUntil := testSQLJobInt64(values[1])
	id := testSQLJobString(values[2])
	attempts := testSQLJobInt64(values[3])
	availableNow := testSQLJobInt64(values[4])
	leaseNow := testSQLJobInt64(values[5])
	record, exists := state.jobs[id]
	if !exists ||
		record.attempts != attempts ||
		record.failedAtValid ||
		record.availableAt > availableNow ||
		(record.reservationToken != "" && record.reservedUntil > leaseNow) {
		return driver.RowsAffected(0), nil
	}
	record.reservationToken = token
	record.reservedUntil = reservedUntil
	record.attempts++
	state.jobs[id] = record
	return driver.RowsAffected(1), nil
}

func (state *testSQLJobState) release(values []driver.Value) (driver.Result, error) {
	if err := state.ready(); err != nil {
		return nil, err
	}
	if len(values) != 4 {
		return nil, fmt.Errorf("release arguments = %d", len(values))
	}
	id := testSQLJobString(values[2])
	token := testSQLJobString(values[3])
	record, exists := state.jobs[id]
	if !exists || record.reservationToken != token {
		return driver.RowsAffected(0), nil
	}
	record.availableAt = testSQLJobInt64(values[0])
	record.lastError = testSQLJobString(values[1])
	record.lastErrorValid = true
	record.reservationToken = ""
	record.reservedUntil = 0
	state.jobs[id] = record
	return driver.RowsAffected(1), nil
}

func (state *testSQLJobState) complete(values []driver.Value) (driver.Result, error) {
	if err := state.ready(); err != nil {
		return nil, err
	}
	if len(values) != 2 {
		return nil, fmt.Errorf("complete arguments = %d", len(values))
	}
	id := testSQLJobString(values[0])
	token := testSQLJobString(values[1])
	record, exists := state.jobs[id]
	if !exists || record.reservationToken != token {
		return driver.RowsAffected(0), nil
	}
	delete(state.jobs, id)
	return driver.RowsAffected(1), nil
}

func (state *testSQLJobState) fail(values []driver.Value) (driver.Result, error) {
	if err := state.ready(); err != nil {
		return nil, err
	}
	if len(values) != 4 {
		return nil, fmt.Errorf("fail arguments = %d", len(values))
	}
	id := testSQLJobString(values[2])
	token := testSQLJobString(values[3])
	record, exists := state.jobs[id]
	if !exists || record.reservationToken != token {
		return driver.RowsAffected(0), nil
	}
	record.reservationToken = ""
	record.reservedUntil = 0
	record.failedAt = testSQLJobInt64(values[0])
	record.failedAtValid = true
	record.lastError = testSQLJobString(values[1])
	record.lastErrorValid = true
	state.jobs[id] = record
	return driver.RowsAffected(1), nil
}

func (state *testSQLJobState) retry(values []driver.Value) (driver.Result, error) {
	if err := state.ready(); err != nil {
		return nil, err
	}
	if len(values) != 2 {
		return nil, fmt.Errorf("retry arguments = %d", len(values))
	}
	id := testSQLJobString(values[1])
	record, exists := state.jobs[id]
	if !exists || !record.failedAtValid {
		return driver.RowsAffected(0), nil
	}
	record.attempts = 0
	record.availableAt = testSQLJobInt64(values[0])
	record.failedAt = 0
	record.failedAtValid = false
	record.lastError = ""
	record.lastErrorValid = false
	state.jobs[id] = record
	return driver.RowsAffected(1), nil
}

func (state *testSQLJobState) forget(values []driver.Value) (driver.Result, error) {
	if err := state.ready(); err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("forget arguments = %d", len(values))
	}
	id := testSQLJobString(values[0])
	record, exists := state.jobs[id]
	if !exists || !record.failedAtValid {
		return driver.RowsAffected(0), nil
	}
	delete(state.jobs, id)
	return driver.RowsAffected(1), nil
}

func (state *testSQLJobState) readyRows(values []driver.Value) driver.Rows {
	if len(values) != 2 {
		return &testSQLJobRows{columns: testSQLReadyColumns}
	}
	availableNow := testSQLJobInt64(values[0])
	leaseNow := testSQLJobInt64(values[1])
	eligible := make([]testSQLJobRecord, 0)
	for _, record := range state.jobs {
		if record.failedAtValid ||
			record.availableAt > availableNow ||
			(record.reservationToken != "" && record.reservedUntil > leaseNow) {
			continue
		}
		eligible = append(eligible, record)
	}
	sort.Slice(eligible, func(left int, right int) bool {
		if eligible[left].availableAt != eligible[right].availableAt {
			return eligible[left].availableAt < eligible[right].availableAt
		}
		if eligible[left].enqueuedAt != eligible[right].enqueuedAt {
			return eligible[left].enqueuedAt < eligible[right].enqueuedAt
		}
		return eligible[left].id < eligible[right].id
	})
	rows := testSQLJobRows{columns: testSQLReadyColumns}
	if len(eligible) == 0 {
		return &rows
	}
	record := eligible[0]
	rows.values = [][]driver.Value{{
		record.id,
		record.handler,
		record.payload,
		record.availableAt,
		record.enqueuedAt,
		record.attempts,
	}}
	return &rows
}

func (state *testSQLJobState) failedRows() driver.Rows {
	failed := make([]testSQLJobRecord, 0)
	for _, record := range state.jobs {
		if record.failedAtValid {
			failed = append(failed, record)
		}
	}
	sort.Slice(failed, func(left int, right int) bool {
		if failed[left].failedAt != failed[right].failedAt {
			return failed[left].failedAt < failed[right].failedAt
		}
		return failed[left].id < failed[right].id
	})
	rows := testSQLJobRows{columns: testSQLFailedColumns}
	for _, record := range failed {
		var lastError driver.Value
		if record.lastErrorValid {
			lastError = record.lastError
		}
		rows.values = append(rows.values, []driver.Value{
			record.id,
			record.handler,
			record.payload,
			record.availableAt,
			record.enqueuedAt,
			record.attempts,
			record.failedAt,
			lastError,
		})
	}
	return &rows
}

type testSQLJobRecord struct {
	id               string
	handler          string
	payload          string
	availableAt      int64
	enqueuedAt       int64
	attempts         int64
	reservationToken string
	reservedUntil    int64
	failedAt         int64
	failedAtValid    bool
	lastError        string
	lastErrorValid   bool
}

var (
	testSQLReadyColumns = []string{
		"id",
		"handler",
		"payload",
		"available_at",
		"enqueued_at",
		"attempts",
	}
	testSQLFailedColumns = append(
		append([]string(nil), testSQLReadyColumns...),
		"failed_at",
		"last_error",
	)
)

type testSQLJobRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (rows *testSQLJobRows) Columns() []string {
	return rows.columns
}

func (*testSQLJobRows) Close() error {
	return nil
}

func (rows *testSQLJobRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(destination, rows.values[rows.index])
	rows.index++
	return nil
}

func testSQLJobValues(arguments []driver.NamedValue) []driver.Value {
	values := make([]driver.Value, len(arguments))
	for index, argument := range arguments {
		values[index] = argument.Value
	}
	return values
}

func testSQLJobString(value driver.Value) string {
	text, _ := value.(string)
	return text
}

func testSQLJobInt64(value driver.Value) int64 {
	number, _ := value.(int64)
	return number
}
