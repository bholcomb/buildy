package util

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ExecutionResult represents the result of command execution
type ExecutionResult struct {
	ReturnCode int
	Stdout     string
	Stderr     string
}

// ExecSpec says where a command runs: natively on the host, or in a Docker container. It comes
// from the toolchain that produced a task, and is part of the task's cache key.
type ExecSpec struct {
	Type    string            `yaml:"type" json:"type,omitempty"`       // "" or "native", or "docker"
	Image   string            `yaml:"image" json:"image,omitempty"`     // Docker image
	Volumes []string          `yaml:"volumes" json:"volumes,omitempty"` // Extra docker -v mounts (${UID} and ${GID} expand)
	User    string            `yaml:"user" json:"user,omitempty"`       // Docker -u; the invoking user if empty
	Env     map[string]string `yaml:"env" json:"env,omitempty"`         // Environment variables for the command
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

// Run runs command natively or in Docker, in its directory (relative to defaultDir). A
// Docker container gets mounts (the workspace root, and the cache directory when it lies
// elsewhere) at their host paths, so the absolute host paths in commands are valid inside it.
func Run(spec ExecSpec, command Command, defaultDir string, mounts []string, timeoutSeconds int) (*ExecutionResult, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if command.IsZero() {
		return nil, fmt.Errorf("no command to run")
	}
	if !filepath.IsAbs(command.Dir) {
		command.Dir = filepath.Join(defaultDir, command.Dir)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if spec.IsDocker() {
		args, err := DockerRunArgs(spec, command, mounts)
		if err != nil {
			return nil, err
		}
		LogDebug("Docker command: %s", JoinArgs(append([]string{"docker"}, args...), runtime.GOOS))
		cmd = exec.CommandContext(ctx, "docker", args...)
	} else {
		if command.Script != "" {
			cmd = shellCommand(ctx, command.Script)
		} else {
			cmd = exec.CommandContext(ctx, command.Args[0], command.Args[1:]...)
		}
		cmd.Dir = command.Dir
		if len(spec.Env) > 0 {
			cmd.Env = append(os.Environ(), EnvList(spec.Env)...)
		}
	}

	// Capture stdout and stderr separately (cl.exe writes diagnostics to stdout)
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	result := &ExecutionResult{Stdout: stdoutBuf.String(), Stderr: stderrBuf.String()}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && ctx.Err() == nil {
			result.ReturnCode = exitErr.ExitCode()
			return result, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("timed out after %d seconds", timeoutSeconds)
		}
		return nil, err
	}
	return result, nil
}

// DockerRunArgs returns the arguments of the `docker run` that runs command for spec: the mounts
// at their host paths, then the spec's extra volumes, the working directory, the user (the
// invoking user unless the spec names one), the environment, the image, and the command's
// arguments as arguments of their own (a script runs as `sh -c script`).
func DockerRunArgs(spec ExecSpec, command Command, mounts []string) ([]string, error) {
	if spec.Image == "" {
		return nil, fmt.Errorf("docker execution needs an image")
	}
	args := []string{"run", "--rm"}
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
