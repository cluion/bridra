package framework

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type accountCreated struct {
	ID string
}

type accountDeleted struct {
	ID string
}

func TestEventDispatcherRunsTypedListenersInRegistrationOrder(t *testing.T) {
	dispatcher := NewEventDispatcher()
	events := []string{}
	if err := Listen(dispatcher, "audit", func(ctx context.Context, event accountCreated) error {
		events = append(events, ctx.Value(eventContextKey{}).(string)+":"+event.ID+":audit")
		return nil
	}); err != nil {
		t.Fatalf("listen audit: %v", err)
	}
	if err := Listen(dispatcher, "notify", func(_ context.Context, event accountCreated) error {
		events = append(events, event.ID+":notify")
		return nil
	}); err != nil {
		t.Fatalf("listen notify: %v", err)
	}
	if err := Listen(dispatcher, "deleted", func(context.Context, accountDeleted) error {
		events = append(events, "unexpected")
		return nil
	}); err != nil {
		t.Fatalf("listen deleted: %v", err)
	}
	ctx := context.WithValue(context.Background(), eventContextKey{}, "request-1")

	if err := Dispatch(ctx, dispatcher, accountCreated{ID: "account-1"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	want := []string{"request-1:account-1:audit", "account-1:notify"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	if names := EventListeners[accountCreated](dispatcher); !reflect.DeepEqual(names, []string{"audit", "notify"}) {
		t.Fatalf("listeners = %#v", names)
	}
}

type eventContextKey struct{}

func TestEventDispatcherStopsAtFirstListenerError(t *testing.T) {
	dispatcher := NewEventDispatcher()
	want := errors.New("mail unavailable")
	called := false
	if err := Listen(dispatcher, "mail", func(context.Context, accountCreated) error {
		return want
	}); err != nil {
		t.Fatalf("listen mail: %v", err)
	}
	if err := Listen(dispatcher, "after", func(context.Context, accountCreated) error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("listen after: %v", err)
	}

	err := Dispatch(context.Background(), dispatcher, accountCreated{})
	if !errors.Is(err, ErrEventDispatchFailed) || !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), `listener "mail"`) {
		t.Fatalf("error does not name listener: %v", err)
	}
	if called {
		t.Fatal("listener ran after a failed listener")
	}
}

func TestEventDispatcherCanStopPropagationWithoutFailure(t *testing.T) {
	dispatcher := NewEventDispatcher()
	called := false
	if err := Listen(dispatcher, "stop", func(context.Context, accountCreated) error {
		return ErrStopEventPropagation
	}); err != nil {
		t.Fatalf("listen stop: %v", err)
	}
	if err := Listen(dispatcher, "after", func(context.Context, accountCreated) error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("listen after: %v", err)
	}

	if err := Dispatch(context.Background(), dispatcher, accountCreated{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if called {
		t.Fatal("listener ran after propagation stopped")
	}
}

func TestEventDispatcherUsesSnapshotDuringConcurrentRegistration(t *testing.T) {
	dispatcher := NewEventDispatcher()
	started := make(chan struct{})
	release := make(chan struct{})
	events := make([]string, 0)
	var mu sync.Mutex
	var startOnce sync.Once
	if err := Listen(dispatcher, "first", func(context.Context, accountCreated) error {
		startOnce.Do(func() { close(started) })
		<-release
		mu.Lock()
		events = append(events, "first")
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("listen first: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- Dispatch(context.Background(), dispatcher, accountCreated{})
	}()
	<-started
	if err := Listen(dispatcher, "second", func(context.Context, accountCreated) error {
		mu.Lock()
		events = append(events, "second")
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("listen second: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	mu.Lock()
	firstDispatch := append([]string(nil), events...)
	mu.Unlock()
	if !reflect.DeepEqual(firstDispatch, []string{"first"}) {
		t.Fatalf("first dispatch events = %#v", firstDispatch)
	}

	if err := Dispatch(context.Background(), dispatcher, accountCreated{}); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	mu.Lock()
	allEvents := append([]string(nil), events...)
	mu.Unlock()
	if !reflect.DeepEqual(allEvents, []string{"first", "first", "second"}) {
		t.Fatalf("all events = %#v", allEvents)
	}
}

func TestEventDispatcherRejectsInvalidAndDuplicateListeners(t *testing.T) {
	dispatcher := NewEventDispatcher()
	listener := EventListener[accountCreated](func(context.Context, accountCreated) error { return nil })
	if err := Listen(dispatcher, "listener", listener); err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := Listen(dispatcher, "listener", listener); !errors.Is(err, ErrEventListenerAlreadyDefined) {
		t.Fatalf("duplicate error = %v", err)
	}
	var nilListener EventListener[accountCreated]
	if err := Listen(dispatcher, "nil", nilListener); !errors.Is(err, ErrInvalidEventListener) {
		t.Fatalf("nil listener error = %v", err)
	}
	if err := Dispatch[accountCreated](nil, dispatcher, accountCreated{}); !errors.Is(err, ErrEventContextUnavailable) {
		t.Fatalf("nil context error = %v", err)
	}
}

func TestEventDispatcherHonorsCanceledContext(t *testing.T) {
	dispatcher := NewEventDispatcher()
	called := false
	if err := Listen(dispatcher, "listener", func(context.Context, accountCreated) error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Dispatch(ctx, dispatcher, accountCreated{})
	if !errors.Is(err, ErrEventDispatchFailed) || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if called {
		t.Fatal("listener ran with a canceled context")
	}
}

func TestApplicationRegistersItsEventDispatcherInContainer(t *testing.T) {
	application := NewApplication(nil)

	dispatcher, err := Resolve(application.Container(), EventDispatcherKey)
	if err != nil {
		t.Fatalf("resolve dispatcher: %v", err)
	}
	if dispatcher != application.Events() {
		t.Fatal("container and application event dispatchers differ")
	}
}
