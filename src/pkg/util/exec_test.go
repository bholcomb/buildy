package util

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// S14: the workspace and cache are mounted at their host paths and the command runs in the
// workspace root, so absolute host paths in commands are valid in the container.
func TestDockerRunArgs(t *testing.T) {
	spec := ExecSpec{
		Type:    "docker",
		Image:   "gcc:13",
		Volumes: []string{"/opt/sdk:/opt/sdk:ro", "/home/u${UID}:/h"},
		Env:     map[string]string{"B": "2", "A": "1"},
	}
	args, err := DockerRunArgs(spec, "g++ -c /ws/main.cpp -o /ws/build/main.o", "/ws", []string{"/ws", "/cache"})
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
	if !strings.HasSuffix(got, "-e A=1 -e B=2 gcc:13 sh -c g++ -c /ws/main.cpp -o /ws/build/main.o") {
		t.Errorf("args %q: wrong environment, image or command", got)
	}
	if args[len(args)-1] != "g++ -c /ws/main.cpp -o /ws/build/main.o" {
		t.Errorf("the command is not a single argument: %q", args[len(args)-1])
	}

	spec.User = "1000:1000"
	args, _ = DockerRunArgs(spec, "true", "/ws", []string{"/ws"})
	if !strings.Contains(strings.Join(args, " "), " -u 1000:1000 ") {
		t.Errorf("args %q ignore the spec's user", args)
	}
}

func TestDockerRunArgsRejects(t *testing.T) {
	if _, err := DockerRunArgs(ExecSpec{Type: "docker"}, "true", "/ws", nil); err == nil {
		t.Error("no image was accepted")
	}
	// The old convention mounted ${PWD} at /workspace; the workspace is now mounted at its path.
	if _, err := DockerRunArgs(ExecSpec{Type: "docker", Image: "x", Volumes: []string{"${PWD}:/workspace"}}, "true", "/ws", nil); err == nil {
		t.Error("${PWD} in a volume was accepted")
	}
	if err := (ExecSpec{Type: "podman"}).Validate(); err == nil {
		t.Error("an unknown execution type was accepted")
	}
}
