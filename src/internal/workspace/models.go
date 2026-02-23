package workspace

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"buildy/pkg/util"
)

// ResourceRequirements defines resource requirements for a task
type ResourceRequirements struct {
	CPUCores int `json:"cpu_cores"`
	MemoryMB int `json:"memory_mb"`
	DiskMB   int `json:"disk_mb"`
}

// NewResourceRequirements creates a ResourceRequirements with default values
func NewResourceRequirements() ResourceRequirements {
	return ResourceRequirements{
		CPUCores: 1,
		MemoryMB: 100,
		DiskMB:   10,
	}
}

// TaskInput represents an input file with content hash
type TaskInput struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// NewTaskInput creates a TaskInput and calculates its hash if the file exists
func NewTaskInput(path string) TaskInput {
	input := TaskInput{
		Path: path,
		Hash: "",
	}
	
	if _, err := os.Stat(path); err == nil {
		input.Hash = input.CalculateHash()
	}
	
	return input
}

// CalculateHash calculates SHA-256 hash of file content
func (t *TaskInput) CalculateHash() string {
	file, err := os.Open(t.Path)
	if err != nil {
		return "missing_file"
	}
	defer file.Close()
	
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "missing_file"
	}
	
	return fmt.Sprintf("%x", hash.Sum(nil))
}

// BuildTask represents an individual build task following Blizzard's parse/execute model
type BuildTask struct {
	TaskID               string               `json:"task_id"`
	TaskType             string               `json:"task_type"`
	Inputs               []TaskInput          `json:"inputs"`
	Outputs              []string             `json:"outputs"`
	Dependencies         []string             `json:"dependencies"`
	Command              string               `json:"command"`
	Platform             string               `json:"platform"`
	Architecture         string               `json:"architecture"`
	Configuration        string               `json:"configuration"`
	Toolchain            string               `json:"toolchain"`              // Toolchain identifier (e.g., "gcc-linux-13.2")
	EstimatedTime        float64              `json:"estimated_time"`
	ResourceRequirements ResourceRequirements `json:"resource_requirements"`
	CacheKey             string               `json:"cache_key"`
}

// NewBuildTask creates a BuildTask with default values
func NewBuildTask(taskID, taskType string, inputs []TaskInput, outputs []string, dependencies []string, command string) BuildTask {
	task := BuildTask{
		TaskID:               taskID,
		TaskType:             taskType,
		Inputs:               inputs,
		Outputs:              outputs,
		Dependencies:         dependencies,
		Command:              command,
		Platform:             "linux",
		Architecture:         "x86_64",
		Configuration:        "debug",
		EstimatedTime:        1.0,
		ResourceRequirements: NewResourceRequirements(),
		CacheKey:             "",
	}
	
	task.CacheKey = task.CalculateCacheKey()
	return task
}

// CalculateCacheKey calculates content-addressable cache key
func (t *BuildTask) CalculateCacheKey() string {
	// Build input tuples with normalized paths for consistent cache keys
	// On Windows, paths are case-insensitive, so we normalize to lowercase
	inputTuples := make([][2]string, len(t.Inputs))
	for i, inp := range t.Inputs {
		normalizedPath := util.NormalizePathForCache(inp.Path)
		inputTuples[i] = [2]string{normalizedPath, inp.Hash}
	}

	// Create content structure for hashing
	// Includes all factors that affect build output
	content := map[string]interface{}{
		"task_type":     t.TaskType,
		"inputs":        inputTuples,
		"command":       t.Command,
		"platform":      t.Platform,
		"architecture":  t.Architecture,
		"configuration": t.Configuration,
		"toolchain":     t.Toolchain, // Include toolchain to invalidate cache on compiler changes
	}

	// Serialize to JSON (Go's json.Marshal sorts keys by default)
	jsonBytes, err := json.Marshal(content)
	if err != nil {
		return ""
	}

	// Calculate SHA-256 hash
	hash := sha256.Sum256(jsonBytes)
	return fmt.Sprintf("%x", hash[:])
}

// UpdateInputHashesAndCacheKey updates input file hashes and recalculates the cache key
// This should be called before cache lookup when input files may have been created by dependencies
func (t *BuildTask) UpdateInputHashesAndCacheKey() error {
	for i := range t.Inputs {
		if t.Inputs[i].Hash == "" {
			// Calculate hash for input file if it exists
			if _, err := os.Stat(t.Inputs[i].Path); err == nil {
				hash, err := calculateFileHashForInput(t.Inputs[i].Path)
				if err != nil {
					return err
				}
				t.Inputs[i].Hash = hash
			}
		}
	}
	// Recalculate cache key with updated input hashes
	t.CacheKey = t.CalculateCacheKey()
	return nil
}

// calculateFileHashForInput calculates SHA-256 hash of a file for input hashing
func calculateFileHashForInput(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	h := sha256.New()
	buf := make([]byte, 8192)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

