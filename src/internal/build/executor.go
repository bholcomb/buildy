package build

import (
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

// TaskExecutor executes tasks with caching, parallel execution, and resource-aware scheduling.
//
// It enforces the task contract: before a task runs, every declared input exists (its producer
// has run); before a buildy-cached task runs, its declared outputs are deleted, so whatever
// exists afterwards came from this run; after it exits 0, every declared output exists and a
// declared dependency file parses, or the task fails. Only then is a cache entry written.
type TaskExecutor struct {
	Cache           *cache.BuildCache
	MaxWorkers      int
	MaxMemoryMB     int
	WorkDir         string   // The workspace root: commands run here; relative dependency-file paths are relative to it
	DockerMounts    []string // What a Docker task mounts at its host path: the workspace root, and the cache dir if elsewhere
	DisableCache    bool
	ExecutionStats  ExecutionStats
	BuildResult     *BuildResult     // Optional: for tracking execution in build report
	ShutdownManager *ShutdownManager // For graceful shutdown handling
	mu              sync.Mutex
	pendingOutputs  map[string][]string // Track outputs of in-progress tasks for cleanup
	totalTaskCount  int                 // Total number of tasks for logging
	producers       map[string]string   // Normalized output path -> task ID that produces it

	// runCommand runs a task's command where its Exec says (util.Run; replaced in tests)
	runCommand func(spec util.ExecSpec, command util.Command, workDir string, mounts []string, timeoutSeconds int) (*util.ExecutionResult, error)
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
func NewTaskExecutor(buildCache *cache.BuildCache, maxWorkers, maxMemoryMB int, workDir string, disableCache bool) *TaskExecutor {
	te := &TaskExecutor{
		Cache:           buildCache,
		MaxWorkers:      maxWorkers,
		MaxMemoryMB:     maxMemoryMB,
		WorkDir:         workDir,
		DockerMounts:    resource.DockerMounts(workDir, buildCache.CacheDir()),
		DisableCache:    disableCache,
		ShutdownManager: GetShutdownManager(),
		pendingOutputs:  make(map[string][]string),
		producers:       make(map[string]string),
		runCommand:      util.Run,
	}

	// Register cleanup for pending outputs on shutdown
	if te.ShutdownManager != nil {
		te.ShutdownManager.RegisterCleanup(te.cleanupPendingOutputs)
	}

	return te
}

// cleanupPendingOutputs removes partial outputs from interrupted tasks
func (te *TaskExecutor) cleanupPendingOutputs() {
	te.mu.Lock()
	defer te.mu.Unlock()

	for taskID, outputs := range te.pendingOutputs {
		util.LogInfo("Cleaning up partial outputs for interrupted task: %s", taskID)
		CleanupPartialOutputs(outputs)
	}
	te.pendingOutputs = make(map[string][]string)
}

// ExecuteTaskGraph executes all tasks in the graph
func (te *TaskExecutor) ExecuteTaskGraph(graph *workspace.TaskGraph, dryRun bool) bool {
	if len(graph.ExecutionStages) == 0 {
		if _, err := graph.BuildExecutionStages(); err != nil {
			util.LogInfo("ERROR: Failed to build execution stages: %v", err)
			return false
		}
	}

	for taskID, task := range graph.Tasks {
		for _, output := range task.Outputs {
			te.producers[util.NormalizePath(output)] = taskID
		}
	}

	startTime := time.Now()
	success := true
	completedTasks := 0
	totalTasks := len(graph.Tasks)

	// Store total task count for logging
	te.totalTaskCount = totalTasks

	util.LogProgress("Starting build with %d tasks in %d stages", totalTasks, len(graph.ExecutionStages))

	if dryRun {
		te.printDryRun(graph)
		return true
	}

	for stageNum, stageTasks := range graph.ExecutionStages {
		// Check for shutdown before starting each stage
		if te.ShutdownManager != nil && te.ShutdownManager.IsShuttingDown() {
			util.LogInfo("Build interrupted, stopping after stage %d", stageNum)
			success = false
			break
		}

		progress := 0.0
		if totalTasks > 0 {
			progress = (float64(completedTasks) / float64(totalTasks)) * 100
		}
		util.LogProgress("\nStage %d/%d: %d task(s) [%.1f%% complete]",
			stageNum+1, len(graph.ExecutionStages), len(stageTasks), progress)

		stageSuccess := te.executeStage(stageTasks, graph.Tasks)
		completedTasks += len(stageTasks)

		if !stageSuccess {
			success = false
			break
		}
	}

	te.ExecutionStats.TotalTime = time.Since(startTime).Seconds()
	te.printSummary()

	return success
}

// executeStage executes a single stage of tasks in parallel with resource-aware scheduling
func (te *TaskExecutor) executeStage(taskIDs []string, tasks map[string]*workspace.BuildTask) bool {
	if len(taskIDs) == 1 {
		// Single task - execute directly
		return te.executeSingleTask(tasks[taskIDs[0]])
	}

	// Resource-aware parallel execution
	// Calculate total memory requirements for the stage
	totalMemoryRequired := 0
	for _, taskID := range taskIDs {
		totalMemoryRequired += tasks[taskID].ResourceRequirements.MemoryMB
	}

	// Adjust max workers based on memory constraints
	effectiveWorkers := te.MaxWorkers
	if totalMemoryRequired > te.MaxMemoryMB {
		// Need to limit parallelism to avoid memory overload
		maxTaskMemory := 0
		for _, taskID := range taskIDs {
			if tasks[taskID].ResourceRequirements.MemoryMB > maxTaskMemory {
				maxTaskMemory = tasks[taskID].ResourceRequirements.MemoryMB
			}
		}
		effectiveWorkers = te.MaxMemoryMB / maxTaskMemory
		if effectiveWorkers < 1 {
			effectiveWorkers = 1
		}
		if effectiveWorkers > te.MaxWorkers {
			effectiveWorkers = te.MaxWorkers
		}
		if effectiveWorkers > len(taskIDs) {
			effectiveWorkers = len(taskIDs)
		}
		util.LogInfo("WARNING: Limiting parallelism to %d workers due to memory constraints "+
			"(%dMB required, %dMB available)", effectiveWorkers, totalMemoryRequired, te.MaxMemoryMB)
	} else {
		if effectiveWorkers > len(taskIDs) {
			effectiveWorkers = len(taskIDs)
		}
	}

	// Execute tasks in parallel with resource limits using goroutines
	var wg sync.WaitGroup
	taskChan := make(chan string, len(taskIDs))
	resultChan := make(chan bool, len(taskIDs))

	// Start worker goroutines
	for i := 0; i < effectiveWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for taskID := range taskChan {
				result := te.executeSingleTask(tasks[taskID])
				resultChan <- result
			}
		}()
	}

	// Send tasks to workers
	for _, taskID := range taskIDs {
		taskChan <- taskID
	}
	close(taskChan)

	// Wait for all workers to finish
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	success := true
	for result := range resultChan {
		if !result {
			success = false
		}
	}

	return success
}

// executeSingleTask runs one task under the task contract, or skips it on a valid cache hit.
func (te *TaskExecutor) executeSingleTask(task *workspace.BuildTask) bool {
	// Check for shutdown before starting
	if te.ShutdownManager != nil && te.ShutdownManager.IsShuttingDown() {
		return false
	}

	te.mu.Lock()
	te.ExecutionStats.TotalTasks++
	te.mu.Unlock()

	// Every declared input exists by now (its producer has run); hash them once, here.
	if err := te.checkInputs(task); err != nil {
		return te.fail(task, 0, err)
	}
	if err := task.HashInputs(); err != nil {
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

	// Track this task's outputs for cleanup on interruption
	te.mu.Lock()
	te.pendingOutputs[task.TaskID] = task.Outputs
	te.mu.Unlock()

	// Track task in shutdown manager
	if te.ShutdownManager != nil {
		te.ShutdownManager.TrackTask()
		defer te.ShutdownManager.TaskDone()
	}

	runErr := te.run(task)
	executionTime := time.Since(startTime).Seconds()

	// Remove from pending outputs tracking
	te.mu.Lock()
	delete(te.pendingOutputs, task.TaskID)
	te.mu.Unlock()

	if runErr == nil {
		runErr = checkOutputs(task)
	}
	if runErr == nil && !task.ToolCached {
		runErr = te.Cache.Record(task, executionTime, te.WorkDir)
	}
	if runErr != nil {
		// Clean up partial outputs on failure
		CleanupPartialOutputs(task.Outputs)
		return te.fail(task, executionTime, runErr)
	}

	util.LogProgress("[%d/%d] %s (%.1fs) - completed", task.TaskNumber, te.totalTaskCount, task.TaskID, executionTime)
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
	util.LogProgress("[%d/%d] %s - FAILED: %v", task.TaskNumber, te.totalTaskCount, task.TaskID, err)
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

// run executes the task's work: its native operation, or its command.
func (te *TaskExecutor) run(task *workspace.BuildTask) error {
	if task.Native != nil {
		return runNative(task)
	}

	// Natively or in Docker, as the task's toolchain says; in its directory, else the workspace root
	result, err := te.runCommand(task.Exec, task.Command, te.WorkDir, te.DockerMounts, util.DefaultTaskTimeoutSeconds)
	if err != nil {
		return err
	}
	if result.ReturnCode != 0 {
		if result.Stderr != "" {
			util.LogProgress("  stderr: %s", result.Stderr)
		}
		if result.Stdout != "" {
			util.LogProgress("  stdout: %s", result.Stdout)
		}
		return fmt.Errorf("exit code %d", result.ReturnCode)
	}
	return nil
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

// printDryRun prints the execution plan: every task, stage by stage, without running any
func (te *TaskExecutor) printDryRun(graph *workspace.TaskGraph) {
	util.LogProgress("Dry run: %d tasks in %d stages (nothing is executed)", len(graph.Tasks), len(graph.ExecutionStages))
	for i, stage := range graph.ExecutionStages {
		ids := append([]string{}, stage...)
		sort.Strings(ids)
		util.LogProgress("  Stage %d: %s", i+1, strings.Join(ids, ", "))
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
