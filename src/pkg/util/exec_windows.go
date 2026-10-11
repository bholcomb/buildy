//go:build windows

package util

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
