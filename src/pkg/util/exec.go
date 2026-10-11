package util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// ExecutionResult is how a command ended: its exit code, and what it wrote to its standard output
// and error, in the order it wrote it (cl.exe writes diagnostics to stdout, gcc to stderr).
type ExecutionResult struct {
	ReturnCode int
	Output     string
}

// ExecSpec says where a command runs: natively on the host, or in a Docker container. It comes
// from the toolchain that produced a task, and is part of the task's cache key.
type ExecSpec struct {
	Type    string            `yaml:"type" json:"type,omitempty"`       // "" or "native", or "docker"
	Image   string            `yaml:"image" json:"image,omitempty"`     // Docker image
	Volumes []string          `yaml:"volumes" json:"volumes,omitempty"` // Extra docker -v mounts (${UID} and ${GID} expand)
	User    string            `yaml:"user" json:"user,omitempty"`       // Docker -u; the invoking user if empty
	Env     map[string]string `yaml:"env" json:"env,omitempty"`         // Environment variables for the command
	// The environment variables the toolchain's tools read (toolchain.reads_env), whose values a
	// native task's cache key holds. A Docker container sees only Env.
	ReadsEnv []string `yaml:"-" json:"reads_env,omitempty"`
}

// EnvValues returns the value of each variable in ReadsEnv that the command will see: Env's, else
// the environment's. An unset variable is absent. Docker commands see only Env, already in the
// spec, so there are none.
func (s ExecSpec) EnvValues() map[string]string {
	if s.IsDocker() || len(s.ReadsEnv) == 0 {
		return nil
	}
	values := make(map[string]string, len(s.ReadsEnv))
	for _, name := range s.ReadsEnv {
		if v, ok := s.Env[name]; ok {
			values[name] = v
		} else if v, ok := os.LookupEnv(name); ok {
			values[name] = v
		}
	}
	return values
}

// IsDocker reports whether the spec runs in Docker.
func (s ExecSpec) IsDocker() bool {
	return s.Type == "docker"
}

// Validate checks the spec can be run.
func (s ExecSpec) Validate() error {
	switch s.Type {
	case "", "native":
		return nil
	case "docker":
		if s.Image == "" {
			return fmt.Errorf("docker execution needs an image")
		}
		return nil
	}
	return fmt.Errorf("unknown execution type %q (expected native or docker)", s.Type)
}

// Command is what a task runs: a program and its arguments, run directly without a shell, or,
// for a tool that asks for one (shell: true), a shell script.
type Command struct {
	Args         []string `json:"args,omitempty"`          // the program, then its arguments
	Script       string   `json:"script,omitempty"`        // instead of Args: run by sh -c, or cmd.exe /c on a Windows host
	Dir          string   `json:"dir,omitempty"`           // the working directory, relative to the workspace root (the root if empty)
	ResponseFile string   `json:"response_file,omitempty"` // how the program reads arguments from an @file, if it can
}

// Response file syntaxes: how a program reads the arguments in an @file.
const (
	ResponseFileGNU  = "gnu"  // GCC, Clang, GNU ar and ld: whitespace separates, backslash escapes
	ResponseFileMSVC = "msvc" // cl, link, lib: the Windows command line's quoting
)

// IsZero reports whether there is nothing to run.
func (c Command) IsZero() bool {
	return len(c.Args) == 0 && c.Script == ""
}

// String is the command as one line, its arguments quoted for the host's shell: for logs and
// compile_commands.json, never to run.
func (c Command) String() string {
	if c.Script != "" {
		return c.Script
	}
	return JoinArgs(c.Args, runtime.GOOS)
}

// ShellQuoter returns how a shell script for spec quotes a value: for sh, in Docker and on Linux
// and macOS hosts; for cmd.exe, on a Windows host.
func ShellQuoter(spec ExecSpec) func(string) (string, error) {
	if !spec.IsDocker() && runtime.GOOS == "windows" {
		return QuoteCmd
	}
	return func(s string) (string, error) { return QuotePOSIX(s), nil }
}

// waitDelay is how long a command's output pipes may stay open after it exits, or after it is
// killed: a process it started that keeps them open (outside its process group) can't hang
// buildy.
var waitDelay = 5 * time.Second

// dockerNames numbers the containers buildy runs, for their names.
var dockerNames atomic.Int64

// Run runs command natively or in Docker, in its directory (relative to defaultDir). A
// Docker container gets mounts (the workspace root, and the cache directory when it lies
// elsewhere) at their host paths, so the absolute host paths in commands are valid inside it.
//
// The command runs in a process group of its own. When ctx is done (an interrupt) or timeout
// passes (if not 0), the whole group is killed, not only the program buildy started, and a Docker
// container is killed with docker kill (it runs with --init and a name).
func Run(ctx context.Context, spec ExecSpec, command Command, defaultDir string, mounts []string, timeout time.Duration) (*ExecutionResult, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if command.IsZero() {
		return nil, fmt.Errorf("no command to run")
	}
	if !filepath.IsAbs(command.Dir) {
		command.Dir = filepath.Join(defaultDir, command.Dir)
	}

	runCtx, cancel := ctx, context.CancelFunc(func() {})
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	var cmd *exec.Cmd
	container := ""
	if spec.IsDocker() {
		container = fmt.Sprintf("buildy-%d-%d", os.Getpid(), dockerNames.Add(1))
		args, err := DockerRunArgs(spec, command, mounts, container)
		if err != nil {
			return nil, err
		}
		LogDebug("Docker command: %s", JoinArgs(append([]string{"docker"}, args...), runtime.GOOS))
		cmd = exec.CommandContext(runCtx, "docker", args...)
	} else {
		if command.Script != "" {
			cmd = shellCommand(runCtx, command.Script)
		} else {
			cmd = exec.CommandContext(runCtx, command.Args[0], command.Args[1:]...)
		}
		cmd.Dir = command.Dir
		if len(spec.Env) > 0 {
			cmd.Env = append(os.Environ(), EnvList(spec.Env)...)
		}
	}
	newProcessGroup(cmd)
	cmd.Cancel = func() error {
		if container != "" {
			// Killing the docker client leaves the container running
			killCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
			defer done()
			exec.CommandContext(killCtx, "docker", "kill", container).Run()
		}
		return killProcessGroup(cmd.Process)
	}
	cmd.WaitDelay = waitDelay

	// One buffer for both streams: os/exec then writes them from one goroutine, in order
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	result := &ExecutionResult{Output: output.String()}
	switch {
	case ctx.Err() != nil:
		return nil, fmt.Errorf("interrupted: %w", ctx.Err())
	case runCtx.Err() != nil:
		return nil, fmt.Errorf("timed out after %s; its processes were killed", timeout)
	case errors.Is(err, exec.ErrWaitDelay):
		return nil, fmt.Errorf("exited, but a process it started kept its output open for %s; buildy stopped waiting for it", waitDelay)
	case err != nil:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ReturnCode = exitErr.ExitCode()
			return result, nil
		}
		return nil, err
	}
	return result, nil
}

// DockerRunArgs returns the arguments of the `docker run` that runs command for spec, in a
// container with the name given: removed when it exits, with an init process that forwards
// signals and reaps zombies; the mounts at their host paths, then the spec's extra volumes, the
// working directory, the user (the invoking user unless the spec names one), the environment,
// the image, and the command's arguments as arguments of their own (a script runs as
// `sh -c script`).
func DockerRunArgs(spec ExecSpec, command Command, mounts []string, name string) ([]string, error) {
	if spec.Image == "" {
		return nil, fmt.Errorf("docker execution needs an image")
	}
	args := []string{"run", "--rm", "--init", "--name", name}
	for _, m := range mounts {
		args = append(args, "-v", m+":"+m)
	}
	for _, vol := range spec.Volumes {
		expanded := expandIDs(vol)
		if strings.Contains(expanded, "${") {
			return nil, fmt.Errorf("docker volume %q: only ${UID} and ${GID} expand (the workspace is already mounted at its host path)", vol)
		}
		args = append(args, "-v", expanded)
	}
	args = append(args, "-w", command.Dir)
	user := expandIDs(spec.User)
	if user == "" && os.Getuid() >= 0 {
		user = fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	}
	if user != "" {
		args = append(args, "-u", user)
	}
	for _, e := range EnvList(spec.Env) {
		args = append(args, "-e", e)
	}
	args = append(args, spec.Image)
	if command.Script != "" {
		return append(args, "sh", "-c", command.Script), nil
	}
	return append(args, command.Args...), nil
}
func expandIDs(s string) string {
	s = strings.ReplaceAll(s, "${UID}", fmt.Sprintf("%d", os.Getuid()))
	return strings.ReplaceAll(s, "${GID}", fmt.Sprintf("%d", os.Getgid()))
}

// EnvList returns KEY=VALUE entries in a stable order.
func EnvList(env map[string]string) []string {
	list := make([]string, 0, len(env))
	for k, v := range env {
		list = append(list, k+"="+v)
	}
	sort.Strings(list)
	return list
}
