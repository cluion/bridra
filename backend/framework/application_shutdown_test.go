package framework

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

type shutdownLifecycleProvider struct {
	name         string
	terminations *[]string
	registerErr  error
	bootErr      error
	shutdownErr  error
}

func (provider *shutdownLifecycleProvider) ProviderName() string {
	return provider.name
}

func (provider *shutdownLifecycleProvider) Register(*Application) error {
	return provider.registerErr
}

func (provider *shutdownLifecycleProvider) Boot(*Application) error {
	return provider.bootErr
}

func (provider *shutdownLifecycleProvider) Terminate(context.Context, *Application) error {
	*provider.terminations = append(*provider.terminations, provider.name)
	return provider.shutdownErr
}

func TestApplicationShutdownTerminatesProvidersInReverseOrderAndIsIdempotent(t *testing.T) {
	terminations := []string{}
	firstError := errors.New("first shutdown failed")
	secondError := errors.New("second shutdown failed")
	first := &shutdownLifecycleProvider{
		name:         "first",
		terminations: &terminations,
		shutdownErr:  firstError,
	}
	second := &shutdownLifecycleProvider{
		name:         "second",
		terminations: &terminations,
		shutdownErr:  secondError,
	}
	application := NewApplication(nil)
	if err := application.Register(first, second); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}

	shutdownError := application.Shutdown(context.Background())
	if !errors.Is(shutdownError, ErrApplicationShutdownFailed) {
		t.Fatalf("shutdown error = %v, want ErrApplicationShutdownFailed", shutdownError)
	}
	if !errors.Is(shutdownError, firstError) || !errors.Is(shutdownError, secondError) {
		t.Fatalf("shutdown error = %v, want both provider errors", shutdownError)
	}
	var failures *ApplicationShutdownErrors
	if !errors.As(shutdownError, &failures) {
		t.Fatalf("shutdown error type = %T, want *ApplicationShutdownErrors", shutdownError)
	}
	wantFailures := []ShutdownFailure{
		{Provider: "second", Err: secondError},
		{Provider: "first", Err: firstError},
	}
	if !reflect.DeepEqual(failures.Failures, wantFailures) {
		t.Fatalf("failures = %#v, want %#v", failures.Failures, wantFailures)
	}
	if !reflect.DeepEqual(terminations, []string{"second", "first"}) {
		t.Fatalf("terminations = %#v", terminations)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if repeated := application.Shutdown(cancelled); repeated != shutdownError {
		t.Fatalf("repeated shutdown error = %v, want original %v", repeated, shutdownError)
	}
	if !reflect.DeepEqual(terminations, []string{"second", "first"}) {
		t.Fatalf("providers terminated more than once: %#v", terminations)
	}
	if !application.ShutdownComplete() {
		t.Fatal("application should report completed shutdown")
	}
	if err := application.Boot(); !errors.Is(err, ErrApplicationShutdown) {
		t.Fatalf("boot after shutdown = %v, want ErrApplicationShutdown", err)
	}
	if err := application.Register(first); !errors.Is(err, ErrApplicationShutdown) {
		t.Fatalf("register after shutdown = %v, want ErrApplicationShutdown", err)
	}
}

func TestApplicationShutdownCleansUpPartialRegistrationFailure(t *testing.T) {
	terminations := []string{}
	registerError := errors.New("registration failed")
	first := &shutdownLifecycleProvider{name: "first", terminations: &terminations}
	failing := &shutdownLifecycleProvider{
		name:         "failing",
		terminations: &terminations,
		registerErr:  registerError,
	}
	notAttempted := &shutdownLifecycleProvider{name: "not-attempted", terminations: &terminations}
	application := NewApplication(nil)

	if err := application.Register(first, failing, notAttempted); !errors.Is(err, registerError) {
		t.Fatalf("register error = %v, want %v", err, registerError)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !reflect.DeepEqual(terminations, []string{"failing", "first"}) {
		t.Fatalf("terminations = %#v", terminations)
	}
}

func TestApplicationShutdownCleansUpAllRegisteredProvidersAfterBootFailure(t *testing.T) {
	terminations := []string{}
	bootError := errors.New("boot failed")
	first := &shutdownLifecycleProvider{name: "first", terminations: &terminations}
	failing := &shutdownLifecycleProvider{
		name:         "failing",
		terminations: &terminations,
		bootErr:      bootError,
	}
	notBooted := &shutdownLifecycleProvider{name: "not-booted", terminations: &terminations}
	application := NewApplication(nil)
	if err := application.Register(first, failing, notBooted); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := application.Boot(); !errors.Is(err, bootError) {
		t.Fatalf("boot error = %v, want %v", err, bootError)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !reflect.DeepEqual(terminations, []string{"not-booted", "failing", "first"}) {
		t.Fatalf("terminations = %#v", terminations)
	}
}

type blockingShutdownProvider struct {
	started chan struct{}
	release chan struct{}
	err     error
	calls   atomic.Int32
	once    sync.Once
}

func (provider *blockingShutdownProvider) Register(*Application) error {
	return nil
}

func (provider *blockingShutdownProvider) Terminate(context.Context, *Application) error {
	provider.calls.Add(1)
	provider.once.Do(func() { close(provider.started) })
	<-provider.release
	return provider.err
}

func TestApplicationConcurrentShutdownCallsShareOneResult(t *testing.T) {
	providerError := errors.New("shutdown failed")
	provider := &blockingShutdownProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
		err:     providerError,
	}
	application := NewApplication(nil)
	if err := application.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}

	const callers = 16
	start := make(chan struct{})
	results := make(chan error, callers)
	for range callers {
		go func() {
			<-start
			results <- application.Shutdown(context.Background())
		}()
	}
	close(start)
	<-provider.started
	close(provider.release)

	var shared error
	for range callers {
		err := <-results
		if !errors.Is(err, providerError) {
			t.Fatalf("shutdown error = %v, want %v", err, providerError)
		}
		if shared == nil {
			shared = err
			continue
		}
		if err != shared {
			t.Fatalf("shutdown result = %p, want shared result %p", err, shared)
		}
	}
	if calls := provider.calls.Load(); calls != 1 {
		t.Fatalf("terminate calls = %d, want 1", calls)
	}
}

func TestApplicationShutdownRejectsUnavailableContextWithoutTransition(t *testing.T) {
	application := NewApplication(nil)
	if err := application.Shutdown(nil); !errors.Is(err, ErrShutdownContextUnavailable) {
		t.Fatalf("nil context error = %v, want ErrShutdownContextUnavailable", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := application.Shutdown(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v, want context.Canceled", err)
	}
	if application.ShutdownComplete() {
		t.Fatal("rejected shutdown should not complete the lifecycle")
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown retry: %v", err)
	}
}
