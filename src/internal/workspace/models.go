package workspace

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// TaskInput is a file (or directory) a task reads. Hash is filled in by HashInputs when the task
// is about to run, after its producers have run; a "symlink:" hash marks an input whose path,
// not content, is what matters.
type TaskInput struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// NewTaskInput declares an input file. It is hashed when the task runs, not now.
func NewTaskInput(path string) TaskInput {
	return TaskInput{Path: path}
}

// DepFile is a dependency file a task's tool writes: the inputs it discovered while running,
// such as the headers a compile included.
type DepFile struct {
	Path   string `json:"path"`
	Format string `json:"format"` // resource.DepFormatMake or resource.DepFormatMSVCJSON
}

// Native operations: work buildy does itself, in Go, instead of running a command.
const (
	NativeCopy     = "copy"     // copy Inputs[0] to Outputs[0]
	NativeSymlink  = "symlink"  // make Outputs[0] a symlink to Inputs[0]
	NativeTemplate = "template" // fill the template file Inputs[0] in, into Outputs[0]
	NativeArchive  = "archive"  // pack the directory Inputs[0] into the archive Outputs[0]
)

// NativeOp describes a task's native operation, for its cache key and the tasks file. The paths
// it works on are the task's input and output.
type NativeOp struct {
	Op             string      `json:"op"`
	Substitutions  [][2]string `json:"substitutions,omitempty"`   // template: @KEY@ and ${KEY} -> value, in order
	Format         string      `json:"format,omitempty"`          // archive: tar, tar.gz or zip
	FollowSymlinks bool        `json:"follow_symlinks,omitempty"` // archive: store what links point to
}

// BuildTask represents an individual build task following Blizzard's parse/execute model.
//
// The contract: Inputs are every file the task reads that can be named before it runs, including
// every upstream output it consumes; Outputs are the files this task, and only this task,
// produces; DepFile names the inputs the tool reports after running. The executor checks the
// inputs exist before running and the outputs exist after.
type BuildTask struct {
	TaskID               string               `json:"task_id"`
	TaskNumber           int                  `json:"task_number"` // Sequential task number for logging (1-based)
	TaskType             string               `json:"task_type"`
	Inputs               []TaskInput          `json:"inputs"`
	Outputs              []string             `json:"outputs"`
	DepFile              *DepFile             `json:"dep_file,omitempty"`
	ToolCached           bool                 `json:"tool_cached,omitempty"` // The tool keeps its own cache: always run, never a buildy cache hit
	Dependencies         []string             `json:"dependencies"`
	LinkLibs             []string             `json:"link_libs,omitempty"` // Libraries a link task links, by name (workspace targets or system libraries)
	Module               string               `json:"module"`              // Module that defined the task ("" for the workspace root config)
	Target               string               `json:"target,omitempty"`    // Target or artifact the task belongs to
	Command              string               `json:"command,omitempty"`
	Native               *NativeOp            `json:"native,omitempty"` // Set instead of Command for work buildy does itself
	Platform             string               `json:"platform"`
	Architecture         string               `json:"architecture"`
	Configuration        string               `json:"configuration"`
	Toolchain            string               `json:"toolchain"`      // Name of the toolchain that produced the task
	ToolchainHash        string               `json:"toolchain_hash"` // Content hash of that toolchain's config
	Exec                 util.ExecSpec        `json:"exec"`           // Where the command runs (native or docker), from that toolchain
	ConfigHash           string               `json:"config_hash"`    // Hash of buildy.yaml to invalidate cache on config changes
	EstimatedTime        float64              `json:"estimated_time"`
	ResourceRequirements ResourceRequirements `json:"resource_requirements"`
	CacheKey             string               `json:"cache_key"`
}

// Describe names the task with the module and target it came from, for error messages.
func (t *BuildTask) Describe() string {
	module := t.Module
	if module == "" {
		module = "workspace"
	}
	if t.Target == "" {
		return fmt.Sprintf("%s (module %s)", t.TaskID, module)
	}
	return fmt.Sprintf("%s (module %s, target %s)", t.TaskID, module, t.Target)
}

// NewNativeTask creates a task that buildy carries out itself.
func NewNativeTask(taskID, taskType string, inputs []TaskInput, outputs []string, dependencies []string, op NativeOp) BuildTask {
	task := NewBuildTask(taskID, taskType, inputs, outputs, dependencies, "")
	task.Native = &op
	return task
}

// NewBuildTask creates a BuildTask with default values
func NewBuildTask(taskID, taskType string, inputs []TaskInput, outputs []string, dependencies []string, command string) BuildTask {
	return BuildTask{
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
	}
}

// CalculateCacheKey calculates the content-addressable cache key from the task's definition and
// its input hashes. Call HashInputs first.
func (t *BuildTask) CalculateCacheKey() string {
	// Build input tuples with normalized paths for consistent cache keys
	// On Windows, paths are case-insensitive, so we normalize to lowercase
	inputTuples := make([][2]string, len(t.Inputs))
	for i, inp := range t.Inputs {
		normalizedPath := util.NormalizePathForCache(inp.Path)
		inputTuples[i] = [2]string{normalizedPath, inp.Hash}
	}
	outputs := make([]string, len(t.Outputs))
	for i, out := range t.Outputs {
		outputs[i] = util.NormalizePathForCache(out)
	}

	// Create content structure for hashing
	// Includes all factors that affect build output
	content := map[string]interface{}{
		"task_type":      t.TaskType,
		"inputs":         inputTuples,
		"outputs":        outputs,
		"command":        t.Command,
		"native":         t.Native,
		"platform":       t.Platform,
		"architecture":   t.Architecture,
		"configuration":  t.Configuration,
		"toolchain_hash": t.ToolchainHash,
		"exec":           t.Exec,
		"config_hash":    t.ConfigHash,
	}

	// Serialize to JSON (Go's json.Marshal sorts keys by default)
	jsonBytes, err := json.Marshal(content)
	if err != nil {
		panic(fmt.Sprintf("task %s: cannot marshal its cache key: %v", t.TaskID, err))
	}

	// Calculate SHA-256 hash
	hash := sha256.Sum256(jsonBytes)
	return fmt.Sprintf("%x", hash[:])
}

// HashInputs hashes every declared input and computes the cache key. Every input must exist:
// the executor calls it once the task's producers have run.
func (t *BuildTask) HashInputs() error {
	for i := range t.Inputs {
		path := t.Inputs[i].Path
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("input %s: %w", path, err)
		}
		// Symlink inputs: the link points at the path, so the path is what is keyed
		if strings.HasPrefix(t.Inputs[i].Hash, "symlink:") {
			continue
		}
		var hash string
		if info.IsDir() {
			// Directory inputs (install tasks packaging a staging directory) hash as a tree
			hash, err = calculateDirectoryHash(path)
		} else {
			hash, err = calculateFileHashForInput(path)
		}
		if err != nil {
			return fmt.Errorf("input %s: %w", path, err)
		}
		t.Inputs[i].Hash = hash
	}
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

// calculateDirectoryHash calculates a hash of directory contents
// This includes all file paths and their content hashes, sorted for determinism
func calculateDirectoryHash(dirPath string) (string, error) {
	var entries []string

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Get relative path for consistent hashing
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return err
		}

		// Calculate file hash
		fileHash, err := calculateFileHashForInput(path)
		if err != nil {
			return err
		}

		// Use forward slashes for cross-platform consistency
		entries = append(entries, filepath.ToSlash(relPath)+":"+fileHash)
		return nil
	})
	if err != nil {
		return "", err
	}

	// Sort for deterministic ordering
	sort.Strings(entries)

	// Hash the combined entries
	h := sha256.New()
	for _, entry := range entries {
		h.Write([]byte(entry))
		h.Write([]byte("\n"))
	}
	return fmt.Sprintf("dir:%x", h.Sum(nil)), nil
}
