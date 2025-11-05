package main

import (
	"fmt"
	"log"
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

// AddTask adds a task to the graph
func (tg *TaskGraph) AddTask(task *BuildTask) {
	tg.Tasks[task.TaskID] = task
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
		"total_stages":            len(tg.ExecutionStages),
		"total_tasks":             len(tg.Tasks),
		"estimated_time_seconds":  totalTime,
		"max_parallel_tasks":      maxParallel,
		"total_cpu_hours":         totalCPUHours,
		"stages":                  stagesList,
	}, nil
}

// LogExecutionPlan logs the execution plan
func (tg *TaskGraph) LogExecutionPlan() {
	plan, err := tg.GetExecutionPlan()
	if err != nil {
		log.Printf("ERROR: Failed to get execution plan: %v", err)
		return
	}

	log.Printf("Execution Plan:")
	log.Printf("  Total stages: %v", plan["total_stages"])
	log.Printf("  Total tasks: %v", plan["total_tasks"])
	log.Printf("  Estimated time: %.2f seconds", plan["estimated_time_seconds"])
	log.Printf("  Max parallel tasks: %v", plan["max_parallel_tasks"])
	log.Printf("  Total CPU hours: %.4f", plan["total_cpu_hours"])

	for i, stage := range tg.ExecutionStages {
		log.Printf("  Stage %d: %d tasks", i+1, len(stage))
	}
}

