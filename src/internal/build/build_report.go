package build

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"buildy/pkg/util"
)

// BuildOptions contains options for a build
type BuildOptions struct {
	DryRun          bool
	Force           bool
	CompileCommands bool
	Timeout         time.Duration // How long a task may run (0: no limit)
}

// BuildResult contains the results of a build
type BuildResult struct {
	Success         bool                   `json:"success"`
	StartTime       time.Time              `json:"start_time"`
	EndTime         time.Time              `json:"end_time"`
	TotalDuration   float64                `json:"total_duration_seconds"`
	TaskGenDuration float64                `json:"task_generation_seconds"`
	ExecDuration    float64                `json:"execution_seconds"`
	Platform        string                 `json:"platform"`
	Architecture    string                 `json:"architecture"`
	Configuration   string                 `json:"configuration"`
	Toolchain       string                 `json:"toolchain"`
	TotalTasks      int                    `json:"total_tasks"`
	ExecutedTasks   int                    `json:"executed_tasks"`
	CacheHits       int                    `json:"cache_hits"`
	CacheMisses     int                    `json:"cache_misses"`
	FailedTasks     int                    `json:"failed_tasks"`
	SkippedTasks    int                    `json:"skipped_tasks"`
	CacheHitRate    float64                `json:"cache_hit_rate_percent"`
	TasksByType     map[string]int         `json:"tasks_by_type"`
	TaskTimings     map[string]float64     `json:"task_timings"`
	FailedTaskIDs   []string               `json:"failed_task_ids,omitempty"`
	CompileTasks    []*CompileCommandEntry `json:"-"` // For compile_commands.json generation
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
	// TasksByType is populated during graph building (total tasks count)
	// Here we only track execution details
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

	if !cacheHit {
		br.ExecutedTasks++
	}
}

// AddCompileCommand adds a compile command entry for compile_commands.json: its arguments, or
// the script of a tool that runs with a shell. The directory is the command's, else defaultDir.
func (br *BuildResult) AddCompileCommand(defaultDir string, command util.Command, file, output string) {
	entry := &CompileCommandEntry{Directory: command.Dir, Arguments: command.Args, File: file, Output: output}
	if entry.Directory == "" {
		entry.Directory = defaultDir
	}
	if command.Script != "" {
		entry.Command = command.Script
	}
	br.CompileTasks = append(br.CompileTasks, entry)
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

	util.LogInfo("Build report saved to %s", path)
	return nil
}

// PrintSummary prints a summary of the build to the terminal
func (br *BuildResult) PrintSummary() {
	const w = 60 // inner width between ║ borders

	line := func(fill string) string {
		return strings.Repeat(fill, w)
	}
	row := func(content string) string {
		pad := w - len(content)
		if pad < 0 {
			pad = 0
			content = content[:w]
		}
		return "║" + content + strings.Repeat(" ", pad) + "║"
	}
	heading := func(title string) string {
		content := "  " + title
		return row(content)
	}
	kv := func(label, value string) string {
		content := fmt.Sprintf("  %-18s%s", label, value)
		return row(content)
	}

	fmt.Println()
	fmt.Println("╔" + line("═") + "╗")
	fmt.Println(row(fmt.Sprintf("%*s", (w+len("BUILD REPORT"))/2, "BUILD REPORT")))
	fmt.Println("╠" + line("═") + "╣")

	status := "SUCCESS"
	if !br.Success {
		status = "FAILED"
	}
	fmt.Println(kv("Status:", status))
	fmt.Println(kv("Platform:", fmt.Sprintf("%s-%s", br.Platform, br.Architecture)))
	fmt.Println(kv("Configuration:", br.Configuration))
	if br.Toolchain != "" {
		fmt.Println(kv("Toolchain:", br.Toolchain))
	}

	fmt.Println("╠" + line("═") + "╣")
	fmt.Println(heading("TIMING"))
	fmt.Println("╠" + line("═") + "╣")
	fmt.Println(kv("Total Duration:", fmt.Sprintf("%.2fs", br.TotalDuration)))
	fmt.Println(kv("Task Generation:", fmt.Sprintf("%.2fs", br.TaskGenDuration)))
	fmt.Println(kv("Execution:", fmt.Sprintf("%.2fs", br.ExecDuration)))

	fmt.Println("╠" + line("═") + "╣")
	fmt.Println(heading("TASKS"))
	fmt.Println("╠" + line("═") + "╣")
	fmt.Println(kv("Total Tasks:", fmt.Sprintf("%d", br.TotalTasks)))
	fmt.Println(kv("Executed:", fmt.Sprintf("%d", br.ExecutedTasks)))
	fmt.Println(kv("Cache Hits:", fmt.Sprintf("%d (%.1f%%)", br.CacheHits, br.CacheHitRate)))
	fmt.Println(kv("Cache Misses:", fmt.Sprintf("%d", br.CacheMisses)))
	if br.FailedTasks > 0 {
		fmt.Println(kv("Failed:", fmt.Sprintf("%d", br.FailedTasks)))
	}

	if len(br.TasksByType) > 0 {
		fmt.Println("╠" + line("═") + "╣")
		fmt.Println(heading("TASKS BY TYPE"))
		fmt.Println("╠" + line("═") + "╣")

		types := make([]string, 0, len(br.TasksByType))
		for t := range br.TasksByType {
			types = append(types, t)
		}
		sort.Strings(types)

		for _, t := range types {
			fmt.Println(kv("  "+t+":", fmt.Sprintf("%d", br.TasksByType[t])))
		}
	}

	if len(br.FailedTaskIDs) > 0 {
		fmt.Println("╠" + line("═") + "╣")
		fmt.Println(heading("FAILED TASKS"))
		fmt.Println("╠" + line("═") + "╣")
		for _, taskID := range br.FailedTaskIDs {
			display := taskID
			maxLen := w - 6 // "║    ✗ " prefix uses 6 chars
			if len(display) > maxLen {
				display = display[:maxLen-3] + "..."
			}
			fmt.Println(row(fmt.Sprintf("    x %s", display)))
		}
	}

	fmt.Println("╚" + line("═") + "╝")
	fmt.Println()
}

// GenerateCompileCommands generates compile_commands.json from collected compile tasks
func (br *BuildResult) GenerateCompileCommands(path string) error {
	if len(br.CompileTasks) == 0 {
		util.LogInfo("No compile tasks to write to compile_commands.json")
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

	util.LogInfo("Generated compile_commands.json with %d entries", len(br.CompileTasks))
	return nil
}
