package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	"buildy/internal/resource"
)

const goAndRustWorkspace = `
project: {name: tools}
targets:
  executables:
    - {name: app, language: go, path: cmd/app}
    - {name: hello, language: rust, path: hello}
    - {name: tool, language: rust, path: multi, bin: other}
  static_libraries:
    - {name: calc, language: rust, path: calc}
  shared_libraries:
    - {name: plugin, language: rust, path: plugin}
`

func outputOf(t *testing.T, tasks []*BuildTask, target string) *BuildTask {
	t.Helper()
	for _, task := range tasks {
		if task.Target == target {
			return task
		}
	}
	t.Fatalf("no task for target %s", target)
	return nil
}

// Findings 4, 5, S6, S7: Go and Rust tasks are tool-cached with no guessed inputs, and Rust
// outputs are where Cargo writes them.
func TestGoAndRustTasksAreToolCached(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"buildy.yaml": goAndRustWorkspace})

	for _, tc := range []struct {
		platform, config string
		want             map[string]string // target -> output relative to the output dir
	}{
		{"linux", "debug", map[string]string{
			"app":    "bin/app",
			"hello":  "cargo/debug/hello",
			"tool":   "cargo/debug/other",
			"calc":   "cargo/debug/libcalc.a",
			"plugin": "cargo/debug/libplugin.so",
		}},
		{"linux", "release", map[string]string{
			"hello": "cargo/release/hello",
			"calc":  "cargo/release/libcalc.a",
		}},
		{"windows", "debug", map[string]string{
			"app":    "bin/app.exe",
			"hello":  "cargo/x86_64-pc-windows-gnu/debug/hello.exe",
			"calc":   "cargo/x86_64-pc-windows-gnu/debug/libcalc.a",
			"plugin": "cargo/x86_64-pc-windows-gnu/debug/plugin.dll",
		}},
	} {
		tasks, err := generateTasksFor(t, root, tc.platform, "x86_64", tc.config)
		if err != nil {
			t.Fatalf("%s-%s: %v", tc.platform, tc.config, err)
		}
		outDir := filepath.Join(root, "build", tc.platform+"-x86_64-"+tc.config)
		for target, want := range tc.want {
			task := outputOf(t, tasks, target)
			if !task.ToolCached {
				t.Errorf("%s-%s %s: not tool-cached", tc.platform, tc.config, target)
			}
			if len(task.Inputs) != 0 {
				t.Errorf("%s-%s %s: declares inputs %v", tc.platform, tc.config, target, task.Inputs)
			}
			if task.TaskType == "cargo_build" && !strings.Contains(strings.Join(task.Command.Args, "\x00"), "--target-dir\x00"+filepath.Join(outDir, "cargo")) {
				t.Errorf("%s-%s %s: command %q doesn't put Cargo's target dir at %s", tc.platform, tc.config, target, task.Command, filepath.Join(outDir, "cargo"))
			}
			if got := task.Outputs[0]; got != filepath.Join(outDir, filepath.FromSlash(want)) {
				t.Errorf("%s-%s %s: output %s, want %s", tc.platform, tc.config, target, got, filepath.Join(outDir, want))
			}
		}
	}
}

// Finding 9: with a Docker toolchain, the tasks carry its Docker execution (they used to run
// on the host).
func TestDockerToolchainTasksRunInDocker(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: d}\n" + cppEnvironment + "targets:\n  executables:\n    - {name: app, language: cpp, sources: [main.cpp]}\n",
		"main.cpp":    "int main() {}",
	})
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.DiscoverModules(BuildContext{Platform: "linux", Architecture: "x86_64", Configuration: "debug"}); err != nil {
		t.Fatal(err)
	}
	toolchains, err := resource.NewToolchainManagerMulti(nil)
	if err != nil {
		t.Fatal(err)
	}
	templates, err := NewBuildTemplateEngineMulti(nil)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := NewConfigParser(BuildContext{Platform: "linux", Architecture: "x86_64", Configuration: "debug"}, nil, toolchains, "docker-cpp-linux", templates, ws, nil).GenerateWorkspaceTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) == 0 {
		t.Fatal("no tasks")
	}
	for _, task := range tasks {
		if !task.Exec.IsDocker() || task.Exec.Image != "gcc:13" {
			t.Errorf("%s runs %+v, want docker gcc:13", task.TaskID, task.Exec)
		}
		if task.ToolchainHash != resource.HashToolchainConfig(toolchains.GetToolchain("docker-cpp-linux")) {
			t.Errorf("%s: toolchain hash %q", task.TaskID, task.ToolchainHash)
		}
	}
}
