package framework

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
)

var (
	ErrConfigFrozen      = errors.New("framework: config is frozen")
	ErrConfigUnavailable = errors.New("framework: config is unavailable")
	ErrInvalidConfigKey  = errors.New("framework: config key is invalid")
)

type ConfigKey[T any] struct {
	name         string
	valueType    reflect.Type
	defaultValue T
	secret       bool
}

func NewConfigKey[T any](name string, defaultValue T) ConfigKey[T] {
	return newConfigKey(name, defaultValue, false)
}

func NewSecretConfigKey[T any](name string, defaultValue T) ConfigKey[T] {
	return newConfigKey(name, defaultValue, true)
}

func newConfigKey[T any](name string, defaultValue T, secret bool) ConfigKey[T] {
	name = strings.TrimSpace(name)
	if name == "" {
		panic("framework: config key name cannot be empty")
	}
	return ConfigKey[T]{
		name:         name,
		valueType:    reflect.TypeFor[T](),
		defaultValue: defaultValue,
		secret:       secret,
	}
}

func (key ConfigKey[T]) Name() string {
	return key.name
}

func (key ConfigKey[T]) Secret() bool {
	return key.secret
}

type configID struct {
	name      string
	valueType reflect.Type
}

func (key ConfigKey[T]) id() (configID, bool) {
	if key.name == "" || key.valueType == nil {
		return configID{}, false
	}
	return configID{name: key.name, valueType: key.valueType}, true
}

type Config struct {
	mu     sync.RWMutex
	values map[configID]storedConfigValue
	frozen bool
}

type storedConfigValue struct {
	value  any
	source string
	secret bool
}

const RedactedConfigValue = "[redacted]"

type ConfigEntry struct {
	Name   string
	Type   string
	Value  any
	Source string
	Secret bool
}

func NewConfig() *Config {
	return &Config{values: make(map[configID]storedConfigValue)}
}

func SetConfig[T any](config *Config, key ConfigKey[T], value T) error {
	if config == nil {
		return ErrConfigUnavailable
	}
	id, ok := key.id()
	if !ok {
		return ErrInvalidConfigKey
	}

	return config.set(id, value, "runtime", key.secret)
}

func ConfigValue[T any](config *Config, key ConfigKey[T]) T {
	if config == nil {
		return key.defaultValue
	}
	id, ok := key.id()
	if !ok {
		return key.defaultValue
	}

	config.mu.RLock()
	entry, exists := config.values[id]
	config.mu.RUnlock()
	if !exists {
		return key.defaultValue
	}
	typed, ok := entry.value.(T)
	if !ok {
		return key.defaultValue
	}
	return typed
}

func (config *Config) Entries() []ConfigEntry {
	if config == nil {
		return nil
	}
	config.mu.RLock()
	entries := make([]ConfigEntry, 0, len(config.values))
	for id, stored := range config.values {
		value := stored.value
		if stored.secret {
			value = RedactedConfigValue
		}
		entries = append(entries, ConfigEntry{
			Name:   id.name,
			Type:   id.valueType.String(),
			Value:  value,
			Source: stored.source,
			Secret: stored.secret,
		})
	}
	config.mu.RUnlock()
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].Name == entries[right].Name {
			return entries[left].Type < entries[right].Type
		}
		return entries[left].Name < entries[right].Name
	})
	return entries
}

func (config *Config) set(id configID, value any, source string, secret bool) error {
	config.mu.Lock()
	defer config.mu.Unlock()
	if config.frozen {
		return ErrConfigFrozen
	}
	config.values[id] = storedConfigValue{
		value:  value,
		source: source,
		secret: secret,
	}
	return nil
}

func HasConfig[T any](config *Config, key ConfigKey[T]) bool {
	if config == nil {
		return false
	}
	id, ok := key.id()
	if !ok {
		return false
	}
	config.mu.RLock()
	_, exists := config.values[id]
	config.mu.RUnlock()
	return exists
}

func (config *Config) Freeze() {
	if config == nil {
		return
	}
	config.mu.Lock()
	config.frozen = true
	config.mu.Unlock()
}

func (config *Config) Frozen() bool {
	if config == nil {
		return false
	}
	config.mu.RLock()
	frozen := config.frozen
	config.mu.RUnlock()
	return frozen
}
