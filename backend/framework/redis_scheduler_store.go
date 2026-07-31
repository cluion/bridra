package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisSchedulerStoreNamespace  = "bridra:scheduler"
	redisSchedulerStoreMaxNamespaceBytes = 256
	redisSchedulerStoreMaxNameBytes      = 255
	redisSchedulerStoreMaxExactTime      = int64(1<<53 - 1)
)

type RedisSchedulerStoreOptions struct {
	Namespace string
}

func DefaultRedisSchedulerStoreOptions() RedisSchedulerStoreOptions {
	return RedisSchedulerStoreOptions{
		Namespace: defaultRedisSchedulerStoreNamespace,
	}
}

type RedisSchedulerStore struct {
	client    redis.Scripter
	namespace string
	keys      []string
}

type redisStoredScheduledTaskMetadata struct {
	Name             string `json:"name"`
	NextRunAt        string `json:"next_run_at"`
	LastScheduledAt  string `json:"last_scheduled_at"`
	LastCompletedAt  string `json:"last_completed_at"`
	LastError        string `json:"last_error"`
	ReservationToken string `json:"reservation_token"`
	ReservedUntil    string `json:"reserved_until"`
}

type redisStoredScheduledTask struct {
	state            StoredScheduledTask
	reservationToken string
}

var _ SchedulerStore = (*RedisSchedulerStore)(nil)

var redisSchedulerStoreInitializeScript = redis.NewScript(`
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 1 then
    return 0
end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
return 1
`)

var redisSchedulerStoreStateScript = redis.NewScript(`
return redis.call('HGET', KEYS[1], ARGV[1])
`)

var redisSchedulerStoreReserveScript = redis.NewScript(`
local encoded = redis.call('HGET', KEYS[1], ARGV[1])
if not encoded then
    return {'missing'}
end

local task = cjson.decode(encoded)
local function integer_string(value)
    if type(value) ~= 'string' or
       string.match(value, '^%-?%d+$') == nil then
        return false
    end
    local number = tonumber(value)
    return number ~= nil and
        number >= -9007199254740991 and
        number <= 9007199254740991
end
if type(task) ~= 'table' or
   task.name ~= ARGV[1] or
   not integer_string(task.next_run_at) or
   type(task.last_scheduled_at) ~= 'string' or
   type(task.last_completed_at) ~= 'string' or
   type(task.last_error) ~= 'string' or
   type(task.reservation_token) ~= 'string' or
   type(task.reserved_until) ~= 'string' or
   ((task.last_scheduled_at == '') ~= (task.last_completed_at == '')) or
   ((task.reservation_token == '') ~= (task.reserved_until == '')) or
   (task.last_scheduled_at == '' and task.last_error ~= '') or
   (task.last_scheduled_at ~= '' and
       (not integer_string(task.last_scheduled_at) or
        not integer_string(task.last_completed_at))) or
   (task.reserved_until ~= '' and not integer_string(task.reserved_until)) then
    return redis.error_reply('bridra: scheduled task state is corrupt')
end

local now = tonumber(ARGV[2])
if task.reservation_token ~= '' and
   tonumber(task.reserved_until) > now then
    return {'reserved'}
end
if tonumber(task.next_run_at) > now then
    return {'not_due'}
end

task.reservation_token = ARGV[3]
task.reserved_until = ARGV[4]
encoded = cjson.encode(task)
redis.call('HSET', KEYS[1], ARGV[1], encoded)
return {'ok', encoded}
`)

var redisSchedulerStoreCompleteScript = redis.NewScript(`
local encoded = redis.call('HGET', KEYS[1], ARGV[1])
if not encoded then
    return 0
end

local task = cjson.decode(encoded)
local function integer_string(value)
    if type(value) ~= 'string' or
       string.match(value, '^%-?%d+$') == nil then
        return false
    end
    local number = tonumber(value)
    return number ~= nil and
        number >= -9007199254740991 and
        number <= 9007199254740991
end
if type(task) ~= 'table' or
   task.name ~= ARGV[1] or
   not integer_string(task.next_run_at) or
   type(task.last_scheduled_at) ~= 'string' or
   type(task.last_completed_at) ~= 'string' or
   type(task.last_error) ~= 'string' or
   type(task.reservation_token) ~= 'string' or
   type(task.reserved_until) ~= 'string' or
   ((task.last_scheduled_at == '') ~= (task.last_completed_at == '')) or
   ((task.reservation_token == '') ~= (task.reserved_until == '')) or
   (task.last_scheduled_at == '' and task.last_error ~= '') or
   (task.last_scheduled_at ~= '' and
       (not integer_string(task.last_scheduled_at) or
        not integer_string(task.last_completed_at))) or
   (task.reserved_until ~= '' and not integer_string(task.reserved_until)) then
    return redis.error_reply('bridra: scheduled task state is corrupt')
end
if task.reservation_token ~= ARGV[2] or
   task.next_run_at ~= ARGV[3] then
    return 0
end
task.next_run_at = ARGV[4]
task.last_scheduled_at = ARGV[3]
task.last_completed_at = ARGV[5]
task.last_error = ARGV[6]
task.reservation_token = ''
task.reserved_until = ''
redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(task))
return 1
`)

func NewRedisSchedulerStore(
	client redis.Scripter,
	options RedisSchedulerStoreOptions,
) (*RedisSchedulerStore, error) {
	if redisScripterIsNil(client) {
		return nil, ErrSchedulerStoreUnavailable
	}
	normalized, err := normalizeRedisSchedulerStoreOptions(options)
	if err != nil {
		return nil, err
	}
	slot := "{" + normalized.Namespace + "}"
	return &RedisSchedulerStore{
		client:    client,
		namespace: normalized.Namespace,
		keys:      []string{slot + ":tasks"},
	}, nil
}

func normalizeRedisSchedulerStoreOptions(
	options RedisSchedulerStoreOptions,
) (RedisSchedulerStoreOptions, error) {
	defaults := DefaultRedisSchedulerStoreOptions()
	options.Namespace = strings.TrimSpace(options.Namespace)
	if options.Namespace == "" {
		options.Namespace = defaults.Namespace
	}
	if len(options.Namespace) > redisSchedulerStoreMaxNamespaceBytes ||
		strings.ContainsAny(options.Namespace, "{}") {
		return RedisSchedulerStoreOptions{}, ErrInvalidRedisSchedulerStoreOptions
	}
	for _, character := range options.Namespace {
		if character < 0x20 || character == 0x7f {
			return RedisSchedulerStoreOptions{}, ErrInvalidRedisSchedulerStoreOptions
		}
	}
	return options, nil
}

func (store *RedisSchedulerStore) Namespace() string {
	if store == nil {
		return ""
	}
	return store.namespace
}

func (store *RedisSchedulerStore) Initialize(
	ctx context.Context,
	name string,
	nextRunAt time.Time,
) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if !validRedisScheduledTaskName(name) || nextRunAt.IsZero() {
		return ErrSchedulerStoreConflict
	}
	nextRunAtValue, err := redisSchedulerStoreTime(nextRunAt.UTC())
	if err != nil {
		return ErrSchedulerStoreConflict
	}
	encoded, err := json.Marshal(redisStoredScheduledTaskMetadata{
		Name:      name,
		NextRunAt: nextRunAtValue,
	})
	if err != nil {
		return redisSchedulerStoreError("encode task", err)
	}
	result, err := redisSchedulerStoreInitializeScript.Run(
		ctx,
		store.client,
		store.keys,
		name,
		encoded,
	).Int64()
	if err != nil {
		return redisSchedulerStoreError("initialize task", err)
	}
	if result != 0 && result != 1 {
		return redisSchedulerStoreError(
			"initialize task",
			fmt.Errorf("result %d", result),
		)
	}
	return nil
}

func (store *RedisSchedulerStore) State(
	ctx context.Context,
	name string,
) (StoredScheduledTask, error) {
	if err := store.ready(ctx); err != nil {
		return StoredScheduledTask{}, err
	}
	if !validRedisScheduledTaskName(name) {
		return StoredScheduledTask{}, ErrSchedulerStoreConflict
	}
	encoded, err := redisSchedulerStoreStateScript.Run(
		ctx,
		store.client,
		store.keys,
		name,
	).Text()
	if errors.Is(err, redis.Nil) {
		return StoredScheduledTask{}, ErrScheduledTaskStateNotFound
	}
	if err != nil {
		return StoredScheduledTask{}, redisSchedulerStoreError("read task state", err)
	}
	record, err := decodeRedisStoredScheduledTask(encoded)
	if err != nil {
		return StoredScheduledTask{}, redisSchedulerStoreError("decode task state", err)
	}
	if record.state.Name != name {
		return StoredScheduledTask{}, redisSchedulerStoreError(
			"decode task state",
			errors.New("task name does not match"),
		)
	}
	return record.state, nil
}

func (store *RedisSchedulerStore) Reserve(
	ctx context.Context,
	name string,
	now time.Time,
	lease time.Duration,
) (ScheduledTaskReservation, error) {
	if err := store.ready(ctx); err != nil {
		return ScheduledTaskReservation{}, err
	}
	if !validRedisScheduledTaskName(name) || now.IsZero() || lease <= 0 {
		return ScheduledTaskReservation{}, ErrScheduledTaskReservationInvalid
	}
	now = now.UTC()
	reservedUntil := now.Add(lease)
	nowValue, err := redisSchedulerStoreTime(now)
	if err != nil {
		return ScheduledTaskReservation{}, ErrScheduledTaskReservationInvalid
	}
	reservedUntilValue, err := redisSchedulerStoreTime(reservedUntil)
	if err != nil {
		return ScheduledTaskReservation{}, ErrScheduledTaskReservationInvalid
	}
	token, err := newSchedulerReservationToken()
	if err != nil {
		return ScheduledTaskReservation{}, err
	}
	values, err := redisSchedulerStoreReserveScript.Run(
		ctx,
		store.client,
		store.keys,
		name,
		nowValue,
		token,
		reservedUntilValue,
	).StringSlice()
	if err != nil {
		return ScheduledTaskReservation{}, redisSchedulerStoreError("reserve task", err)
	}
	if len(values) == 1 {
		switch values[0] {
		case "missing":
			return ScheduledTaskReservation{}, ErrScheduledTaskStateNotFound
		case "reserved":
			return ScheduledTaskReservation{}, ErrScheduledTaskReserved
		case "not_due":
			return ScheduledTaskReservation{}, ErrScheduledTaskNotDue
		}
	}
	if len(values) != 2 || values[0] != "ok" {
		return ScheduledTaskReservation{}, redisSchedulerStoreError(
			"reserve task",
			fmt.Errorf("returned %d unexpected values", len(values)),
		)
	}
	record, err := decodeRedisStoredScheduledTask(values[1])
	if err != nil {
		return ScheduledTaskReservation{}, redisSchedulerStoreError(
			"decode reserved task",
			err,
		)
	}
	if record.state.Name != name ||
		record.reservationToken != token ||
		!record.state.ReservedUntil.Equal(reservedUntil) {
		return ScheduledTaskReservation{}, redisSchedulerStoreError(
			"decode reserved task",
			errors.New("reservation state does not match"),
		)
	}
	return ScheduledTaskReservation{
		Task:          record.state,
		Token:         token,
		ReservedUntil: reservedUntil,
	}, nil
}

func (store *RedisSchedulerStore) Complete(
	ctx context.Context,
	reservation ScheduledTaskReservation,
	nextRunAt time.Time,
	completedAt time.Time,
	lastError string,
) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if !validRedisScheduledTaskName(reservation.Task.Name) ||
		reservation.Token == "" ||
		reservation.Task.NextRunAt.IsZero() ||
		nextRunAt.IsZero() ||
		completedAt.IsZero() ||
		!nextRunAt.After(completedAt) {
		return ErrScheduledTaskReservationInvalid
	}
	scheduledAtValue, err := redisSchedulerStoreTime(
		reservation.Task.NextRunAt.UTC(),
	)
	if err != nil {
		return ErrScheduledTaskReservationInvalid
	}
	nextRunAtValue, err := redisSchedulerStoreTime(nextRunAt.UTC())
	if err != nil {
		return ErrScheduledTaskReservationInvalid
	}
	completedAtValue, err := redisSchedulerStoreTime(completedAt.UTC())
	if err != nil {
		return ErrScheduledTaskReservationInvalid
	}
	result, err := redisSchedulerStoreCompleteScript.Run(
		ctx,
		store.client,
		store.keys,
		reservation.Task.Name,
		reservation.Token,
		scheduledAtValue,
		nextRunAtValue,
		completedAtValue,
		normalizeFileSchedulerStoreError(lastError),
	).Int64()
	if err != nil {
		return redisSchedulerStoreError("complete task", err)
	}
	if result == 0 {
		return ErrScheduledTaskReservationInvalid
	}
	if result != 1 {
		return redisSchedulerStoreError("complete task", fmt.Errorf("result %d", result))
	}
	return nil
}

func (store *RedisSchedulerStore) ready(ctx context.Context) error {
	if store == nil || redisScripterIsNil(store.client) {
		return ErrSchedulerStoreUnavailable
	}
	if ctx == nil {
		return ErrSchedulerContextUnavailable
	}
	return ctx.Err()
}

func decodeRedisStoredScheduledTask(
	encoded string,
) (redisStoredScheduledTask, error) {
	var metadata redisStoredScheduledTaskMetadata
	decoder := json.NewDecoder(bytes.NewBufferString(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return redisStoredScheduledTask{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return redisStoredScheduledTask{}, errors.New(
			"Redis scheduled task metadata must contain one object",
		)
	}
	if !validRedisScheduledTaskName(metadata.Name) ||
		(metadata.LastScheduledAt == "") != (metadata.LastCompletedAt == "") ||
		(metadata.ReservationToken == "") != (metadata.ReservedUntil == "") ||
		(metadata.LastScheduledAt == "" && metadata.LastError != "") ||
		len(metadata.LastError) > fileSchedulerStoreMaxErrorBytes {
		return redisStoredScheduledTask{}, errors.New(
			"invalid Redis scheduled task metadata",
		)
	}
	nextRunAt, err := parseRedisSchedulerStoreTime(metadata.NextRunAt)
	if err != nil {
		return redisStoredScheduledTask{}, err
	}
	record := redisStoredScheduledTask{
		state: StoredScheduledTask{
			Name:      metadata.Name,
			NextRunAt: nextRunAt,
			LastError: metadata.LastError,
		},
		reservationToken: metadata.ReservationToken,
	}
	if metadata.LastScheduledAt != "" {
		record.state.LastScheduledAt, err = parseRedisSchedulerStoreTime(
			metadata.LastScheduledAt,
		)
		if err != nil {
			return redisStoredScheduledTask{}, err
		}
		record.state.LastCompletedAt, err = parseRedisSchedulerStoreTime(
			metadata.LastCompletedAt,
		)
		if err != nil {
			return redisStoredScheduledTask{}, err
		}
	}
	if metadata.ReservedUntil != "" {
		record.state.ReservedUntil, err = parseRedisSchedulerStoreTime(
			metadata.ReservedUntil,
		)
		if err != nil {
			return redisStoredScheduledTask{}, err
		}
	}
	return record, nil
}

func validRedisScheduledTaskName(name string) bool {
	return validStoredScheduledTaskName(name) &&
		len(name) <= redisSchedulerStoreMaxNameBytes
}

func redisSchedulerStoreTime(value time.Time) (string, error) {
	if value.IsZero() {
		return "", errors.New("Redis scheduler time is zero")
	}
	microseconds := value.UTC().UnixMicro()
	if microseconds < -redisSchedulerStoreMaxExactTime ||
		microseconds > redisSchedulerStoreMaxExactTime {
		return "", errors.New("Redis scheduler time exceeds exact range")
	}
	return strconv.FormatInt(microseconds, 10), nil
}

func parseRedisSchedulerStoreTime(value string) (time.Time, error) {
	microseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil ||
		microseconds < -redisSchedulerStoreMaxExactTime ||
		microseconds > redisSchedulerStoreMaxExactTime {
		return time.Time{}, errors.New("invalid Redis scheduler time")
	}
	return time.UnixMicro(microseconds).UTC(), nil
}

func redisSchedulerStoreError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrSchedulerStoreOperationFailed, operation, err)
}
