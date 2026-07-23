//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

var generateConsoleCtrlEvent = syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")

func configureDevCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func signalDevProcess(process *os.Process, _ os.Signal) error {
	result, _, callErr := generateConsoleCtrlEvent.Call(
		uintptr(syscall.CTRL_BREAK_EVENT),
		uintptr(process.Pid),
	)
	if result != 0 {
		return nil
	}
	fallbackErr := taskkillDevProcessTree(process.Pid, false)
	if fallbackErr == nil {
		return nil
	}
	if callErr == syscall.Errno(0) {
		return fallbackErr
	}
	return errors.Join(
		fmt.Errorf("GenerateConsoleCtrlEvent for process group %d: %w", process.Pid, callErr),
		fallbackErr,
	)
}

func killDevProcess(process *os.Process) error {
	err := taskkillDevProcessTree(process.Pid, true)
	if err == nil {
		return nil
	}
	directErr := process.Kill()
	if directErr == nil || errors.Is(directErr, os.ErrProcessDone) {
		return nil
	}
	return errors.Join(err, directErr)
}

func taskkillDevProcessTree(processID int, force bool) error {
	arguments := []string{"/PID", strconv.Itoa(processID), "/T"}
	if force {
		arguments = append(arguments, "/F")
	}
	output, err := exec.Command("taskkill", arguments...).CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if message == "" {
		message = "no taskkill output"
	}
	return fmt.Errorf("taskkill process tree %d: %w: %s", processID, err, message)
}
