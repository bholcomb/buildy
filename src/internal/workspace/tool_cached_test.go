package workspace

import (
	"path/filepath"
	"strings"
	"testing"
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
			if task.TaskType == "cargo_build" && !strings.Contains(task.Command, "--target-dir "+filepath.Join(outDir, "cargo")) {
				t.Errorf("%s-%s %s: command %q doesn't put Cargo's target dir at %s", tc.platform, tc.config, target, task.Command, filepath.Join(outDir, "cargo"))
			}
			if got := task.Outputs[0]; got != filepath.Join(outDir, filepath.FromSlash(want)) {
				t.Errorf("%s-%s %s: output %s, want %s", tc.platform, tc.config, target, got, filepath.Join(outDir, want))
			}
		}
	}
}
