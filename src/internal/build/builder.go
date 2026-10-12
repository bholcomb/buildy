package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"buildy/internal/cache"
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
//
// ctx is the run's: when it is done (an interrupt), the running tasks are killed and no more start.
func (b *Builder) BuildWorkspace(
	ctx context.Context,
	ws *workspace.Workspace,
	configParser *workspace.ConfigParser,
	targets []string,
	options BuildOptions,
) (*BuildResult, error) {
	util.LogProgress("Starting workspace build...")

	// Initialize build result
	result := NewBuildResult()
	result.Platform = configParser.Platform
	result.Architecture = configParser.Architecture
	result.Configuration = configParser.Configuration

	// Select the workspace-level toolchain (used by workspace-level staging and install tasks)
	if configParser != nil && ws != nil && ws.Config != nil {
		tc, err := configParser.SelectToolchain(ws.Config.File)
		if err != nil {
			result.Finish(false)
			return result, err
		}
		configParser.CurrentToolchain = tc
		result.Toolchain = tc.Name
	}

	// Task Generation Phase
	util.LogProgress("Phase 1: Task Generation")
	taskGenStart := time.Now()

	// Generate tasks for all modules
	allTasks, err := configParser.GenerateWorkspaceTasks()
	if err != nil {
		result.Finish(false)
		return result, fmt.Errorf("failed to generate workspace tasks: %w", err)
	}

	// Build and validate the whole task graph (under --dry-run too)
	graph := workspace.NewTaskGraph()
	for _, task := range allTasks {
		if err := graph.AddTask(task); err != nil {
			result.Finish(false)
			return result, err
		}
	}
	if err := graph.Validate(); err != nil {
		result.Finish(false)
		return result, err
	}

	// --target: run the named targets and everything they depend on, in any module
	if len(targets) > 0 {
		if graph, err = graph.Select(targets); err != nil {
			result.Finish(false)
			return result, err
		}
		allTasks = allTasks[:0]
		for _, task := range graph.Tasks {
			allTasks = append(allTasks, task)
		}
		sort.Slice(allTasks, func(i, j int) bool { return allTasks[i].TaskID < allTasks[j].TaskID })
		util.LogProgress("Selected %d tasks for %s", len(allTasks), strings.Join(targets, ", "))
	}
	result.TotalTasks = len(allTasks)
	for _, task := range allTasks {
		result.TasksByType[task.TaskType]++
	}

	// An order to run them in (a cycle is an error); it numbers the tasks for the logs
	order, err := graph.Order()
	if err != nil {
		result.Finish(false)
		return result, err
	}

	// Save task graph for debugging
	b.saveTaskGraph(allTasks, order, configParser)

	// Collect compile commands if requested
	if options.CompileCommands {
		b.collectCompileCommands(allTasks, result, ws.RootDir)
	}

	result.TaskGenDuration = time.Since(taskGenStart).Seconds()
	util.LogProgress("Task generation completed in %.2fs (%d tasks)", result.TaskGenDuration, len(allTasks))

	// Execution Phase
	util.LogProgress("Phase 2: Task Execution")
	execStart := time.Now()

	// Execute all tasks (cache will handle skipping unchanged tasks). Commands run in the
	// workspace root; every path in them is absolute.
	executor := NewTaskExecutor(b.Cache, b.MaxWorkers, ws.RootDir, options.Force)
	executor.BuildResult = result // Pass result for tracking
	executor.Timeout = options.Timeout
	executor.Context = ctx
	success := executor.ExecuteTaskGraph(graph, options.DryRun)

	// Compact the cache: what the finished tasks recorded goes into its index
	if err := b.Cache.Close(); err != nil {
		result.Finish(false)
		return result, err
	}

	result.ExecDuration = time.Since(execStart).Seconds()
	util.LogProgress("Task execution completed in %.2fs", result.ExecDuration)

	// Finalize result
	result.Finish(success)

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

// saveTaskGraph saves task graph to tasks.json
func (b *Builder) saveTaskGraph(allTasks []*workspace.BuildTask, order []string, configParser *workspace.ConfigParser) {
	// Get toolchain info (may be nil in workspace mode)
	var toolchainInfo map[string]any
	if configParser.CurrentToolchain != nil {
		toolchainInfo = map[string]any{
			"name":                configParser.CurrentToolchain.Name,
			"description":         configParser.CurrentToolchain.Description,
			"target_platform":     configParser.CurrentToolchain.Target.Platform,
			"target_architecture": configParser.CurrentToolchain.Target.Architecture,
			"execution_type":      configParser.CurrentToolchain.Execution.Type,
		}
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
		"execution_order":    order,
	}

	// Always save to cache directory, per build context so platforms and configurations coexist
	tasksFileName := fmt.Sprintf("tasks_%s-%s-%s.json", configParser.Platform, configParser.Architecture, configParser.Configuration)
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
