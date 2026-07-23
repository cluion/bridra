package framework_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/cluion/bridra/backend/framework"
)

var (
	_ framework.ServiceProvider           = (*framework.SchedulerServiceProvider)(nil)
	_ framework.BootableServiceProvider   = (*framework.SchedulerServiceProvider)(nil)
	_ framework.TerminableServiceProvider = (*framework.SchedulerServiceProvider)(nil)
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
