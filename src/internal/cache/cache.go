package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"buildy/internal/workspace"
	"buildy/pkg/util"
)

// missingHash records a discovered input that did not exist when the entry was written: if it
// appears later, the task must run again.
const missingHash = "missing"

// CacheEntry records a successful run of a task: the hash of every declared output and of every
// input its dependency file reported.
type CacheEntry struct {
	TaskID        string            `json:"task_id"`
	TaskType      string            `json:"task_type"`
	CachedAt      float64           `json:"cached_at"`
	ExecutionTime float64           `json:"execution_time"`
	Outputs       map[string]string `json:"outputs"`              // normalized path -> hash
	DepInputs     map[string]string `json:"dep_inputs,omitempty"` // path -> hash, or "missing"
	Platform      string            `json:"platform"`
	Architecture  string            `json:"architecture"`
	Configuration string            `json:"configuration"`
}

// BuildCache maps cache keys to the recorded results of tasks. A lookup is a hit only when the
// recorded outputs are all still there, unchanged, and no discovered input changed: the cache
// stores no copies of outputs, it decides whether a task needs to run.
type BuildCache struct {
	cacheDir       string
	cacheIndexFile string
	cacheIndex     map[string]CacheEntry
	mutex          sync.RWMutex // For thread-safe cache index access
}

// NewBuildCache opens the cache in cacheDir, creating it if needed.
func NewBuildCache(cacheDir string) (*BuildCache, error) {
	if cacheDir == "" {
		return nil, fmt.Errorf("no cache directory given")
	}

	cache := &BuildCache{
		cacheDir:       cacheDir,
		cacheIndexFile: filepath.Join(cacheDir, "cache_index.json"),
		cacheIndex:     make(map[string]CacheEntry),
	}

	if err := os.MkdirAll(cache.cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	if err := cache.loadCacheIndex(); err != nil {
		return nil, err
	}

	return cache, nil
}

// CacheDir returns the cache directory path
func (bc *BuildCache) CacheDir() string {
	return bc.cacheDir
}

// loadCacheIndex loads cache index from disk
func (bc *BuildCache) loadCacheIndex() error {
	bc.mutex.Lock()
	defer bc.mutex.Unlock()

	data, err := os.ReadFile(bc.cacheIndexFile)
	if os.IsNotExist(err) {
		return nil // No cache index yet, that's fine
	}
	if err != nil {
		return fmt.Errorf("failed to read cache index %s: %w", bc.cacheIndexFile, err)
	}

	if err := json.Unmarshal(data, &bc.cacheIndex); err != nil {
		return fmt.Errorf("failed to parse cache index %s (delete it to start a fresh cache): %w", bc.cacheIndexFile, err)
	}

	return nil
}

// saveCacheIndexLocked saves cache index to disk atomically
// IMPORTANT: Caller must hold at least a read lock on bc.mutex
func (bc *BuildCache) saveCacheIndexLocked() error {
	// Create temp file
	tempFile, err := os.CreateTemp(bc.cacheDir, "cache_index_*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()

	// Write JSON
	encoder := json.NewEncoder(tempFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(bc.cacheIndex); err != nil {
		tempFile.Close()
		os.Remove(tempPath)
		return fmt.Errorf("failed to encode cache index: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempPath, bc.cacheIndexFile); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// Lookup reports whether the task's recorded result is still valid, and if not, why. The task's
// inputs must be hashed (BuildTask.HashInputs). A hit needs an entry for the task's key, every
// declared output present with its recorded hash, and every input the dependency file reported
// unchanged (a discovered input that was missing must still be missing).
func (bc *BuildCache) Lookup(task *workspace.BuildTask) (bool, string) {
	bc.mutex.RLock()
	entry, exists := bc.cacheIndex[task.CacheKey]
	bc.mutex.RUnlock()
	if !exists {
		return false, "no cache entry for its inputs and command"
	}

	for _, output := range task.Outputs {
		recorded, ok := entry.Outputs[util.NormalizePath(output)]
		if !ok {
			return false, fmt.Sprintf("output %s was not recorded", output)
		}
		if _, err := os.Stat(output); err != nil {
			return false, fmt.Sprintf("output %s is missing", output)
		}
		actual, err := util.HashFile(output)
		if err != nil || actual != recorded {
			return false, fmt.Sprintf("output %s changed", output)
		}
	}

	for path, recorded := range entry.DepInputs {
		_, statErr := os.Stat(path)
		if recorded == missingHash {
			if statErr == nil {
				return false, fmt.Sprintf("%s now exists", path)
			}
			continue
		}
		if statErr != nil {
			return false, fmt.Sprintf("%s was removed", path)
		}
		actual, err := util.HashFile(path)
		if err != nil || actual != recorded {
			return false, fmt.Sprintf("%s changed", path)
		}
	}

	return true, ""
}

// Record writes the cache entry for a task that just succeeded. Every declared output must exist,
// and a declared dependency file must exist and parse; otherwise the task did not do what it
// declared, and Record fails without writing an entry. Relative paths in the dependency file are
// relative to workDir, where the task ran.
func (bc *BuildCache) Record(task *workspace.BuildTask, executionTime float64, workDir string) error {
	outputs := make(map[string]string, len(task.Outputs))
	for _, output := range task.Outputs {
		hash, err := util.HashFile(output)
		if err != nil {
			return fmt.Errorf("declared output %s: %w", output, err)
		}
		outputs[util.NormalizePath(output)] = hash
	}

	depInputs, err := discoveredInputs(task, workDir)
	if err != nil {
		return err
	}

	bc.mutex.Lock()
	defer bc.mutex.Unlock()
	bc.cacheIndex[task.CacheKey] = CacheEntry{
		TaskID:        task.TaskID,
		TaskType:      task.TaskType,
		CachedAt:      float64(time.Now().Unix()),
		ExecutionTime: executionTime,
		Outputs:       outputs,
		DepInputs:     depInputs,
		Platform:      task.Platform,
		Architecture:  task.Architecture,
		Configuration: task.Configuration,
	}
	return bc.saveCacheIndexLocked()
}

// discoveredInputs reads the task's dependency file and hashes every input it lists, apart from
// the task's declared inputs (the source), which are already part of the key.
func discoveredInputs(task *workspace.BuildTask, workDir string) (map[string]string, error) {
	if task.DepFile == nil {
		return nil, nil
	}
	data, err := os.ReadFile(task.DepFile.Path)
	if err != nil {
		return nil, fmt.Errorf("dependency file %s: %w", task.DepFile.Path, err)
	}
	paths, err := parseDepFile(data, task.DepFile.Format)
	if err != nil {
		return nil, fmt.Errorf("dependency file %s: %w", task.DepFile.Path, err)
	}

	declared := make(map[string]bool, len(task.Inputs))
	for _, input := range task.Inputs {
		declared[util.NormalizePath(absPath(input.Path, workDir))] = true
	}

	hashes := make(map[string]string, len(paths))
	for _, p := range paths {
		p = absPath(p, workDir)
		if declared[util.NormalizePath(p)] {
			continue
		}
		hash, err := util.HashFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			hash = missingHash
		} else if err != nil {
			return nil, fmt.Errorf("dependency file %s lists %s: %w", task.DepFile.Path, p, err)
		}
		hashes[p] = hash
	}
	return hashes, nil
}

func absPath(path, workDir string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(workDir, path)
}

// GetCacheStats returns cache statistics
func (bc *BuildCache) GetCacheStats() map[string]interface{} {
	bc.mutex.RLock()
	totalEntries := len(bc.cacheIndex)
	bc.mutex.RUnlock()

	return map[string]interface{}{
		"total_entries":   totalEntries,
		"cache_directory": bc.cacheDir,
	}
}
