package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisJobStoreNamespace  = "bridra:jobs"
	redisJobStoreMaxNamespaceBytes = 256
	redisJobStoreRecoveryBatch     = 128
	redisJobStoreMaxExactTime      = int64(1<<53 - 1)
)

type RedisJobStoreOptions struct {
	Namespace       string
	MaxPayloadBytes int
}

func DefaultRedisJobStoreOptions() RedisJobStoreOptions {
	return RedisJobStoreOptions{
		Namespace:       defaultRedisJobStoreNamespace,
		MaxPayloadBytes: defaultFileJobStoreMaxPayloadBytes,
	}
}

type RedisJobStore struct {
	client          redis.Scripter
	namespace       string
	maxPayloadBytes int
	keys            []string
}

type redisStoredJobMetadata struct {
	ID               string `json:"id"`
	Handler          string `json:"handler"`
	AvailableAt      string `json:"available_at"`
	EnqueuedAt       string `json:"enqueued_at"`
	Attempts         string `json:"attempts"`
	ReadyMember      string `json:"ready_member"`
	ReservationToken string `json:"reservation_token"`
	ReservedUntil    string `json:"reserved_until"`
	FailedAt         string `json:"failed_at"`
	LastError        string `json:"last_error"`
}

var _ JobStore = (*RedisJobStore)(nil)

var redisJobStoreEnqueueScript = redis.NewScript(`
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 1 or
   redis.call('HEXISTS', KEYS[2], ARGV[1]) == 1 then
    return 0
end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
redis.call('HSET', KEYS[2], ARGV[1], ARGV[3])
redis.call('ZADD', KEYS[3], ARGV[4], ARGV[5])
return 1
`)

var redisJobStoreReserveScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local expired = redis.call(
    'ZRANGEBYSCORE', KEYS[4], '-inf', ARGV[1],
    'LIMIT', 0, tonumber(ARGV[4])
)
for _, id in ipairs(expired) do
    local encoded = redis.call('HGET', KEYS[1], id)
    if not encoded then
        redis.call('ZREM', KEYS[4], id)
    else
        local job = cjson.decode(encoded)
        if job.failed_at ~= '' then
            return redis.error_reply('bridra: reserved job is failed')
        end
        if job.reservation_token == '' then
            redis.call('ZREM', KEYS[4], id)
        elseif tonumber(job.reserved_until) <= now then
            job.reservation_token = ''
            job.reserved_until = ''
            redis.call('HSET', KEYS[1], id, cjson.encode(job))
            redis.call('ZREM', KEYS[4], id)
            redis.call('ZADD', KEYS[3], job.available_at, job.ready_member)
        end
    end
end

local candidate = redis.call('ZRANGE', KEYS[3], 0, 0, 'WITHSCORES')
if #candidate == 0 or tonumber(candidate[2]) > now then
    if #expired == tonumber(ARGV[4]) then
        return {'retry'}
    end
    return {}
end

local member = candidate[1]
local id = string.sub(member, 18)
local encoded = redis.call('HGET', KEYS[1], id)
local payload = redis.call('HGET', KEYS[2], id)
if not encoded or not payload then
    return redis.error_reply('bridra: ready job data is missing')
end
local job = cjson.decode(encoded)
if job.id ~= id or job.ready_member ~= member or
   job.failed_at ~= '' or job.reservation_token ~= '' then
    return redis.error_reply('bridra: ready job state is corrupt')
end

local attempts = tonumber(job.attempts)
if not attempts or attempts < 0 then
    return redis.error_reply('bridra: job attempt count is corrupt')
end
job.attempts = tostring(attempts + 1)
job.reservation_token = ARGV[2]
job.reserved_until = ARGV[3]
encoded = cjson.encode(job)
redis.call('HSET', KEYS[1], id, encoded)
redis.call('ZREM', KEYS[3], member)
redis.call('ZADD', KEYS[4], ARGV[3], id)
return {encoded, payload}
`)

var redisJobStoreReleaseScript = redis.NewScript(`
local encoded = redis.call('HGET', KEYS[1], ARGV[1])
if not encoded then
    return 0
end
local job = cjson.decode(encoded)
if job.reservation_token ~= ARGV[2] then
    return 0
end
job.available_at = ARGV[3]
job.reservation_token = ''
job.reserved_until = ''
job.last_error = ARGV[4]
redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(job))
redis.call('ZREM', KEYS[4], ARGV[1])
redis.call('ZADD', KEYS[3], ARGV[3], job.ready_member)
return 1
`)

var redisJobStoreCompleteScript = redis.NewScript(`
local encoded = redis.call('HGET', KEYS[1], ARGV[1])
if not encoded then
    return 0
end
local job = cjson.decode(encoded)
if job.reservation_token ~= ARGV[2] then
    return 0
end
redis.call('HDEL', KEYS[1], ARGV[1])
redis.call('HDEL', KEYS[2], ARGV[1])
redis.call('ZREM', KEYS[3], job.ready_member)
redis.call('ZREM', KEYS[4], ARGV[1])
redis.call('ZREM', KEYS[5], ARGV[1])
return 1
`)

var redisJobStoreFailScript = redis.NewScript(`
local encoded = redis.call('HGET', KEYS[1], ARGV[1])
if not encoded then
    return 0
end
local job = cjson.decode(encoded)
if job.reservation_token ~= ARGV[2] then
    return 0
end
job.reservation_token = ''
job.reserved_until = ''
job.failed_at = ARGV[3]
job.last_error = ARGV[4]
redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(job))
redis.call('ZREM', KEYS[3], job.ready_member)
redis.call('ZREM', KEYS[4], ARGV[1])
redis.call('ZADD', KEYS[5], ARGV[3], ARGV[1])
return 1
`)

var redisJobStoreFailedJobsScript = redis.NewScript(`
local ids = redis.call('ZRANGE', KEYS[5], 0, -1)
local result = {}
for _, id in ipairs(ids) do
    local encoded = redis.call('HGET', KEYS[1], id)
    local payload = redis.call('HGET', KEYS[2], id)
    if not encoded or not payload then
        return redis.error_reply('bridra: failed job data is missing')
    end
    table.insert(result, encoded)
    table.insert(result, payload)
end
return result
`)

var redisJobStoreRetryFailedScript = redis.NewScript(`
local encoded = redis.call('HGET', KEYS[1], ARGV[1])
if not encoded then
    return 0
end
local job = cjson.decode(encoded)
if job.failed_at == '' then
    return 0
end
job.attempts = '0'
job.available_at = ARGV[2]
job.reservation_token = ''
job.reserved_until = ''
job.failed_at = ''
job.last_error = ''
redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(job))
redis.call('ZREM', KEYS[5], ARGV[1])
redis.call('ZADD', KEYS[3], ARGV[2], job.ready_member)
return 1
`)

var redisJobStoreForgetFailedScript = redis.NewScript(`
local encoded = redis.call('HGET', KEYS[1], ARGV[1])
if not encoded then
    return 0
end
local job = cjson.decode(encoded)
if job.failed_at == '' then
    return 0
end
redis.call('HDEL', KEYS[1], ARGV[1])
redis.call('HDEL', KEYS[2], ARGV[1])
redis.call('ZREM', KEYS[3], job.ready_member)
redis.call('ZREM', KEYS[4], ARGV[1])
redis.call('ZREM', KEYS[5], ARGV[1])
return 1
`)

func NewRedisJobStore(
	client redis.Scripter,
	options RedisJobStoreOptions,
) (*RedisJobStore, error) {
	if redisScripterIsNil(client) {
		return nil, ErrJobStoreUnavailable
	}
	normalized, err := normalizeRedisJobStoreOptions(options)
	if err != nil {
		return nil, err
	}
	slot := "{" + normalized.Namespace + "}"
	return &RedisJobStore{
		client:          client,
		namespace:       normalized.Namespace,
		maxPayloadBytes: normalized.MaxPayloadBytes,
		keys: []string{
			slot + ":records",
			slot + ":payloads",
			slot + ":ready",
			slot + ":reserved",
			slot + ":failed",
		},
	}, nil
}

func normalizeRedisJobStoreOptions(
	options RedisJobStoreOptions,
) (RedisJobStoreOptions, error) {
	defaults := DefaultRedisJobStoreOptions()
	options.Namespace = strings.TrimSpace(options.Namespace)
	if options.Namespace == "" {
		options.Namespace = defaults.Namespace
	}
	if options.MaxPayloadBytes == 0 {
		options.MaxPayloadBytes = defaults.MaxPayloadBytes
	}
	if len(options.Namespace) > redisJobStoreMaxNamespaceBytes ||
		strings.ContainsAny(options.Namespace, "{}") ||
		options.MaxPayloadBytes < 0 {
		return RedisJobStoreOptions{}, ErrInvalidRedisJobStoreOptions
	}
	for _, character := range options.Namespace {
		if character < 0x20 || character == 0x7f {
			return RedisJobStoreOptions{}, ErrInvalidRedisJobStoreOptions
		}
	}
	return options, nil
}

func (store *RedisJobStore) Namespace() string {
	if store == nil {
		return ""
	}
	return store.namespace
}

func (store *RedisJobStore) Enqueue(ctx context.Context, job StoredJob) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if err := validateStoredJob(job, store.maxPayloadBytes); err != nil {
		return err
	}
	metadata, err := newRedisStoredJobMetadata(job)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return redisJobStoreError("encode job", err)
	}
	result, err := redisJobStoreEnqueueScript.Run(
		ctx,
		store.client,
		store.keys,
		job.ID,
		encoded,
		[]byte(job.Payload),
		metadata.AvailableAt,
		metadata.ReadyMember,
	).Int64()
	if err != nil {
		return redisJobStoreError("enqueue job", err)
	}
	if result == 0 {
		return ErrJobStoreConflict
	}
	if result != 1 {
		return redisJobStoreError("enqueue job", fmt.Errorf("result %d", result))
	}
	return nil
}

func (store *RedisJobStore) Reserve(
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
	nowValue, err := redisJobStoreTime(now)
	if err != nil {
		return JobReservation{}, ErrJobReservationInvalid
	}
	reservedUntilValue, err := redisJobStoreTime(reservedUntil)
	if err != nil {
		return JobReservation{}, ErrJobReservationInvalid
	}

	for {
		if err := ctx.Err(); err != nil {
			return JobReservation{}, err
		}
		token, err := newJobIdentifier()
		if err != nil {
			return JobReservation{}, err
		}
		values, err := redisJobStoreReserveScript.Run(
			ctx,
			store.client,
			store.keys,
			nowValue,
			token,
			reservedUntilValue,
			redisJobStoreRecoveryBatch,
		).StringSlice()
		if err != nil {
			return JobReservation{}, redisJobStoreError("reserve job", err)
		}
		if len(values) == 0 {
			return JobReservation{}, ErrJobStoreEmpty
		}
		if len(values) == 1 && values[0] == "retry" {
			continue
		}
		if len(values) != 2 {
			return JobReservation{}, redisJobStoreError(
				"reserve job",
				fmt.Errorf("returned %d values", len(values)),
			)
		}
		metadata, job, err := decodeRedisStoredJob(
			values[0],
			values[1],
			store.maxPayloadBytes,
		)
		if err != nil {
			return JobReservation{}, redisJobStoreError("decode reserved job", err)
		}
		if metadata.ReservationToken != token ||
			metadata.ReservedUntil != reservedUntilValue {
			return JobReservation{}, redisJobStoreError(
				"decode reserved job",
				errors.New("reservation state does not match"),
			)
		}
		return JobReservation{
			Job:           job,
			Token:         token,
			ReservedUntil: reservedUntil,
		}, nil
	}
}

func (store *RedisJobStore) Release(
	ctx context.Context,
	reservation JobReservation,
	availableAt time.Time,
	lastError string,
) error {
	if availableAt.IsZero() {
		return ErrJobReservationInvalid
	}
	if err := validateRedisReservation(reservation); err != nil {
		return err
	}
	if err := store.ready(ctx); err != nil {
		return err
	}
	availableAtValue, err := redisJobStoreTime(availableAt.UTC())
	if err != nil {
		return ErrJobReservationInvalid
	}
	return store.requireOne(
		ctx,
		redisJobStoreReleaseScript,
		"release job",
		ErrJobReservationInvalid,
		reservation.Job.ID,
		reservation.Token,
		availableAtValue,
		normalizeFileJobStoreError(lastError),
	)
}

func (store *RedisJobStore) Complete(
	ctx context.Context,
	reservation JobReservation,
) error {
	if err := validateRedisReservation(reservation); err != nil {
		return err
	}
	if err := store.ready(ctx); err != nil {
		return err
	}
	return store.requireOne(
		ctx,
		redisJobStoreCompleteScript,
		"complete job",
		ErrJobReservationInvalid,
		reservation.Job.ID,
		reservation.Token,
	)
}

func (store *RedisJobStore) Fail(
	ctx context.Context,
	reservation JobReservation,
	lastError string,
) error {
	if err := validateRedisReservation(reservation); err != nil {
		return err
	}
	if err := store.ready(ctx); err != nil {
		return err
	}
	failedAt, err := redisJobStoreTime(time.Now().UTC())
	if err != nil {
		return redisJobStoreError("fail job", err)
	}
	return store.requireOne(
		ctx,
		redisJobStoreFailScript,
		"fail job",
		ErrJobReservationInvalid,
		reservation.Job.ID,
		reservation.Token,
		failedAt,
		normalizeFileJobStoreError(lastError),
	)
}

func (store *RedisJobStore) FailedJobs(
	ctx context.Context,
) ([]FailedStoredJob, error) {
	if err := store.ready(ctx); err != nil {
		return nil, err
	}
	values, err := redisJobStoreFailedJobsScript.Run(
		ctx,
		store.client,
		store.keys,
	).StringSlice()
	if err != nil {
		return nil, redisJobStoreError("query failed jobs", err)
	}
	if len(values)%2 != 0 {
		return nil, redisJobStoreError(
			"query failed jobs",
			fmt.Errorf("returned %d values", len(values)),
		)
	}
	failed := make([]FailedStoredJob, 0, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		metadata, job, err := decodeRedisStoredJob(
			values[index],
			values[index+1],
			store.maxPayloadBytes,
		)
		if err != nil {
			return nil, redisJobStoreError("decode failed job", err)
		}
		failedAt, err := parseRedisJobStoreTime(metadata.FailedAt)
		if err != nil {
			return nil, redisJobStoreError("decode failed job", err)
		}
		failed = append(failed, FailedStoredJob{
			Job:      job,
			FailedAt: failedAt,
			Error:    metadata.LastError,
		})
	}
	return failed, nil
}

func (store *RedisJobStore) RetryFailed(
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
	availableAtValue, err := redisJobStoreTime(availableAt.UTC())
	if err != nil {
		return ErrJobStoreConflict
	}
	return store.requireOne(
		ctx,
		redisJobStoreRetryFailedScript,
		"retry failed job",
		ErrJobStoreConflict,
		id,
		availableAtValue,
	)
}

func (store *RedisJobStore) ForgetFailed(ctx context.Context, id string) error {
	if id == "" {
		return ErrJobStoreConflict
	}
	if err := store.ready(ctx); err != nil {
		return err
	}
	return store.requireOne(
		ctx,
		redisJobStoreForgetFailedScript,
		"forget failed job",
		ErrJobStoreConflict,
		id,
	)
}

func (store *RedisJobStore) requireOne(
	ctx context.Context,
	script *redis.Script,
	operation string,
	zeroError error,
	arguments ...any,
) error {
	result, err := script.Run(ctx, store.client, store.keys, arguments...).Int64()
	if err != nil {
		return redisJobStoreError(operation, err)
	}
	if result == 0 {
		return zeroError
	}
	if result != 1 {
		return redisJobStoreError(operation, fmt.Errorf("result %d", result))
	}
	return nil
}

func (store *RedisJobStore) ready(ctx context.Context) error {
	if store == nil || redisScripterIsNil(store.client) {
		return ErrJobStoreUnavailable
	}
	if ctx == nil {
		return ErrJobContextUnavailable
	}
	return ctx.Err()
}

func redisScripterIsNil(client redis.Scripter) bool {
	if client == nil {
		return true
	}
	value := reflect.ValueOf(client)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func newRedisStoredJobMetadata(
	job StoredJob,
) (redisStoredJobMetadata, error) {
	availableAt, err := redisJobStoreTime(job.AvailableAt)
	if err != nil {
		return redisStoredJobMetadata{}, ErrJobStoreConflict
	}
	enqueuedAt, err := redisJobStoreTime(job.EnqueuedAt)
	if err != nil {
		return redisStoredJobMetadata{}, ErrJobStoreConflict
	}
	return redisStoredJobMetadata{
		ID:          job.ID,
		Handler:     job.Handler,
		AvailableAt: availableAt,
		EnqueuedAt:  enqueuedAt,
		Attempts:    strconv.Itoa(job.Attempts),
		ReadyMember: redisJobStoreReadyMember(job.EnqueuedAt, job.ID),
	}, nil
}

func decodeRedisStoredJob(
	encoded string,
	payload string,
	maxPayloadBytes int,
) (redisStoredJobMetadata, StoredJob, error) {
	var metadata redisStoredJobMetadata
	decoder := json.NewDecoder(bytes.NewBufferString(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return redisStoredJobMetadata{}, StoredJob{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return redisStoredJobMetadata{}, StoredJob{}, errors.New(
			"Redis job metadata must contain one object",
		)
	}
	availableAt, err := parseRedisJobStoreTime(metadata.AvailableAt)
	if err != nil {
		return redisStoredJobMetadata{}, StoredJob{}, err
	}
	enqueuedAt, err := parseRedisJobStoreTime(metadata.EnqueuedAt)
	if err != nil {
		return redisStoredJobMetadata{}, StoredJob{}, err
	}
	attempts, err := strconv.ParseInt(metadata.Attempts, 10, 0)
	if err != nil || attempts < 0 {
		return redisStoredJobMetadata{}, StoredJob{}, errors.New(
			"invalid Redis job attempt count",
		)
	}
	job := StoredJob{
		ID:          metadata.ID,
		Handler:     metadata.Handler,
		Payload:     append(json.RawMessage(nil), payload...),
		AvailableAt: availableAt,
		EnqueuedAt:  enqueuedAt,
		Attempts:    int(attempts),
	}
	if err := validateStoredJob(job, maxPayloadBytes); err != nil {
		return redisStoredJobMetadata{}, StoredJob{}, err
	}
	if metadata.ReadyMember != redisJobStoreReadyMember(job.EnqueuedAt, job.ID) {
		return redisStoredJobMetadata{}, StoredJob{}, errors.New(
			"invalid Redis job ready member",
		)
	}
	return metadata, job, nil
}

func redisJobStoreReadyMember(enqueuedAt time.Time, id string) string {
	value := uint64(enqueuedAt.UTC().UnixMicro()) ^ uint64(1<<63)
	return fmt.Sprintf("%016x:%s", value, id)
}

func redisJobStoreTime(value time.Time) (string, error) {
	if value.IsZero() {
		return "", errors.New("Redis job time is zero")
	}
	microseconds := value.UTC().UnixMicro()
	if microseconds < -redisJobStoreMaxExactTime ||
		microseconds > redisJobStoreMaxExactTime {
		return "", errors.New("Redis job time exceeds exact score range")
	}
	return strconv.FormatInt(microseconds, 10), nil
}

func parseRedisJobStoreTime(value string) (time.Time, error) {
	microseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil ||
		microseconds < -redisJobStoreMaxExactTime ||
		microseconds > redisJobStoreMaxExactTime {
		return time.Time{}, errors.New("invalid Redis job time")
	}
	return time.UnixMicro(microseconds).UTC(), nil
}

func validateRedisReservation(reservation JobReservation) error {
	if reservation.Job.ID == "" || reservation.Token == "" {
		return ErrJobReservationInvalid
	}
	return nil
}

func redisJobStoreError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrJobStoreOperationFailed, operation, err)
}
