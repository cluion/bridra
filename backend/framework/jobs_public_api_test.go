package framework_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cluion/bridra/backend/framework"
)

type publicJob struct {
	Value string
}

type publicJobHandlerProvider struct {
	handled *[]string
}

func (provider *publicJobHandlerProvider) Register(application *framework.Application) error {
	queue, err := framework.Resolve(application.Container(), framework.JobQueueKey)
	if err != nil {
		return err
	}
	return framework.HandleJob(
		queue,
		"public.handle",
		func(_ context.Context, job publicJob) error {
			*provider.handled = append(*provider.handled, job.Value)
			return nil
		},
	)
}

var (
	_ framework.ServiceProvider           = (*framework.QueueServiceProvider)(nil)
	_ framework.BootableServiceProvider   = (*framework.QueueServiceProvider)(nil)
	_ framework.TerminableServiceProvider = (*framework.QueueServiceProvider)(nil)
	_ framework.JobStore                  = (*framework.FileJobStore)(nil)
)

func TestPublicJobQueueProviderAPI(t *testing.T) {
	handled := []string{}
	queueProvider := framework.NewQueueServiceProvider(framework.JobQueueOptions{
		Capacity: 2,
		Workers:  1,
	})
	application := framework.NewApplication(nil)
	if err := application.Register(
		queueProvider,
		&publicJobHandlerProvider{handled: &handled},
	); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	queue, err := framework.Resolve(application.Container(), framework.JobQueueKey)
	if err != nil {
		t.Fatalf("resolve queue: %v", err)
	}
	if !queue.Running() {
		t.Fatal("queue should start during Application boot")
	}
	if err := framework.DispatchJob(context.Background(), queue, publicJob{Value: "first"}); err != nil {
		t.Fatalf("dispatch first: %v", err)
	}
	if err := framework.DispatchJob(context.Background(), queue, publicJob{Value: "second"}); err != nil {
		t.Fatalf("dispatch second: %v", err)
	}
	if err := framework.DispatchJobAfter(
		context.Background(),
		queue,
		0,
		publicJob{Value: "third"},
	); err != nil {
		t.Fatalf("dispatch third: %v", err)
	}
	if err := framework.DispatchJobAt(
		context.Background(),
		queue,
		time.Now().Add(-time.Second),
		publicJob{Value: "fourth"},
	); err != nil {
		t.Fatalf("dispatch fourth: %v", err)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("application shutdown: %v", err)
	}
	if !reflect.DeepEqual(handled, []string{"first", "second", "third", "fourth"}) {
		t.Fatalf("handled = %#v", handled)
	}
	if !queue.Stopped() {
		t.Fatal("queue should stop during Application shutdown")
	}
}

func TestPublicJobFailurePreservesOriginalError(t *testing.T) {
	providerError := errors.New("send failed")
	failures := make(chan framework.JobFailure, 1)
	attempts := 0
	queue, err := framework.NewJobQueue(framework.JobQueueOptions{
		ReportFailure: func(failure framework.JobFailure) {
			failures <- failure
		},
	})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if err := framework.HandleJobWithOptions(
		queue,
		"public.failure",
		framework.JobHandlerOptions{MaxAttempts: 2},
		func(context.Context, publicJob) error {
			attempts++
			return providerError
		},
	); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := framework.DispatchJob(context.Background(), queue, publicJob{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	failure := <-failures
	if failure.Handler != "public.failure" || failure.JobType != reflect.TypeFor[publicJob]() ||
		failure.Attempts != 2 || failure.MaxAttempts != 2 || attempts != 2 {
		t.Fatalf("failure = %#v", failure)
	}
	if !errors.Is(failure.Err, framework.ErrJobExecutionFailed) ||
		!errors.Is(failure.Err, framework.ErrJobRetriesExhausted) ||
		!errors.Is(failure.Err, providerError) {
		t.Fatalf("failure error = %v", failure.Err)
	}
}

func TestPublicPersistentFileJobQueueSurvivesProviderRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue", "jobs.log")
	storeOptions := framework.DefaultFileJobStoreOptions(path)
	storeOptions.MaxJobs = 10
	storeOptions.MaxPayloadBytes = 1024
	firstStore, err := framework.NewFileJobStore(storeOptions)
	if err != nil {
		t.Fatalf("new first store: %v", err)
	}
	var ranEarly atomic.Bool
	firstProvider := framework.NewQueueServiceProvider(
		publicPersistentQueueOptions(firstStore),
	)
	firstApplication := framework.NewApplication(nil)
	if err := firstApplication.Register(
		firstProvider,
		&publicPersistentJobProvider{
			handle: func(context.Context, publicJob) error {
				ranEarly.Store(true)
				return nil
			},
		},
	); err != nil {
		t.Fatalf("register first application: %v", err)
	}
	if err := firstApplication.Boot(); err != nil {
		t.Fatalf("boot first application: %v", err)
	}
	firstQueue, err := framework.Resolve(
		firstApplication.Container(),
		framework.JobQueueKey,
	)
	if err != nil {
		t.Fatalf("resolve first queue: %v", err)
	}
	readyAt := time.Now().Add(100 * time.Millisecond)
	if err := framework.DispatchJobAt(
		context.Background(),
		firstQueue,
		readyAt,
		publicJob{Value: "persisted"},
	); err != nil {
		t.Fatalf("dispatch persistent job: %v", err)
	}
	if err := firstApplication.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown first application: %v", err)
	}
	if _, err := firstStore.Reserve(
		context.Background(),
		time.Now(),
		time.Second,
	); !errors.Is(err, framework.ErrJobStoreClosed) {
		t.Fatalf("first provider did not close store: %v", err)
	}
	if ranEarly.Load() {
		t.Fatal("persistent job ran before its ready time")
	}

	secondStore, err := framework.NewFileJobStore(storeOptions)
	if err != nil {
		t.Fatalf("new second store: %v", err)
	}
	handled := make(chan publicJob, 1)
	secondApplication := framework.NewApplication(nil)
	if err := secondApplication.Register(
		framework.NewQueueServiceProvider(
			publicPersistentQueueOptions(secondStore),
		),
		&publicPersistentJobProvider{
			handle: func(_ context.Context, job publicJob) error {
				handled <- job
				return nil
			},
		},
	); err != nil {
		t.Fatalf("register second application: %v", err)
	}
	if err := secondApplication.Boot(); err != nil {
		t.Fatalf("boot second application: %v", err)
	}
	select {
	case job := <-handled:
		if job.Value != "persisted" {
			t.Fatalf("handled job = %#v", job)
		}
	case <-time.After(time.Second):
		t.Fatal("persistent job did not run after provider restart")
	}
	if err := secondApplication.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown second application: %v", err)
	}
	if _, err := secondStore.Reserve(
		context.Background(),
		time.Now(),
		time.Second,
	); !errors.Is(err, framework.ErrJobStoreClosed) {
		t.Fatalf("second provider did not close store: %v", err)
	}
}

type publicPersistentJobProvider struct {
	handle framework.JobHandler[publicJob]
}

func (provider *publicPersistentJobProvider) Register(
	application *framework.Application,
) error {
	queue, err := framework.Resolve(application.Container(), framework.JobQueueKey)
	if err != nil {
		return err
	}
	return framework.HandleJob(queue, "public.persistent", provider.handle)
}

func publicPersistentQueueOptions(store framework.JobStore) framework.JobQueueOptions {
	options := framework.DefaultJobQueueOptions()
	options.Store = store
	options.PollInterval = 5 * time.Millisecond
	options.JobTimeout = 20 * time.Millisecond
	options.LeaseDuration = 100 * time.Millisecond
	return options
}
