package workspace

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// TaskIDGenerator generates unique task identifiers scoped to a module
type TaskIDGenerator struct {
	prefix   string
	counters map[string]int
}

// NewTaskIDGenerator creates a generator using the provided module path as prefix
func NewTaskIDGenerator(modulePath string) *TaskIDGenerator {
	cleanPath := strings.ReplaceAll(modulePath, string(filepath.Separator), "_")
	cleanPath = strings.ReplaceAll(cleanPath, "/", "_")
	cleanPath = strings.ReplaceAll(cleanPath, "\\", "_")
	prefix := sanitizeToken(cleanPath)
	if prefix == "" {
		prefix = "root"
	}

	return &TaskIDGenerator{
		prefix:   prefix,
		counters: make(map[string]int),
	}
}

// Next returns a unique task identifier given a task type and logical name
func (gen *TaskIDGenerator) Next(taskType, logicalName string) string {
	sanitizedType := sanitizeToken(taskType)
	if sanitizedType == "" {
		sanitizedType = "task"
	}
	sanitizedName := sanitizeToken(logicalName)

	key := sanitizedType + ":" + sanitizedName
	gen.counters[key]++
	count := gen.counters[key]

	base := sanitizedType
	if sanitizedName != "" {
		base = base + "_" + sanitizedName
	}

	id := fmt.Sprintf("%s_%03d", base, count)
	return fmt.Sprintf("%s__%s", id, gen.prefix)
}

func sanitizeToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, string(filepath.Separator), "_")
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, "\\", "_")
	value = nonAlphanumeric.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	return strings.ToLower(value)
}

// TaskIDRegistry maintains a bidirectional mapping between target names and task IDs
// This avoids fragile string parsing to extract target names from task IDs
type TaskIDRegistry struct {
	mu sync.RWMutex
	
	// targetToLinkTask maps target name -> link task ID
	targetToLinkTask map[string]string
	
	// taskToTarget maps task ID -> target name
	taskToTarget map[string]string
	
	// moduleTargets maps module path -> list of target names
	moduleTargets map[string][]string
	
	// targetOutputPath maps target name -> primary output file path
	targetOutputPath map[string]string
	
	// outputPathToTask maps normalized output path -> task ID (for all outputs including secondary)
	outputPathToTask map[string]string
}

// NewTaskIDRegistry creates a new TaskIDRegistry
func NewTaskIDRegistry() *TaskIDRegistry {
	return &TaskIDRegistry{
		targetToLinkTask: make(map[string]string),
		taskToTarget:     make(map[string]string),
		moduleTargets:    make(map[string][]string),
		targetOutputPath: make(map[string]string),
		outputPathToTask: make(map[string]string),
	}
}

// goos is the operating system for path normalization (injectable for testing)
var goos = runtime.GOOS

// normalizePathForLookup normalizes a path for case-insensitive lookup on Windows
func normalizePathForLookup(path string) string {
	// Clean the path to normalize separators
	cleaned := filepath.Clean(path)
	// On Windows, lowercase for case-insensitive comparison
	if goos == "windows" {
		cleaned = strings.ToLower(cleaned)
	}
	return cleaned
}

// RegisterTarget registers a target name to its link task ID
func (r *TaskIDRegistry) RegisterTarget(targetName, linkTaskID, modulePath string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	r.targetToLinkTask[targetName] = linkTaskID
	r.taskToTarget[linkTaskID] = targetName
	
	// Track targets per module
	r.moduleTargets[modulePath] = append(r.moduleTargets[modulePath], targetName)
}

// RegisterTargetWithOutput registers a target name with its link task ID and output path
func (r *TaskIDRegistry) RegisterTargetWithOutput(targetName, linkTaskID, modulePath, outputPath string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	r.targetToLinkTask[targetName] = linkTaskID
	r.taskToTarget[linkTaskID] = targetName
	r.targetOutputPath[targetName] = outputPath
	
	// Track targets per module
	r.moduleTargets[modulePath] = append(r.moduleTargets[modulePath], targetName)
}

// RegisterTargetOutputs registers all output paths for a task (including secondary outputs like import libraries)
func (r *TaskIDRegistry) RegisterTargetOutputs(linkTaskID string, outputs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	for _, output := range outputs {
		normalized := normalizePathForLookup(output)
		r.outputPathToTask[normalized] = linkTaskID
	}
}

// GetTargetOutputPath returns the output path for a target name
func (r *TaskIDRegistry) GetTargetOutputPath(targetName string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	outputPath, ok := r.targetOutputPath[targetName]
	return outputPath, ok
}

// GetLinkTaskID returns the link task ID for a target name
func (r *TaskIDRegistry) GetLinkTaskID(targetName string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	taskID, ok := r.targetToLinkTask[targetName]
	return taskID, ok
}

// GetTargetName returns the target name for a task ID
func (r *TaskIDRegistry) GetTargetName(taskID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	targetName, ok := r.taskToTarget[taskID]
	return targetName, ok
}

// GetModuleTargets returns all target names for a module
func (r *TaskIDRegistry) GetModuleTargets(modulePath string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	targets := r.moduleTargets[modulePath]
	// Return a copy to avoid races
	result := make([]string, len(targets))
	copy(result, targets)
	return result
}

// GetAllTargets returns all registered target names
func (r *TaskIDRegistry) GetAllTargets() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	targets := make([]string, 0, len(r.targetToLinkTask))
	for name := range r.targetToLinkTask {
		targets = append(targets, name)
	}
	return targets
}

// ResolveDependency resolves a dependency string to a task ID
// It handles both raw target names and already-resolved task IDs
func (r *TaskIDRegistry) ResolveDependency(dep string) (string, bool) {
	// If it looks like a task ID already, return it
	if isTaskID(dep) {
		return dep, true
	}
	
	// Otherwise, look it up as a target name
	return r.GetLinkTaskID(dep)
}

// GetTaskIDByOutputPath returns the link task ID for a given output file path
func (r *TaskIDRegistry) GetTaskIDByOutputPath(outputPath string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	// Normalize the path for lookup (handles case-insensitivity on Windows)
	normalized := normalizePathForLookup(outputPath)
	
	// First check the direct output path map (includes secondary outputs like import libraries)
	if taskID, ok := r.outputPathToTask[normalized]; ok {
		return taskID, true
	}
	
	// Fall back to searching target output paths (primary outputs only)
	for targetName, path := range r.targetOutputPath {
		if normalizePathForLookup(path) == normalized {
			taskID, ok := r.targetToLinkTask[targetName]
			return taskID, ok
		}
	}
	return "", false
}

// Clear removes all entries from the registry
func (r *TaskIDRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	r.targetToLinkTask = make(map[string]string)
	r.taskToTarget = make(map[string]string)
	r.moduleTargets = make(map[string][]string)
	r.targetOutputPath = make(map[string]string)
	r.outputPathToTask = make(map[string]string)
}

// isTaskID checks if a string looks like a task ID (rather than a target name)
func isTaskID(s string) bool {
	prefixes := []string{"setup_", "compile_", "link_", "build_", "copy_", "transform_", "generate_"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// Global registry instance for cross-module dependency resolution
var globalTaskRegistry = NewTaskIDRegistry()
