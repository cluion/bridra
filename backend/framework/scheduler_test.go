package framework

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type controlledSchedulerClock struct {
	timers chan *controlledSchedulerTimer
	now    time.Time
	mu     sync.Mutex
}

type controlledSchedulerTimer struct {
	delay   time.Duration
	due     time.Time
	clock   *controlledSchedulerClock
	ticks   chan time.Time
	stopped atomic.Bool
}

func newControlledSchedulerClock() *controlledSchedulerClock {
	return &controlledSchedulerClock{
		timers: make(chan *controlledSchedulerTimer, 32),
		now:    time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
}

func (clock *controlledSchedulerClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *controlledSchedulerClock) SetNow(now time.Time) {
	clock.mu.Lock()
	clock.now = now
	clock.mu.Unlock()
}

func (clock *controlledSchedulerClock) NewTimer(delay time.Duration) schedulerTimer {
	clock.mu.Lock()
	due := clock.now.Add(delay)
	clock.mu.Unlock()
	timer := &controlledSchedulerTimer{
		delay: delay,
		due:   due,
		clock: clock,
		ticks: make(chan time.Time, 1),
	}
	clock.timers <- timer
	return timer
}

func (timer *controlledSchedulerTimer) C() <-chan time.Time {
	return timer.ticks
}

func (timer *controlledSchedulerTimer) Stop() bool {
	return !timer.stopped.Swap(true)
}

func (timer *controlledSchedulerTimer) Fire() bool {
	if timer.stopped.Load() {
		return false
	}
	timer.clock.mu.Lock()
	if timer.due.After(timer.clock.now) {
		timer.clock.now = timer.due
	}
	now := timer.clock.now
	timer.clock.mu.Unlock()
	select {
	case timer.ticks <- now:
		return true
	default:
		return false
	}
}

func nextControlledTimer(t *testing.T, clock *controlledSchedulerClock) *controlledSchedulerTimer {
	t.Helper()
	select {
	case timer := <-clock.timers:
		return timer
	case <-time.After(time.Second):
		t.Fatal("scheduler did not create its next timer")
		return nil
	}
}

func TestSchedulerUsesFixedDelayAndPreventsTaskOverlap(t *testing.T) {
	clock := newControlledSchedulerClock()
	scheduler, err := newScheduler(SchedulerOptions{}, clock)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	started := make(chan struct{})
	completed := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseTask := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		releaseTask()
		_ = scheduler.Shutdown(context.Background())
	})
	var runs atomic.Int32
	if err := ScheduleTask(scheduler, "reports.refresh", time.Minute, func(context.Context) error {
		runs.Add(1)
		close(started)
		<-release
		close(completed)
		return nil
	}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("second start: %v", err)
	}
	first := nextControlledTimer(t, clock)
	if first.delay != time.Minute || !first.Fire() {
		t.Fatalf("first timer = %#v", first)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("scheduled task did not start")
	}
	if pending := len(clock.timers); pending != 0 {
		t.Fatalf("scheduler created %d timer(s) before the task completed", pending)
	}

	releaseTask()
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("scheduled task did not complete")
	}
	second := nextControlledTimer(t, clock)
	if second.delay != time.Minute {
		t.Fatalf("second timer delay = %v", second.delay)
	}
	if runs.Load() != 1 {
		t.Fatalf("runs = %d, want 1", runs.Load())
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !second.stopped.Load() {
		t.Fatal("shutdown should stop a pending task timer")
	}
}

func TestSchedulerRunsDifferentTasksConcurrently(t *testing.T) {
	clock := newControlledSchedulerClock()
	scheduler, err := newScheduler(SchedulerOptions{}, clock)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	for _, name := range []string{"first", "second"} {
		name := name
		if err := ScheduleTask(scheduler, name, time.Minute, func(context.Context) error {
			started <- name
			<-release
			return nil
		}); err != nil {
			t.Fatalf("schedule %s: %v", name, err)
		}
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	first := nextControlledTimer(t, clock)
	second := nextControlledTimer(t, clock)
	first.Fire()
	second.Fire()
	seen := map[string]bool{}
	for range 2 {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(time.Second):
			close(release)
			t.Fatal("scheduled tasks did not run concurrently")
		}
	}
	close(release)
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("started tasks = %#v", seen)
	}
}

func TestSchedulerReportsErrorsAndRecoveredPanicsThenContinues(t *testing.T) {
	clock := newControlledSchedulerClock()
	providerError := errors.New("report service unavailable")
	failures := make(chan ScheduledTaskFailure, 2)
	scheduler, err := newScheduler(SchedulerOptions{
		ReportFailure: func(failure ScheduledTaskFailure) {
			failures <- failure
		},
	}, clock)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	var runs atomic.Int32
	succeeded := make(chan struct{})
	if err := ScheduleTask(scheduler, "reports.generate", time.Minute, func(context.Context) error {
		switch runs.Add(1) {
		case 1:
			return providerError
		case 2:
			panic("broken report")
		default:
			close(succeeded)
			return nil
		}
	}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	nextControlledTimer(t, clock).Fire()
	first := <-failures
	if first.Task != "reports.generate" ||
		!errors.Is(first.Err, ErrScheduledTaskExecutionFailed) ||
		!errors.Is(first.Err, providerError) {
		t.Fatalf("first failure = %#v", first)
	}
	nextControlledTimer(t, clock).Fire()
	second := <-failures
	if second.Task != "reports.generate" ||
		!errors.Is(second.Err, ErrScheduledTaskExecutionFailed) ||
		!strings.Contains(second.Err.Error(), "broken report") {
		t.Fatalf("second failure = %#v", second)
	}
	nextControlledTimer(t, clock).Fire()
	select {
	case <-succeeded:
	case <-time.After(time.Second):
		t.Fatal("task loop did not continue after failures")
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestSchedulerAppliesTaskTimeout(t *testing.T) {
	clock := newControlledSchedulerClock()
	failures := make(chan ScheduledTaskFailure, 1)
	scheduler, err := newScheduler(SchedulerOptions{
		TaskTimeout: 10 * time.Millisecond,
		ReportFailure: func(failure ScheduledTaskFailure) {
			failures <- failure
		},
	}, clock)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	if err := ScheduleTask(scheduler, "timeout", time.Minute, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	nextControlledTimer(t, clock).Fire()
	failure := <-failures
	if !errors.Is(failure.Err, ErrScheduledTaskExecutionFailed) ||
		!errors.Is(failure.Err, context.DeadlineExceeded) {
		t.Fatalf("timeout failure = %#v", failure)
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestSchedulerShutdownTimeoutContinuesWaitingForRunningTask(t *testing.T) {
	clock := newControlledSchedulerClock()
	scheduler, err := newScheduler(SchedulerOptions{}, clock)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	if err := ScheduleTask(scheduler, "blocking", time.Minute, func(context.Context) error {
		close(started)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	nextControlledTimer(t, clock).Fire()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := scheduler.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		t.Fatalf("shutdown error = %v", err)
	}
	if scheduler.Running() {
		close(release)
		t.Fatal("scheduler should stop reporting running after shutdown begins")
	}
	close(release)
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("wait for shutdown: %v", err)
	}
	if !scheduler.Stopped() {
		t.Fatal("scheduler should finish after the task exits")
	}
}

func TestSchedulerRejectsInvalidAndLateTasks(t *testing.T) {
	if _, err := NewScheduler(SchedulerOptions{TaskTimeout: -time.Second}); !errors.Is(
		err,
		ErrInvalidSchedulerOptions,
	) {
		t.Fatalf("invalid options error = %v", err)
	}
	scheduler, err := NewScheduler(SchedulerOptions{})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	task := ScheduledTask(func(context.Context) error { return nil })
	if err := ScheduleTask(nil, "task", time.Second, task); !errors.Is(err, ErrSchedulerUnavailable) {
		t.Fatalf("nil scheduler error = %v", err)
	}
	for _, test := range []struct {
		name     string
		interval time.Duration
		task     ScheduledTask
	}{
		{name: "", interval: time.Second, task: task},
		{name: "task", interval: 0, task: task},
		{name: "task", interval: time.Second, task: nil},
	} {
		if err := ScheduleTask(scheduler, test.name, test.interval, test.task); !errors.Is(
			err,
			ErrInvalidScheduledTask,
		) {
			t.Fatalf("invalid task %#v error = %v", test, err)
		}
	}
	if err := ScheduleTask(scheduler, "first", time.Hour, task); err != nil {
		t.Fatalf("schedule first: %v", err)
	}
	if err := ScheduleTask(scheduler, "second", time.Hour, task); err != nil {
		t.Fatalf("schedule second: %v", err)
	}
	if err := ScheduleTask(scheduler, "first", time.Hour, task); !errors.Is(
		err,
		ErrScheduledTaskAlreadyDefined,
	) {
		t.Fatalf("duplicate task error = %v", err)
	}
	if names := ScheduledTasks(scheduler); !reflect.DeepEqual(names, []string{"first", "second"}) {
		t.Fatalf("tasks = %#v", names)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := ScheduleTask(scheduler, "late", time.Hour, task); !errors.Is(
		err,
		ErrScheduledTaskRegistrationClosed,
	) {
		t.Fatalf("late task error = %v", err)
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestSchedulerRejectsUnavailableContextWithoutTransition(t *testing.T) {
	scheduler, err := NewScheduler(SchedulerOptions{})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	if err := scheduler.Shutdown(nil); !errors.Is(err, ErrSchedulerContextUnavailable) {
		t.Fatalf("nil context error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scheduler.Shutdown(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v", err)
	}
	if scheduler.Stopped() {
		t.Fatal("rejected shutdown should not transition the scheduler")
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

type scheduledQueueJob struct {
	Value string
}

func TestScheduledTaskCanDispatchJob(t *testing.T) {
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 1, Workers: 1})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	handled := make(chan string, 1)
	if err := HandleJob(queue, "scheduled.handle", func(_ context.Context, job scheduledQueueJob) error {
		handled <- job.Value
		return nil
	}); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("start queue: %v", err)
	}
	clock := newControlledSchedulerClock()
	scheduler, err := newScheduler(SchedulerOptions{}, clock)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	dispatched := make(chan struct{})
	if err := ScheduleTask(scheduler, "scheduled.dispatch", time.Minute, func(ctx context.Context) error {
		err := DispatchJob(ctx, queue, scheduledQueueJob{Value: "scheduled"})
		close(dispatched)
		return err
	}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	nextControlledTimer(t, clock).Fire()
	<-dispatched
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown scheduler: %v", err)
	}
	if err := queue.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown queue: %v", err)
	}
	select {
	case value := <-handled:
		if value != "scheduled" {
			t.Fatalf("handled value = %q", value)
		}
	default:
		t.Fatal("scheduled job was not drained")
	}
}

type schedulerLifecycleProvider struct{}

func (schedulerLifecycleProvider) Register(application *Application) error {
	scheduler, err := Resolve(application.Container(), SchedulerKey)
	if err != nil {
		return err
	}
	return ScheduleTask(scheduler, "lifecycle.task", time.Hour, func(context.Context) error {
		return nil
	})
}

func TestSchedulerServiceProviderFollowsApplicationLifecycle(t *testing.T) {
	application := NewApplication(nil)
	if err := application.Register(
		NewQueueServiceProvider(JobQueueOptions{}),
		NewSchedulerServiceProvider(SchedulerOptions{}),
		schedulerLifecycleProvider{},
	); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	scheduler, err := Resolve(application.Container(), SchedulerKey)
	if err != nil {
		t.Fatalf("resolve scheduler: %v", err)
	}
	queue, err := Resolve(application.Container(), JobQueueKey)
	if err != nil {
		t.Fatalf("resolve queue: %v", err)
	}
	if !scheduler.Running() || !queue.Running() {
		t.Fatal("Application Boot should start Queue and Scheduler")
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("application shutdown: %v", err)
	}
	if !scheduler.Stopped() || !queue.Stopped() {
		t.Fatal("Application Shutdown should stop Scheduler and Queue")
	}
}
