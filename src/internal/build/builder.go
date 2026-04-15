package build

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"buildy/internal/cache"
	"buildy/internal/resource"
	"buildy/internal/workspace"
	"buildy/pkg/util"
)

// Builder manages workspace builds with caching
type Builder struct {
	Cache      *cache.BuildCache
	CacheDir   string
	MaxWorkers int
}

// NewBuilder creates a new Builder
func NewBuilder(cacheDir string, maxWorkers int) (*Builder, error) {
	c, err := cache.NewBuildCache(cacheDir)
	if err != nil {
		return nil, err
	}

	return &Builder{
		Cache:      c,
		CacheDir:   cacheDir,
		MaxWorkers: maxWorkers,
	}, nil
}

// BuildWorkspace performs build for entire workspace
func (b *Builder) BuildWorkspace(
	ws *workspace.Workspace,
	configParser *workspace.ConfigParser,
	targetFilter []string,
	options BuildOptions,
) (*BuildResult, error) {
	util.LogProgress("Starting workspace build...")
	
	// Reset error collector for clean state
	util.ResetErrorCollector()
	
	// Initialize build result
	result := NewBuildResult()
	result.Platform = configParser.Platform
	result.Architecture = configParser.Architecture
	result.Configuration = configParser.Configuration

	stateFileName := fmt.Sprintf("incremental_build_%s.json", configParser.Configuration)
	workspaceStateFile := filepath.Join(b.CacheDir, stateFileName)
	existingState, err := cache.LoadBuildState(workspaceStateFile)
	if err != nil {
		util.LogInfo("WARNING: Failed to load previous build state: %v", err)
	}

	var buildState *cache.BuildState
	if existingState != nil {
		buildState = existingState
	} else {
		buildState = cache.NewBuildState()
	}

	changeDetector := cache.NewChangeDetector(buildState, b.Cache)

	// Precompute toolchain hash (workspace-level) if available
	currentToolchainHash := ""
	if configParser != nil && ws != nil && ws.Config != nil {
		if tc, err := configParser.SelectToolchain(ws.Config.RawConfig); err == nil && tc != nil {
			configParser.CurrentToolchain = tc
			configParser.ToolchainHash = resource.HashToolchainConfig(tc)
			currentToolchainHash = configParser.ToolchainHash
			result.Toolchain = tc.Name
		} else if err != nil {
			util.LogInfo("WARNING: Failed to preselect toolchain for change detection: %v", err)
		}
	}

	// Task Generation Phase
	util.LogProgress("Phase 1: Task Generation")
	taskGenStart := time.Now()

	// Discover modules (with caching)
	if _, err := ws.DiscoverModules(false); err != nil {
		result.Finish(false)
		return result, fmt.Errorf("failed to discover modules: %w", err)
	}

	// Save discovery cache
	if err := ws.SaveDiscoveryCache(b.CacheDir); err != nil {
		util.LogInfo("WARNING: Failed to save discovery cache: %v", err)
	}

	// If we have prior build state and we're not forcing, check for changes first
	if !options.Force && buildState != nil && buildState.ConfigHash != "" && !options.DryRun {
		var currentConfig map[string]any
		if ws != nil && ws.Config != nil {
			currentConfig = ws.Config.RawConfig
		}

		configFile := filepath.Join(ws.RootDir, "buildy.yaml")
		changes, detectErr := changeDetector.DetectChanges(configFile, currentConfig, currentToolchainHash)
		if detectErr != nil {
			util.LogInfo("WARNING: Change detection failed: %v", detectErr)
		} else if !changes.HasChanges() {
			util.LogInfo("No source or config changes detected; verifying cache state before execution.")
		} else if changes.RequiresFullRebuild() {
			util.LogInfo("Detected configuration/toolchain changes. Performing full rebuild.")
		} else {
			util.LogInfo("Detected source changes. Rebuilding affected tasks.")
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
	graph := workspace.NewTaskGraph()
	for _, task := range allTasks {
		graph.AddTask(task)
		result.TasksByType[task.TaskType]++
	}

	// Save task graph for debugging
	b.saveTaskGraph(allTasks, graph, configParser)

	// Collect compile commands if requested
	if options.CompileCommands {
		b.collectCompileCommands(allTasks, result, ws.RootDir)
	}

	result.TaskGenDuration = time.Since(taskGenStart).Seconds()
	util.LogProgress("Task generation completed in %.2fs (%d tasks)", result.TaskGenDuration, len(allTasks))

	// Execution Phase
	util.LogProgress("Phase 2: Task Execution")
	execStart := time.Now()

	// Execute all tasks (cache will handle skipping unchanged tasks)
	executor := NewTaskExecutor(b.Cache, changeDetector, b.MaxWorkers, 8192, nil, options.Force)
	executor.BuildResult = result // Pass result for tracking
	success := executor.ExecuteTaskGraph(graph, options.DryRun)

	result.ExecDuration = time.Since(execStart).Seconds()
	util.LogProgress("Task execution completed in %.2fs", result.ExecDuration)

	// Check for collected errors/warnings (warnings are treated as errors)
	errorCollector := util.GetErrorCollector()
	if errorCollector.HasErrors() {
		errorCollector.PrintSummary()
		success = false
	}

	// Finalize result
	result.Finish(success)

	// Save build state for future reference
	if success && !options.DryRun {
		workspaceConfigHash, err := util.HashConfig(ws.Config.RawConfig)
		if err != nil {
			util.LogInfo("WARNING: Failed to hash workspace config: %v", err)
			workspaceConfigHash = ""
		}

		// Build file mtimes map with normalized paths for consistent lookup
		// On Windows, paths are case-insensitive, so we normalize to ensure
		// consistent comparisons between builds
		fileMtimes := make(map[string]float64)
		for _, task := range allTasks {
			for _, input := range task.Inputs {
				if info, err := os.Stat(input.Path); err == nil {
					normalizedPath := util.NormalizePath(input.Path)
					fileMtimes[normalizedPath] = float64(info.ModTime().Unix())
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

		newState := &cache.BuildState{
			LastBuildTime:  float64(time.Now().Unix()),
			TaskGraphHash:  b.hashGraph(graph),
			CompletedTasks: completedTasks,
			FileMtimes:     fileMtimes,
			ConfigHash:     workspaceConfigHash,
			ToolchainHash:  configParser.ToolchainHash,
		}

		if err := newState.Save(workspaceStateFile); err != nil {
			util.LogInfo("WARNING: Failed to save workspace build state: %v", err)
		}
	}

	return result, nil
}

// collectCompileCommands collects compile commands from all compile tasks
func (b *Builder) collectCompileCommands(allTasks []*workspace.BuildTask, result *BuildResult, workspaceRoot string) {
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
	
	util.LogInfo("Collected %d compile commands for compile_commands.json", len(result.CompileTasks))
}

// hashGraph generates hash of task graph structure
func (b *Builder) hashGraph(graph *workspace.TaskGraph) string {
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
func (b *Builder) saveTaskGraph(allTasks []*workspace.BuildTask, graph *workspace.TaskGraph, configParser *workspace.ConfigParser) {
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
		util.LogInfo("WARNING: Failed to get execution plan: %v", err)
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

	// Always save to cache directory, per-configuration so debug/release coexist
	tasksFileName := fmt.Sprintf("tasks_%s.json", configParser.Configuration)
	outputPath := filepath.Join(b.CacheDir, tasksFileName)

	data, err := json.MarshalIndent(outputData, "", "  ")
	if err != nil {
		util.LogWarning("Failed to marshal task graph: %v", err)
		return
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		util.LogWarning("Failed to write task graph: %v", err)
		return
	}

	util.LogInfo("Task graph saved to %s", outputPath)
}
