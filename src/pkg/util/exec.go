package util

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
	Type    string            `json:"type,omitempty"`    // "" or "native", or "docker"
	Image   string            `json:"image,omitempty"`   // Docker image
	Volumes []string          `json:"volumes,omitempty"` // Extra docker -v mounts (${UID} and ${GID} expand)
	User    string            `json:"user,omitempty"`    // Docker -u; the invoking user if empty
	Env     map[string]string `json:"env,omitempty"`     // Environment variables for the command
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

// Run runs command through the platform shell, natively or in Docker, with workDir as its
// working directory. A Docker container gets mounts (the workspace root, and the cache directory
// when it lies elsewhere) at their host paths, so the absolute host paths in commands are valid
// inside it.
func Run(spec ExecSpec, command, workDir string, mounts []string, timeoutSeconds int) (*ExecutionResult, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if spec.IsDocker() {
		args, err := DockerRunArgs(spec, command, workDir, mounts)
		if err != nil {
			return nil, err
		}
		LogDebug("Docker command: docker %s", strings.Join(args, " "))
		cmd = exec.CommandContext(ctx, "docker", args...)
	} else {
		if runtime.GOOS == "windows" {
			// Use /S to strip quotes and parse command string literally
			cmd = exec.CommandContext(ctx, "cmd.exe", "/S", "/C", command)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", command)
		}
		cmd.Dir = workDir
		if len(spec.Env) > 0 {
			cmd.Env = append(os.Environ(), envList(spec.Env)...)
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
// invoking user unless the spec names one), the environment, the image, and `sh -c command`.
func DockerRunArgs(spec ExecSpec, command, workDir string, mounts []string) ([]string, error) {
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
	args = append(args, "-w", workDir)
	user := expandIDs(spec.User)
	if user == "" && os.Getuid() >= 0 {
		user = fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	}
	if user != "" {
		args = append(args, "-u", user)
	}
	for _, e := range envList(spec.Env) {
		args = append(args, "-e", e)
	}
	return append(args, spec.Image, "sh", "-c", command), nil
}

func expandIDs(s string) string {
	s = strings.ReplaceAll(s, "${UID}", fmt.Sprintf("%d", os.Getuid()))
	return strings.ReplaceAll(s, "${GID}", fmt.Sprintf("%d", os.Getgid()))
}

// envList returns KEY=VALUE entries in a stable order.
func envList(env map[string]string) []string {
	list := make([]string, 0, len(env))
	for k, v := range env {
		list = append(list, k+"="+v)
	}
	sort.Strings(list)
	return list
}
