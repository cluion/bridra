package framework

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrSchedulerUnavailable            = errors.New("framework: scheduler is unavailable")
	ErrSchedulerContextUnavailable     = errors.New("framework: scheduler context is unavailable")
	ErrInvalidSchedulerOptions         = errors.New("framework: scheduler options are invalid")
	ErrInvalidScheduledTask            = errors.New("framework: scheduled task is invalid")
	ErrInvalidCronExpression           = errors.New("framework: cron expression is invalid")
	ErrScheduledTaskAlreadyDefined     = errors.New("framework: scheduled task is already defined")
	ErrScheduledTaskRegistrationClosed = errors.New("framework: scheduled task registration is closed")
	ErrSchedulerStopped                = errors.New("framework: scheduler has stopped")
	ErrScheduledTaskExecutionFailed    = errors.New("framework: scheduled task execution failed")
)

type ScheduledTask func(context.Context) error

type ScheduledTaskFailure struct {
	Task string
	Err  error
}

type ScheduledTaskFailureReporter func(ScheduledTaskFailure)

type SchedulerOptions struct {
	TaskTimeout   time.Duration
	ReportFailure ScheduledTaskFailureReporter
	Location      *time.Location
}

func DefaultSchedulerOptions() SchedulerOptions {
	return SchedulerOptions{
		TaskTimeout: 30 * time.Second,
		Location:    time.Local,
	}
}

type schedulerState uint8

const (
	schedulerCollecting schedulerState = iota
	schedulerRunning
	schedulerStopping
	schedulerStopped
)

type Scheduler struct {
	options      SchedulerOptions
	clock        schedulerClock
	tasks        []scheduledTaskEntry
	taskNames    map[string]struct{}
	stopping     chan struct{}
	shutdownDone chan struct{}
	state        schedulerState
	runners      sync.WaitGroup
	mu           sync.Mutex
}

type scheduledTaskEntry struct {
	name     string
	schedule scheduledTaskSchedule
	run      ScheduledTask
}

type scheduledTaskSchedule interface {
	nextDelay(time.Time) (time.Duration, bool)
}

type fixedDelaySchedule struct {
	interval time.Duration
}

func (schedule fixedDelaySchedule) nextDelay(time.Time) (time.Duration, bool) {
	return schedule.interval, true
}

type schedulerClock interface {
	Now() time.Time
	NewTimer(time.Duration) schedulerTimer
}

type schedulerTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type systemSchedulerClock struct{}

func (systemSchedulerClock) Now() time.Time {
	return time.Now()
}

func (systemSchedulerClock) NewTimer(delay time.Duration) schedulerTimer {
	return systemSchedulerTimer{timer: time.NewTimer(delay)}
}

type systemSchedulerTimer struct {
	timer *time.Timer
}

func (timer systemSchedulerTimer) C() <-chan time.Time {
	return timer.timer.C
}

func (timer systemSchedulerTimer) Stop() bool {
	return timer.timer.Stop()
}

func NewScheduler(options SchedulerOptions) (*Scheduler, error) {
	return newScheduler(options, systemSchedulerClock{})
}

func newScheduler(options SchedulerOptions, clock schedulerClock) (*Scheduler, error) {
	if options.TaskTimeout < 0 {
		return nil, ErrInvalidSchedulerOptions
	}
	if clock == nil {
		return nil, ErrSchedulerUnavailable
	}
	if options.Location == nil {
		options.Location = time.Local
	}
	return &Scheduler{
		options:      options,
		clock:        clock,
		taskNames:    make(map[string]struct{}),
		stopping:     make(chan struct{}),
		shutdownDone: make(chan struct{}),
	}, nil
}

func ScheduleTask(
	scheduler *Scheduler,
	name string,
	interval time.Duration,
	task ScheduledTask,
) error {
	if scheduler == nil {
		return ErrSchedulerUnavailable
	}
	name = strings.TrimSpace(name)
	if name == "" || interval <= 0 || task == nil {
		return ErrInvalidScheduledTask
	}
	return scheduler.registerTask(
		name,
		fixedDelaySchedule{interval: interval},
		task,
	)
}

func (scheduler *Scheduler) registerTask(
	name string,
	schedule scheduledTaskSchedule,
	task ScheduledTask,
) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if scheduler.state != schedulerCollecting {
		return ErrScheduledTaskRegistrationClosed
	}
	if _, exists := scheduler.taskNames[name]; exists {
		return fmt.Errorf("%w: %s", ErrScheduledTaskAlreadyDefined, name)
	}
	scheduler.taskNames[name] = struct{}{}
	scheduler.tasks = append(scheduler.tasks, scheduledTaskEntry{
		name:     name,
		schedule: schedule,
		run:      task,
	})
	return nil
}

func ScheduledTasks(scheduler *Scheduler) []string {
	if scheduler == nil {
		return nil
	}
	scheduler.mu.Lock()
	names := make([]string, 0, len(scheduler.tasks))
	for _, task := range scheduler.tasks {
		names = append(names, task.name)
	}
	scheduler.mu.Unlock()
	return names
}

func (scheduler *Scheduler) Start() error {
	if scheduler == nil {
		return ErrSchedulerUnavailable
	}
	scheduler.mu.Lock()
	switch scheduler.state {
	case schedulerRunning:
		scheduler.mu.Unlock()
		return nil
	case schedulerStopping, schedulerStopped:
		scheduler.mu.Unlock()
		return ErrSchedulerStopped
	}
	scheduler.state = schedulerRunning
	tasks := append([]scheduledTaskEntry(nil), scheduler.tasks...)
	scheduler.runners.Add(len(tasks))
	scheduler.mu.Unlock()

	for _, task := range tasks {
		go scheduler.runTaskLoop(task)
	}
	return nil
}

func (scheduler *Scheduler) runTaskLoop(task scheduledTaskEntry) {
	defer scheduler.runners.Done()
	for {
		select {
		case <-scheduler.stopping:
			return
		default:
		}

		delay, ok := task.schedule.nextDelay(scheduler.clock.Now())
		if !ok {
			return
		}
		timer := scheduler.clock.NewTimer(delay)
		select {
		case <-scheduler.stopping:
			timer.Stop()
			return
		case <-timer.C():
		}
		select {
		case <-scheduler.stopping:
			return
		default:
		}
		scheduler.execute(task)
	}
}

func (scheduler *Scheduler) execute(task scheduledTaskEntry) {
	ctx := context.Background()
	cancel := func() {}
	if scheduler.options.TaskTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, scheduler.options.TaskTimeout)
	}
	defer cancel()

	var taskError error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				taskError = fmt.Errorf("task panic: %v", recovered)
			}
		}()
		taskError = task.run(ctx)
	}()
	if taskError == nil {
		return
	}
	scheduler.report(ScheduledTaskFailure{
		Task: task.name,
		Err: fmt.Errorf(
			"%w: task %q: %w",
			ErrScheduledTaskExecutionFailed,
			task.name,
			taskError,
		),
	})
}

func (scheduler *Scheduler) report(failure ScheduledTaskFailure) {
	reporter := scheduler.options.ReportFailure
	if reporter == nil {
		return
	}
	func() {
		defer func() {
			_ = recover()
		}()
		reporter(failure)
	}()
}

func (scheduler *Scheduler) Shutdown(ctx context.Context) error {
	if scheduler == nil {
		return ErrSchedulerUnavailable
	}
	if ctx == nil {
		return ErrSchedulerContextUnavailable
	}

	scheduler.mu.Lock()
	switch scheduler.state {
	case schedulerStopped:
		scheduler.mu.Unlock()
		return nil
	case schedulerStopping:
		done := scheduler.shutdownDone
		scheduler.mu.Unlock()
		return waitForSchedulerShutdown(ctx, done)
	}
	if err := ctx.Err(); err != nil {
		scheduler.mu.Unlock()
		return err
	}
	if scheduler.state == schedulerCollecting {
		scheduler.state = schedulerStopped
		close(scheduler.stopping)
		close(scheduler.shutdownDone)
		scheduler.mu.Unlock()
		return nil
	}

	scheduler.state = schedulerStopping
	close(scheduler.stopping)
	done := scheduler.shutdownDone
	scheduler.mu.Unlock()

	go scheduler.finishShutdown()
	return waitForSchedulerShutdown(ctx, done)
}

func (scheduler *Scheduler) finishShutdown() {
	scheduler.runners.Wait()
	scheduler.mu.Lock()
	scheduler.state = schedulerStopped
	close(scheduler.shutdownDone)
	scheduler.mu.Unlock()
}

func waitForSchedulerShutdown(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (scheduler *Scheduler) Running() bool {
	if scheduler == nil {
		return false
	}
	scheduler.mu.Lock()
	running := scheduler.state == schedulerRunning
	scheduler.mu.Unlock()
	return running
}

func (scheduler *Scheduler) Stopped() bool {
	if scheduler == nil {
		return false
	}
	scheduler.mu.Lock()
	stopped := scheduler.state == schedulerStopped
	scheduler.mu.Unlock()
	return stopped
}
