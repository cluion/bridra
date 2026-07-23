//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureDevCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalDevProcess(process *os.Process, signal os.Signal) error {
	value, ok := signal.(syscall.Signal)
	if !ok {
		return process.Signal(signal)
	}
	return devProcessGroupError(syscall.Kill(-process.Pid, value))
}

func killDevProcess(process *os.Process) error {
	return devProcessGroupError(syscall.Kill(-process.Pid, syscall.SIGKILL))
}

func devProcessGroupError(err error) error {
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
