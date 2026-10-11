package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"buildy/pkg/util"
)

// ExitInterrupted is buildy's exit code when it was interrupted (128 + SIGINT, as shells report).
const ExitInterrupted = 130

// HandleInterrupts returns a context the first interrupt (Ctrl-C, SIGINT or SIGTERM) cancels:
// everything that runs commands or fetches takes it, so the running commands' process groups are
// killed, their partial outputs removed, nothing new starts, and the cache keeps what finished.
// A second interrupt exits at once with ExitInterrupted.
func HandleInterrupts() context.Context {
	ctx, cancel := context.WithCancelCause(context.Background())
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-signals
		util.LogProgress("Received %v: stopping the build (interrupt again to exit at once)", sig)
		cancel(errInterrupted)
		<-signals
		util.LogProgress("Received a second interrupt: exiting")
		os.Exit(ExitInterrupted)
	}()
	return ctx
}

var errInterrupted = errors.New("interrupted")

// Interrupted reports whether ctx was cancelled by an interrupt.
func Interrupted(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), errInterrupted)
}

// CleanupPartialOutputs removes what a task that failed or was interrupted left of its outputs
func CleanupPartialOutputs(outputs []string) {
	for _, output := range outputs {
		if err := os.Remove(output); err != nil && !errors.Is(err, os.ErrNotExist) {
			util.LogWarning("%v", fmt.Errorf("failed to remove the partial output %s: %w", output, err))
		}
	}
}
