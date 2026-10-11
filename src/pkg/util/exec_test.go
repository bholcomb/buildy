package util

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// S14: the workspace and cache are mounted at their host paths and the command runs in its
// directory, so absolute host paths in commands are valid in the container. The command's
// arguments stay arguments: no shell inside the container.
func TestDockerRunArgs(t *testing.T) {
	spec := ExecSpec{
		Type:    "docker",
		Image:   "gcc:13",
		Volumes: []string{"/opt/sdk:/opt/sdk:ro", "/home/u${UID}:/h"},
		Env:     map[string]string{"B": "2", "A": "1"},
	}
	command := Command{Args: []string{"g++", "-DMSG=\"a b\"", "-c", "/ws/my src/main.cpp", "-o", "/ws/build/main.o"}, Dir: "/ws"}
	args, err := DockerRunArgs(spec, command, []string{"/ws", "/cache"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	want := fmt.Sprintf("run --rm -v /ws:/ws -v /cache:/cache -v /opt/sdk:/opt/sdk:ro -v /home/u%d:/h -w /ws", os.Getuid())
	if !strings.HasPrefix(got, want) {
		t.Errorf("args\n  %s\nwant prefix\n  %s", got, want)
	}
	if os.Getuid() >= 0 && !strings.Contains(got, fmt.Sprintf(" -u %d:%d ", os.Getuid(), os.Getgid())) {
		t.Errorf("args %q don't run as the invoking user", got)
	}
	if !strings.Contains(got, "-e A=1 -e B=2 gcc:13 g++") {
		t.Errorf("args %q: wrong environment or image", got)
	}
	if tail := args[len(args)-len(command.Args):]; strings.Join(tail, "\x00") != strings.Join(command.Args, "\x00") {
		t.Errorf("the command's arguments %q are not the last arguments of %q", command.Args, args)
	}

	script := Command{Script: "gen > '/ws/out file'", Dir: "/ws/sub"}
	args, _ = DockerRunArgs(spec, script, []string{"/ws"})
	got = strings.Join(args, " ")
	if !strings.Contains(got, " -w /ws/sub ") || !strings.HasSuffix(got, "gcc:13 sh -c "+script.Script) || args[len(args)-1] != script.Script {
		t.Errorf("a script runs as %q", args)
	}

	spec.User = "1000:1000"
	args, _ = DockerRunArgs(spec, Command{Args: []string{"true"}, Dir: "/ws"}, []string{"/ws"})
	if !strings.Contains(strings.Join(args, " "), " -u 1000:1000 ") {
		t.Errorf("args %q ignore the spec's user", args)
	}
}

func TestDockerRunArgsRejects(t *testing.T) {
	if _, err := DockerRunArgs(ExecSpec{Type: "docker"}, Command{Args: []string{"true"}}, nil); err == nil {
		t.Error("no image was accepted")
	}
	// The old convention mounted ${PWD} at /workspace; the workspace is now mounted at its path.
	if _, err := DockerRunArgs(ExecSpec{Type: "docker", Image: "x", Volumes: []string{"${PWD}:/workspace"}}, Command{Args: []string{"true"}}, nil); err == nil {
		t.Error("${PWD} in a volume was accepted")
	}
	if err := (ExecSpec{Type: "podman"}).Validate(); err == nil {
		t.Error("an unknown execution type was accepted")
	}
}
