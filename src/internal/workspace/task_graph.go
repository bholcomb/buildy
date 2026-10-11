package workspace

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"buildy/pkg/util"
)

// TaskGraph represents a task dependency graph with topological sorting
type TaskGraph struct {
	Tasks           map[string]*BuildTask
	ExecutionStages [][]string
}

// NewTaskGraph creates a new TaskGraph
func NewTaskGraph() *TaskGraph {
	return &TaskGraph{
		Tasks:           make(map[string]*BuildTask),
		ExecutionStages: [][]string{},
	}
}

// AddTask adds a task to the graph. Task IDs are unique.
func (tg *TaskGraph) AddTask(task *BuildTask) error {
	if existing, ok := tg.Tasks[task.TaskID]; ok {
		return fmt.Errorf("two tasks have the ID %s: %s and %s", task.TaskID, existing.Describe(), task.Describe())
	}
	tg.Tasks[task.TaskID] = task
	return nil
}

// unresolvedVariable matches a ${...} reference that is not escaped as \${...}.
var unresolvedVariable = regexp.MustCompile(`(^|[^\\])(\$\{[^}]*\})`)

// Validate checks the graph before anything runs, under --dry-run too, and adds the edges that
// follow from data. Each failure names the tasks and targets involved.
//
//  1. Every output has exactly one producer.
//  2. If a task's input is another task's output, the task depends on that producer.
//  3. Every dependency names a task in the graph.
//  4. No ${...} is left unresolved in a command, input or output.
func (tg *TaskGraph) Validate() error {
	var problems []string

	producers := make(map[string]*BuildTask)
	for _, id := range tg.sortedIDs() {
		task := tg.Tasks[id]
		for _, output := range task.Outputs {
			key := util.NormalizePath(output)
			if other, ok := producers[key]; ok && other != task {
				problems = append(problems, fmt.Sprintf("output %s is produced by both %s and %s",
					output, other.Describe(), task.Describe()))
				continue
			}
			producers[key] = task
		}
	}

	for _, id := range tg.sortedIDs() {
		task := tg.Tasks[id]
		for _, input := range task.Inputs {
			producer, ok := producers[util.NormalizePath(input.Path)]
			if ok && producer != task && !hasString(task.Dependencies, producer.TaskID) {
				task.Dependencies = append(task.Dependencies, producer.TaskID)
			}
		}
		for _, dep := range task.Dependencies {
			if _, ok := tg.Tasks[dep]; !ok {
				problems = append(problems, fmt.Sprintf("%s depends on %s, which is not a task", task.Describe(), dep))
			}
		}
		check := func(what, text string) {
			if m := unresolvedVariable.FindStringSubmatch(text); m != nil {
				problems = append(problems, fmt.Sprintf("%s: unresolved variable %s in %s: %s", task.Describe(), m[2], what, text))
			}
		}
		check("command", task.Command)
		for _, input := range task.Inputs {
			check("input", input.Path)
		}
		for _, output := range task.Outputs {
			check("output", output)
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid task graph:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

func (tg *TaskGraph) sortedIDs() []string {
	ids := make([]string, 0, len(tg.Tasks))
	for id := range tg.Tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func hasString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// BuildExecutionStages builds parallel execution stages using topological sort
func (tg *TaskGraph) BuildExecutionStages() ([][]string, error) {
	// Build dependency graph
	dependencies := make(map[string]map[string]bool)
	dependents := make(map[string]map[string]bool)

	for taskID, task := range tg.Tasks {
		dependencies[taskID] = make(map[string]bool)
		for _, dep := range task.Dependencies {
			dependencies[taskID][dep] = true
		}
		dependents[taskID] = make(map[string]bool)
	}

	// Build reverse dependencies
	for taskID, deps := range dependencies {
		for dep := range deps {
			if _, exists := dependents[dep]; exists {
				dependents[dep][taskID] = true
			}
		}
	}

	// Topological sort with parallel stages
	stages := [][]string{}
	remainingTasks := make(map[string]bool)
	for taskID := range tg.Tasks {
		remainingTasks[taskID] = true
	}

	for len(remainingTasks) > 0 {
		// Find tasks with no remaining dependencies
		readyTasks := []string{}
		for taskID := range remainingTasks {
			if len(dependencies[taskID]) == 0 {
				readyTasks = append(readyTasks, taskID)
			}
		}

		if len(readyTasks) == 0 {
			// Circular dependency detected
			remaining := []string{}
			for taskID := range remainingTasks {
				remaining = append(remaining, taskID)
			}
			return nil, fmt.Errorf("circular dependency detected in tasks: %v", remaining)
		}

		stages = append(stages, readyTasks)

		// Remove ready tasks and update dependencies
		for _, taskID := range readyTasks {
			delete(remainingTasks, taskID)
			for dependent := range dependents[taskID] {
				delete(dependencies[dependent], taskID)
			}
		}
	}

	tg.ExecutionStages = stages

	// Assign sequential task numbers in execution order (1-based)
	taskNumber := 1
	for _, stage := range stages {
		for _, taskID := range stage {
			if task, ok := tg.Tasks[taskID]; ok {
				task.TaskNumber = taskNumber
				taskNumber++
			}
		}
	}

	return stages, nil
}

// GetExecutionPlan gets detailed execution plan
func (tg *TaskGraph) GetExecutionPlan() (map[string]any, error) {
	if len(tg.ExecutionStages) == 0 {
		if _, err := tg.BuildExecutionStages(); err != nil {
			return nil, err
		}
	}

	totalTime := 0.0
	maxParallel := 0
	totalCPUHours := 0.0

	for _, stage := range tg.ExecutionStages {
		stageTime := 0.0
		stageCPUs := 0
		for _, taskID := range stage {
			task := tg.Tasks[taskID]
			if task.EstimatedTime > stageTime {
				stageTime = task.EstimatedTime
			}
			stageCPUs += task.ResourceRequirements.CPUCores
			totalCPUHours += (task.EstimatedTime / 3600.0) * float64(task.ResourceRequirements.CPUCores)
		}

		totalTime += stageTime
		if len(stage) > maxParallel {
			maxParallel = len(stage)
		}
	}

	stagesList := []map[string]any{}
	for i, stage := range tg.ExecutionStages {
		stagesList = append(stagesList, map[string]any{
			fmt.Sprintf("stage_%d", i+1): stage,
		})
	}

	return map[string]any{
		"total_stages":           len(tg.ExecutionStages),
		"total_tasks":            len(tg.Tasks),
		"estimated_time_seconds": totalTime,
		"max_parallel_tasks":     maxParallel,
		"total_cpu_hours":        totalCPUHours,
		"stages":                 stagesList,
	}, nil
}

// LogExecutionPlan logs the execution plan
func (tg *TaskGraph) LogExecutionPlan() {
	plan, err := tg.GetExecutionPlan()
	if err != nil {
		util.LogInfo("ERROR: Failed to get execution plan: %v", err)
		return
	}

	util.LogInfo("Execution Plan:")
	util.LogInfo("  Total stages: %v", plan["total_stages"])
	util.LogInfo("  Total tasks: %v", plan["total_tasks"])
	util.LogInfo("  Estimated time: %.2f seconds", plan["estimated_time_seconds"])
	util.LogInfo("  Max parallel tasks: %v", plan["max_parallel_tasks"])
	util.LogInfo("  Total CPU hours: %.4f", plan["total_cpu_hours"])

	for i, stage := range tg.ExecutionStages {
		util.LogVerbose("  Stage %d: %d tasks", i+1, len(stage))
	}
}
