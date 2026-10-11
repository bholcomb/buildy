//go:build !windows

package util

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// gone waits up to 3 s for the process in pidFile to be gone.
func gone(t *testing.T, pidFile string) bool {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the script didn't write its child's pid: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	for range 30 {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	return false
}

// Finding 3.2: on timeout the command's whole process group is killed, and buildy doesn't wait
// for a grandchild holding its output open.
func TestRunKillsTheProcessGroupOnTimeout(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	start := time.Now()
	_, err := Run(context.Background(), ExecSpec{}, Command{Script: "sleep 30 & echo $! > pid; sleep 30"}, dir, nil, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("got %v, want a timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Run took %s to give up", elapsed)
	}
	if !gone(t, pidFile) {
		t.Error("the grandchild survived the timeout")
	}
}

// An interrupt (ctx done) kills the group too.
func TestRunKillsTheProcessGroupOnInterrupt(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	_, err := Run(ctx, ExecSpec{}, Command{Script: "sleep 30 & echo $! > pid; wait"}, dir, nil, 0)
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("got %v, want an interruption", err)
	}
	if !gone(t, pidFile) {
		t.Error("the grandchild survived the interrupt")
	}
}

// A process that leaves the group (setsid) and keeps the output open can't hang buildy.
func TestRunDoesNotWaitForAnEscapedProcess(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("skipping: no setsid")
	}
	old := waitDelay
	waitDelay = 300 * time.Millisecond
	defer func() { waitDelay = old }()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	start := time.Now()
	_, err := Run(context.Background(), ExecSpec{}, Command{Script: "setsid sh -c 'echo $$ > pid; exec sleep 30' & sleep 0.2"}, dir, nil, 0)
	if err == nil || !strings.Contains(err.Error(), "kept its output open") {
		t.Errorf("got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Run took %s", elapsed)
	}
	gone(t, pidFile) // and kill it
}

// Finding 3.2: a Docker container is killed on timeout, not only the docker client.
func TestRunKillsTheContainerOnTimeout(t *testing.T) {
	if os.Getenv("BUILDY_TEST_DOCKER") != "1" {
		t.Skip("skipping: set BUILDY_TEST_DOCKER=1 to run the Docker case")
	}
	dir := t.TempDir()
	_, err := Run(context.Background(), ExecSpec{Type: "docker", Image: "busybox"}, Command{Args: []string{"sleep", "60"}, Dir: dir}, dir, []string{dir}, 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("got %v, want a timeout", err)
	}
	out, err := exec.Command("docker", "ps", "-q", "--filter", "name=buildy-"+strconv.Itoa(os.Getpid())+"-").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("containers still running: %s", out)
		exec.Command("docker", "kill", strings.TrimSpace(string(out))).Run()
	}
}
