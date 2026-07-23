package framework

import (
	"errors"
	"reflect"
	"sync"
)

var ErrInvalidExceptionRenderer = errors.New("framework: exception renderer cannot be nil")
var ErrInvalidExceptionMapper = errors.New("framework: exception mapper cannot be nil")

type ExceptionRenderer interface {
	Render(error) *RPCError
}

type ExceptionRendererFunc func(error) *RPCError

func (renderer ExceptionRendererFunc) Render(err error) *RPCError {
	return renderer(err)
}

type ExceptionMapper func(error) (*RPCError, bool)

type ExceptionRegistry struct {
	mu      sync.RWMutex
	mappers []ExceptionMapper
}

func NewExceptionRegistry(mappers ...ExceptionMapper) *ExceptionRegistry {
	registry := &ExceptionRegistry{}
	if err := registry.Register(mappers...); err != nil {
		panic(err)
	}
	return registry
}

func (registry *ExceptionRegistry) Register(mappers ...ExceptionMapper) error {
	for _, mapper := range mappers {
		if mapper == nil {
			return ErrInvalidExceptionMapper
		}
	}
	registry.mu.Lock()
	registry.mappers = append(registry.mappers, mappers...)
	registry.mu.Unlock()
	return nil
}

func (registry *ExceptionRegistry) Render(err error) *RPCError {
	if registry != nil {
		registry.mu.RLock()
		mappers := append([]ExceptionMapper(nil), registry.mappers...)
		registry.mu.RUnlock()
		for _, mapper := range mappers {
			if rendered, ok := mapper(err); ok && rendered != nil {
				return rendered
			}
		}
	}
	return renderFrameworkException(err)
}

func MapException[T error](render func(T) *RPCError) ExceptionMapper {
	if render == nil {
		panic(ErrInvalidExceptionMapper)
	}
	return func(err error) (*RPCError, bool) {
		var exception T
		if !errors.As(err, &exception) {
			return nil, false
		}
		return render(exception), true
	}
}

func DefaultExceptionRenderer() ExceptionRenderer {
	return NewExceptionRegistry()
}

func renderFrameworkException(err error) *RPCError {
	var rpcError *RPCError
	if errors.As(err, &rpcError) {
		return rpcError
	}
	var validationErrors *ValidationErrors
	if errors.As(err, &validationErrors) {
		return NewErrorWithData(
			"validation_error",
			"The request failed validation.",
			map[string]any{"violations": validationErrors.Violations},
		)
	}
	return NewError("internal_error", "The Go backend could not complete the request.")
}

func exceptionRendererIsNil(renderer ExceptionRenderer) bool {
	if renderer == nil {
		return true
	}
	value := reflect.ValueOf(renderer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
