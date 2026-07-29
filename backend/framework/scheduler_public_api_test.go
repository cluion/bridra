package framework_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cluion/bridra/backend/framework"
)

var (
	_ framework.ServiceProvider           = (*framework.SchedulerServiceProvider)(nil)
	_ framework.BootableServiceProvider   = (*framework.SchedulerServiceProvider)(nil)
	_ framework.TerminableServiceProvider = (*framework.SchedulerServiceProvider)(nil)
	_ framework.SchedulerStore            = (*framework.FileSchedulerStore)(nil)
)

func TestPublicSchedulerRunsNamedTaskAndShutsDown(t *testing.T) {
	scheduler, err := framework.NewScheduler(framework.SchedulerOptions{})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	ran := make(chan struct{})
	var once sync.Once
	if err := framework.ScheduleTask(
		scheduler,
		"public.task",
		5*time.Millisecond,
		func(context.Context) error {
			once.Do(func() { close(ran) })
			return nil
		},
	); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if names := framework.ScheduledTasks(scheduler); !reflect.DeepEqual(
		names,
		[]string{"public.task"},
	) {
		t.Fatalf("tasks = %#v", names)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("scheduled task did not run")
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !scheduler.Stopped() {
		t.Fatal("scheduler should be stopped")
	}
}

func TestPublicSchedulerRegistersCronTask(t *testing.T) {
	scheduler, err := framework.NewScheduler(framework.SchedulerOptions{
		Location: time.UTC,
	})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	if err := framework.ScheduleCronTask(
		scheduler,
		"public.cron",
		"*/15 9-17 * * MON-FRI",
		func(context.Context) error { return nil },
	); err != nil {
		t.Fatalf("schedule cron task: %v", err)
	}
	if names := framework.ScheduledTasks(scheduler); !reflect.DeepEqual(
		names,
		[]string{"public.cron"},
	) {
		t.Fatalf("tasks = %#v", names)
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

type publicSchedulerTaskProvider struct{}

func (publicSchedulerTaskProvider) Register(application *framework.Application) error {
	scheduler, err := framework.Resolve(application.Container(), framework.SchedulerKey)
	if err != nil {
		return err
	}
	return framework.ScheduleTask(
		scheduler,
		"public.lifecycle",
		time.Hour,
		func(context.Context) error { return nil },
	)
}

func TestPublicSchedulerServiceProviderAPI(t *testing.T) {
	application := framework.NewApplication(nil)
	if err := application.Register(
		framework.NewSchedulerServiceProvider(framework.DefaultSchedulerOptions()),
		publicSchedulerTaskProvider{},
	); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	scheduler, err := framework.Resolve(application.Container(), framework.SchedulerKey)
	if err != nil {
		t.Fatalf("resolve scheduler: %v", err)
	}
	if !scheduler.Running() {
		t.Fatal("scheduler should start during Application Boot")
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !scheduler.Stopped() {
		t.Fatal("scheduler should stop during Application Shutdown")
	}
}

func TestPublicPersistentSchedulerSurvivesProviderRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduler", "tasks.log")
	storeOptions := framework.DefaultFileSchedulerStoreOptions(path)
	storeOptions.MaxTasks = 10
	firstStore, err := framework.NewFileSchedulerStore(storeOptions)
	if err != nil {
		t.Fatalf("new first store: %v", err)
	}
	var ranEarly atomic.Bool
	firstApplication := framework.NewApplication(nil)
	if err := firstApplication.Register(
		framework.NewSchedulerServiceProvider(
			publicPersistentSchedulerOptions(firstStore),
		),
		&publicPersistentScheduledTaskProvider{
			run: func(context.Context) error {
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
	if err := firstApplication.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown first application: %v", err)
	}
	if _, err := firstStore.State(
		context.Background(),
		"public.persistent",
	); !errors.Is(err, framework.ErrSchedulerStoreClosed) {
		t.Fatalf("first provider did not close store: %v", err)
	}
	if ranEarly.Load() {
		t.Fatal("persistent task ran before its due time")
	}

	secondStore, err := framework.NewFileSchedulerStore(storeOptions)
	if err != nil {
		t.Fatalf("new second store: %v", err)
	}
	ran := make(chan struct{}, 1)
	secondApplication := framework.NewApplication(nil)
	if err := secondApplication.Register(
		framework.NewSchedulerServiceProvider(
			publicPersistentSchedulerOptions(secondStore),
		),
		&publicPersistentScheduledTaskProvider{
			run: func(context.Context) error {
				ran <- struct{}{}
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
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("persistent task did not run after provider restart")
	}
	if err := secondApplication.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown second application: %v", err)
	}
	if _, err := secondStore.State(
		context.Background(),
		"public.persistent",
	); !errors.Is(err, framework.ErrSchedulerStoreClosed) {
		t.Fatalf("second provider did not close store: %v", err)
	}
}

type publicPersistentScheduledTaskProvider struct {
	run framework.ScheduledTask
}

func (provider *publicPersistentScheduledTaskProvider) Register(
	application *framework.Application,
) error {
	scheduler, err := framework.Resolve(
		application.Container(),
		framework.SchedulerKey,
	)
	if err != nil {
		return err
	}
	return framework.ScheduleTask(
		scheduler,
		"public.persistent",
		100*time.Millisecond,
		provider.run,
	)
}

func publicPersistentSchedulerOptions(
	store framework.SchedulerStore,
) framework.SchedulerOptions {
	options := framework.DefaultSchedulerOptions()
	options.Store = store
	options.PollInterval = 5 * time.Millisecond
	options.TaskTimeout = 20 * time.Millisecond
	options.LeaseDuration = 100 * time.Millisecond
	return options
}
