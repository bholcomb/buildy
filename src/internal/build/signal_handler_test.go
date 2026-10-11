//go:build !windows

package build

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSignalHelperProcess is not a test: TestSecondInterruptExits runs the test binary as a
// process that handles interrupts, reports when the first one cancelled its context, and then
// hangs, as a stuck cleanup would.
func TestSignalHelperProcess(t *testing.T) {
	if os.Getenv("BUILDY_SIGNAL_HELPER") != "1" {
		return
	}
	ctx := HandleInterrupts()
	fmt.Println("ready")
	<-ctx.Done()
	fmt.Println("cancelled", Interrupted(ctx))
	select {}
}

// Finding 3.1: the first interrupt cancels the run's context; a second one exits at once with 130,
// even while the first one's handling hangs.
func TestSecondInterruptExits(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalHelperProcess$")
	cmd.Env = append(os.Environ(), "BUILDY_SIGNAL_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	lines := make(chan string)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	expect := func(want string) {
		t.Helper()
		select {
		case line := <-lines:
			for !strings.HasPrefix(line, want) && line != "" {
				line = <-lines
			}
			if !strings.HasPrefix(line, want) {
				t.Fatalf("no %q from the helper", want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no %q from the helper within 10 s", want)
		}
	}
	expect("ready")
	cmd.Process.Signal(syscall.SIGINT)
	expect("cancelled true")
	cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		if code := cmd.ProcessState.ExitCode(); code != ExitInterrupted {
			t.Errorf("exit code %d, want %d", code, ExitInterrupted)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second interrupt didn't exit")
	}
}
