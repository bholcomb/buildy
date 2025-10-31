package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	// Build input tuples
	inputTuples := make([][2]string, len(t.Inputs))
	for i, inp := range t.Inputs {
		inputTuples[i] = [2]string{inp.Path, inp.Hash}
	}
	
	// Create content structure matching Python version
	content := map[string]interface{}{
		"task_type":     t.TaskType,
		"inputs":        inputTuples,
		"command":       t.Command,
		"platform":      t.Platform,
		"architecture":  t.Architecture,
		"configuration": t.Configuration,
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

