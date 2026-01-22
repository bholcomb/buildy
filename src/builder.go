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
	options BuildOptions,
) (*BuildResult, error) {
	log.Printf("Starting workspace build...")
	
	// Initialize build result
	result := NewBuildResult()
	result.Platform = configParser.Platform
	result.Architecture = configParser.Architecture
	result.Configuration = configParser.Configuration

	workspaceStateFile := filepath.Join(b.CacheDir, "incremental_build.json")
	existingState, err := LoadBuildState(workspaceStateFile)
	if err != nil {
		log.Printf("WARNING: Failed to load previous build state: %v", err)
	}

	var buildState *BuildState
	if existingState != nil {
		buildState = existingState
	} else {
		buildState = NewBuildState()
	}

	changeDetector := NewChangeDetector(buildState, b.Cache)

	// Precompute toolchain hash (workspace-level) if available
	currentToolchainHash := ""
	if configParser != nil && workspace != nil && workspace.Config != nil {
		if tc, err := configParser.selectToolchain(workspace.Config.RawConfig); err == nil && tc != nil {
			configParser.CurrentToolchain = tc
			configParser.ToolchainHash = HashToolchainConfig(tc)
			currentToolchainHash = configParser.ToolchainHash
			result.Toolchain = tc.Name
		} else if err != nil {
			log.Printf("WARNING: Failed to preselect toolchain for change detection: %v", err)
		}
	}

	// Task Generation Phase
	log.Printf("Phase 1: Task Generation")
	taskGenStart := time.Now()

	// Discover modules (with caching)
	if _, err := workspace.DiscoverModules(false); err != nil {
		result.Finish(false)
		return result, fmt.Errorf("failed to discover modules: %w", err)
	}

	// Save discovery cache
	if err := workspace.SaveDiscoveryCache(b.CacheDir); err != nil {
		log.Printf("WARNING: Failed to save discovery cache: %v", err)
	}

	// If we have prior build state and we're not forcing, check for changes first
	if !options.Force && buildState != nil && buildState.ConfigHash != "" && !options.DryRun {
		var currentConfig map[string]any
		if workspace != nil && workspace.Config != nil {
			currentConfig = workspace.Config.RawConfig
		}

		configFile := filepath.Join(workspace.RootDir, "buildy.yaml")
		changes, detectErr := changeDetector.DetectChanges(configFile, currentConfig, currentToolchainHash)
		if detectErr != nil {
			log.Printf("WARNING: Change detection failed: %v", detectErr)
		} else if !changes.HasChanges() {
			log.Printf("No source or config changes detected; verifying cache state before execution.")
		} else if changes.RequiresFullRebuild() {
			log.Printf("Detected configuration/toolchain changes. Performing full rebuild.")
		} else {
			log.Printf("Detected source changes. Rebuilding affected tasks.")
		}
	}

	// Generate tasks for all modules
	allTasks, err := configParser.GenerateWorkspaceTasks(targetFilter)
	if err != nil {
		result.Finish(false)
		return result, fmt.Errorf("failed to generate workspace tasks: %w", err)
	}

	result.TotalTasks = len(allTasks)

	// Build task graph
	graph := NewTaskGraph()
	for _, task := range allTasks {
		graph.AddTask(task)
		result.TasksByType[task.TaskType]++
	}

	// Save task graph for debugging
	b.saveTaskGraph(allTasks, graph, configParser)

	// Collect compile commands if requested
	if options.CompileCommands {
		b.collectCompileCommands(allTasks, result, workspace.RootDir)
	}

	result.TaskGenDuration = time.Since(taskGenStart).Seconds()
	log.Printf("Task generation completed in %.2fs (%d tasks)", result.TaskGenDuration, len(allTasks))

	// Execution Phase
	log.Printf("Phase 2: Task Execution")
	execStart := time.Now()

	// Execute all tasks (cache will handle skipping unchanged tasks)
	executor := NewTaskExecutor(b.Cache, changeDetector, b.MaxWorkers, 8192, nil, options.Force)
	executor.BuildResult = result // Pass result for tracking
	success := executor.ExecuteTaskGraph(graph, options.DryRun)

	result.ExecDuration = time.Since(execStart).Seconds()
	log.Printf("Task execution completed in %.2fs", result.ExecDuration)

	// Finalize result
	result.Finish(success)

	// Save build state for future reference
	if success && !options.DryRun {
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
			LastBuildTime:  float64(time.Now().Unix()),
			TaskGraphHash:  b.hashGraph(graph),
			CompletedTasks: completedTasks,
			FileMtimes:     fileMtimes,
			ConfigHash:     workspaceConfigHash,
			ToolchainHash:  configParser.ToolchainHash,
		}

		if err := newState.Save(workspaceStateFile); err != nil {
			log.Printf("WARNING: Failed to save workspace build state: %v", err)
		}
	}

	return result, nil
}

// collectCompileCommands collects compile commands from all compile tasks
func (b *Builder) collectCompileCommands(allTasks []*BuildTask, result *BuildResult, workspaceRoot string) {
	for _, task := range allTasks {
		if task.TaskType == "compile" && len(task.Inputs) > 0 {
			// Get the source file (first input)
			sourceFile := task.Inputs[0].Path
			
			// Make source file path absolute
			absSource := sourceFile
			if !filepath.IsAbs(sourceFile) {
				absSource = filepath.Join(workspaceRoot, sourceFile)
			}
			
			// Get output file (first output, typically .o file)
			var outputFile string
			for _, out := range task.Outputs {
				if filepath.Ext(out) == ".o" || filepath.Ext(out) == ".obj" {
					outputFile = out
					break
				}
			}
			
			result.AddCompileCommand(workspaceRoot, task.Command, absSource, outputFile)
		}
	}
	
	log.Printf("Collected %d compile commands for compile_commands.json", len(result.CompileTasks))
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
