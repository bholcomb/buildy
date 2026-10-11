package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"buildy/internal/cache"
	"buildy/internal/resource"
	"buildy/internal/workspace"
	"buildy/pkg/util"
)

// TaskExecutor runs a task graph: in parallel, each task once its dependencies have finished,
// skipping those the cache says are up to date.
//
// It enforces the task contract: before a task runs, every declared input exists (its producer
// has run); before a buildy-cached task runs, its declared outputs are deleted, so whatever
// exists afterwards came from this run; after it exits 0, every declared output exists and a
// declared dependency file parses, or the task fails. Only then is a cache entry written.
type TaskExecutor struct {
	Cache          *cache.BuildCache
	MaxWorkers     int
	Timeout        time.Duration   // How long a task may run (0: no limit)
	Context        context.Context // Done when the build is interrupted: running tasks are killed
	WorkDir        string          // The workspace root: commands run here; relative dependency-file paths are relative to it
	DockerMounts   []string        // What a Docker task mounts at its host path: the workspace root, and the cache dir if elsewhere
	DisableCache   bool
	ExecutionStats ExecutionStats
	BuildResult    *BuildResult // Optional: for tracking execution in build report
	mu             sync.Mutex
	totalTaskCount int               // Total number of tasks for logging
	producers      map[string]string // Normalized output path -> task ID that produces it
	tools          *toolIdentities   // What each program is, for cache keys

	// runCommand runs a task's command where its Exec says (util.Run; replaced in tests)
	runCommand func(ctx context.Context, spec util.ExecSpec, command util.Command, workDir string, mounts []string, timeout time.Duration) (*util.ExecutionResult, error)
}

// ExecutionStats tracks execution statistics
type ExecutionStats struct {
	TotalTasks  int
	CacheHits   int
	CacheMisses int
	FailedTasks int
	TotalTime   float64
}

// NewTaskExecutor creates a new TaskExecutor
func NewTaskExecutor(buildCache *cache.BuildCache, maxWorkers int, workDir string, disableCache bool) *TaskExecutor {
	te := &TaskExecutor{
		Cache:        buildCache,
		MaxWorkers:   maxWorkers,
		WorkDir:      workDir,
		DockerMounts: resource.DockerMounts(workDir, buildCache.CacheDir()),
		DisableCache: disableCache,
		producers:    make(map[string]string),
		runCommand:   util.Run,
		Context:      context.Background(),
	}
	te.tools = newToolIdentities(buildCache.HashFile)
	return te
}

// ExecuteTaskGraph runs the graph's tasks, each as soon as its dependencies have finished, up to
// MaxWorkers at a time. After a failure (or an interrupt) it starts no more tasks and waits for
// the running ones. With dryRun it prints the plan instead.
func (te *TaskExecutor) ExecuteTaskGraph(graph *workspace.TaskGraph, dryRun bool) bool {
	order, err := graph.Order()
	if err != nil {
		util.LogError("%v", err)
		return false
	}
	for taskID, task := range graph.Tasks {
		for _, output := range task.Outputs {
			te.producers[util.NormalizePath(output)] = taskID
		}
	}
	te.totalTaskCount = len(order)

	if dryRun {
		te.printDryRun(graph, order)
		return true
	}

	startTime := time.Now()
	util.LogProgress("Starting build with %d tasks, up to %d at a time", len(order), te.MaxWorkers)
	started, success := te.schedule(graph)
	if notStarted := len(order) - started; notStarted > 0 {
		util.LogProgress("%d task(s) not started: the build stopped", notStarted)
		if te.BuildResult != nil {
			te.BuildResult.SkippedTasks = notStarted
		}
	}

	te.ExecutionStats.TotalTime = time.Since(startTime).Seconds()
	te.printSummary()
	return success
}

// schedule runs the tasks: a ready queue of the tasks whose dependencies have finished, smallest
// ID first, feeding up to MaxWorkers running tasks. It returns how many tasks it started, and
// whether all of them succeeded.
func (te *TaskExecutor) schedule(graph *workspace.TaskGraph) (int, bool) {
	waiting := make(map[string]int, len(graph.Tasks))
	dependents := make(map[string][]string, len(graph.Tasks))
	ready := &workspace.IDQueue{}
	for id, task := range graph.Tasks {
		seen := map[string]bool{}
		for _, dep := range task.Dependencies {
			if !seen[dep] {
				seen[dep] = true
				waiting[id]++
				dependents[dep] = append(dependents[dep], id)
			}
		}
		if waiting[id] == 0 {
			ready.PushID(id)
		}
	}

	workers := max(te.MaxWorkers, 1)
	type result struct {
		id string
		ok bool
	}
	done := make(chan result)
	running, started, success := 0, 0, true
	for {
		for success && running < workers && ready.Len() > 0 && !te.stopping() {
			id := ready.PopID()
			running++
			started++
			go func() { done <- result{id, te.executeSingleTask(graph.Tasks[id])} }()
		}
		if running == 0 {
			break
		}
		r := <-done
		running--
		if !r.ok {
			success = false
			continue
		}
		for _, dependent := range dependents[r.id] {
			if waiting[dependent]--; waiting[dependent] == 0 {
				ready.PushID(dependent)
			}
		}
	}
	if te.stopping() {
		success = false
	}
	return started, success
}

// stopping reports whether the build was interrupted.
func (te *TaskExecutor) stopping() bool {
	return te.Context.Err() != nil
}

// executeSingleTask runs one task under the task contract, or skips it on a valid cache hit.
func (te *TaskExecutor) executeSingleTask(task *workspace.BuildTask) bool {
	te.mu.Lock()
	te.ExecutionStats.TotalTasks++
	te.mu.Unlock()

	// Every declared input exists by now (its producer has run); hash them once, here.
	if err := te.checkInputs(task); err != nil {
		return te.fail(task, 0, err)
	}
	// What the command runs is part of the key: the program's path and content, or the image
	if task.Native == nil {
		tool, err := te.tools.of(te.Context, task.Exec, task.Command, te.WorkDir)
		if err != nil {
			return te.fail(task, 0, err)
		}
		task.Tool = tool
	}
	if err := task.HashInputs(te.Cache.HashFile); err != nil {
		return te.fail(task, 0, err)
	}

	// Tool-cached tasks (go build, cargo build) always run: their tool decides what to redo.
	if !task.ToolCached && !te.DisableCache {
		hit, reason := te.Cache.Lookup(task)
		if hit {
			util.LogProgress("[%d/%d] %s - cache hit", task.TaskNumber, te.totalTaskCount, task.TaskID)
			te.mu.Lock()
			te.ExecutionStats.CacheHits++
			if te.BuildResult != nil {
				te.BuildResult.RecordTaskExecution(task.TaskID, task.TaskType, 0, true, true)
			}
			te.mu.Unlock()
			return true
		}
		util.LogInfo("Cache miss for %s: %s", task.TaskID, reason)
	}

	// Execute task
	util.LogProgress("[%d/%d] %s - executing", task.TaskNumber, te.totalTaskCount, task.TaskID)
	if task.Native == nil {
		util.LogInfo("  Command: %s", task.Command)
	}
	startTime := time.Now()

	if err := te.prepareOutputs(task); err != nil {
		return te.fail(task, 0, err)
	}

	output, runErr := te.run(task)
	executionTime := time.Since(startTime).Seconds()

	if runErr == nil {
		runErr = checkOutputs(task)
	}
	if runErr == nil && !task.ToolCached {
		runErr = te.Cache.Record(task, te.WorkDir)
	}
	if runErr != nil {
		// A failed or interrupted (killed) task leaves no partial outputs
		CleanupPartialOutputs(task.Outputs)
		return te.fail(task, executionTime, runErr)
	}

	// What the command printed (warnings), in one message: it stays together under -j
	message := fmt.Sprintf("[%d/%d] %s (%.1fs) - completed", task.TaskNumber, te.totalTaskCount, task.TaskID, executionTime)
	if output != "" {
		message += ", with output:\n" + indent(output)
	}
	util.LogProgress("%s", message)
	te.mu.Lock()
	te.ExecutionStats.CacheMisses++
	if te.BuildResult != nil {
		te.BuildResult.RecordTaskExecution(task.TaskID, task.TaskType, executionTime, true, false)
	}
	te.mu.Unlock()
	return true
}

// fail reports a failed task and records it.
func (te *TaskExecutor) fail(task *workspace.BuildTask, executionTime float64, err error) bool {
	if te.stopping() {
		util.LogProgress("[%d/%d] %s - interrupted", task.TaskNumber, te.totalTaskCount, task.TaskID)
	} else {
		util.LogProgress("[%d/%d] %s - FAILED: %v", task.TaskNumber, te.totalTaskCount, task.TaskID, err)
	}
	te.mu.Lock()
	te.ExecutionStats.FailedTasks++
	if te.BuildResult != nil {
		te.BuildResult.RecordTaskExecution(task.TaskID, task.TaskType, executionTime, false, false)
	}
	te.mu.Unlock()
	return false
}

// checkInputs fails if a declared input does not exist, naming the task that should produce it.
func (te *TaskExecutor) checkInputs(task *workspace.BuildTask) error {
	for _, input := range task.Inputs {
		if _, err := os.Stat(input.Path); err == nil {
			continue
		}
		if producer, ok := te.producers[util.NormalizePath(input.Path)]; ok {
			return fmt.Errorf("input %s does not exist; task %s should have produced it", input.Path, producer)
		}
		return fmt.Errorf("input %s does not exist, and no task produces it", input.Path)
	}
	return nil
}

// prepareOutputs creates every output's directory. For a buildy-cached task it also deletes the
// declared output files, so that whatever exists after the run came from the run (an archiver
// that appends, like `ar rcs`, would otherwise keep members of removed sources). Tool-cached
// tasks keep their outputs: their tool uses them for its own incremental work.
func (te *TaskExecutor) prepareOutputs(task *workspace.BuildTask) error {
	for _, output := range task.Outputs {
		te.Cache.Forget(output)
		if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
			return fmt.Errorf("failed to create output directory %s: %w", filepath.Dir(output), err)
		}
		if task.ToolCached {
			continue
		}
		if err := os.Remove(output); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove the previous %s: %w", output, err)
		}
	}
	return nil
}

// checkOutputs fails if the task exited 0 without producing a declared output.
func checkOutputs(task *workspace.BuildTask) error {
	for _, output := range task.Outputs {
		if _, err := os.Stat(output); err != nil {
			hint := ""
			if task.ToolCached {
				hint = " (the tool named its output differently: check the target's name, or bin: for a Rust executable)"
			}
			return fmt.Errorf("%s exited 0 but did not produce declared output %s%s", task.TaskID, output, hint)
		}
	}
	if task.DepFile != nil {
		if _, err := os.Stat(task.DepFile.Path); err != nil {
			return fmt.Errorf("%s exited 0 but did not write its dependency file %s", task.TaskID, task.DepFile.Path)
		}
	}
	return nil
}

// run executes the task's work: its native operation, or its command. It returns what the command
// printed; when the command fails, the error has its command line and output.
func (te *TaskExecutor) run(task *workspace.BuildTask) (string, error) {
	if task.Native != nil {
		return "", runNative(task)
	}

	// Arguments over the host's limits go in a response file, in the cache directory (which a
	// Docker task mounts)
	env := append(os.Environ(), util.EnvList(task.Exec.Env)...)
	command, remove, err := withResponseFile(task.Command, env, te.responseFilePath(task))
	if err != nil {
		return "", err
	}
	defer remove()

	// Natively or in Docker, as the task's toolchain says; in its directory, else the workspace root
	result, err := te.runCommand(te.Context, task.Exec, command, te.WorkDir, te.DockerMounts, te.Timeout)
	if err != nil {
		return "", err
	}
	if result.ReturnCode != 0 {
		message := fmt.Sprintf("exit code %d\n    command: %s", result.ReturnCode, task.Command)
		if result.Output != "" {
			message += "\n" + indent(result.Output)
		}
		return "", errors.New(message)
	}
	return result.Output, nil
}

// indent indents every line of a command's output, to set it off from buildy's.
func indent(output string) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n")
}

// responseFilePath is where a task's response file goes, if it needs one.
func (te *TaskExecutor) responseFilePath(task *workspace.BuildTask) string {
	name := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == '.' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return r
		}
		return '_'
	}, task.TaskID)
	dir, err := filepath.Abs(filepath.Join(te.Cache.CacheDir(), "response_files"))
	if err != nil {
		dir = filepath.Join(te.Cache.CacheDir(), "response_files")
	}
	return filepath.Join(dir, name+".rsp")
}

// runNative carries out a task's native operation on its input and output.
func runNative(task *workspace.BuildTask) error {
	if len(task.Inputs) != 1 || len(task.Outputs) != 1 {
		return fmt.Errorf("%s task %s needs exactly one input and one output, has %d and %d",
			task.Native.Op, task.TaskID, len(task.Inputs), len(task.Outputs))
	}
	in, out := task.Inputs[0].Path, task.Outputs[0]
	switch task.Native.Op {
	case workspace.NativeCopy:
		return util.CopyFile(in, out)
	case workspace.NativeSymlink:
		return os.Symlink(in, out)
	case workspace.NativeTemplate:
		return util.FillTemplate(in, out, task.Native.Substitutions)
	case workspace.NativeArchive:
		return util.WriteArchive(in, out, task.Native.Format, task.Native.FollowSymlinks)
	}
	return fmt.Errorf("task %s: unknown native operation %q", task.TaskID, task.Native.Op)
}

// printDryRun prints the plan: every task in an order that runs each after its dependencies, with
// what it waits for. Nothing runs.
func (te *TaskExecutor) printDryRun(graph *workspace.TaskGraph, order []string) {
	util.LogProgress("Dry run: %d tasks, in an order they can run in (nothing is executed)", len(order))
	for _, id := range order {
		task := graph.Tasks[id]
		line := fmt.Sprintf("  [%d] %s", task.TaskNumber, id)
		if deps := task.Dependencies; len(deps) > 0 {
			sorted := append([]string{}, deps...)
			sort.Strings(sorted)
			if len(sorted) > 4 {
				sorted = append(sorted[:4], fmt.Sprintf("and %d more", len(deps)-4))
			}
			line += "  (after " + strings.Join(sorted, ", ") + ")"
		}
		util.LogProgress("%s", line)
	}
}

// printSummary prints execution summary
func (te *TaskExecutor) printSummary() {
	stats := te.ExecutionStats
	util.LogProgress("\nBuild completed in %.1fs", stats.TotalTime)
	util.LogProgress("Tasks: %d total, %d cache hits, %d executed",
		stats.TotalTasks, stats.CacheHits, stats.CacheMisses)
	if stats.FailedTasks > 0 {
		util.LogProgress("Failed tasks: %d", stats.FailedTasks)
	}
}

// GetExecutionStats returns the current execution statistics
func (te *TaskExecutor) GetExecutionStats() ExecutionStats {
	te.mu.Lock()
	defer te.mu.Unlock()
	return te.ExecutionStats
}
