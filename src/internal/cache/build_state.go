package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// TaskResult represents the result of a completed task
type TaskResult struct {
	TaskID    string   `json:"task_id"`
	CacheKey  string   `json:"cache_key"`
	Success   bool     `json:"success"`
	Outputs   []string `json:"outputs"`
	Timestamp float64  `json:"timestamp"`
}

// BuildState represents persistent build state across invocations
type BuildState struct {
	LastBuildTime  float64                    `json:"last_build_time"`
	TaskGraphHash  string                     `json:"task_graph_hash"`  // Hash of the config + toolchain
	CompletedTasks map[string]map[string]interface{} `json:"completed_tasks"` // task_id -> result dict
	FileMtimes     map[string]float64         `json:"file_mtimes"`      // file_path -> modification time
	ConfigHash     string                     `json:"config_hash"`      // Hash of build configuration
	ToolchainHash  string                     `json:"toolchain_hash"`   // Hash of toolchain configuration
}

// NewBuildState creates a new BuildState with default values
func NewBuildState() *BuildState {
	return &BuildState{
		LastBuildTime:  0,
		TaskGraphHash:  "",
		CompletedTasks: make(map[string]map[string]interface{}),
		FileMtimes:     make(map[string]float64),
		ConfigHash:     "",
		ToolchainHash:  "",
	}
}

// Save saves build state to disk
func (bs *BuildState) Save(stateFile string) error {
	// Ensure parent directory exists
	dir := filepath.Dir(stateFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}
	
	// Marshal to JSON
	data, err := json.MarshalIndent(bs, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal build state: %w", err)
	}
	
	// Write to file
	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write build state: %w", err)
	}
	
	log.Printf("Saved build state to %s", stateFile)
	return nil
}

// LoadBuildState loads build state from disk
func LoadBuildState(stateFile string) (*BuildState, error) {
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		log.Printf("No build state found at %s", stateFile)
		return nil, nil
	}
	
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read build state: %w", err)
	}
	
	var state BuildState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse build state: %w", err)
	}
	
	// Handle missing fields for backward compatibility
	if state.CompletedTasks == nil {
		state.CompletedTasks = make(map[string]map[string]interface{})
	}
	if state.FileMtimes == nil {
		state.FileMtimes = make(map[string]float64)
	}
	
	log.Printf("Loaded build state from %s", stateFile)
	return &state, nil
}

// HashConfig generates hash of configuration
func HashConfig(config map[string]interface{}) (string, error) {
	// Convert config to stable JSON string (sorted keys)
	configJSON, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("failed to marshal config: %w", err)
	}
	
	hash := sha256.Sum256(configJSON)
	return fmt.Sprintf("%x", hash[:]), nil
}

// HashFile generates hash of a file
func HashFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()
	
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}
	
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

