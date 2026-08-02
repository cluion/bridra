package framework

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type runtimeStressJob struct {
	Cycle int
	Index int
}

func TestRuntimeStressJobQueueLifecycle(t *testing.T) {
	cycles := runtimeStressCycles(t)
	const jobsPerCycle = 256
	const dispatchers = 8

	for cycle := range cycles {
		queue, err := NewJobQueue(JobQueueOptions{
			Capacity: 32,
			Workers:  8,
		})
		if err != nil {
			t.Fatalf("cycle %d: new queue: %v", cycle, err)
		}
		var handled atomic.Int64
		seen := make([]atomic.Int64, jobsPerCycle)
		if err := HandleJob(
			queue,
			"runtime-stress",
			func(_ context.Context, job runtimeStressJob) error {
				if job.Cycle != cycle || job.Index < 0 || job.Index >= jobsPerCycle {
					return fmt.Errorf("unexpected job %#v", job)
				}
				if delivery := seen[job.Index].Add(1); delivery != 1 {
					return fmt.Errorf(
						"job %d delivered %d times",
						job.Index,
						delivery,
					)
				}
				handled.Add(1)
				return nil
			},
		); err != nil {
			t.Fatalf("cycle %d: handle job: %v", cycle, err)
		}
		if err := queue.Start(); err != nil {
			t.Fatalf("cycle %d: start queue: %v", cycle, err)
		}

		errors := make(chan error, dispatchers)
		var wait sync.WaitGroup
		for dispatcher := range dispatchers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				for index := dispatcher; index < jobsPerCycle; index += dispatchers {
					if err := DispatchJob(
						context.Background(),
						queue,
						runtimeStressJob{Cycle: cycle, Index: index},
					); err != nil {
						errors <- err
						return
					}
				}
			}()
		}
		wait.Wait()
		close(errors)
		for err := range errors {
			t.Fatalf("cycle %d: dispatch: %v", cycle, err)
		}
		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		err = queue.Shutdown(shutdownContext)
		cancel()
		if err != nil {
			t.Fatalf("cycle %d: shutdown queue: %v", cycle, err)
		}
		if got := handled.Load(); got != jobsPerCycle {
			t.Fatalf("cycle %d: handled %d jobs, want %d", cycle, got, jobsPerCycle)
		}
		for index := range jobsPerCycle {
			if deliveries := seen[index].Load(); deliveries != 1 {
				t.Fatalf(
					"cycle %d: job %d delivered %d times",
					cycle,
					index,
					deliveries,
				)
			}
		}
		if !queue.Stopped() {
			t.Fatalf("cycle %d: queue did not stop", cycle)
		}
	}
}

func TestRuntimeStressSchedulerLifecycle(t *testing.T) {
	cycles := runtimeStressCycles(t)
	const tasksPerCycle = 4
	const minimumRuns = 20

	for cycle := range cycles {
		scheduler, err := NewScheduler(SchedulerOptions{})
		if err != nil {
			t.Fatalf("cycle %d: new scheduler: %v", cycle, err)
		}
		var runs atomic.Int64
		reached := make(chan struct{})
		var reachedOnce sync.Once
		for taskIndex := range tasksPerCycle {
			name := fmt.Sprintf("runtime-stress-%d-%d", cycle, taskIndex)
			if err := ScheduleTask(
				scheduler,
				name,
				time.Millisecond,
				func(context.Context) error {
					if runs.Add(1) >= minimumRuns {
						reachedOnce.Do(func() { close(reached) })
					}
					return nil
				},
			); err != nil {
				t.Fatalf("cycle %d: schedule task: %v", cycle, err)
			}
		}
		if err := scheduler.Start(); err != nil {
			t.Fatalf("cycle %d: start scheduler: %v", cycle, err)
		}
		select {
		case <-reached:
		case <-time.After(2 * time.Second):
			t.Fatalf("cycle %d: scheduler ran %d times", cycle, runs.Load())
		}
		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		err = scheduler.Shutdown(shutdownContext)
		cancel()
		if err != nil {
			t.Fatalf("cycle %d: shutdown scheduler: %v", cycle, err)
		}
		if !scheduler.Stopped() {
			t.Fatalf("cycle %d: scheduler did not stop", cycle)
		}
		afterShutdown := runs.Load()
		time.Sleep(2 * time.Millisecond)
		if got := runs.Load(); got != afterShutdown {
			t.Fatalf(
				"cycle %d: scheduler ran after shutdown: before=%d after=%d",
				cycle,
				afterShutdown,
				got,
			)
		}
	}
}

func runtimeStressCycles(t *testing.T) int {
	t.Helper()
	if os.Getenv("BRIDRA_STRESS") != "1" {
		t.Skip("set BRIDRA_STRESS=1 to run Runtime stress tests")
	}
	const defaultCycles = 50
	value := os.Getenv("BRIDRA_STRESS_CYCLES")
	if value == "" {
		return defaultCycles
	}
	cycles, err := strconv.Atoi(value)
	if err != nil || cycles < 1 || cycles > 1000 {
		t.Fatalf("BRIDRA_STRESS_CYCLES must be between 1 and 1000, got %q", value)
	}
	return cycles
}
