//go:build !windows

package util

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// shellCommand runs a script with sh.
func shellCommand(ctx context.Context, script string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", script)
}

// newProcessGroup makes the command the leader of a process group of its own, so that what it
// starts can be killed with it. A terminal's Ctrl-C then reaches buildy only, which kills the
// groups it started.
func newProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup kills the process group whose leader is p.
func killProcessGroup(p *os.Process) error {
	if p == nil {
		return nil
	}
	if err := syscall.Kill(-p.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
