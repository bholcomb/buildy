package build

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"buildy/internal/cache"
	"buildy/internal/workspace"
	"buildy/pkg/util"
)

// TestHelperProcess is not a test: tasks run the test binary as their command (the Go
// helper-process pattern), so the commands behave the same on Linux, macOS and Windows.
//
//	write <file> <text>   write text to file
//	append <file> <text>  append text to file
//	nothing               exit 0 without writing anything
func TestHelperProcess(t *testing.T) {
	if os.Getenv("BUILDY_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	switch args[0] {
	case "write":
		os.WriteFile(args[1], []byte(args[2]), 0o644)
	case "append":
		f, err := os.OpenFile(args[1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			os.Exit(2)
		}
		f.WriteString(args[2])
		f.Close()
	case "nothing":
	}
	os.Exit(0)
}

// helperCommand returns a shell command running TestHelperProcess with args.
func helperCommand(t *testing.T, args ...string) string {
	t.Setenv("BUILDY_HELPER_PROCESS", "1")
	quote := func(s string) string {
		if runtime.GOOS == "windows" {
			return `"` + s + `"`
		}
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	parts := []string{quote(os.Args[0]), "-test.run=^TestHelperProcess$", "--"}
	for _, a := range args {
		parts = append(parts, quote(a))
	}
	return strings.Join(parts, " ")
}

func newExecutor(t *testing.T, dir string, force bool) *TaskExecutor {
	t.Helper()
	bc, err := cache.NewBuildCache(filepath.Join(dir, ".buildy_cache"))
	if err != nil {
		t.Fatal(err)
	}
	return NewTaskExecutor(bc, 2, 8192, dir, force)
}

func runTasks(te *TaskExecutor, tasks ...*workspace.BuildTask) bool {
	graph := workspace.NewTaskGraph()
	for _, task := range tasks {
		graph.AddTask(task)
	}
	return te.ExecuteTaskGraph(graph, false)
}

func newTask(id string, inputs []string, outputs []string, command string) *workspace.BuildTask {
	var in []workspace.TaskInput
	for _, i := range inputs {
		in = append(in, workspace.NewTaskInput(i))
	}
	task := workspace.NewBuildTask(id, "compile", in, outputs, nil, command)
	return &task
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Finding 5: a task that exits 0 without its declared output fails, and writes no cache entry.
func TestExecutorFailsWhenADeclaredOutputIsMissing(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out", "hello")
	task := newTask("cargo_build_hello", nil, []string{out}, helperCommand(t, "nothing"))

	te := newExecutor(t, dir, false)
	if runTasks(te, task) {
		t.Fatal("the task succeeded without producing its output")
	}
	if te.ExecutionStats.FailedTasks != 1 {
		t.Errorf("failed tasks = %d, want 1", te.ExecutionStats.FailedTasks)
	}
	if stats := te.Cache.GetCacheStats(); stats["total_entries"] != 0 {
		t.Errorf("cache entries = %v, want 0", stats["total_entries"])
	}
}

// S9: a declared input that doesn't exist fails the task before it runs, naming its producer.
func TestExecutorFailsOnAMissingInputBeforeRunning(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	missing := filepath.Join(dir, "gen", "config.h")
	task := newTask("compile_main", []string{missing}, []string{filepath.Join(dir, "main.o")},
		helperCommand(t, "write", marker, "ran"))

	te := newExecutor(t, dir, false)
	if runTasks(te, task) {
		t.Fatal("the task ran with a missing input")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran")
	}

	// With a producer in the graph, the error names it.
	producer := newTask("generate_config", nil, []string{missing}, helperCommand(t, "nothing"))
	te = newExecutor(t, dir, false)
	te.producers[missing] = producer.TaskID
	err := te.checkInputs(task)
	if err == nil || !strings.Contains(err.Error(), "generate_config") {
		t.Errorf("error %v doesn't name the producer", err)
	}
}

// S3: the declared outputs of a buildy-cached task are deleted before it runs; a tool-cached
// task keeps them.
func TestExecutorDeletesOutputsOfCachedTasks(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "libcalc.a")
	if err := os.WriteFile(out, []byte("stale+"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := newTask("link_calc", nil, []string{out}, helperCommand(t, "append", out, "new"))
	if !runTasks(newExecutor(t, dir, false), task) {
		t.Fatal("task failed")
	}
	if got := read(t, out); got != "new" {
		t.Errorf("output = %q, want %q: the stale output was not removed", got, "new")
	}

	toolOut := filepath.Join(dir, "app")
	if err := os.WriteFile(toolOut, []byte("previous+"), 0o644); err != nil {
		t.Fatal(err)
	}
	toolTask := newTask("build_app", nil, []string{toolOut}, helperCommand(t, "append", toolOut, "new"))
	toolTask.ToolCached = true
	if !runTasks(newExecutor(t, dir, false), toolTask) {
		t.Fatal("tool-cached task failed")
	}
	if got := read(t, toolOut); got != "previous+new" {
		t.Errorf("tool-cached output = %q, want %q", got, "previous+new")
	}
}

// A second run is a cache hit; deleting the output makes it run again. A tool-cached task always
// runs.
func TestExecutorCacheHitsAndMisses(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.cpp")
	if err := os.WriteFile(src, []byte("int main() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "main.o")
	log := filepath.Join(dir, "runs.log")
	mk := func() *workspace.BuildTask {
		return newTask("compile_main", []string{src}, []string{out},
			helperCommand(t, "write", out, "object")+" && "+helperCommand(t, "append", log, "x"))
	}

	for i, want := range []string{"x", "x", "xx"} {
		if i == 2 {
			os.Remove(out)
		}
		if !runTasks(newExecutor(t, dir, false), mk()) {
			t.Fatalf("run %d failed", i+1)
		}
		if got := read(t, log); got != want {
			t.Errorf("after run %d the command ran %d times, want %d", i+1, len(got), len(want))
		}
	}

	// --force runs it again.
	if !runTasks(newExecutor(t, dir, true), mk()) {
		t.Fatal("forced run failed")
	}
	if got := read(t, log); got != "xxx" {
		t.Errorf("forced run didn't run the command: %d runs", len(got))
	}

	toolTask := mk()
	toolTask.ToolCached = true
	te := newExecutor(t, dir, false)
	if !runTasks(te, toolTask) {
		t.Fatal("tool-cached run failed")
	}
	if got := read(t, log); got != "xxxx" {
		t.Errorf("tool-cached task was a cache hit: %d runs", len(got))
	}
	if te.ExecutionStats.CacheHits != 0 {
		t.Errorf("tool-cached task counted as a cache hit")
	}
}

// A failing command fails the task with its exit code.
func TestExecutorReportsCommandFailure(t *testing.T) {
	dir := t.TempDir()
	cmd := "exit 3"
	if runtime.GOOS == "windows" {
		cmd = "exit /b 3"
	}
	task := newTask("compile_bad", nil, []string{filepath.Join(dir, "x.o")}, cmd)
	if runTasks(newExecutor(t, dir, false), task) {
		t.Fatal("a failing command succeeded")
	}
}

// Finding 9: a task's Exec decides where it runs; a Docker task goes to Docker with the
// workspace (and the cache, when elsewhere) mounted, a native one runs natively.
func TestExecutorRunsEachTaskWhereItsExecSays(t *testing.T) {
	dir := t.TempDir()
	te := newExecutor(t, dir, false)
	var mu sync.Mutex // the stage's tasks run in parallel
	var specs []util.ExecSpec
	var mounts [][]string
	te.runCommand = func(spec util.ExecSpec, command, workDir string, m []string, timeout int) (*util.ExecutionResult, error) {
		mu.Lock()
		specs = append(specs, spec)
		mounts = append(mounts, m)
		mu.Unlock()
		if workDir != dir {
			t.Errorf("command ran in %s, want the workspace root %s", workDir, dir)
		}
		return util.Run(util.ExecSpec{}, command, workDir, nil, timeout)
	}

	dockerTask := newTask("compile_in_docker", nil, []string{filepath.Join(dir, "a.o")}, helperCommand(t, "write", filepath.Join(dir, "a.o"), "a"))
	dockerTask.Exec = util.ExecSpec{Type: "docker", Image: "gcc:13"}
	nativeTask := newTask("compile_native", nil, []string{filepath.Join(dir, "b.o")}, helperCommand(t, "write", filepath.Join(dir, "b.o"), "b"))
	if !runTasks(te, dockerTask, nativeTask) {
		t.Fatal("tasks failed")
	}
	var sawDocker, sawNative bool
	for i, spec := range specs {
		if spec.IsDocker() {
			sawDocker = true
			if spec.Image != "gcc:13" || len(mounts[i]) == 0 || mounts[i][0] != dir {
				t.Errorf("docker task got spec %+v, mounts %v", spec, mounts[i])
			}
		} else {
			sawNative = true
		}
	}
	if !sawDocker || !sawNative {
		t.Errorf("specs %+v: want one docker and one native run", specs)
	}
}
