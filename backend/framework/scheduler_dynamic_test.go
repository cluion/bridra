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

func waitDynamicTask(t *testing.T, handle *ScheduledTaskHandle) {
	t.Helper()
	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("dynamic task did not finish")
	}
}

func receiveDynamicRun(t *testing.T, started <-chan int32, want int32) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("run = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("dynamic task did not run")
	}
}

func TestDynamicTaskControlsDelayCoalescingAndNameReuse(t *testing.T) {
	clock := newControlledSchedulerClock()
	scheduler, err := newScheduler(SchedulerOptions{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Shutdown(context.Background()) })
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	started := make(chan int32, 8)
	release := make(chan struct{}, 8)
	var runs atomic.Int32
	handle, err := StartTask(scheduler, "monitor.poll", time.Minute, func(ctx context.Context) error {
		started <- runs.Add(1)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := ScheduledTasks(scheduler); !reflect.DeepEqual(got, []string{"monitor.poll"}) {
		t.Fatalf("names: %v", got)
	}
	first := nextControlledTimer(t, clock)
	if first.delay != time.Minute || runs.Load() != 0 {
		t.Fatal("task did not wait its initial interval")
	}
	if err := handle.SetInterval(2 * time.Minute); err != nil {
		t.Fatal(err)
	}
	second := nextControlledTimer(t, clock)
	if second.delay != 2*time.Minute || !first.stopped.Load() {
		t.Fatal("interval update did not replace the pending timer")
	}
	if err := handle.TriggerNow(); err != nil {
		t.Fatal(err)
	}
	receiveDynamicRun(t, started, 1)
	if !second.stopped.Load() {
		t.Fatal("immediate trigger did not stop pending timer")
	}
	for range 20 {
		if err := handle.TriggerNow(); err != nil {
			t.Fatal(err)
		}
	}
	if err := handle.SetInterval(3 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if runs.Load() != 1 || len(clock.timers) != 0 {
		t.Fatal("trigger overlapped the active invocation")
	}
	release <- struct{}{}
	receiveDynamicRun(t, started, 2)
	release <- struct{}{}
	third := nextControlledTimer(t, clock)
	if third.delay != 3*time.Minute || runs.Load() != 2 {
		t.Fatal("queued triggers did not coalesce or updated delay was lost")
	}
	third.Fire()
	receiveDynamicRun(t, started, 3)
	release <- struct{}{}
	fourth := nextControlledTimer(t, clock)
	handle.Stop()
	handle.Stop()
	waitDynamicTask(t, handle)
	if !fourth.stopped.Load() || len(ScheduledTasks(scheduler)) != 0 {
		t.Fatal("stop did not remove the timer and name")
	}
	if err := handle.TriggerNow(); !errors.Is(err, ErrScheduledTaskStopped) {
		t.Fatalf("trigger after stop: %v", err)
	}
	if err := handle.SetInterval(time.Minute); !errors.Is(err, ErrScheduledTaskStopped) {
		t.Fatalf("interval after stop: %v", err)
	}
	restarted, err := StartTask(scheduler, "monitor.poll", time.Minute, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("reuse completed name: %v", err)
	}
	restarted.Stop()
	waitDynamicTask(t, restarted)
}

func TestDynamicTaskStopCancelsActiveInvocationAndShutdownWaits(t *testing.T) {
	failures := make(chan ScheduledTaskFailure, 1)
	scheduler, err := NewScheduler(SchedulerOptions{ReportFailure: func(f ScheduledTaskFailure) { failures <- f }})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseTask := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() { releaseTask(); _ = scheduler.Shutdown(context.Background()) })
	handle, err := StartTask(scheduler, "monitor.poll", time.Hour, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.TriggerNow(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}
	handle.Stop()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel invocation context")
	}
	select {
	case <-handle.Done():
		t.Fatal("Done closed while task still active")
	default:
	}
	if _, err := StartTask(scheduler, "monitor.poll", time.Hour, func(context.Context) error { return nil }); !errors.Is(err, ErrScheduledTaskAlreadyDefined) {
		t.Fatalf("reused active name: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := scheduler.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown must wait for active invocation: %v", err)
	}
	releaseTask()
	waitDynamicTask(t, handle)
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatal("intentional stop reported as task failure")
	}
}

func TestDynamicTaskRejectsInvalidStateAndPersistentSchedulers(t *testing.T) {
	task := func(context.Context) error { return nil }
	if _, err := StartTask(nil, "task", time.Minute, task); !errors.Is(err, ErrSchedulerUnavailable) {
		t.Fatal(err)
	}
	scheduler, err := NewScheduler(SchedulerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StartTask(scheduler, "task", time.Minute, task); !errors.Is(err, ErrSchedulerNotRunning) {
		t.Fatalf("before start: %v", err)
	}
	if err := ScheduleTask(scheduler, "static", time.Hour, task); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Shutdown(context.Background()) })
	for _, test := range []struct {
		name  string
		delay time.Duration
		run   ScheduledTask
	}{{"", time.Minute, task}, {"task", 0, task}, {"task", -time.Minute, task}, {"task", time.Minute, nil}} {
		if _, err := StartTask(scheduler, test.name, test.delay, test.run); !errors.Is(err, ErrInvalidScheduledTask) {
			t.Fatalf("invalid input: %v", err)
		}
	}
	if _, err := StartTask(scheduler, "static", time.Minute, task); !errors.Is(err, ErrScheduledTaskAlreadyDefined) {
		t.Fatalf("static name conflict: %v", err)
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := StartTask(scheduler, "task", time.Minute, task); !errors.Is(err, ErrSchedulerStopped) {
		t.Fatalf("after shutdown: %v", err)
	}
	store, err := NewFileSchedulerStore(DefaultFileSchedulerStoreOptions(t.TempDir() + "/scheduler.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	persistent, err := NewScheduler(SchedulerOptions{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if err := persistent.Start(); err != nil {
		t.Fatal(err)
	}
	defer persistent.Shutdown(context.Background())
	if _, err := StartTask(persistent, "task", time.Minute, task); !errors.Is(err, ErrDynamicSchedulingUnsupported) {
		t.Fatalf("persistent dynamic scheduling: %v", err)
	}
}

func TestDynamicTaskConcurrentControlsAndSchedulerShutdown(t *testing.T) {
	scheduler, err := NewScheduler(SchedulerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	ready := make(chan struct{})
	controls := make(chan struct{})
	accepted := make(chan struct{}, 24)
	for range 24 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-ready
			handle, err := StartTask(scheduler, "contended", time.Hour, func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			})
			if err != nil {
				if !errors.Is(err, ErrSchedulerStopped) && !errors.Is(err, ErrScheduledTaskAlreadyDefined) {
					t.Errorf("concurrent start: %v", err)
				}
				return
			}
			accepted <- struct{}{}
			<-controls
			for range 20 {
				for _, err := range []error{handle.TriggerNow(), handle.SetInterval(time.Minute)} {
					if err != nil && !errors.Is(err, ErrScheduledTaskStopped) {
						t.Errorf("concurrent control: %v", err)
					}
				}
			}
			handle.Stop()
			waitDynamicTask(t, handle)
		}()
	}
	close(ready)
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("no concurrent task was accepted")
	}
	close(controls)
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
}
