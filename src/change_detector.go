package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// ChangeSet represents the set of changes detected since last build
type ChangeSet struct {
	ConfigChanged    bool
	ToolchainChanged bool
	ModifiedFiles    []string
	NewFiles         []string
	DeletedFiles     []string
}

// HasChanges checks if any changes were detected
func (cs *ChangeSet) HasChanges() bool {
	return cs.ConfigChanged ||
		cs.ToolchainChanged ||
		len(cs.ModifiedFiles) > 0 ||
		len(cs.NewFiles) > 0 ||
		len(cs.DeletedFiles) > 0
}

// RequiresFullRebuild checks if changes require a full rebuild
func (cs *ChangeSet) RequiresFullRebuild() bool {
	return cs.ConfigChanged || cs.ToolchainChanged
}

// ChangeDetector detects changes since last build
type ChangeDetector struct {
	buildState *BuildState
	cache      *BuildCache
}

// NewChangeDetector creates a new change detector
func NewChangeDetector(buildState *BuildState, cache *BuildCache) *ChangeDetector {
	return &ChangeDetector{
		buildState: buildState,
		cache:      cache,
	}
}

// IsCachedResultValid validates that the cached results for a task remain usable
func (cd *ChangeDetector) IsCachedResultValid(task *BuildTask) bool {
	if cd == nil || cd.cache == nil || task == nil {
		return false
	}

	cd.cache.mutex.RLock()
	cacheEntry, exists := cd.cache.cacheIndex[task.CacheKey]
	cd.cache.mutex.RUnlock()
	if !exists {
		log.Printf("Cache miss for %s: no cache entry found", task.TaskID)
		return false
	}

	// Ensure all outputs still exist and match expected hashes
	for outputPath, expectedHash := range cacheEntry.Outputs {
		if _, err := os.Stat(outputPath); os.IsNotExist(err) {
			log.Printf("Cache miss for %s: output %s not found", task.TaskID, outputPath)
			return false
		}

		if expectedHash == "directory" {
			continue
		}

		actualHash, err := cd.cache.calculateFileHash(outputPath)
		if err != nil || actualHash != expectedHash {
			log.Printf("Cache miss for %s: output %s hash changed", task.TaskID, outputPath)
			return false
		}
	}

	// For compile tasks, we require header dependency information to be present
	// If no header hashes are recorded, this is a first-time build and we must
	// rebuild to capture header dependencies (Option C)
	if task.TaskType == "compile" {
		if len(cacheEntry.HeaderHashes) == 0 && len(cacheEntry.HeaderDependencies) == 0 {
			log.Printf("Cache miss for %s: no header dependencies recorded (first build)", task.TaskID)
			return false
		}

		// Validate header dependencies
		for headerPath, expectedHash := range cacheEntry.HeaderHashes {
			if _, err := os.Stat(headerPath); os.IsNotExist(err) {
				log.Printf("Cache miss for %s: header %s deleted", task.TaskID, headerPath)
				return false
			}

			actualHash, err := cd.cache.calculateFileHash(headerPath)
			if err != nil || actualHash != expectedHash {
				log.Printf("Cache miss for %s: header %s modified", task.TaskID, headerPath)
				return false
			}
		}

		log.Printf("Cache hit for %s: %d headers unchanged", task.TaskID, len(cacheEntry.HeaderHashes))
	}

	return true
}

// DetectChanges detects what changed since last build
func (cd *ChangeDetector) DetectChanges(configFile string, currentConfig map[string]interface{}, currentToolchainHash string) (*ChangeSet, error) {
	changes := &ChangeSet{
		ModifiedFiles: make([]string, 0),
		NewFiles:      make([]string, 0),
		DeletedFiles:  make([]string, 0),
	}

	// Check config hash
	configHash, err := HashConfig(currentConfig)
	if err != nil {
		return nil, err
	}

	if configHash != cd.buildState.ConfigHash {
		log.Println("Configuration changed")
		changes.ConfigChanged = true
		return changes, nil // Full rebuild needed
	}

	// Check toolchain hash
	// Compare hashes - any difference triggers rebuild
	if currentToolchainHash != cd.buildState.ToolchainHash {
		log.Println("Toolchain changed")
		// Only trigger rebuild if at least one side has a toolchain hash
		if currentToolchainHash != "" || cd.buildState.ToolchainHash != "" {
			changes.ToolchainChanged = true
			return changes, nil // Full rebuild needed
		}
	}

	// Check file modifications
	for filePath, oldMtime := range cd.buildState.FileMtimes {
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			changes.DeletedFiles = append(changes.DeletedFiles, filePath)
			log.Printf("Deleted: %s", filePath)
			continue
		}

		info, err := os.Stat(filePath)
		if err != nil {
			continue
		}

		currentMtime := float64(info.ModTime().Unix())
		if currentMtime > oldMtime {
			changes.ModifiedFiles = append(changes.ModifiedFiles, filePath)
			log.Printf("Modified: %s", filePath)
		}
	}

	// Check for new files
	currentFiles := cd.getAllSourceFiles(currentConfig)
	oldFiles := make(map[string]bool)
	for file := range cd.buildState.FileMtimes {
		oldFiles[file] = true
	}

	for file := range currentFiles {
		if !oldFiles[file] {
			changes.NewFiles = append(changes.NewFiles, file)
		}
	}

	if len(changes.NewFiles) > 0 {
		log.Printf("New files: %d", len(changes.NewFiles))
	}

	return changes, nil
}

// GetAffectedTasks gets task IDs that need to be rebuilt
func (cd *ChangeDetector) GetAffectedTasks(changes *ChangeSet, taskGraph *TaskGraph) map[string]bool {
	affected := make(map[string]bool)

	// Direct dependencies: tasks that use modified files as inputs
	for taskID, task := range taskGraph.Tasks {
		for _, inputFile := range task.Inputs {
			if contains(changes.ModifiedFiles, inputFile.Path) {
				affected[taskID] = true
				log.Printf("Task %s affected by modified input %s", taskID, inputFile.Path)
			} else if contains(changes.NewFiles, inputFile.Path) {
				affected[taskID] = true
				log.Printf("Task %s affected by new input %s", taskID, inputFile.Path)
			}
		}
	}

	// Header dependencies: check .d files
	for _, modifiedFile := range changes.ModifiedFiles {
		if isHeaderFile(modifiedFile) {
			headerTasks := cd.findTasksUsingHeader(modifiedFile)
			for taskID := range headerTasks {
				affected[taskID] = true
			}
			if len(headerTasks) > 0 {
				log.Printf("Header %s affects %d tasks", modifiedFile, len(headerTasks))
			}
		}
	}

	// Transitive dependencies: tasks that depend on affected tasks
	originalAffected := make(map[string]bool)
	for taskID := range affected {
		originalAffected[taskID] = true
	}

	for taskID := range originalAffected {
		dependentTasks := cd.getDependentTasks(taskID, taskGraph)
		for depTaskID := range dependentTasks {
			affected[depTaskID] = true
		}
		if len(dependentTasks) > 0 {
			log.Printf("Task %s has %d dependent tasks", taskID, len(dependentTasks))
		}
	}

	return affected
}

// isHeaderFile checks if file is a header file
func isHeaderFile(filePath string) bool {
	headerExtensions := []string{".h", ".hpp", ".hxx", ".hh", ".H", ".inl", ".inc"}
	for _, ext := range headerExtensions {
		if strings.HasSuffix(filePath, ext) {
			return true
		}
	}
	return false
}

// findTasksUsingHeader finds all tasks that depend on a header file
func (cd *ChangeDetector) findTasksUsingHeader(headerPath string) map[string]bool {
	affected := make(map[string]bool)

	// Normalize the header path for comparison
	headerPathNormalized := filepath.Clean(headerPath)

	cd.cache.mutex.RLock()
	defer cd.cache.mutex.RUnlock()

	for _, cacheEntry := range cd.cache.cacheIndex {
		// Normalize each dependency path for comparison
		for _, dep := range cacheEntry.HeaderDependencies {
			depNormalized := filepath.Clean(dep)
			if depNormalized == headerPathNormalized {
				if cacheEntry.TaskID != "" {
					affected[cacheEntry.TaskID] = true
				}
				break
			}
		}
	}

	return affected
}

// getDependentTasks gets all tasks that depend on the given task
func (cd *ChangeDetector) getDependentTasks(taskID string, taskGraph *TaskGraph) map[string]bool {
	dependent := make(map[string]bool)

	// Get the task's outputs
	task, exists := taskGraph.Tasks[taskID]
	if !exists {
		return dependent
	}

	// Build set of task outputs
	taskOutputs := make(map[string]bool)
	for _, out := range task.Outputs {
		taskOutputs[out] = true
	}

	// Find tasks that use these outputs as inputs
	for otherID, otherTask := range taskGraph.Tasks {
		if otherID == taskID {
			continue
		}

		// Build set of other task's inputs
		otherInputs := make(map[string]bool)
		for _, inp := range otherTask.Inputs {
			otherInputs[inp.Path] = true
		}

		// Check for intersection
		hasIntersection := false
		for output := range taskOutputs {
			if otherInputs[output] {
				hasIntersection = true
				break
			}
		}

		if hasIntersection {
			dependent[otherID] = true
			// Recursively get dependents
			recursiveDeps := cd.getDependentTasks(otherID, taskGraph)
			for depID := range recursiveDeps {
				dependent[depID] = true
			}
		}
	}

	return dependent
}

// getAllSourceFiles gets all source files from configuration
func (cd *ChangeDetector) getAllSourceFiles(config map[string]interface{}) map[string]bool {
	files := make(map[string]bool)

	// Traverse config to find file references
	var collectFiles func(obj interface{})
	collectFiles = func(obj interface{}) {
		switch v := obj.(type) {
		case map[string]interface{}:
			for key, value := range v {
				if key == "sources" || key == "inputs" || key == "files" {
					switch val := value.(type) {
					case []interface{}:
						for _, item := range val {
							if str, ok := item.(string); ok {
								files[str] = true
							}
						}
					case string:
						files[val] = true
					}
				} else {
					collectFiles(value)
				}
			}
		case []interface{}:
			for _, item := range v {
				collectFiles(item)
			}
		}
	}

	collectFiles(config)
	return files
}

// contains checks if a string slice contains a value
func contains(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}
