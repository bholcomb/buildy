package util

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ExecutionResult represents the result of command execution
type ExecutionResult struct {
	ReturnCode int
	Stdout     string
	Stderr     string
}

// ExecutionEnvironment represents different execution environments (native, docker, etc.)
type ExecutionEnvironment struct {
	Type   string
	Config map[string]any
}

// NewNativeExecution creates a native execution environment
func NewNativeExecution() *ExecutionEnvironment {
	return &ExecutionEnvironment{
		Type:   "native",
		Config: make(map[string]any),
	}
}

// NewDockerExecution creates a Docker execution environment
func NewDockerExecution(config map[string]any) *ExecutionEnvironment {
	return &ExecutionEnvironment{
		Type:   "docker",
		Config: config,
	}
}

// Execute executes a command in the environment
func (ee *ExecutionEnvironment) Execute(command, cwd string, timeout int) (*ExecutionResult, error) {
	switch ee.Type {
	case "native":
		return ee.executeNative(command, cwd, timeout)
	case "docker":
		return ee.executeDocker(command, cwd, timeout)
	default:
		LogWarning("Unknown execution type '%s', falling back to native", ee.Type)
		return ee.executeNative(command, cwd, timeout)
	}
}

// executeNative executes command directly on host
func (ee *ExecutionEnvironment) executeNative(command, cwd string, timeout int) (*ExecutionResult, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	// Execute command using platform-appropriate shell
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Use /S to strip quotes and parse command string literally
		cmd = exec.CommandContext(ctx, "cmd.exe", "/S", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = cwd

	// Capture both stdout and stderr using CombinedOutput
	// This ensures we see all compiler output (warnings/errors go to stdout for cl.exe)
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &ExecutionResult{
				ReturnCode: exitErr.ExitCode(),
				Stdout:     stdout,
				Stderr:     stderr,
			}, nil
		}
		// Other errors (timeout, etc.)
		return nil, err
	}

	return &ExecutionResult{
		ReturnCode: 0,
		Stdout:     stdout,
		Stderr:     stderr,
	}, nil
}

// executeDocker executes command inside Docker container
func (ee *ExecutionEnvironment) executeDocker(command, cwd string, timeout int) (*ExecutionResult, error) {
	// Get Docker configuration
	image := "gcc:13"
	if img, ok := ee.Config["image"].(string); ok {
		image = img
	}

	volumes := []string{}
	if vols, ok := ee.Config["volumes"].([]any); ok {
		for _, vol := range vols {
			if volStr, ok := vol.(string); ok {
				volumes = append(volumes, volStr)
			}
		}
	} else if vols, ok := ee.Config["volumes"].([]string); ok {
		volumes = vols
	}

	workingDir := "/workspace"
	if wd, ok := ee.Config["working_dir"].(string); ok {
		workingDir = wd
	}

	user := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	if u, ok := ee.Config["user"].(string); ok {
		user = u
	}

	// Expand environment variables in volume mounts
	expandedVolumes := []string{}
	for _, vol := range volumes {
		// Replace ${PWD} with current directory
		expanded := strings.ReplaceAll(vol, "${PWD}", cwd)
		// Replace ${UID} and ${GID}
		expanded = strings.ReplaceAll(expanded, "${UID}", fmt.Sprintf("%d", os.Getuid()))
		expanded = strings.ReplaceAll(expanded, "${GID}", fmt.Sprintf("%d", os.Getgid()))
		expandedVolumes = append(expandedVolumes, expanded)
	}

	// Expand user string
	user = strings.ReplaceAll(user, "${UID}", fmt.Sprintf("%d", os.Getuid()))
	user = strings.ReplaceAll(user, "${GID}", fmt.Sprintf("%d", os.Getgid()))

	// Build docker run command
	dockerArgs := []string{"run", "--rm"}
	for _, vol := range expandedVolumes {
		dockerArgs = append(dockerArgs, "-v", vol)
	}
	dockerArgs = append(dockerArgs, "-w", workingDir)
	dockerArgs = append(dockerArgs, "-u", user)
	dockerArgs = append(dockerArgs, image)
	dockerArgs = append(dockerArgs, "sh", "-c", command)

	LogDebug("Docker command: docker %s", strings.Join(dockerArgs, " "))

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	// Execute docker command
	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)

	// Capture output
	stdout, err := cmd.Output()
	stderr := ""
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
			return &ExecutionResult{
				ReturnCode: exitErr.ExitCode(),
				Stdout:     string(stdout),
				Stderr:     stderr,
			}, nil
		}
		// Other errors (timeout, etc.)
		return nil, err
	}

	return &ExecutionResult{
		ReturnCode: 0,
		Stdout:     string(stdout),
		Stderr:     "",
	}, nil
}
