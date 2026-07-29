package framework

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPersistentSchedulerPreservesNextRunAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.log")
	clock := newControlledSchedulerClock()
	startedAt := clock.Now()
	firstStore := newTestFileSchedulerStore(t, path, FileSchedulerStoreOptions{})
	firstScheduler := newTestPersistentScheduler(t, firstStore, clock, nil)
	var ranBeforeRestart atomic.Bool
	if err := ScheduleTask(
		firstScheduler,
		"persistent.restart",
		time.Hour,
		func(context.Context) error {
			ranBeforeRestart.Store(true)
			return nil
		},
	); err != nil {
		t.Fatalf("schedule first: %v", err)
	}
	if err := firstScheduler.Start(); err != nil {
		t.Fatalf("start first: %v", err)
	}
	firstTimer := nextControlledTimer(t, clock)
	if firstTimer.delay != time.Hour {
		t.Fatalf("first delay = %v", firstTimer.delay)
	}
	if err := firstScheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown first: %v", err)
	}
	if ranBeforeRestart.Load() {
		t.Fatal("task ran before restart")
	}
	if err := firstStore.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	clock.SetNow(startedAt.Add(30 * time.Minute))
	secondStore := newTestFileSchedulerStore(t, path, FileSchedulerStoreOptions{})
	ran := make(chan struct{}, 1)
	secondScheduler := newTestPersistentScheduler(t, secondStore, clock, nil)
	if err := ScheduleTask(
		secondScheduler,
		"persistent.restart",
		time.Hour,
		func(context.Context) error {
			ran <- struct{}{}
			return nil
		},
	); err != nil {
		t.Fatalf("schedule second: %v", err)
	}
	if err := secondScheduler.Start(); err != nil {
		t.Fatalf("start second: %v", err)
	}
	secondTimer := nextControlledTimer(t, clock)
	if secondTimer.delay != 30*time.Minute || !secondTimer.Fire() {
		t.Fatalf("second timer = %#v", secondTimer)
	}
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("persisted task did not run")
	}
	nextTimer := nextControlledTimer(t, clock)
	if nextTimer.delay != time.Hour {
		t.Fatalf("next fixed delay = %v", nextTimer.delay)
	}
	state, err := secondStore.State(context.Background(), "persistent.restart")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.LastScheduledAt != startedAt.Add(time.Hour) ||
		state.LastCompletedAt != startedAt.Add(time.Hour) ||
		state.NextRunAt != startedAt.Add(2*time.Hour) {
		t.Fatalf("state = %#v", state)
	}
	if err := secondScheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown second: %v", err)
	}
	if err := secondStore.Close(); err != nil {
		t.Fatalf("close second store: %v", err)
	}
}

func TestPersistentSchedulerRunsOneMissedCronOccurrenceAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.log")
	clock := newControlledSchedulerClock()
	startedAt := clock.Now()
	store := newTestFileSchedulerStore(t, path, FileSchedulerStoreOptions{})
	if err := store.Initialize(
		context.Background(),
		"persistent.missed",
		startedAt.Add(time.Hour),
	); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	clock.SetNow(startedAt.Add(3*time.Hour + 30*time.Minute))
	ran := make(chan struct{}, 1)
	scheduler := newTestPersistentScheduler(t, store, clock, nil)
	if err := ScheduleCronTask(
		scheduler,
		"persistent.missed",
		"0 * * * *",
		func(context.Context) error {
			ran <- struct{}{}
			return nil
		},
	); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("missed occurrence was not recovered")
	}
	nextTimer := nextControlledTimer(t, clock)
	if nextTimer.delay != 30*time.Minute {
		t.Fatalf("next delay = %v", nextTimer.delay)
	}
	state, err := store.State(context.Background(), "persistent.missed")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.LastScheduledAt != startedAt.Add(time.Hour) ||
		state.NextRunAt != startedAt.Add(4*time.Hour) {
		t.Fatalf("state = %#v", state)
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func TestPersistentSchedulersCoordinateOneTaskReservation(t *testing.T) {
	store := newTestFileSchedulerStore(
		t,
		filepath.Join(t.TempDir(), "tasks.log"),
		FileSchedulerStoreOptions{},
	)
	clock := newControlledSchedulerClock()
	started := make(chan string, 2)
	release := make(chan struct{})
	first := newTestPersistentScheduler(t, store, clock, nil)
	second := newTestPersistentScheduler(t, store, clock, nil)
	for name, scheduler := range map[string]*Scheduler{
		"first":  first,
		"second": second,
	} {
		name := name
		if err := ScheduleTask(
			scheduler,
			"persistent.coordinated",
			time.Minute,
			func(context.Context) error {
				started <- name
				<-release
				return nil
			},
		); err != nil {
			t.Fatalf("schedule %s: %v", name, err)
		}
		if err := scheduler.Start(); err != nil {
			t.Fatalf("start %s: %v", name, err)
		}
	}
	firstTimer := nextControlledTimer(t, clock)
	secondTimer := nextControlledTimer(t, clock)
	firstTimer.Fire()
	secondTimer.Fire()
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("coordinated task did not start")
	}
	select {
	case duplicate := <-started:
		close(release)
		t.Fatalf("duplicate task execution by %s", duplicate)
	case <-time.After(25 * time.Millisecond):
	}

	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		firstDone <- first.Shutdown(context.Background())
	}()
	go func() {
		secondDone <- second.Shutdown(context.Background())
	}()
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("shutdown first: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("shutdown second: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func TestPersistentSchedulerPersistsTaskFailure(t *testing.T) {
	clock := newControlledSchedulerClock()
	store := newTestFileSchedulerStore(
		t,
		filepath.Join(t.TempDir(), "tasks.log"),
		FileSchedulerStoreOptions{},
	)
	failures := make(chan ScheduledTaskFailure, 1)
	scheduler := newTestPersistentScheduler(
		t,
		store,
		clock,
		func(failure ScheduledTaskFailure) {
			failures <- failure
		},
	)
	taskError := errors.New("task failed")
	if err := ScheduleTask(
		scheduler,
		"persistent.failure-state",
		time.Minute,
		func(context.Context) error {
			return taskError
		},
	); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	timer := nextControlledTimer(t, clock)
	if !timer.Fire() {
		t.Fatal("timer did not fire")
	}
	select {
	case failure := <-failures:
		if !errors.Is(failure.Err, taskError) {
			t.Fatalf("failure = %#v", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("task failure was not reported")
	}
	nextControlledTimer(t, clock)
	state, err := store.State(context.Background(), "persistent.failure-state")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.LastError != taskError.Error() ||
		state.LastScheduledAt.IsZero() ||
		state.LastCompletedAt.IsZero() {
		t.Fatalf("failure state = %#v", state)
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func TestPersistentSchedulerReportsTaskAndStoreErrors(t *testing.T) {
	clock := newControlledSchedulerClock()
	fileStore := newTestFileSchedulerStore(
		t,
		filepath.Join(t.TempDir(), "tasks.log"),
		FileSchedulerStoreOptions{},
	)
	storeError := errors.New("complete failed")
	store := &completionFailingSchedulerStore{
		SchedulerStore: fileStore,
		err:            storeError,
	}
	failures := make(chan ScheduledTaskFailure, 2)
	scheduler := newTestPersistentScheduler(
		t,
		store,
		clock,
		func(failure ScheduledTaskFailure) {
			failures <- failure
		},
	)
	taskError := errors.New("task failed")
	if err := ScheduleTask(
		scheduler,
		"persistent.failure",
		time.Minute,
		func(context.Context) error {
			return taskError
		},
	); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	timer := nextControlledTimer(t, clock)
	if !timer.Fire() {
		t.Fatal("timer did not fire")
	}
	var taskFailure ScheduledTaskFailure
	select {
	case taskFailure = <-failures:
	case <-time.After(time.Second):
		t.Fatal("task failure was not reported")
	}
	if taskFailure.Task != "persistent.failure" ||
		taskFailure.ScheduledAt.IsZero() ||
		!errors.Is(taskFailure.Err, ErrScheduledTaskExecutionFailed) ||
		!errors.Is(taskFailure.Err, taskError) {
		t.Fatalf("task failure = %#v", taskFailure)
	}
	select {
	case storeFailure := <-failures:
		if !errors.Is(storeFailure.Err, ErrSchedulerStoreOperationFailed) ||
			!errors.Is(storeFailure.Err, storeError) {
			t.Fatalf("store failure = %#v", storeFailure)
		}
	case <-time.After(time.Second):
		t.Fatal("store completion failure was not reported")
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := fileStore.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func TestPersistentSchedulerRejectsInvalidOptionsAndInitializationFailure(t *testing.T) {
	store := newTestFileSchedulerStore(
		t,
		filepath.Join(t.TempDir(), "tasks.log"),
		FileSchedulerStoreOptions{},
	)
	clock := newControlledSchedulerClock()
	for _, options := range []SchedulerOptions{
		{
			Store:         store,
			TaskTimeout:   time.Second,
			LeaseDuration: time.Second,
		},
		{Store: store, PollInterval: -time.Second},
	} {
		if _, err := newScheduler(options, clock); !errors.Is(
			err,
			ErrInvalidSchedulerOptions,
		) {
			t.Fatalf("options %#v error = %v", options, err)
		}
	}

	initializeError := errors.New("initialize failed")
	scheduler := newTestPersistentScheduler(
		t,
		&initializationFailingSchedulerStore{
			SchedulerStore: store,
			err:            initializeError,
		},
		clock,
		nil,
	)
	if err := ScheduleTask(
		scheduler,
		"persistent.initialize",
		time.Minute,
		func(context.Context) error { return nil },
	); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := scheduler.Start(); !errors.Is(err, ErrSchedulerStoreOperationFailed) ||
		!errors.Is(err, initializeError) {
		t.Fatalf("start error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func newTestPersistentScheduler(
	t *testing.T,
	store SchedulerStore,
	clock schedulerClock,
	reporter ScheduledTaskFailureReporter,
) *Scheduler {
	t.Helper()
	scheduler, err := newScheduler(SchedulerOptions{
		TaskTimeout:   20 * time.Millisecond,
		ReportFailure: reporter,
		Location:      time.UTC,
		Store:         store,
		PollInterval:  5 * time.Millisecond,
		LeaseDuration: 100 * time.Millisecond,
	}, clock)
	if err != nil {
		t.Fatalf("new persistent scheduler: %v", err)
	}
	return scheduler
}

type completionFailingSchedulerStore struct {
	SchedulerStore
	err error
}

func (store *completionFailingSchedulerStore) Complete(
	context.Context,
	ScheduledTaskReservation,
	time.Time,
	time.Time,
	string,
) error {
	return store.err
}

type initializationFailingSchedulerStore struct {
	SchedulerStore
	err error
}

func (store *initializationFailingSchedulerStore) Initialize(
	context.Context,
	string,
	time.Time,
) error {
	return store.err
}
