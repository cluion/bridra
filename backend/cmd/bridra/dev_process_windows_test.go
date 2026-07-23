//go:build windows

package main

import (
	"bytes"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const (
	devWindowsProcessHelperMode = "BRIDRA_DEV_WINDOWS_PROCESS_HELPER"
	devWindowsProcessReadyPath  = "BRIDRA_DEV_WINDOWS_PROCESS_READY_PATH"
)

func TestDevCommandCreatesWindowsProcessGroup(t *testing.T) {
	command := newDevExecCommand(devProcessSpec{Name: "cmd.exe", Arguments: []string{"/c", "exit"}})
	if command.SysProcAttr == nil ||
		command.SysProcAttr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatal("dev command did not create a Windows process group")
	}
}

func TestDevProcessGroupForwardsCtrlBreak(t *testing.T) {
	if os.Getenv(devWindowsProcessHelperMode) != "" {
		notifications := make(chan os.Signal, 1)
		signal.Notify(notifications, os.Interrupt)
		defer signal.Stop(notifications)
		if err := os.WriteFile(os.Getenv(devWindowsProcessReadyPath), []byte("ready"), 0o644); err != nil {
			t.Fatalf("write ready file: %v", err)
		}
		select {
		case received := <-notifications:
			if received != os.Interrupt {
				t.Fatalf("received %v, want os.Interrupt", received)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for Ctrl+Break")
		}
		return
	}

	directory := t.TempDir()
	readyPath := filepath.Join(directory, "ready")
	var output bytes.Buffer
	command := newDevExecCommand(devProcessSpec{
		Name:      os.Args[0],
		Arguments: []string{"-test.run=^TestDevProcessGroupForwardsCtrlBreak$"},
		Environment: []string{
			devWindowsProcessHelperMode + "=1",
			devWindowsProcessReadyPath + "=" + readyPath,
		},
		Stdout: &output,
		Stderr: &output,
	})
	if err := command.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	process := &execDevProcess{command: command}
	waitForWindowsDevProcessTestFile(t, readyPath)
	if err := process.Signal(os.Interrupt); err != nil {
		_ = process.Kill()
		t.Fatalf("signal helper process group: %v", err)
	}

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- command.Wait()
	}()
	select {
	case err := <-waitResult:
		if err != nil {
			t.Fatalf("wait for helper: %v\n%s", err, output.String())
		}
	case <-time.After(5 * time.Second):
		_ = process.Kill()
		t.Fatalf("timed out waiting for helper\n%s", output.String())
	}
}

func waitForWindowsDevProcessTestFile(t *testing.T, path string) {
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
