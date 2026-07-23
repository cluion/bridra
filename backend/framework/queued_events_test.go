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

type queuedOrderPlaced struct {
	OrderID string
}

type sendOrderEmail struct {
	OrderID string
}

type queuedEventContextKey struct{}

func TestQueuedEventListenerMapsAndEnqueuesWithoutWaitingForHandler(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 1, Workers: 1})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	started := make(chan sendOrderEmail, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		releaseHandler()
		_ = queue.Shutdown(context.Background())
	})
	if err := HandleJob(queue, "mail.send", func(_ context.Context, job sendOrderEmail) error {
		started <- job
		<-release
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	dispatcher := NewEventDispatcher()
	mapperContext := ""
	if err := ListenQueued(
		dispatcher,
		queue,
		"orders.queue-mail",
		func(ctx context.Context, event queuedOrderPlaced) (sendOrderEmail, error) {
			mapperContext, _ = ctx.Value(queuedEventContextKey{}).(string)
			return sendOrderEmail{OrderID: event.OrderID}, nil
		},
	); err != nil {
		t.Fatalf("listen queued: %v", err)
	}

	dispatchDone := make(chan error, 1)
	ctx := context.WithValue(context.Background(), queuedEventContextKey{}, "request-1")
	go func() {
		dispatchDone <- Dispatch(ctx, dispatcher, queuedOrderPlaced{OrderID: "ORDER-1"})
	}()
	select {
	case err := <-dispatchDone:
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("event dispatch waited for queued handler completion")
	}
	var job sendOrderEmail
	select {
	case job = <-started:
	case <-time.After(time.Second):
		t.Fatal("queued handler did not start")
	}
	if mapperContext != "request-1" || job.OrderID != "ORDER-1" {
		t.Fatalf("mapper context = %q, job = %#v", mapperContext, job)
	}
	if names := EventListeners[queuedOrderPlaced](dispatcher); !reflect.DeepEqual(
		names,
		[]string{"orders.queue-mail"},
	) {
		t.Fatalf("listeners = %#v", names)
	}
	releaseHandler()
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestQueuedEventListenerPreservesMappingFailure(t *testing.T) {
	mapperError := errors.New("order is incomplete")
	queue, err := NewJobQueue(JobQueueOptions{})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	dispatcher := NewEventDispatcher()
	if err := ListenQueued(
		dispatcher,
		queue,
		"orders.map-mail",
		func(context.Context, queuedOrderPlaced) (sendOrderEmail, error) {
			return sendOrderEmail{}, mapperError
		},
	); err != nil {
		t.Fatalf("listen queued: %v", err)
	}

	err = Dispatch(context.Background(), dispatcher, queuedOrderPlaced{})
	if !errors.Is(err, ErrEventDispatchFailed) ||
		!errors.Is(err, ErrQueuedEventMappingFailed) ||
		!errors.Is(err, mapperError) {
		t.Fatalf("dispatch error = %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestQueuedEventListenerPreservesQueueFailure(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := HandleJob(queue, "mail.send", func(context.Context, sendOrderEmail) error {
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	dispatcher := NewEventDispatcher()
	if err := ListenQueued(
		dispatcher,
		queue,
		"orders.queue-mail",
		func(_ context.Context, event queuedOrderPlaced) (sendOrderEmail, error) {
			return sendOrderEmail{OrderID: event.OrderID}, nil
		},
	); err != nil {
		t.Fatalf("listen queued: %v", err)
	}

	err = Dispatch(context.Background(), dispatcher, queuedOrderPlaced{})
	if !errors.Is(err, ErrEventDispatchFailed) ||
		!errors.Is(err, ErrQueuedEventEnqueueFailed) ||
		!errors.Is(err, ErrJobDispatchFailed) ||
		!errors.Is(err, ErrJobQueueNotRunning) {
		t.Fatalf("dispatch error = %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

type queuedBlockingJob struct {
	ID int
}

type queuedBlockingEvent struct {
	ID int
}

func TestQueuedEventListenerAppliesQueueBackpressureAndContext(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 1, Workers: 1})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		releaseHandler()
		_ = queue.Shutdown(context.Background())
	})
	var handled atomic.Int32
	if err := HandleJob(queue, "blocking", func(context.Context, queuedBlockingJob) error {
		if handled.Add(1) == 1 {
			close(started)
		}
		<-release
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	dispatcher := NewEventDispatcher()
	if err := ListenQueued(
		dispatcher,
		queue,
		"blocking.queue",
		func(_ context.Context, event queuedBlockingEvent) (queuedBlockingJob, error) {
			return queuedBlockingJob{ID: event.ID}, nil
		},
	); err != nil {
		t.Fatalf("listen queued: %v", err)
	}
	if err := Dispatch(context.Background(), dispatcher, queuedBlockingEvent{ID: 1}); err != nil {
		t.Fatalf("dispatch first: %v", err)
	}
	<-started
	if err := Dispatch(context.Background(), dispatcher, queuedBlockingEvent{ID: 2}); err != nil {
		t.Fatalf("dispatch second: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err = Dispatch(ctx, dispatcher, queuedBlockingEvent{ID: 3})
	if !errors.Is(err, ErrEventDispatchFailed) ||
		!errors.Is(err, ErrQueuedEventEnqueueFailed) ||
		!errors.Is(err, ErrJobDispatchFailed) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dispatch third error = %v", err)
	}
	releaseHandler()
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if handled.Load() != 2 {
		t.Fatalf("handled = %d, want 2", handled.Load())
	}
}

func TestQueuedEventListenerRejectsUnavailableDependenciesAndInvalidMapper(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	dispatcher := NewEventDispatcher()
	mapper := EventJobMapper[queuedOrderPlaced, sendOrderEmail](
		func(_ context.Context, event queuedOrderPlaced) (sendOrderEmail, error) {
			return sendOrderEmail{OrderID: event.OrderID}, nil
		},
	)
	if err := ListenQueued(nil, queue, "queued", mapper); !errors.Is(
		err,
		ErrEventDispatcherUnavailable,
	) {
		t.Fatalf("nil dispatcher error = %v", err)
	}
	if err := ListenQueued(dispatcher, nil, "queued", mapper); !errors.Is(
		err,
		ErrJobQueueUnavailable,
	) {
		t.Fatalf("nil queue error = %v", err)
	}
	var nilMapper EventJobMapper[queuedOrderPlaced, sendOrderEmail]
	if err := ListenQueued(dispatcher, queue, "queued", nilMapper); !errors.Is(
		err,
		ErrInvalidQueuedEventMapper,
	) {
		t.Fatalf("nil mapper error = %v", err)
	}
	if err := ListenQueued(dispatcher, queue, "", mapper); !errors.Is(
		err,
		ErrInvalidEventListener,
	) {
		t.Fatalf("invalid name error = %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

type queuedLifecycleProvider struct {
	attempts *atomic.Int32
	handled  chan string
}

func (provider *queuedLifecycleProvider) Register(application *Application) error {
	queue, err := Resolve(application.Container(), JobQueueKey)
	if err != nil {
		return err
	}
	if err := HandleJobWithOptions(
		queue,
		"lifecycle.handle",
		JobHandlerOptions{MaxAttempts: 2},
		func(_ context.Context, job sendOrderEmail) error {
			if provider.attempts.Add(1) == 1 {
				return errors.New("retry")
			}
			provider.handled <- job.OrderID
			return nil
		},
	); err != nil {
		return err
	}
	return ListenQueued(
		application.Events(),
		queue,
		"lifecycle.queue",
		func(_ context.Context, event queuedOrderPlaced) (sendOrderEmail, error) {
			return sendOrderEmail{OrderID: event.OrderID}, nil
		},
	)
}

func TestQueuedEventListenerRetriesAndDrainsThroughApplicationLifecycle(t *testing.T) {
	var attempts atomic.Int32
	handled := make(chan string, 1)
	application := NewApplication(nil)
	if err := application.Register(
		NewQueueServiceProvider(JobQueueOptions{Capacity: 1, Workers: 1}),
		&queuedLifecycleProvider{attempts: &attempts, handled: handled},
	); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if err := Dispatch(
		context.Background(),
		application.Events(),
		queuedOrderPlaced{OrderID: "ORDER-2"},
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
	select {
	case orderID := <-handled:
		if orderID != "ORDER-2" {
			t.Fatalf("order ID = %q", orderID)
		}
	default:
		t.Fatal("queued job did not drain before Application shutdown")
	}
}
