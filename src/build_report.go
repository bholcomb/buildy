package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// BuildOptions contains options for a build
type BuildOptions struct {
	DryRun          bool
	Force           bool
	CompileCommands bool
}

// BuildResult contains the results of a build
type BuildResult struct {
	Success          bool                   `json:"success"`
	StartTime        time.Time              `json:"start_time"`
	EndTime          time.Time              `json:"end_time"`
	TotalDuration    float64                `json:"total_duration_seconds"`
	TaskGenDuration  float64                `json:"task_generation_seconds"`
	ExecDuration     float64                `json:"execution_seconds"`
	Platform         string                 `json:"platform"`
	Architecture     string                 `json:"architecture"`
	Configuration    string                 `json:"configuration"`
	Toolchain        string                 `json:"toolchain"`
	TotalTasks       int                    `json:"total_tasks"`
	ExecutedTasks    int                    `json:"executed_tasks"`
	CacheHits        int                    `json:"cache_hits"`
	CacheMisses      int                    `json:"cache_misses"`
	FailedTasks      int                    `json:"failed_tasks"`
	SkippedTasks     int                    `json:"skipped_tasks"`
	CacheHitRate     float64                `json:"cache_hit_rate_percent"`
	TasksByType      map[string]int         `json:"tasks_by_type"`
	TaskTimings      map[string]float64     `json:"task_timings"`
	FailedTaskIDs    []string               `json:"failed_task_ids,omitempty"`
	CompileTasks     []*CompileCommandEntry `json:"-"` // For compile_commands.json generation
}

// CompileCommandEntry represents a single entry in compile_commands.json
type CompileCommandEntry struct {
	Directory string   `json:"directory"`
	Command   string   `json:"command,omitempty"`
	Arguments []string `json:"arguments,omitempty"`
	File      string   `json:"file"`
	Output    string   `json:"output,omitempty"`
}

// NewBuildResult creates a new BuildResult
func NewBuildResult() *BuildResult {
	return &BuildResult{
		StartTime:     time.Now(),
		TasksByType:   make(map[string]int),
		TaskTimings:   make(map[string]float64),
		FailedTaskIDs: []string{},
		CompileTasks:  []*CompileCommandEntry{},
	}
}

// Finish finalizes the build result with timing information
func (br *BuildResult) Finish(success bool) {
	br.EndTime = time.Now()
	br.TotalDuration = br.EndTime.Sub(br.StartTime).Seconds()
	br.Success = success
	
	// Calculate cache hit rate
	total := br.CacheHits + br.CacheMisses
	if total > 0 {
		br.CacheHitRate = (float64(br.CacheHits) / float64(total)) * 100
	}
}

// RecordTaskExecution records a task execution
func (br *BuildResult) RecordTaskExecution(taskID, taskType string, duration float64, success bool, cacheHit bool) {
	br.TasksByType[taskType]++
	br.TaskTimings[taskID] = duration
	
	if cacheHit {
		br.CacheHits++
	} else {
		br.CacheMisses++
	}
	
	if !success {
		br.FailedTasks++
		br.FailedTaskIDs = append(br.FailedTaskIDs, taskID)
	}
	
	br.ExecutedTasks++
}

// AddCompileCommand adds a compile command entry for compile_commands.json
func (br *BuildResult) AddCompileCommand(directory, command, file, output string) {
	br.CompileTasks = append(br.CompileTasks, &CompileCommandEntry{
		Directory: directory,
		Command:   command,
		File:      file,
		Output:    output,
	})
}

// SaveReport saves the build report to a JSON file
func (br *BuildResult) SaveReport(path string) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create report directory: %w", err)
	}

	data, err := json.MarshalIndent(br, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal build report: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write build report: %w", err)
	}

	log.Printf("Build report saved to %s", path)
	return nil
}

// PrintSummary prints a summary of the build to the terminal
func (br *BuildResult) PrintSummary() {
	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║                      BUILD REPORT                          ║")
	fmt.Println("╠════════════════════════════════════════════════════════════╣")
	
	status := "✓ SUCCESS"
	if !br.Success {
		status = "✗ FAILED"
	}
	fmt.Printf("║  Status:          %-40s ║\n", status)
	fmt.Printf("║  Platform:        %-40s ║\n", fmt.Sprintf("%s-%s", br.Platform, br.Architecture))
	fmt.Printf("║  Configuration:   %-40s ║\n", br.Configuration)
	if br.Toolchain != "" {
		fmt.Printf("║  Toolchain:       %-40s ║\n", br.Toolchain)
	}
	
	fmt.Println("╠════════════════════════════════════════════════════════════╣")
	fmt.Println("║  TIMING                                                    ║")
	fmt.Println("╠════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Total Duration:  %-40s ║\n", fmt.Sprintf("%.2fs", br.TotalDuration))
	fmt.Printf("║  Task Generation: %-40s ║\n", fmt.Sprintf("%.2fs", br.TaskGenDuration))
	fmt.Printf("║  Execution:       %-40s ║\n", fmt.Sprintf("%.2fs", br.ExecDuration))
	
	fmt.Println("╠════════════════════════════════════════════════════════════╣")
	fmt.Println("║  TASKS                                                     ║")
	fmt.Println("╠════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Total Tasks:     %-40s ║\n", fmt.Sprintf("%d", br.TotalTasks))
	fmt.Printf("║  Executed:        %-40s ║\n", fmt.Sprintf("%d", br.ExecutedTasks))
	fmt.Printf("║  Cache Hits:      %-40s ║\n", fmt.Sprintf("%d (%.1f%%)", br.CacheHits, br.CacheHitRate))
	fmt.Printf("║  Cache Misses:    %-40s ║\n", fmt.Sprintf("%d", br.CacheMisses))
	if br.FailedTasks > 0 {
		fmt.Printf("║  Failed:          %-40s ║\n", fmt.Sprintf("%d", br.FailedTasks))
	}
	
	// Tasks by type
	if len(br.TasksByType) > 0 {
		fmt.Println("╠════════════════════════════════════════════════════════════╣")
		fmt.Println("║  TASKS BY TYPE                                             ║")
		fmt.Println("╠════════════════════════════════════════════════════════════╣")
		
		// Sort task types for consistent output
		types := make([]string, 0, len(br.TasksByType))
		for t := range br.TasksByType {
			types = append(types, t)
		}
		sort.Strings(types)
		
		for _, t := range types {
			count := br.TasksByType[t]
			fmt.Printf("║    %-14s %-42s ║\n", t+":", fmt.Sprintf("%d", count))
		}
	}
	
	// Failed tasks
	if len(br.FailedTaskIDs) > 0 {
		fmt.Println("╠════════════════════════════════════════════════════════════╣")
		fmt.Println("║  FAILED TASKS                                              ║")
		fmt.Println("╠════════════════════════════════════════════════════════════╣")
		for _, taskID := range br.FailedTaskIDs {
			// Truncate if too long
			display := taskID
			if len(display) > 56 {
				display = display[:53] + "..."
			}
			fmt.Printf("║    ✗ %-54s ║\n", display)
		}
	}
	
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()
}

// GenerateCompileCommands generates compile_commands.json from collected compile tasks
func (br *BuildResult) GenerateCompileCommands(path string) error {
	if len(br.CompileTasks) == 0 {
		log.Printf("No compile tasks to write to compile_commands.json")
		return nil
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for compile_commands.json: %w", err)
	}

	data, err := json.MarshalIndent(br.CompileTasks, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal compile_commands.json: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write compile_commands.json: %w", err)
	}

	log.Printf("Generated compile_commands.json with %d entries", len(br.CompileTasks))
	return nil
}

// LoadBuildReport loads a build report from a JSON file
func LoadBuildReport(path string) (*BuildResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read build report: %w", err)
	}

	var result BuildResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse build report: %w", err)
	}

	return &result, nil
}
