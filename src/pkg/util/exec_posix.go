//go:build !windows

package util

import (
	"context"
	"os/exec"
)

// shellCommand runs a script with sh.
func shellCommand(ctx context.Context, script string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", script)
}
