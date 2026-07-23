package framework

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidConfigSetting       = errors.New("framework: config setting is invalid")
	ErrConfigSettingAlreadyExists = errors.New("framework: config setting is already defined")
	ErrInvalidConfigSource        = errors.New("framework: config source is invalid")
)

type ConfigViolation struct {
	Key     string `json:"key"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

type ConfigLoadErrors struct {
	Violations []ConfigViolation
}

func (e *ConfigLoadErrors) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return "configuration loading failed"
	}
	first := e.Violations[0]
	return fmt.Sprintf(
		"configuration loading failed for %s from %s: %s",
		first.Key,
		first.Source,
		first.Message,
	)
}

type ConfigSource interface {
	Name() string
	Lookup(string) (any, bool)
}

type MapConfigSource struct {
	name   string
	values map[string]any
}

func NewMapConfigSource(name string, values map[string]any) *MapConfigSource {
	name = strings.TrimSpace(name)
	if name == "" {
		panic(ErrInvalidConfigSource)
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return &MapConfigSource{name: name, values: cloned}
}

func (source *MapConfigSource) Name() string {
	if source == nil {
		return ""
	}
	return source.name
}

func (source *MapConfigSource) Lookup(key string) (any, bool) {
	if source == nil {
		return nil, false
	}
	value, exists := source.values[key]
	return value, exists
}

type EnvironmentLookup func(string) (string, bool)

type EnvironmentConfigSource struct {
	name   string
	prefix string
	lookup EnvironmentLookup
}

func NewEnvironmentConfigSource(prefix string) *EnvironmentConfigSource {
	return NewEnvironmentConfigSourceWithLookup("environment", prefix, os.LookupEnv)
}

func NewEnvironmentConfigSourceWithLookup(
	name string,
	prefix string,
	lookup EnvironmentLookup,
) *EnvironmentConfigSource {
	name = strings.TrimSpace(name)
	if name == "" || lookup == nil {
		panic(ErrInvalidConfigSource)
	}
	return &EnvironmentConfigSource{name: name, prefix: prefix, lookup: lookup}
}

func (source *EnvironmentConfigSource) Name() string {
	if source == nil {
		return ""
	}
	return source.name
}

func (source *EnvironmentConfigSource) Lookup(key string) (any, bool) {
	if source == nil || source.lookup == nil {
		return nil, false
	}
	name := strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))
	value, exists := source.lookup(source.prefix + name)
	return value, exists
}

type ConfigDecoder[T any] func(string) (T, error)

type ConfigValidator[T any] func(T) error

type ConfigSetting interface {
	configID() configID
	configName() string
	resolve([]ConfigSource) (resolvedConfigValue, []ConfigViolation)
}

type configSetting[T any] struct {
	key        ConfigKey[T]
	decoder    ConfigDecoder[T]
	validators []ConfigValidator[T]
}

type resolvedConfigValue struct {
	id     configID
	value  any
	source string
	secret bool
}

func ParsedConfig[T any](
	key ConfigKey[T],
	decoder ConfigDecoder[T],
	validators ...ConfigValidator[T],
) ConfigSetting {
	if _, ok := key.id(); !ok || decoder == nil {
		panic(ErrInvalidConfigSetting)
	}
	ensureConfigValidators(validators)
	return configSetting[T]{
		key:        key,
		decoder:    decoder,
		validators: append([]ConfigValidator[T](nil), validators...),
	}
}

func TypedConfig[T any](key ConfigKey[T], validators ...ConfigValidator[T]) ConfigSetting {
	if _, ok := key.id(); !ok {
		panic(ErrInvalidConfigSetting)
	}
	ensureConfigValidators(validators)
	return configSetting[T]{
		key:        key,
		validators: append([]ConfigValidator[T](nil), validators...),
	}
}

func StringConfig(key ConfigKey[string], validators ...ConfigValidator[string]) ConfigSetting {
	return ParsedConfig(key, func(value string) (string, error) { return value, nil }, validators...)
}

func IntConfig(key ConfigKey[int], validators ...ConfigValidator[int]) ConfigSetting {
	return ParsedConfig(key, func(value string) (int, error) {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return 0, errors.New("must be an integer")
		}
		return parsed, nil
	}, validators...)
}

func BoolConfig(key ConfigKey[bool], validators ...ConfigValidator[bool]) ConfigSetting {
	return ParsedConfig(key, func(value string) (bool, error) {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return false, errors.New("must be a boolean")
		}
		return parsed, nil
	}, validators...)
}

func DurationConfig(
	key ConfigKey[time.Duration],
	validators ...ConfigValidator[time.Duration],
) ConfigSetting {
	return ParsedConfig(key, func(value string) (time.Duration, error) {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return 0, errors.New("must be a duration such as 500ms or 2s")
		}
		return parsed, nil
	}, validators...)
}

func RequiredString(message string) ConfigValidator[string] {
	return func(value string) error {
		if strings.TrimSpace(value) != "" {
			return nil
		}
		return safeConfigError{message: message}
	}
}

type safeConfigError struct {
	message string
}

func (err safeConfigError) Error() string {
	return err.message
}

func (setting configSetting[T]) configID() configID {
	id, _ := setting.key.id()
	return id
}

func (setting configSetting[T]) configName() string {
	return setting.key.name
}

func (setting configSetting[T]) resolve(sources []ConfigSource) (
	resolvedConfigValue,
	[]ConfigViolation,
) {
	candidate := any(setting.key.defaultValue)
	sourceName := "default"
	for _, source := range sources {
		if value, exists := source.Lookup(setting.key.name); exists {
			candidate = value
			sourceName = source.Name()
		}
	}

	value, err := setting.decode(candidate)
	if err != nil {
		message := err.Error()
		if setting.key.secret {
			message = "value is invalid"
		}
		return resolvedConfigValue{}, []ConfigViolation{{
			Key: setting.key.name, Source: sourceName, Message: message,
		}}
	}

	violations := make([]ConfigViolation, 0)
	for _, validator := range setting.validators {
		if err := validator(value); err != nil {
			message := err.Error()
			var safeError safeConfigError
			if setting.key.secret && !errors.As(err, &safeError) {
				message = "value is invalid"
			}
			violations = append(violations, ConfigViolation{
				Key: setting.key.name, Source: sourceName, Message: message,
			})
		}
	}
	return resolvedConfigValue{
		id: setting.configID(), value: value, source: sourceName, secret: setting.key.secret,
	}, violations
}

func (setting configSetting[T]) decode(candidate any) (T, error) {
	if typed, ok := candidate.(T); ok {
		return typed, nil
	}
	if raw, ok := candidate.(string); ok && setting.decoder != nil {
		return setting.decoder(raw)
	}
	var zero T
	return zero, fmt.Errorf("must be %s", setting.key.valueType)
}

type ConfigLoader struct {
	mu       sync.RWMutex
	settings []ConfigSetting
	ids      map[configID]struct{}
	names    map[string]configID
}

func NewConfigLoader(settings ...ConfigSetting) *ConfigLoader {
	loader := &ConfigLoader{
		ids:   make(map[configID]struct{}),
		names: make(map[string]configID),
	}
	if err := loader.Register(settings...); err != nil {
		panic(err)
	}
	return loader
}

func (loader *ConfigLoader) Register(settings ...ConfigSetting) error {
	if loader == nil {
		return ErrInvalidConfigSetting
	}
	for _, setting := range settings {
		if configSettingIsNil(setting) {
			return ErrInvalidConfigSetting
		}
	}
	loader.mu.Lock()
	defer loader.mu.Unlock()
	if loader.ids == nil {
		loader.ids = make(map[configID]struct{})
	}
	if loader.names == nil {
		loader.names = make(map[string]configID)
	}
	pendingIDs := make(map[configID]struct{}, len(settings))
	pendingNames := make(map[string]configID, len(settings))
	for _, setting := range settings {
		id := setting.configID()
		if _, exists := loader.ids[id]; exists {
			return fmt.Errorf("%w: %s", ErrConfigSettingAlreadyExists, setting.configName())
		}
		if _, exists := pendingIDs[id]; exists {
			return fmt.Errorf("%w: %s", ErrConfigSettingAlreadyExists, setting.configName())
		}
		if previous, exists := loader.names[setting.configName()]; exists && previous != id {
			return fmt.Errorf("%w: %s", ErrConfigSettingAlreadyExists, setting.configName())
		}
		if previous, exists := pendingNames[setting.configName()]; exists && previous != id {
			return fmt.Errorf("%w: %s", ErrConfigSettingAlreadyExists, setting.configName())
		}
		pendingIDs[id] = struct{}{}
		pendingNames[setting.configName()] = id
	}
	for _, setting := range settings {
		id := setting.configID()
		loader.ids[id] = struct{}{}
		loader.names[setting.configName()] = id
		loader.settings = append(loader.settings, setting)
	}
	return nil
}

func (loader *ConfigLoader) Load(sources ...ConfigSource) (*Config, error) {
	if loader == nil {
		return nil, ErrInvalidConfigSetting
	}
	for _, source := range sources {
		if configSourceIsNil(source) || strings.TrimSpace(source.Name()) == "" {
			return nil, ErrInvalidConfigSource
		}
	}
	loader.mu.RLock()
	settings := append([]ConfigSetting(nil), loader.settings...)
	loader.mu.RUnlock()

	resolved := make([]resolvedConfigValue, 0, len(settings))
	violations := make([]ConfigViolation, 0)
	for _, setting := range settings {
		value, settingViolations := setting.resolve(sources)
		resolved = append(resolved, value)
		violations = append(violations, settingViolations...)
	}
	if len(violations) > 0 {
		return nil, &ConfigLoadErrors{Violations: violations}
	}

	config := NewConfig()
	for _, value := range resolved {
		if err := config.set(value.id, value.value, value.source, value.secret); err != nil {
			return nil, err
		}
	}
	return config, nil
}

func ensureConfigValidators[T any](validators []ConfigValidator[T]) {
	for _, validator := range validators {
		if validator == nil {
			panic(ErrInvalidConfigSetting)
		}
	}
}

func configSettingIsNil(setting ConfigSetting) bool {
	return configInterfaceIsNil(setting)
}

func configSourceIsNil(source ConfigSource) bool {
	return configInterfaceIsNil(source)
}

func configInterfaceIsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
