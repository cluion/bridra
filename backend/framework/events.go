package framework

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

var (
	ErrEventDispatcherUnavailable  = errors.New("framework: event dispatcher is unavailable")
	ErrEventContextUnavailable     = errors.New("framework: event context is unavailable")
	ErrInvalidEventListener        = errors.New("framework: event listener is invalid")
	ErrEventListenerAlreadyDefined = errors.New("framework: event listener is already defined")
	ErrEventDispatchFailed         = errors.New("framework: event dispatch failed")
	ErrStopEventPropagation        = errors.New("framework: stop event propagation")
)

var EventDispatcherKey = NewServiceKey[*EventDispatcher]("framework.events")

type EventListener[T any] func(context.Context, T) error

type EventDispatcher struct {
	mu        sync.RWMutex
	listeners map[reflect.Type][]eventListenerEntry
}

type eventListenerEntry struct {
	name   string
	handle func(context.Context, any) error
}

func NewEventDispatcher() *EventDispatcher {
	return &EventDispatcher{listeners: make(map[reflect.Type][]eventListenerEntry)}
}

func Listen[T any](
	dispatcher *EventDispatcher,
	name string,
	listener EventListener[T],
) error {
	if dispatcher == nil {
		return ErrEventDispatcherUnavailable
	}
	name = strings.TrimSpace(name)
	if name == "" || listener == nil {
		return ErrInvalidEventListener
	}
	eventType := reflect.TypeFor[T]()
	entry := eventListenerEntry{
		name: name,
		handle: func(ctx context.Context, event any) error {
			typed, ok := event.(T)
			if !ok {
				return fmt.Errorf("framework: event %s has an invalid runtime type", eventType)
			}
			return listener(ctx, typed)
		},
	}

	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if dispatcher.listeners == nil {
		dispatcher.listeners = make(map[reflect.Type][]eventListenerEntry)
	}
	for _, registered := range dispatcher.listeners[eventType] {
		if registered.name == name {
			return fmt.Errorf(
				"%w: %s listener %q",
				ErrEventListenerAlreadyDefined,
				eventType,
				name,
			)
		}
	}
	dispatcher.listeners[eventType] = append(dispatcher.listeners[eventType], entry)
	return nil
}

func Dispatch[T any](ctx context.Context, dispatcher *EventDispatcher, event T) error {
	if dispatcher == nil {
		return ErrEventDispatcherUnavailable
	}
	if ctx == nil {
		return ErrEventContextUnavailable
	}
	eventType := reflect.TypeFor[T]()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: event %s context: %w", ErrEventDispatchFailed, eventType, err)
	}
	dispatcher.mu.RLock()
	listeners := append([]eventListenerEntry(nil), dispatcher.listeners[eventType]...)
	dispatcher.mu.RUnlock()

	for _, listener := range listeners {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: event %s context: %w", ErrEventDispatchFailed, eventType, err)
		}
		if err := listener.handle(ctx, event); err != nil {
			if errors.Is(err, ErrStopEventPropagation) {
				return nil
			}
			return fmt.Errorf(
				"%w: event %s listener %q: %w",
				ErrEventDispatchFailed,
				eventType,
				listener.name,
				err,
			)
		}
	}
	return nil
}

func EventListeners[T any](dispatcher *EventDispatcher) []string {
	if dispatcher == nil {
		return nil
	}
	eventType := reflect.TypeFor[T]()
	dispatcher.mu.RLock()
	listeners := dispatcher.listeners[eventType]
	names := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		names = append(names, listener.name)
	}
	dispatcher.mu.RUnlock()
	return names
}
