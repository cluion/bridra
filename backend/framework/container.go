package framework

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

var (
	ErrContainerUnavailable       = errors.New("framework: container is unavailable")
	ErrInvalidServiceKey          = errors.New("framework: service key is invalid")
	ErrInvalidServiceAlias        = errors.New("framework: service alias is invalid")
	ErrServiceAlreadyRegistered   = errors.New("framework: service is already registered")
	ErrServiceNotFound            = errors.New("framework: service is not registered")
	ErrScopeRequired              = errors.New("framework: scoped service requires a scope")
	ErrCircularDependency         = errors.New("framework: circular service dependency")
	ErrScopedServiceFromSingleton = errors.New("framework: singleton cannot depend on a scoped service")
)

type ServiceLifetime string

const (
	LifetimeSingleton ServiceLifetime = "singleton"
	LifetimeTransient ServiceLifetime = "transient"
	LifetimeScoped    ServiceLifetime = "scoped"
)

type ServiceKey[T any] struct {
	name      string
	valueType reflect.Type
}

func NewServiceKey[T any](name string) ServiceKey[T] {
	name = strings.TrimSpace(name)
	if name == "" {
		panic("framework: service key name cannot be empty")
	}
	return ServiceKey[T]{name: name, valueType: reflect.TypeFor[T]()}
}

func (key ServiceKey[T]) Name() string {
	return key.name
}

type serviceID struct {
	name      string
	valueType reflect.Type
}

func (key ServiceKey[T]) id() (serviceID, bool) {
	if key.name == "" || key.valueType == nil {
		return serviceID{}, false
	}
	return serviceID{name: key.name, valueType: key.valueType}, true
}

// Resolver is a resolution context supplied by a Container, Scope, or binding factory.
// Implementations are owned by the framework so dependency stacks and scopes cannot be lost.
type Resolver interface {
	resolve(serviceID) (any, error)
}

type Container struct {
	mu       sync.RWMutex
	services map[serviceID]any
	bindings map[serviceID]*serviceBinding
	aliases  map[serviceID]serviceID
}

type serviceBinding struct {
	lifetime    ServiceLifetime
	factory     func(Resolver) (any, error)
	mu          sync.Mutex
	initialized bool
	instance    any
}

type Scope struct {
	container *Container
	mu        sync.Mutex
	entries   map[serviceID]*scopeEntry
}

type scopeEntry struct {
	mu          sync.Mutex
	initialized bool
	instance    any
}

type resolution struct {
	container *Container
	scope     *Scope
	stack     []resolutionFrame
}

type resolutionFrame struct {
	id       serviceID
	lifetime ServiceLifetime
}

func NewContainer() *Container {
	return &Container{
		services: make(map[serviceID]any),
		bindings: make(map[serviceID]*serviceBinding),
		aliases:  make(map[serviceID]serviceID),
	}
}

func (container *Container) NewScope() *Scope {
	return &Scope{
		container: container,
		entries:   make(map[serviceID]*scopeEntry),
	}
}

func (scope *Scope) Container() *Container {
	if scope == nil {
		return nil
	}
	return scope.container
}

func Instance[T any](container *Container, key ServiceKey[T], service T) error {
	if container == nil {
		return ErrContainerUnavailable
	}
	id, ok := key.id()
	if !ok {
		return ErrInvalidServiceKey
	}

	container.mu.Lock()
	defer container.mu.Unlock()
	if container.registered(id) {
		return fmt.Errorf("%w: %s", ErrServiceAlreadyRegistered, key.name)
	}
	container.services[id] = service
	return nil
}

type Factory[T any] func(*Container) (T, error)

func Provide[T any](container *Container, key ServiceKey[T], factory Factory[T]) error {
	if container == nil {
		return ErrContainerUnavailable
	}
	if _, ok := key.id(); !ok {
		return ErrInvalidServiceKey
	}
	if factory == nil {
		return fmt.Errorf("framework: service factory for %q is nil", key.name)
	}

	service, err := factory(container)
	if err != nil {
		return fmt.Errorf("framework: provide service %q: %w", key.name, err)
	}
	return Instance(container, key, service)
}

type BindingFactory[T any] func(Resolver) (T, error)

func BindSingleton[T any](
	container *Container,
	key ServiceKey[T],
	factory BindingFactory[T],
) error {
	return bind(container, key, LifetimeSingleton, factory)
}

func BindTransient[T any](
	container *Container,
	key ServiceKey[T],
	factory BindingFactory[T],
) error {
	return bind(container, key, LifetimeTransient, factory)
}

func BindScoped[T any](
	container *Container,
	key ServiceKey[T],
	factory BindingFactory[T],
) error {
	return bind(container, key, LifetimeScoped, factory)
}

func bind[T any](
	container *Container,
	key ServiceKey[T],
	lifetime ServiceLifetime,
	factory BindingFactory[T],
) error {
	if container == nil {
		return ErrContainerUnavailable
	}
	id, ok := key.id()
	if !ok {
		return ErrInvalidServiceKey
	}
	if factory == nil {
		return fmt.Errorf("framework: service factory for %q is nil", key.name)
	}

	binding := &serviceBinding{
		lifetime: lifetime,
		factory: func(resolver Resolver) (any, error) {
			return factory(resolver)
		},
	}
	container.mu.Lock()
	defer container.mu.Unlock()
	if container.registered(id) {
		return fmt.Errorf("%w: %s", ErrServiceAlreadyRegistered, key.name)
	}
	container.bindings[id] = binding
	return nil
}

func Alias[T any, U any](
	container *Container,
	alias ServiceKey[T],
	target ServiceKey[U],
) error {
	if container == nil {
		return ErrContainerUnavailable
	}
	aliasID, aliasOK := alias.id()
	targetID, targetOK := target.id()
	if !aliasOK || !targetOK {
		return ErrInvalidServiceKey
	}
	if aliasID == targetID || !target.valueType.AssignableTo(alias.valueType) {
		return fmt.Errorf(
			"%w: %s cannot reference %s",
			ErrInvalidServiceAlias,
			alias.name,
			target.name,
		)
	}

	container.mu.Lock()
	defer container.mu.Unlock()
	if container.registered(aliasID) {
		return fmt.Errorf("%w: %s", ErrServiceAlreadyRegistered, alias.name)
	}
	if !container.registered(targetID) {
		return fmt.Errorf("%w: %s", ErrServiceNotFound, target.name)
	}
	container.aliases[aliasID] = targetID
	return nil
}

func Resolve[T any](resolver Resolver, key ServiceKey[T]) (T, error) {
	var zero T
	if resolver == nil {
		return zero, ErrContainerUnavailable
	}
	id, ok := key.id()
	if !ok {
		return zero, ErrInvalidServiceKey
	}

	service, err := resolver.resolve(id)
	if err != nil {
		return zero, err
	}
	typed, ok := service.(T)
	if !ok {
		return zero, fmt.Errorf("framework: service %q has an invalid registered type", key.name)
	}
	return typed, nil
}

func HasService[T any](container *Container, key ServiceKey[T]) bool {
	if container == nil {
		return false
	}
	id, ok := key.id()
	if !ok {
		return false
	}
	container.mu.RLock()
	exists := container.registered(id)
	container.mu.RUnlock()
	return exists
}

func (container *Container) registered(id serviceID) bool {
	if _, exists := container.services[id]; exists {
		return true
	}
	if _, exists := container.bindings[id]; exists {
		return true
	}
	_, exists := container.aliases[id]
	return exists
}

func (container *Container) resolve(id serviceID) (any, error) {
	if container == nil {
		return nil, ErrContainerUnavailable
	}
	return (&resolution{container: container}).resolve(id)
}

func (scope *Scope) resolve(id serviceID) (any, error) {
	if scope == nil || scope.container == nil {
		return nil, ErrContainerUnavailable
	}
	return (&resolution{container: scope.container, scope: scope}).resolve(id)
}

func (current *resolution) resolve(id serviceID) (any, error) {
	if index := current.stackIndex(id); index >= 0 {
		path := make([]string, 0, len(current.stack)-index+1)
		for _, frame := range current.stack[index:] {
			path = append(path, frame.id.name)
		}
		path = append(path, id.name)
		return nil, fmt.Errorf("%w: %s", ErrCircularDependency, strings.Join(path, " -> "))
	}

	current.container.mu.RLock()
	service, instanceExists := current.container.services[id]
	binding, bindingExists := current.container.bindings[id]
	alias, aliasExists := current.container.aliases[id]
	current.container.mu.RUnlock()

	if instanceExists {
		return service, nil
	}
	if aliasExists {
		return current.push(id, "").resolve(alias)
	}
	if !bindingExists {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, id.name)
	}
	if binding.lifetime == LifetimeScoped && current.hasLifetime(LifetimeSingleton) {
		return nil, fmt.Errorf(
			"%w: %s",
			ErrScopedServiceFromSingleton,
			id.name,
		)
	}

	next := current.push(id, binding.lifetime)
	service, err := binding.resolve(next)
	if err != nil {
		return nil, fmt.Errorf("framework: resolve service %q: %w", id.name, err)
	}
	return service, nil
}

func (binding *serviceBinding) resolve(current *resolution) (any, error) {
	switch binding.lifetime {
	case LifetimeTransient:
		return binding.factory(current)
	case LifetimeScoped:
		if current.scope == nil {
			return nil, ErrScopeRequired
		}
		entry := current.scope.entry(current.stack[len(current.stack)-1].id)
		entry.mu.Lock()
		defer entry.mu.Unlock()
		if entry.initialized {
			return entry.instance, nil
		}
		service, err := binding.factory(current)
		if err != nil {
			return nil, err
		}
		entry.instance = service
		entry.initialized = true
		return service, nil
	case LifetimeSingleton:
		binding.mu.Lock()
		defer binding.mu.Unlock()
		if binding.initialized {
			return binding.instance, nil
		}
		service, err := binding.factory(current)
		if err != nil {
			return nil, err
		}
		binding.instance = service
		binding.initialized = true
		return service, nil
	default:
		return nil, fmt.Errorf("framework: unsupported service lifetime %q", binding.lifetime)
	}
}

func (scope *Scope) entry(id serviceID) *scopeEntry {
	scope.mu.Lock()
	defer scope.mu.Unlock()
	entry, exists := scope.entries[id]
	if !exists {
		entry = &scopeEntry{}
		scope.entries[id] = entry
	}
	return entry
}

func (current *resolution) stackIndex(id serviceID) int {
	for index, frame := range current.stack {
		if frame.id == id {
			return index
		}
	}
	return -1
}

func (current *resolution) hasLifetime(lifetime ServiceLifetime) bool {
	for _, frame := range current.stack {
		if frame.lifetime == lifetime {
			return true
		}
	}
	return false
}

func (current *resolution) push(id serviceID, lifetime ServiceLifetime) *resolution {
	stack := make([]resolutionFrame, len(current.stack), len(current.stack)+1)
	copy(stack, current.stack)
	stack = append(stack, resolutionFrame{id: id, lifetime: lifetime})
	return &resolution{
		container: current.container,
		scope:     current.scope,
		stack:     stack,
	}
}
