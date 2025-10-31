package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Builder manages workspace builds with caching
type Builder struct {
	Cache      *BuildCache
	CacheDir   string
	MaxWorkers int
}

// NewBuilder creates a new Builder
func NewBuilder(cacheDir string, maxWorkers int) (*Builder, error) {
	cache, err := NewBuildCache(cacheDir)
	if err != nil {
		return nil, err
	}

	return &Builder{
		Cache:      cache,
		CacheDir:   cacheDir,
		MaxWorkers: maxWorkers,
	}, nil
}

// BuildWorkspace performs build for entire workspace
func (b *Builder) BuildWorkspace(
	workspace *Workspace,
	configParser *ConfigParser,
	targetFilter []string,
	dryRun, force bool,
) (bool, error) {
	log.Printf("Starting workspace build...")
	startTime := time.Now()

	// Task Generation Phase
	log.Printf("Phase 1: Task Generation")
	taskGenStart := time.Now()

	// Discover modules (with caching)
	if _, err := workspace.DiscoverModules(false); err != nil {
		return false, fmt.Errorf("failed to discover modules: %w", err)
	}

	// Save discovery cache
	if err := workspace.SaveDiscoveryCache(b.CacheDir); err != nil {
		log.Printf("WARNING: Failed to save discovery cache: %v", err)
	}

	// Generate tasks for all modules
	allTasks, err := configParser.GenerateWorkspaceTasks(targetFilter)
	if err != nil {
		return false, fmt.Errorf("failed to generate workspace tasks: %w", err)
	}

	// Build task graph
	graph := NewTaskGraph()
	for _, task := range allTasks {
		graph.AddTask(task)
	}

	// Save task graph for debugging
	b.saveTaskGraph(allTasks, graph, configParser)

	taskGenDuration := time.Since(taskGenStart)
	log.Printf("Task generation completed in %.2fs (%d tasks)", taskGenDuration.Seconds(), len(allTasks))

	// Execution Phase
	log.Printf("Phase 2: Task Execution")
	execStart := time.Now()

	// Execute all tasks (cache will handle skipping unchanged tasks)
	executor := NewTaskExecutor(b.Cache, b.MaxWorkers, 8192, nil, force)
	success := executor.ExecuteTaskGraph(graph, dryRun)

	execDuration := time.Since(execStart)
	log.Printf("Task execution completed in %.2fs", execDuration.Seconds())

	totalDuration := time.Since(startTime)
	log.Printf("Total build time: %.2fs (generation: %.2fs, execution: %.2fs)",
		totalDuration.Seconds(), taskGenDuration.Seconds(), execDuration.Seconds())

	// Save build state for future reference
	if success && !dryRun {
		workspaceStateFile := filepath.Join(b.CacheDir, "workspace_state.json")
		workspaceConfigHash, err := HashConfig(workspace.Config.RawConfig)
		if err != nil {
			log.Printf("WARNING: Failed to hash workspace config: %v", err)
			workspaceConfigHash = ""
		}

		// Build file mtimes map
		fileMtimes := make(map[string]float64)
		for _, task := range allTasks {
			for _, input := range task.Inputs {
				if info, err := os.Stat(input.Path); err == nil {
					fileMtimes[input.Path] = float64(info.ModTime().Unix())
				}
			}
		}

		// Build completed tasks map
		completedTasks := make(map[string]map[string]interface{})
		for _, task := range allTasks {
			completedTasks[task.TaskID] = map[string]interface{}{
				"cache_key": task.CacheKey,
				"command":   task.Command,
			}
		}

		newState := &BuildState{
			LastBuildTime:   float64(time.Now().Unix()),
			TaskGraphHash:   b.hashGraph(graph),
			CompletedTasks:  completedTasks,
			FileMtimes:      fileMtimes,
			ConfigHash:      workspaceConfigHash,
			ToolchainHash:   "", // TODO: Track toolchain per module
		}

		if err := newState.Save(workspaceStateFile); err != nil {
			log.Printf("WARNING: Failed to save workspace build state: %v", err)
		}
	}

	return success, nil
}

// hashGraph generates hash of task graph structure
func (b *Builder) hashGraph(graph *TaskGraph) string {
	// Build dependencies dict from tasks
	dependencies := make(map[string][]string)
	taskIDs := []string{}

	for taskID, task := range graph.Tasks {
		taskIDs = append(taskIDs, taskID)
		deps := make([]string, len(task.Dependencies))
		copy(deps, task.Dependencies)
		sort.Strings(deps)
		dependencies[taskID] = deps
	}

	sort.Strings(taskIDs)

	// Create a stable representation of the graph
	graphRepr := map[string]any{
		"tasks":        taskIDs,
		"dependencies": dependencies,
	}

	graphBytes, _ := json.Marshal(graphRepr)
	hash := sha256.Sum256(graphBytes)
	return fmt.Sprintf("%x", hash[:])
}

// saveTaskGraph saves task graph to tasks.json
func (b *Builder) saveTaskGraph(allTasks []*BuildTask, graph *TaskGraph, configParser *ConfigParser) {
	// Get toolchain info (may be nil in workspace mode)
	var toolchainInfo map[string]any
	if configParser.CurrentToolchain != nil {
		toolchainInfo = map[string]any{
			"name":                configParser.CurrentToolchain.Name,
			"description":         configParser.CurrentToolchain.Description,
			"target_platform":     configParser.CurrentToolchain.TargetPlatform,
			"target_architecture": configParser.CurrentToolchain.TargetArchitecture,
			"execution_type":      configParser.CurrentToolchain.ExecutionType,
		}
	}

	executionPlan, err := graph.GetExecutionPlan()
	if err != nil {
		log.Printf("WARNING: Failed to get execution plan: %v", err)
		executionPlan = make(map[string]any)
	}

	outputData := map[string]any{
		"metadata": map[string]any{
			"platform":      configParser.Platform,
			"architecture":  configParser.Architecture,
			"configuration": configParser.Configuration,
			"generated_at":  time.Now().Unix(),
			"total_tasks":   len(allTasks),
			"toolchain":     toolchainInfo,
		},
		"resolved_variables": configParser.VarEnv.GetAllVariables(true),
		"tasks":              allTasks,
		"execution_plan":     executionPlan,
	}

	// Always save to cache directory
	outputPath := filepath.Join(b.CacheDir, "tasks.json")

	data, err := json.MarshalIndent(outputData, "", "  ")
	if err != nil {
		log.Printf("WARNING: Failed to marshal task graph: %v", err)
		return
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		log.Printf("WARNING: Failed to write task graph: %v", err)
		return
	}

	log.Printf("Task graph saved to %s", outputPath)
}
