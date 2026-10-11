//go:build windows

package util

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// shellCommand runs a script with cmd.exe. The command line is written out in full: Go would
// quote the script for the C runtime's rules, which cmd.exe doesn't follow. /s /c "..." runs
// what is between the outer quotes as written; the script's values were quoted by QuoteCmd.
func shellCommand(ctx context.Context, script string) *exec.Cmd {
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	cmd := exec.CommandContext(ctx, comspec)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c "` + script + `"`}
	return cmd
}

// newProcessGroup starts the command in a process group of its own: a console's Ctrl-C reaches
// buildy, which kills the process trees it started.
func newProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// killProcessGroup kills p and every process it started, with taskkill /T (Windows has no process
// group kill; a job object would need the process created suspended).
func killProcessGroup(p *os.Process) error {
	if p == nil {
		return nil
	}
	exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run()
	// taskkill fails for a process that already exited; make sure the process itself is gone
	if err := p.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
