package framework

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type shutdownLifecycleProvider struct {
	name         string
	terminations *[]string
	registerErr  error
	bootErr      error
	shutdownErr  error
}

type contextShutdownProvider struct {
	name      string
	terminate func(context.Context) error
}

func (provider *contextShutdownProvider) Register(*Application) error { return nil }
func (provider *contextShutdownProvider) ProviderName() string        { return provider.name }
func (provider *contextShutdownProvider) Terminate(ctx context.Context, _ *Application) error {
	return provider.terminate(ctx)
}

func TestApplicationShutdownContinuesCleanupAfterCancellation(t *testing.T) {
	type contextKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "value"))
	defer cancel()
	order := []string{}
	application := NewApplication(nil)
	cleanup := func(name string) *contextShutdownProvider {
		return &contextShutdownProvider{name: name, terminate: func(ctx context.Context) error {
			order = append(order, name)
			if ctx.Err() != nil || ctx.Value(contextKey{}) != "value" {
				return errors.New("cleanup context is canceled or lost its values")
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > applicationShutdownCleanupTimeout {
				return errors.New("cleanup context has no bounded grace period")
			}
			return nil
		}}
	}
	slow := &contextShutdownProvider{name: "slow", terminate: func(ctx context.Context) error {
		order = append(order, "slow")
		cancel()
		return ctx.Err()
	}}
	if err := application.Register(cleanup("first"), cleanup("second"), slow); err != nil {
		t.Fatal(err)
	}
	_ = application.Shutdown(ctx)
	wait, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	err := application.Shutdown(wait)
	var failures *ApplicationShutdownErrors
	if !errors.As(err, &failures) || len(failures.Failures) != 1 || failures.Failures[0].Provider != "slow" ||
		!errors.Is(failures.Failures[0].Err, context.Canceled) {
		t.Fatalf("shutdown error = %v, want only slow provider cancellation", err)
	}
	if !reflect.DeepEqual(order, []string{"slow", "second", "first"}) {
		t.Fatalf("termination order = %v", order)
	}
}

func TestApplicationShutdownDeadlineBoundsWaitWithoutCompletingCleanup(t *testing.T) {
	provider := &blockingShutdownProvider{started: make(chan struct{}), release: make(chan struct{})}
	application := NewApplication(nil)
	if err := application.Register(provider); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- application.Shutdown(ctx) }()
	<-provider.started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not stop waiting after cancellation")
	}
	if application.ShutdownComplete() {
		t.Fatal("blocked cleanup must remain in progress")
	}
	close(provider.release)
	wait, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := application.Shutdown(wait); err != nil {
		t.Fatal(err)
	}
	if !application.ShutdownComplete() || provider.calls.Load() != 1 {
		t.Fatal("cleanup was not completed exactly once")
	}
}

func TestApplicationShutdownReservesDeadlineForLaterProviders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	application := NewApplication(nil)
	cleanupStarted := false
	cleanup := &contextShutdownProvider{name: "cleanup", terminate: func(cleanupCtx context.Context) error {
		cleanupStarted = true
		if ctx.Err() != nil || cleanupCtx.Err() != nil {
			return errors.New("cleanup started after the overall deadline")
		}
		return nil
	}}
	slow := &contextShutdownProvider{name: "slow", terminate: func(providerCtx context.Context) error {
		originalDeadline, _ := ctx.Deadline()
		providerDeadline, ok := providerCtx.Deadline()
		if !ok || !providerDeadline.Before(originalDeadline) {
			return errors.New("slow provider received the entire shutdown budget")
		}
		<-providerCtx.Done()
		return providerCtx.Err()
	}}
	if err := application.Register(cleanup, slow); err != nil {
		t.Fatal(err)
	}
	err := application.Shutdown(ctx)
	var failures *ApplicationShutdownErrors
	if !errors.As(err, &failures) || len(failures.Failures) != 1 || failures.Failures[0].Provider != "slow" ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v", err)
	}
	if !cleanupStarted || !application.ShutdownComplete() || ctx.Err() != nil {
		t.Fatal("later cleanup did not finish within the overall budget")
	}
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

type shutdownWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (ctx *shutdownWaitContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.entered) })
	return nil
}

func TestApplicationShutdownWaitsForInProgressShutdown(t *testing.T) {
	provider := &blockingShutdownProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	application := NewApplication(nil)
	if err := application.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- application.Shutdown(context.Background())
	}()
	<-provider.started

	waiterEntered := make(chan struct{})
	waiterContext := &shutdownWaitContext{
		Context: context.Background(),
		entered: waiterEntered,
	}
	go func() {
		<-waiterEntered
		close(provider.release)
	}()

	if err := application.Shutdown(waiterContext); err != nil {
		t.Fatalf("wait for shutdown: %v", err)
	}
	if err := <-firstResult; err != nil {
		t.Fatalf("initial shutdown: %v", err)
	}
	if calls := provider.calls.Load(); calls != 1 {
		t.Fatalf("terminate calls = %d, want 1", calls)
	}
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
