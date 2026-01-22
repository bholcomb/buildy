package main

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TaskExecutor executes tasks with caching, parallel execution, and resource-aware scheduling
type TaskExecutor struct {
	Cache               *BuildCache
	ChangeDetector      *ChangeDetector
	MaxWorkers          int
	MaxMemoryMB         int
	ExecEnv             *ExecutionEnvironment
	DisableCache        bool
	ExecutionStats      ExecutionStats
	BuildResult         *BuildResult            // Optional: for tracking execution in build report
	ShutdownManager     *ShutdownManager        // For graceful shutdown handling
	ResponseFileManager *ResponseFileManager    // For handling long command lines on Windows
	mu                  sync.Mutex
	pendingOutputs      map[string][]string     // Track outputs of in-progress tasks for cleanup
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
func NewTaskExecutor(cache *BuildCache, changeDetector *ChangeDetector, maxWorkers, maxMemoryMB int, execEnv *ExecutionEnvironment, disableCache bool) *TaskExecutor {
	if execEnv == nil {
		execEnv = NewNativeExecution()
	}

	// Initialize response file manager with cache directory
	cacheDir := ".buildy_cache"
	if cache != nil {
		cacheDir = cache.cacheDir
	}
	rfmConfig := DefaultResponseFileConfig(cacheDir)
	rfm := NewResponseFileManager(rfmConfig)

	te := &TaskExecutor{
		Cache:               cache,
		ChangeDetector:      changeDetector,
		MaxWorkers:          maxWorkers,
		MaxMemoryMB:         maxMemoryMB,
		ExecEnv:             execEnv,
		DisableCache:        disableCache,
		ShutdownManager:     GetShutdownManager(),
		ResponseFileManager: rfm,
		pendingOutputs:      make(map[string][]string),
		ExecutionStats: ExecutionStats{
			TotalTasks:  0,
			CacheHits:   0,
			CacheMisses: 0,
			FailedTasks: 0,
			TotalTime:   0,
		},
	}
	
	// Register cleanup for pending outputs and response files on shutdown
	if te.ShutdownManager != nil {
		te.ShutdownManager.RegisterCleanup(func() {
			te.cleanupPendingOutputs()
			if te.ResponseFileManager != nil {
				te.ResponseFileManager.Cleanup()
			}
		})
	}
	
	return te
}

// cleanupPendingOutputs removes partial outputs from interrupted tasks
func (te *TaskExecutor) cleanupPendingOutputs() {
	te.mu.Lock()
	defer te.mu.Unlock()
	
	for taskID, outputs := range te.pendingOutputs {
		log.Printf("Cleaning up partial outputs for interrupted task: %s", taskID)
		CleanupPartialOutputs(outputs)
	}
	te.pendingOutputs = make(map[string][]string)
}

// ExecuteTaskGraph executes all tasks in the graph
func (te *TaskExecutor) ExecuteTaskGraph(graph *TaskGraph, dryRun bool) bool {
	if len(graph.ExecutionStages) == 0 {
		if _, err := graph.BuildExecutionStages(); err != nil {
			log.Printf("ERROR: Failed to build execution stages: %v", err)
			return false
		}
	}

	startTime := time.Now()
	success := true
	completedTasks := 0
	totalTasks := len(graph.Tasks)

	log.Printf("Starting build with %d tasks in %d stages", totalTasks, len(graph.ExecutionStages))

	if dryRun {
		te.printDryRun(graph)
		return true
	}

	for stageNum, stageTasks := range graph.ExecutionStages {
		// Check for shutdown before starting each stage
		if te.ShutdownManager != nil && te.ShutdownManager.IsShuttingDown() {
			log.Printf("Build interrupted, stopping after stage %d", stageNum)
			success = false
			break
		}
		
		progress := 0.0
		if totalTasks > 0 {
			progress = (float64(completedTasks) / float64(totalTasks)) * 100
		}
		log.Printf("\nStage %d/%d: %d task(s) [%.1f%% complete]",
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
func (te *TaskExecutor) executeStage(taskIDs []string, tasks map[string]*BuildTask) bool {
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
		log.Printf("WARNING: Limiting parallelism to %d workers due to memory constraints "+
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

// executeSingleTask executes a single task with caching
func (te *TaskExecutor) executeSingleTask(task *BuildTask) bool {
	// Check for shutdown before starting
	if te.ShutdownManager != nil && te.ShutdownManager.IsShuttingDown() {
		return false
	}
	
	te.mu.Lock()
	te.ExecutionStats.TotalTasks++
	te.mu.Unlock()

	// Update input hashes and cache key before cache lookup
	// This ensures cache keys are correct for tasks whose inputs were created by dependencies
	if err := task.UpdateInputHashesAndCacheKey(); err != nil {
		log.Printf("Warning: failed to update input hashes for %s: %v", task.TaskID, err)
	}

	// Check cache first (unless disabled by force flag)
	if !te.DisableCache && te.ChangeDetector != nil && te.ChangeDetector.IsCachedResultValid(task) {
		if err := te.Cache.RestoreCachedResult(task); err == nil {
			te.mu.Lock()
			te.ExecutionStats.CacheHits++
			if te.BuildResult != nil {
				te.BuildResult.RecordTaskExecution(task.TaskID, task.TaskType, 0, true, true)
			}
			te.mu.Unlock()
			return true
		}
		log.Printf("Cache restore failed for %s, falling back to execution", task.TaskID)
	}

	// Execute task
	log.Printf("→ %s - executing", task.TaskID)
	log.Printf("  Command: %s", task.Command)
	startTime := time.Now()

	// Ensure output directories exist
	for _, output := range task.Outputs {
		outputDir := filepath.Dir(output)
		if outputDir != "" {
			if err := os.MkdirAll(outputDir, 0755); err != nil {
				log.Printf("ERROR: Failed to create output directory %s: %v", outputDir, err)
				te.mu.Lock()
				te.ExecutionStats.FailedTasks++
				if te.BuildResult != nil {
					te.BuildResult.RecordTaskExecution(task.TaskID, task.TaskType, 0, false, false)
				}
				te.mu.Unlock()
				return false
			}
		}
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

	// Process command through response file manager for long commands (Windows)
	command := task.Command
	if te.ResponseFileManager != nil {
		processedCmd, err := te.ResponseFileManager.ProcessCommand(command, task.TaskID)
		if err != nil {
			log.Printf("WARNING: Failed to create response file for %s: %v", task.TaskID, err)
			// Continue with original command
		} else {
			command = processedCmd
		}
	}

	// Execute command using execution environment
	cwd, _ := os.Getwd()
	result, err := te.ExecEnv.Execute(command, cwd, DefaultTaskTimeoutSeconds)

	executionTime := time.Since(startTime).Seconds()

	// Remove from pending outputs tracking
	te.mu.Lock()
	delete(te.pendingOutputs, task.TaskID)
	te.mu.Unlock()

	if err != nil {
		log.Printf("✗ %s - failed: %v", task.TaskID, err)
		if result != nil && result.Stderr != "" {
			log.Printf("  stderr: %s", result.Stderr)
		}
		// Clean up partial outputs on failure
		CleanupPartialOutputs(task.Outputs)
		te.mu.Lock()
		te.ExecutionStats.FailedTasks++
		if te.BuildResult != nil {
			te.BuildResult.RecordTaskExecution(task.TaskID, task.TaskType, executionTime, false, false)
		}
		te.mu.Unlock()
		return false
	}

	if result.ReturnCode == 0 {
		log.Printf("✓ %s (%.1fs) - completed", task.TaskID, executionTime)
		te.Cache.CacheTaskResult(task, executionTime, true)
		te.mu.Lock()
		te.ExecutionStats.CacheMisses++
		if te.BuildResult != nil {
			te.BuildResult.RecordTaskExecution(task.TaskID, task.TaskType, executionTime, true, false)
		}
		te.mu.Unlock()
		return true
	}

	log.Printf("✗ %s - failed with exit code %d", task.TaskID, result.ReturnCode)
	if result.Stderr != "" {
		log.Printf("  stderr: %s", result.Stderr)
	}
	// Clean up partial outputs on failure
	CleanupPartialOutputs(task.Outputs)
	te.mu.Lock()
	te.ExecutionStats.FailedTasks++
	if te.BuildResult != nil {
		te.BuildResult.RecordTaskExecution(task.TaskID, task.TaskType, executionTime, false, false)
	}
	te.mu.Unlock()
	return false
}

// printDryRun prints dry run information
func (te *TaskExecutor) printDryRun(graph *TaskGraph) {
	plan, err := graph.GetExecutionPlan()
	if err != nil {
		log.Printf("ERROR: Failed to get execution plan: %v", err)
		return
	}

	log.Printf("Execution Plan:")
	log.Printf("  Total tasks: %v", plan["total_tasks"])
	log.Printf("  Total stages: %v", plan["total_stages"])
	log.Printf("  Estimated time: %.1fs", plan["estimated_time_seconds"])
	log.Printf("  Max parallel tasks: %v", plan["max_parallel_tasks"])

	if stages, ok := plan["stages"].([]map[string]any); ok {
		for _, stageInfo := range stages {
			for stageKey, tasksRaw := range stageInfo {
				if tasks, ok := tasksRaw.([]string); ok {
					log.Printf("  %s: %v", stageKey, tasks)
				}
			}
		}
	}
}

// printSummary prints execution summary
func (te *TaskExecutor) printSummary() {
	stats := te.ExecutionStats
	log.Printf("\n✓ Build completed in %.1fs", stats.TotalTime)
	log.Printf("✓ Tasks: %d total, %d cache hits, %d executed",
		stats.TotalTasks, stats.CacheHits, stats.CacheMisses)
	if stats.FailedTasks > 0 {
		log.Printf("✗ Failed tasks: %d", stats.FailedTasks)
	}
}

// GetExecutionStats returns the current execution statistics
func (te *TaskExecutor) GetExecutionStats() ExecutionStats {
	te.mu.Lock()
	defer te.mu.Unlock()
	return te.ExecutionStats
}
