package workspace

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"buildy/pkg/util"
)

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
	NativeArchive  = "archive"  // pack the Inputs, files under Root, into the archive Outputs[0]
)

// NativeOp describes a task's native operation, for its cache key and the tasks file. The paths
// it works on are the task's input and output.
type NativeOp struct {
	Op             string      `json:"op"`
	Substitutions  [][2]string `json:"substitutions,omitempty"`   // template: @KEY@ and ${KEY} -> value, in order
	Root           string      `json:"root,omitempty"`            // archive: the directory entry names are relative to
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
	TaskID        string        `json:"task_id"`
	TaskNumber    int           `json:"task_number"` // Sequential task number for logging (1-based)
	TaskType      string        `json:"task_type"`
	Inputs        []TaskInput   `json:"inputs"`
	Outputs       []string      `json:"outputs"`
	DepFile       *DepFile      `json:"dep_file,omitempty"`
	ToolCached    bool          `json:"tool_cached,omitempty"` // The tool keeps its own cache: always run, never a buildy cache hit
	Dependencies  []string      `json:"dependencies"`
	LinkLibs      []string      `json:"link_libs,omitempty"` // Libraries a link task links, by name (workspace targets or system libraries)
	Module        string        `json:"module"`              // Module that defined the task ("" for the workspace root config)
	Target        string        `json:"target,omitempty"`    // Target or artifact the task belongs to
	Command       util.Command  `json:"command,omitzero"`    // What the task runs, and where
	Native        *NativeOp     `json:"native,omitempty"`    // Set instead of Command for work buildy does itself
	Platform      string        `json:"platform"`
	Architecture  string        `json:"architecture"`
	Configuration string        `json:"configuration"`
	Toolchain     string        `json:"toolchain"` // Name of the toolchain that produced the task
	Exec          util.ExecSpec `json:"exec"`      // Where the command runs (native or docker), from that toolchain
	Tool          string        `json:"-"`         // What the command runs (ToolIdentity), filled in when the task is about to run
	CacheKey      string        `json:"cache_key"`
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
	task := NewBuildTask(taskID, taskType, inputs, outputs, dependencies, util.Command{})
	task.Native = &op
	return task
}

// NewBuildTask creates a BuildTask with default values
func NewBuildTask(taskID, taskType string, inputs []TaskInput, outputs []string, dependencies []string, command util.Command) BuildTask {
	return BuildTask{
		TaskID:        taskID,
		TaskType:      taskType,
		Inputs:        inputs,
		Outputs:       outputs,
		Dependencies:  dependencies,
		Command:       command,
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "debug",
	}
}

// cacheKeyContent is what a task's cache key is a hash of: exactly what determines its outputs.
// The configuration reaches a task only through these (its flags, defines, include and library
// directories, standard and output paths are in its command and outputs), so a change to one
// target's config reruns that target's tasks and no others.
type cacheKeyContent struct {
	Inputs  [][2]string       `json:"inputs"`  // each declared input's path and content hash
	Outputs []string          `json:"outputs"` // where the outputs go
	Command util.Command      `json:"command"` // the arguments (or script) and directory
	Native  *NativeOp         `json:"native"`  // or the native operation
	Exec    util.ExecSpec     `json:"exec"`    // natively or in which image, with which environment
	Tool    string            `json:"tool"`    // the program the command runs: its path and content, or the image's ID
	Env     map[string]string `json:"env"`     // the values of the variables the toolchain reads
}

// CalculateCacheKey calculates the content-addressable cache key from the task's definition and
// its input hashes. Call HashInputs first.
func (t *BuildTask) CalculateCacheKey() string {
	// Paths are normalized: on Windows they are case-insensitive
	content := cacheKeyContent{
		Inputs:  make([][2]string, len(t.Inputs)),
		Outputs: make([]string, len(t.Outputs)),
		Command: t.Command,
		Native:  t.Native,
		Exec:    t.Exec,
		Tool:    t.Tool,
		Env:     t.Exec.EnvValues(),
	}
	for i, inp := range t.Inputs {
		content.Inputs[i] = [2]string{util.NormalizePathForCache(inp.Path), inp.Hash}
	}
	for i, out := range t.Outputs {
		content.Outputs[i] = util.NormalizePathForCache(out)
	}
	data, err := json.Marshal(content)
	if err != nil {
		panic(fmt.Sprintf("task %s: cannot marshal its cache key: %v", t.TaskID, err))
	}
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:])
}

// HashInputs hashes every declared input with hash, which hashes a file, and computes the cache
// key. Every input must exist: the executor calls it once the task's producers have run.
func (t *BuildTask) HashInputs(hash func(path string) (string, error)) error {
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
		var h string
		if info.IsDir() {
			// A directory input (a Go package, a Cargo crate) hashes as a tree
			h, err = directoryHash(path, hash)
		} else {
			h, err = hash(path)
		}
		if err != nil {
			return fmt.Errorf("input %s: %w", path, err)
		}
		t.Inputs[i].Hash = h
	}
	t.CacheKey = t.CalculateCacheKey()
	return nil
}

// directoryHash hashes a directory's files: each one's path relative to it and its hash, sorted.
func directoryHash(dirPath string, hash func(path string) (string, error)) (string, error) {
	var entries []string
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return err
		}
		fileHash, err := hash(path)
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
	sort.Strings(entries)
	h := sha256.New()
	for _, entry := range entries {
		h.Write([]byte(entry))
		h.Write([]byte("\n"))
	}
	return fmt.Sprintf("dir:%x", h.Sum(nil)), nil
}
