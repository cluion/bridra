package framework_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cluion/bridra/backend/framework"
)

const parentProcessHelperMode = "BRIDRA_PARENT_PROCESS_HELPER"

func TestParentProcessContextStopsWhenCallerCancels(t *testing.T) {
	ctx, stop, err := framework.ParentProcessContext(context.Background())
	if err != nil {
		t.Fatalf("parent process context: %v", err)
	}
	stop()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("parent process context did not stop")
	}
	if !errors.Is(context.Cause(ctx), context.Canceled) {
		t.Fatalf("context cause = %v", context.Cause(ctx))
	}
}

func TestParentProcessContextCancelsWhenParentExits(t *testing.T) {
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestParentProcessContextHelper$",
	)
	command.Env = parentProcessHelperEnvironment("parent")
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("parent stdout: %v", err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start parent helper: %v", err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	})

	lines := make(chan string)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	ready := waitForParentProcessHelperLine(t, lines, "child-ready ")
	childPID, err := strconv.Atoi(strings.TrimPrefix(ready, "child-ready "))
	if err != nil {
		t.Fatalf("child pid from %q: %v", ready, err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("kill parent helper: %v", err)
	}
	waitForParentProcessHelperLine(t, lines, "child-exit")
	_, _ = command.Process.Wait()

	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		if child, err := os.FindProcess(childPID); err == nil {
			_ = child.Kill()
		}
	})
}

func TestParentProcessContextHelper(t *testing.T) {
	switch os.Getenv(parentProcessHelperMode) {
	case "":
		return
	case "parent":
		command := exec.Command(
			os.Args[0],
			"-test.run=^TestParentProcessContextHelper$",
		)
		command.Env = parentProcessHelperEnvironment("child")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "start child helper: %v\n", err)
			os.Exit(2)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "child":
		ctx, stop, err := framework.ParentProcessContext(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "parent process context: %v\n", err)
			os.Exit(3)
		}
		defer stop()
		fmt.Printf("child-ready %d\n", os.Getpid())
		<-ctx.Done()
		if !errors.Is(context.Cause(ctx), framework.ErrParentProcessExited) {
			fmt.Fprintf(os.Stderr, "context cause: %v\n", context.Cause(ctx))
			os.Exit(4)
		}
		fmt.Println("child-exit")
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode\n")
		os.Exit(5)
	}
}

func parentProcessHelperEnvironment(mode string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	prefix := parentProcessHelperMode + "="
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, prefix) {
			environment = append(environment, value)
		}
	}
	return append(environment, prefix+mode)
}

func waitForParentProcessHelperLine(
	t *testing.T,
	lines <-chan string,
	prefix string,
) string {
	t.Helper()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case line, open := <-lines:
			if !open {
				t.Fatalf("parent process helper ended before %q", prefix)
			}
			if strings.HasPrefix(line, prefix) {
				return line
			}
		case <-timeout.C:
			t.Fatalf("timed out waiting for parent process helper %q", prefix)
		}
	}
}
