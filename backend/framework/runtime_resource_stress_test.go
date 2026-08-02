package framework

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const runtimeResourceRequestsPerCycle = 32
const runtimeResourceJobsPerCycle = 32

type runtimeResourceJob struct {
	Cycle int
	Index int
}

type runtimeResourceSnapshot struct {
	Goroutines          int
	HeapAlloc           uint64
	OpenFileDescriptors int
	FileDescriptorsOK   bool
}

func TestRuntimeResourceStability(t *testing.T) {
	cycles := runtimeStressCycles(t)
	limits := struct {
		goroutines int
		heapBytes  uint64
		fds        int
	}{
		goroutines: runtimeResourceLimit(
			t,
			"BRIDRA_RESOURCE_MAX_GOROUTINE_GROWTH",
			4,
		),
		heapBytes: uint64(runtimeResourceLimit(
			t,
			"BRIDRA_RESOURCE_MAX_HEAP_GROWTH_MIB",
			8,
		)) * 1024 * 1024,
		fds: runtimeResourceLimit(t, "BRIDRA_RESOURCE_MAX_FD_GROWTH", 4),
	}
	storePath := filepath.Join(t.TempDir(), "jobs.log")

	for warmup := range min(cycles, 3) {
		runRuntimeResourceCycle(t, -warmup-1, storePath)
	}
	baseline := takeRuntimeResourceSnapshot(t)
	midpoint := baseline
	for cycle := range cycles {
		runRuntimeResourceCycle(t, cycle, storePath)
		if cycle+1 == (cycles+1)/2 {
			midpoint = takeRuntimeResourceSnapshot(t)
		}
	}
	final := takeRuntimeResourceSnapshot(t)

	t.Logf(
		"runtime resources baseline=%s midpoint=%s final=%s",
		baseline,
		midpoint,
		final,
	)
	if growth := positiveIntGrowth(final.Goroutines, baseline.Goroutines); growth > limits.goroutines {
		t.Fatalf(
			"goroutine growth = %d, maximum = %d; baseline=%d final=%d",
			growth,
			limits.goroutines,
			baseline.Goroutines,
			final.Goroutines,
		)
	}
	if growth := positiveUint64Growth(final.HeapAlloc, baseline.HeapAlloc); growth > limits.heapBytes {
		t.Fatalf(
			"retained heap growth = %d bytes, maximum = %d; baseline=%d final=%d",
			growth,
			limits.heapBytes,
			baseline.HeapAlloc,
			final.HeapAlloc,
		)
	}
	if baseline.FileDescriptorsOK && final.FileDescriptorsOK {
		if growth := positiveIntGrowth(
			final.OpenFileDescriptors,
			baseline.OpenFileDescriptors,
		); growth > limits.fds {
			t.Fatalf(
				"file descriptor growth = %d, maximum = %d; baseline=%d final=%d",
				growth,
				limits.fds,
				baseline.OpenFileDescriptors,
				final.OpenFileDescriptors,
			)
		}
	}
}

func runRuntimeResourceCycle(t *testing.T, cycle int, storePath string) {
	t.Helper()
	runRuntimeResourceServerCycle(t, cycle)
	runRuntimeResourceQueueCycle(t, cycle)
	runRuntimeResourceSchedulerCycle(t, cycle)

	store, err := NewFileJobStore(DefaultFileJobStoreOptions(storePath))
	if err != nil {
		t.Fatalf("cycle %d: open FileJobStore: %v", cycle, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("cycle %d: close FileJobStore: %v", cycle, err)
	}
}

func runRuntimeResourceServerCycle(t *testing.T, cycle int) {
	t.Helper()
	router := NewRouter()
	router.Handle("runtime.resource", func(*Context) (any, error) {
		return map[string]int{"cycle": cycle}, nil
	})
	var input strings.Builder
	for request := range runtimeResourceRequestsPerCycle {
		fmt.Fprintf(
			&input,
			`{"id":"%d-%d","method":"runtime.resource"}`+"\n",
			cycle,
			request,
		)
	}
	var output bytes.Buffer
	server := &Server{
		Router: router,
		Input:  strings.NewReader(input.String()),
		Output: &output,
	}
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("cycle %d: serve requests: %v", cycle, err)
	}
	if responses := bytes.Count(output.Bytes(), []byte{'\n'}); responses != runtimeResourceRequestsPerCycle {
		t.Fatalf(
			"cycle %d: responses = %d, want %d",
			cycle,
			responses,
			runtimeResourceRequestsPerCycle,
		)
	}
}

func runRuntimeResourceQueueCycle(t *testing.T, cycle int) {
	t.Helper()
	queue, err := NewJobQueue(JobQueueOptions{Capacity: 16, Workers: 4})
	if err != nil {
		t.Fatalf("cycle %d: create queue: %v", cycle, err)
	}
	var handled atomic.Int64
	if err := HandleJob(
		queue,
		"runtime-resource",
		func(_ context.Context, job runtimeResourceJob) error {
			if job.Cycle != cycle {
				return fmt.Errorf("unexpected cycle %d", job.Cycle)
			}
			handled.Add(1)
			return nil
		},
	); err != nil {
		t.Fatalf("cycle %d: register job: %v", cycle, err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("cycle %d: start queue: %v", cycle, err)
	}
	for job := range runtimeResourceJobsPerCycle {
		if err := DispatchJob(
			context.Background(),
			queue,
			runtimeResourceJob{Cycle: cycle, Index: job},
		); err != nil {
			t.Fatalf("cycle %d: dispatch job %d: %v", cycle, job, err)
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = queue.Shutdown(shutdownContext)
	cancel()
	if err != nil {
		t.Fatalf("cycle %d: shut down queue: %v", cycle, err)
	}
	if got := handled.Load(); got != runtimeResourceJobsPerCycle {
		t.Fatalf("cycle %d: handled jobs = %d, want %d", cycle, got, runtimeResourceJobsPerCycle)
	}
}

func runRuntimeResourceSchedulerCycle(t *testing.T, cycle int) {
	t.Helper()
	scheduler, err := NewScheduler(SchedulerOptions{})
	if err != nil {
		t.Fatalf("cycle %d: create scheduler: %v", cycle, err)
	}
	ran := make(chan struct{}, 1)
	if err := ScheduleTask(
		scheduler,
		fmt.Sprintf("runtime-resource-%d", cycle),
		time.Millisecond,
		func(context.Context) error {
			select {
			case ran <- struct{}{}:
			default:
			}
			return nil
		},
	); err != nil {
		t.Fatalf("cycle %d: register task: %v", cycle, err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("cycle %d: start scheduler: %v", cycle, err)
	}
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatalf("cycle %d: scheduler did not run", cycle)
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = scheduler.Shutdown(shutdownContext)
	cancel()
	if err != nil {
		t.Fatalf("cycle %d: shut down scheduler: %v", cycle, err)
	}
}

func takeRuntimeResourceSnapshot(t *testing.T) runtimeResourceSnapshot {
	t.Helper()
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	fds, supported, err := runtimeOpenFileDescriptors()
	if err != nil {
		t.Fatalf("count open file descriptors: %v", err)
	}
	return runtimeResourceSnapshot{
		Goroutines:          runtime.NumGoroutine(),
		HeapAlloc:           memory.HeapAlloc,
		OpenFileDescriptors: fds,
		FileDescriptorsOK:   supported,
	}
}

func runtimeOpenFileDescriptors() (int, bool, error) {
	path := ""
	switch runtime.GOOS {
	case "linux":
		path = "/proc/self/fd"
	default:
		return 0, false, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, true, err
	}
	return len(entries), true, nil
}

func runtimeResourceLimit(t *testing.T, name string, fallback int) int {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 0 || limit > 1_000_000 {
		t.Fatalf("%s must be between 0 and 1000000, got %q", name, value)
	}
	return limit
}

func positiveIntGrowth(current, baseline int) int {
	if current <= baseline {
		return 0
	}
	return current - baseline
}

func positiveUint64Growth(current, baseline uint64) uint64 {
	if current <= baseline {
		return 0
	}
	return current - baseline
}

func (snapshot runtimeResourceSnapshot) String() string {
	fds := "unsupported"
	if snapshot.FileDescriptorsOK {
		fds = strconv.Itoa(snapshot.OpenFileDescriptors)
	}
	return fmt.Sprintf(
		"{goroutines:%d heap_bytes:%d fds:%s}",
		snapshot.Goroutines,
		snapshot.HeapAlloc,
		fds,
	)
}
