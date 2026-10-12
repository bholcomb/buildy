package build

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"buildy/internal/cache"
	"buildy/internal/workspace"
	"buildy/pkg/util"
)

// TestHelperProcess is not a test: tasks run the test binary as their command (the Go
// helper-process pattern), so the commands behave the same on Linux, macOS and Windows. Its
// arguments are steps separated by "&&":
//
//	write <file> <text>   write text to file
//	append <file> <text>  append text to file
//	exit <code>           exit with code
//	nothing               do nothing
//	sleep <ms>            sleep
func TestHelperProcess(t *testing.T) {
	if os.Getenv("BUILDY_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	for len(args) > 0 {
		step, rest := args, []string(nil)
		for i, a := range args {
			if a == "&&" {
				step, rest = args[:i], args[i+1:]
				break
			}
		}
		args = rest
		switch step[0] {
		case "write":
			os.WriteFile(step[1], []byte(step[2]), 0o644)
		case "append":
			f, err := os.OpenFile(step[1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				os.Exit(2)
			}
			f.WriteString(step[2])
			f.Close()
		case "exit":
			code, _ := strconv.Atoi(step[1])
			os.Exit(code)
		case "nothing":
		case "sleep":
			ms, _ := strconv.Atoi(step[1])
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
	}
	os.Exit(0)
}

// helperCommand returns a command running TestHelperProcess with args.
func helperCommand(t *testing.T, args ...string) util.Command {
	t.Setenv("BUILDY_HELPER_PROCESS", "1")
	return util.Command{Args: append([]string{os.Args[0], "-test.run=^TestHelperProcess$", "--"}, args...)}
}

func newExecutor(t *testing.T, dir string, force bool) *TaskExecutor {
	t.Helper()
	bc, err := cache.NewBuildCache(filepath.Join(dir, ".buildy_cache"))
	if err != nil {
		t.Fatal(err)
	}
	return NewTaskExecutor(bc, 2, dir, force)
}

// runTasks runs the tasks as a build does, which ends by closing the cache (Builder.Build): an
// open journal can't be removed on Windows, nor the temporary directory that holds it.
func runTasks(t *testing.T, te *TaskExecutor, tasks ...*workspace.BuildTask) bool {
	t.Helper()
	graph := workspace.NewTaskGraph()
	for _, task := range tasks {
		graph.AddTask(task)
	}
	ok := te.ExecuteTaskGraph(graph, false)
	if err := te.Cache.Close(); err != nil {
		t.Fatal(err)
	}
	return ok
}

func newTask(id string, inputs []string, outputs []string, command util.Command) *workspace.BuildTask {
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
	if runTasks(t, te, task) {
		t.Fatal("the task succeeded without producing its output")
	}
	if te.ExecutionStats.FailedTasks != 1 {
		t.Errorf("failed tasks = %d, want 1", te.ExecutionStats.FailedTasks)
	}
	if n := te.Cache.Entries(); n != 0 {
		t.Errorf("cache entries = %d, want 0", n)
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
	if runTasks(t, te, task) {
		t.Fatal("the task ran with a missing input")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran")
	}

	// With a producer in the graph, the error names it.
	producer := newTask("generate_config", nil, []string{missing}, helperCommand(t, "nothing"))
	te = newExecutor(t, dir, false)
	te.producers[util.NormalizePath(missing)] = producer.TaskID
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
	if !runTasks(t, newExecutor(t, dir, false), task) {
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
	if !runTasks(t, newExecutor(t, dir, false), toolTask) {
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
			helperCommand(t, "write", out, "object", "&&", "append", log, "x"))
	}

	for i, want := range []string{"x", "x", "xx"} {
		if i == 2 {
			os.Remove(out)
		}
		if !runTasks(t, newExecutor(t, dir, false), mk()) {
			t.Fatalf("run %d failed", i+1)
		}
		if got := read(t, log); got != want {
			t.Errorf("after run %d the command ran %d times, want %d", i+1, len(got), len(want))
		}
	}

	// --force runs it again.
	if !runTasks(t, newExecutor(t, dir, true), mk()) {
		t.Fatal("forced run failed")
	}
	if got := read(t, log); got != "xxx" {
		t.Errorf("forced run didn't run the command: %d runs", len(got))
	}

	toolTask := mk()
	toolTask.ToolCached = true
	te := newExecutor(t, dir, false)
	if !runTasks(t, te, toolTask) {
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
	task := newTask("compile_bad", nil, []string{filepath.Join(dir, "x.o")}, helperCommand(t, "exit", "3"))
	if runTasks(t, newExecutor(t, dir, false), task) {
		t.Fatal("a failing command succeeded")
	}
}

// Finding 9: a task's Exec decides where it runs; a Docker task goes to Docker with the
// workspace (and the cache, when elsewhere) mounted, a native one runs natively.
func TestExecutorRunsEachTaskWhereItsExecSays(t *testing.T) {
	dir := t.TempDir()
	te := newExecutor(t, dir, false)
	// The Docker task's image is not pulled: what runs is the native helper, below
	native := te.toolIdentity
	te.toolIdentity = func(ctx context.Context, spec util.ExecSpec, command util.Command, root string) (string, error) {
		if spec.IsDocker() {
			return "docker image " + spec.Image + " sha256:test", nil
		}
		return native(ctx, spec, command, root)
	}
	var mu sync.Mutex // the stage's tasks run in parallel
	var specs []util.ExecSpec
	var mounts [][]string
	te.runCommand = func(ctx context.Context, spec util.ExecSpec, command util.Command, workDir string, m []string, timeout time.Duration) (*util.ExecutionResult, error) {
		mu.Lock()
		specs = append(specs, spec)
		mounts = append(mounts, m)
		mu.Unlock()
		if workDir != dir {
			t.Errorf("command ran in %s, want the workspace root %s", workDir, dir)
		}
		return util.Run(ctx, util.ExecSpec{}, command, workDir, nil, timeout)
	}

	dockerTask := newTask("compile_in_docker", nil, []string{filepath.Join(dir, "a.o")}, helperCommand(t, "write", filepath.Join(dir, "a.o"), "a"))
	dockerTask.Exec = util.ExecSpec{Type: "docker", Image: "gcc:13"}
	nativeTask := newTask("compile_native", nil, []string{filepath.Join(dir, "b.o")}, helperCommand(t, "write", filepath.Join(dir, "b.o"), "b"))
	if !runTasks(t, te, dockerTask, nativeTask) {
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

// Copies, symlinks, template files and install archives are native: buildy does them itself,
// whatever the paths hold, and they are cached like any task.
func TestExecutorRunsNativeOperations(t *testing.T) {
	root := filepath.Join(t.TempDir(), `it's a "dir"`)
	if runtime.GOOS == "windows" {
		root = filepath.Join(t.TempDir(), "it's a dir")
	}
	src := filepath.Join(root, "in", "version.h.in")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("v@V@\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, "stage")
	gen := filepath.Join(root, "gen", "version.h")
	copied := filepath.Join(stage, "copied.h")
	archive := filepath.Join(root, "dist", "pkg.tar.gz")
	native := func(id string, in, out string, op workspace.NativeOp, deps ...string) *workspace.BuildTask {
		task := workspace.NewNativeTask(id, op.Op, []workspace.TaskInput{workspace.NewTaskInput(in)}, []string{out}, deps, op)
		return &task
	}
	tasks := []*workspace.BuildTask{
		native("generate_version", src, gen, workspace.NativeOp{Op: workspace.NativeTemplate, Substitutions: [][2]string{{"V", "1 & 2"}}}),
		native("stage_copy", gen, copied, workspace.NativeOp{Op: workspace.NativeCopy}, "generate_version"),
		native("install_pkg", copied, archive, workspace.NativeOp{Op: workspace.NativeArchive, Root: stage, Format: "tar.gz", FollowSymlinks: true}, "stage_copy"),
	}
	if runtime.GOOS != "windows" {
		link := native("stage_link", gen, filepath.Join(stage, "linked.h"), workspace.NativeOp{Op: workspace.NativeSymlink}, "generate_version")
		link.Inputs[0].Hash = "symlink:" + gen
		tasks = append(tasks, link)
		tasks[2].Dependencies = append(tasks[2].Dependencies, "stage_link")
		tasks[2].Inputs = append(tasks[2].Inputs, workspace.NewTaskInput(filepath.Join(stage, "linked.h")))
	}
	te := newExecutor(t, root, false)
	if !runTasks(t, te, tasks...) {
		t.Fatal("native tasks failed")
	}
	if got := read(t, copied); got != "v1 & 2\n" {
		t.Errorf("copied.h = %q", got)
	}
	if runtime.GOOS != "windows" {
		if target, err := os.Readlink(filepath.Join(stage, "linked.h")); err != nil || target != gen {
			t.Errorf("linked.h -> %q (%v), want %s", target, err, gen)
		}
	}
	if _, err := os.Stat(archive); err != nil {
		t.Errorf("no archive: %v", err)
	}

	te = newExecutor(t, root, false)
	if !runTasks(t, te, tasks...) {
		t.Fatal("second run failed")
	}
	if te.ExecutionStats.CacheHits != len(tasks) {
		t.Errorf("second run: %d cache hits, want %d", te.ExecutionStats.CacheHits, len(tasks))
	}
}

// Finding 4.5: a task starts as soon as its dependencies finish, not when every task of a stage
// has: second runs after first while slow is still running.
func TestExecutorStartsATaskWhenItsDependenciesFinish(t *testing.T) {
	dir := t.TempDir()
	slowOut, firstOut, secondOut := filepath.Join(dir, "slow"), filepath.Join(dir, "first"), filepath.Join(dir, "second")
	slow := newTask("slow", nil, []string{slowOut}, helperCommand(t, "sleep", "1500", "&&", "write", slowOut, "s"))
	first := newTask("first", nil, []string{firstOut}, helperCommand(t, "write", firstOut, "f"))
	second := newTask("second", []string{firstOut}, []string{secondOut}, helperCommand(t, "write", secondOut, "2"))
	second.Dependencies = []string{"first"}
	if !runTasks(t, newExecutor(t, dir, false), slow, first, second) {
		t.Fatal("the build failed")
	}
	slowInfo, _ := os.Stat(slowOut)
	secondInfo, _ := os.Stat(secondOut)
	if !secondInfo.ModTime().Before(slowInfo.ModTime()) {
		t.Error("second waited for slow, which it doesn't depend on")
	}
}

// After a failure no task starts, and the running ones finish.
func TestExecutorStopsStartingTasksAfterAFailure(t *testing.T) {
	dir := t.TempDir()
	longOut, laterOut := filepath.Join(dir, "long"), filepath.Join(dir, "later")
	failing := newTask("a_fails", nil, nil, helperCommand(t, "exit", "1"))
	long := newTask("b_long", nil, []string{longOut}, helperCommand(t, "sleep", "500", "&&", "write", longOut, "l"))
	later := newTask("c_later", nil, []string{laterOut}, helperCommand(t, "write", laterOut, "x"))
	te := newExecutor(t, dir, false) // two workers: a_fails and b_long start first
	if runTasks(t, te, failing, long, later) {
		t.Fatal("the build succeeded")
	}
	if _, err := os.Stat(longOut); err != nil {
		t.Error("the running task didn't finish")
	}
	if _, err := os.Stat(laterOut); err == nil {
		t.Error("a task started after the failure")
	}
}

// Finding 3.2: --timeout fails a task that runs too long, and removes its partial output.
func TestExecutorTimesOutATask(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	task := newTask("hangs", nil, []string{out}, helperCommand(t, "write", out, "partial", "&&", "sleep", "10000"))
	te := newExecutor(t, dir, false)
	te.Timeout = 500 * time.Millisecond
	start := time.Now()
	if runTasks(t, te, task) {
		t.Fatal("the task succeeded")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the timeout took %s", elapsed)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("the partial output was left")
	}
}
