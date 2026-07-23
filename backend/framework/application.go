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
	ErrApplicationBooted          = errors.New("framework: application has already booted")
	ErrApplicationBusy            = errors.New("framework: application lifecycle is busy")
	ErrApplicationFailed          = errors.New("framework: application lifecycle failed")
	ErrApplicationShutdown        = errors.New("framework: application has shut down")
	ErrApplicationShutdownFailed  = errors.New("framework: application shutdown failed")
	ErrShutdownContextUnavailable = errors.New("framework: shutdown context is unavailable")
)

type ServiceProvider interface {
	Register(*Application) error
}

type BootableServiceProvider interface {
	Boot(*Application) error
}

type TerminableServiceProvider interface {
	Terminate(context.Context, *Application) error
}

type NamedServiceProvider interface {
	ProviderName() string
}

type ShutdownFailure struct {
	Provider string
	Err      error
}

type ApplicationShutdownErrors struct {
	Failures []ShutdownFailure
}

func (e *ApplicationShutdownErrors) Error() string {
	if e == nil || len(e.Failures) == 0 {
		return "application shutdown failed"
	}
	first := e.Failures[0]
	return fmt.Sprintf("application shutdown failed for provider %q: %v", first.Provider, first.Err)
}

func (e *ApplicationShutdownErrors) Is(target error) bool {
	return target == ErrApplicationShutdownFailed
}

func (e *ApplicationShutdownErrors) Unwrap() []error {
	if e == nil {
		return nil
	}
	causes := make([]error, 0, len(e.Failures))
	for _, failure := range e.Failures {
		causes = append(causes, failure.Err)
	}
	return causes
}

type applicationState uint8

const (
	applicationCollecting applicationState = iota
	applicationRegistering
	applicationBooting
	applicationBooted
	applicationFailed
)

type applicationShutdownState uint8

const (
	applicationShutdownIdle applicationShutdownState = iota
	applicationShuttingDown
	applicationShutdownComplete
)

type Application struct {
	config            *Config
	container         *Container
	router            *Router
	events            *EventDispatcher
	providers         []ServiceProvider
	shutdownProviders []ServiceProvider
	state             applicationState
	failure           error
	shutdownState     applicationShutdownState
	shutdownDone      chan struct{}
	shutdownErr       error
	mu                sync.Mutex
}

func NewApplication(config *Config) *Application {
	if config == nil {
		config = NewConfig()
	}
	container := NewContainer()
	events := NewEventDispatcher()
	if err := Instance(container, EventDispatcherKey, events); err != nil {
		panic(fmt.Errorf("framework: register event dispatcher: %w", err))
	}
	return &Application{
		config:    config,
		container: container,
		router:    NewRouterWithContainer(container),
		events:    events,
	}
}

func (application *Application) Config() *Config {
	return application.config
}

func (application *Application) Container() *Container {
	return application.container
}

func (application *Application) Router() *Router {
	return application.router
}

func (application *Application) Events() *EventDispatcher {
	return application.events
}

func (application *Application) Register(providers ...ServiceProvider) error {
	application.mu.Lock()
	if application.shutdownState != applicationShutdownIdle {
		application.mu.Unlock()
		return ErrApplicationShutdown
	}
	switch application.state {
	case applicationBooted:
		application.mu.Unlock()
		return ErrApplicationBooted
	case applicationFailed:
		err := application.failure
		application.mu.Unlock()
		return err
	case applicationCollecting:
		application.state = applicationRegistering
	default:
		application.mu.Unlock()
		return ErrApplicationBusy
	}
	application.mu.Unlock()

	registered := make([]ServiceProvider, 0, len(providers))
	for _, provider := range providers {
		if serviceProviderIsNil(provider) {
			return application.fail(fmt.Errorf(
				"%w: register provider: service provider cannot be nil",
				ErrApplicationFailed,
			))
		}
		application.trackShutdownProvider(provider)
		if err := provider.Register(application); err != nil {
			return application.fail(fmt.Errorf(
				"%w: register provider %T: %w",
				ErrApplicationFailed,
				provider,
				err,
			))
		}
		registered = append(registered, provider)
	}
	application.finishRegistration(registered)
	return nil
}

func (application *Application) trackShutdownProvider(provider ServiceProvider) {
	application.mu.Lock()
	application.shutdownProviders = append(application.shutdownProviders, provider)
	application.mu.Unlock()
}

func serviceProviderIsNil(provider ServiceProvider) bool {
	if provider == nil {
		return true
	}
	value := reflect.ValueOf(provider)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (application *Application) finishRegistration(providers []ServiceProvider) {
	application.mu.Lock()
	application.providers = append(application.providers, providers...)
	application.state = applicationCollecting
	application.mu.Unlock()
}

func (application *Application) fail(err error) error {
	application.mu.Lock()
	application.failure = err
	application.state = applicationFailed
	application.mu.Unlock()
	return err
}

func (application *Application) Boot() error {
	application.mu.Lock()
	if application.shutdownState != applicationShutdownIdle {
		application.mu.Unlock()
		return ErrApplicationShutdown
	}
	switch application.state {
	case applicationBooted:
		application.mu.Unlock()
		return nil
	case applicationFailed:
		err := application.failure
		application.mu.Unlock()
		return err
	case applicationCollecting:
		application.state = applicationBooting
	default:
		application.mu.Unlock()
		return ErrApplicationBusy
	}
	providers := append([]ServiceProvider(nil), application.providers...)
	application.mu.Unlock()

	for _, provider := range providers {
		bootable, ok := provider.(BootableServiceProvider)
		if !ok {
			continue
		}
		if err := bootable.Boot(application); err != nil {
			return application.fail(fmt.Errorf(
				"%w: boot provider %T: %w",
				ErrApplicationFailed,
				provider,
				err,
			))
		}
	}
	application.config.Freeze()
	application.mu.Lock()
	application.state = applicationBooted
	application.mu.Unlock()
	return nil
}

func (application *Application) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return ErrShutdownContextUnavailable
	}

	application.mu.Lock()
	switch application.shutdownState {
	case applicationShutdownComplete:
		err := application.shutdownErr
		application.mu.Unlock()
		return err
	case applicationShuttingDown:
		done := application.shutdownDone
		application.mu.Unlock()
		select {
		case <-done:
			application.mu.Lock()
			err := application.shutdownErr
			application.mu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		application.mu.Unlock()
		return err
	}
	if application.state == applicationRegistering || application.state == applicationBooting {
		application.mu.Unlock()
		return ErrApplicationBusy
	}
	application.shutdownState = applicationShuttingDown
	application.shutdownDone = make(chan struct{})
	providers := append([]ServiceProvider(nil), application.shutdownProviders...)
	application.mu.Unlock()

	failures := make([]ShutdownFailure, 0)
	for index := len(providers) - 1; index >= 0; index-- {
		provider := providers[index]
		terminable, ok := provider.(TerminableServiceProvider)
		if !ok {
			continue
		}
		name := serviceProviderName(provider)
		if err := ctx.Err(); err != nil {
			failures = append(failures, ShutdownFailure{Provider: name, Err: err})
			break
		}
		if err := terminable.Terminate(ctx, application); err != nil {
			failures = append(failures, ShutdownFailure{Provider: name, Err: err})
		}
	}

	var shutdownErr error
	if len(failures) > 0 {
		shutdownErr = &ApplicationShutdownErrors{Failures: failures}
	}
	application.mu.Lock()
	application.shutdownErr = shutdownErr
	application.shutdownState = applicationShutdownComplete
	close(application.shutdownDone)
	application.mu.Unlock()
	return shutdownErr
}

func serviceProviderName(provider ServiceProvider) string {
	if named, ok := provider.(NamedServiceProvider); ok {
		if name := strings.TrimSpace(named.ProviderName()); name != "" {
			return name
		}
	}
	return fmt.Sprintf("%T", provider)
}

func (application *Application) ShutdownComplete() bool {
	application.mu.Lock()
	complete := application.shutdownState == applicationShutdownComplete
	application.mu.Unlock()
	return complete
}

func (application *Application) Booted() bool {
	application.mu.Lock()
	booted := application.state == applicationBooted
	application.mu.Unlock()
	return booted
}

func (application *Application) Failed() bool {
	application.mu.Lock()
	failed := application.state == applicationFailed
	application.mu.Unlock()
	return failed
}
