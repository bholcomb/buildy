package build

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"buildy/pkg/util"
)

// toolIdentities tells, once per run per program, what a command runs, for its task's cache key:
// a native program's resolved path and content hash, or a Docker image's ID. Upgrading the
// compiler, or putting another one earlier on the PATH, changes the key of every task it runs.
//
// The content hash goes through the cache's file hashes, so an unchanged compiler is not read
// again in a later run (its stat is). A driver such as gcc finds its own programs (cc1, as, ld)
// relative to itself: a compiler package upgrade replaces the driver too.
type toolIdentities struct {
	hash func(path string) (string, error)
	mu   sync.Mutex
	ids  map[string]*toolIdentity
}

type toolIdentity struct {
	once sync.Once
	id   string
	err  error
}

func newToolIdentities(hash func(path string) (string, error)) *toolIdentities {
	return &toolIdentities{hash: hash, ids: map[string]*toolIdentity{}}
}

// of returns the identity of what command runs where spec says. A relative program path is
// relative to the command's directory (dir, the workspace root if empty).
func (ti *toolIdentities) of(ctx context.Context, spec util.ExecSpec, command util.Command, workspaceRoot string) (string, error) {
	var key string
	var resolve func() (string, error)
	switch {
	case spec.IsDocker():
		key = "docker image " + spec.Image
		resolve = func() (string, error) { return dockerImageID(ctx, spec.Image) }
	default:
		program := shellProgram()
		if command.Script == "" {
			program = command.Args[0]
		}
		// A path with a directory part runs relative to the command's directory; a bare name is
		// looked up on the PATH, as os/exec does when it starts the command
		if !filepath.IsAbs(program) && strings.ContainsAny(program, `/\`) {
			dir := command.Dir
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(workspaceRoot, dir)
			}
			program = filepath.Join(dir, program)
		}
		key = "program " + program
		resolve = func() (string, error) { return ti.programID(program) }
	}

	ti.mu.Lock()
	id, ok := ti.ids[key]
	if !ok {
		id = &toolIdentity{}
		ti.ids[key] = id
	}
	ti.mu.Unlock()
	id.once.Do(func() { id.id, id.err = resolve() })
	return id.id, id.err
}

// programID is a native program's real path (symlinks resolved) and content hash.
func (ti *toolIdentities) programID(program string) (string, error) {
	path, err := exec.LookPath(program)
	if err != nil {
		return "", fmt.Errorf("can't find the program %s: %w", program, err)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("the program %s: %w", path, err)
	}
	if real, err = filepath.Abs(real); err != nil {
		return "", err
	}
	hash, err := ti.hash(real)
	if err != nil {
		return "", fmt.Errorf("the program %s: %w", real, err)
	}
	return real + " " + hash, nil
}

// shellProgram is the shell a script runs with on this host (util.Run).
func shellProgram() string {
	if runtime.GOOS == "windows" {
		return "cmd.exe"
	}
	return "sh"
}

// dockerImageID returns the ID of a Docker image, pulling the image first if it isn't present (as
// docker run would).
func dockerImageID(ctx context.Context, image string) (string, error) {
	inspect := func() (string, error) {
		out, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", image).Output()
		return strings.TrimSpace(string(out)), err
	}
	id, err := inspect()
	if err != nil {
		util.LogProgress("Pulling the Docker image %s", image)
		if out, pullErr := exec.CommandContext(ctx, "docker", "pull", "--quiet", image).CombinedOutput(); pullErr != nil {
			return "", fmt.Errorf("docker pull %s: %v\n%s", image, pullErr, out)
		}
		if id, err = inspect(); err != nil {
			return "", fmt.Errorf("docker image inspect %s: %w", image, err)
		}
	}
	if id == "" {
		return "", fmt.Errorf("docker image inspect %s gave no ID", image)
	}
	return "docker image " + image + " " + id, nil
}
