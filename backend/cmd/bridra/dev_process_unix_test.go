//go:build !windows

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	devProcessHelperMode      = "BRIDRA_DEV_PROCESS_HELPER"
	devProcessParentReadyPath = "BRIDRA_DEV_PARENT_READY_PATH"
	devProcessChildReadyPath  = "BRIDRA_DEV_CHILD_READY_PATH"
	devProcessChildPIDPath    = "BRIDRA_DEV_CHILD_PID_PATH"
)

func TestDevProcessGroupForwardsSignalToDescendant(t *testing.T) {
	if os.Getenv(devProcessHelperMode) != "" {
		runDevProcessHelper(t)
		return
	}

	directory := t.TempDir()
	parentReady := filepath.Join(directory, "parent-ready")
	childReady := filepath.Join(directory, "child-ready")
	childPIDPath := filepath.Join(directory, "child-pid")
	var output bytes.Buffer
	command := newDevExecCommand(devProcessSpec{
		Name:      os.Args[0],
		Arguments: []string{"-test.run=^TestDevProcessGroupForwardsSignalToDescendant$"},
		Environment: []string{
			devProcessHelperMode + "=parent",
			devProcessParentReadyPath + "=" + parentReady,
			devProcessChildReadyPath + "=" + childReady,
			devProcessChildPIDPath + "=" + childPIDPath,
		},
		Stdout: &output,
		Stderr: &output,
	})
	if command.SysProcAttr == nil || !command.SysProcAttr.Setpgid {
		t.Fatal("dev command did not create a Unix process group")
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	process := &execDevProcess{command: command}
	waitForDevProcessTestFile(t, parentReady)
	childPIDContents, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(childPIDContents)))
	if err != nil {
		t.Fatalf("parse child pid: %v", err)
	}

	if err := process.Signal(os.Interrupt); err != nil {
		_ = process.Kill()
		t.Fatalf("signal helper process group: %v", err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait for helper: %v\n%s", err, output.String())
	}
	waitForDevProcessExit(t, childPID)
}

func runDevProcessHelper(t *testing.T) {
	t.Helper()
	notifications := make(chan os.Signal, 1)
	signal.Notify(notifications, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(notifications)

	switch os.Getenv(devProcessHelperMode) {
	case "parent":
		child := exec.Command(
			os.Args[0],
			"-test.run=^TestDevProcessGroupForwardsSignalToDescendant$",
		)
		child.Env = append(os.Environ(), devProcessHelperMode+"=child")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			t.Fatalf("start child helper: %v", err)
		}
		if err := os.WriteFile(
			os.Getenv(devProcessChildPIDPath),
			[]byte(strconv.Itoa(child.Process.Pid)),
			0o644,
		); err != nil {
			_ = child.Process.Kill()
			t.Fatalf("write child pid: %v", err)
		}
		waitForDevProcessTestFile(t, os.Getenv(devProcessChildReadyPath))
		writeDevProcessTestFile(t, os.Getenv(devProcessParentReadyPath))
		<-notifications
		if err := child.Wait(); err != nil {
			t.Fatalf("wait for child helper: %v", err)
		}
	case "child":
		writeDevProcessTestFile(t, os.Getenv(devProcessChildReadyPath))
		<-notifications
	default:
		t.Fatalf("unknown helper mode %q", os.Getenv(devProcessHelperMode))
	}
}

func writeDevProcessTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("ready"), 0o644); err != nil {
		t.Fatalf("write ready file: %v", err)
	}
}

func waitForDevProcessTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat helper file: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func waitForDevProcessExit(t *testing.T, processID int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(processID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			t.Fatalf("inspect child process %d: %v", processID, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child process %d remained after group shutdown", processID)
}
