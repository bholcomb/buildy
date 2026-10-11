package cache

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"buildy/internal/workspace"
	"buildy/pkg/util"
)

// missingHash records a discovered input that did not exist when the entry was written: if it
// appears later, the task must run again.
const missingHash = "missing"

// The cache's files in the cache directory: a snapshot of every entry, written at the end of a
// run, and a journal each finished task appends its entry to. Loading replays the journal over
// the snapshot, so an interrupted run keeps the entries of the tasks that finished.
const (
	indexFile   = "cache_index.json"
	journalFile = "cache_journal.jsonl"

	// indexVersion changes when the cache key or the entry format does: an index of another
	// version is discarded, and every task runs again.
	indexVersion = 2
)

// CacheEntry records a successful run of a task: the hash of every declared output and of every
// input its dependency file reported.
type CacheEntry struct {
	Key       string            `json:"key"`                  // the task's cache key
	TaskID    string            `json:"task_id"`              // with Context, which task: compaction keeps its newest entry
	Context   string            `json:"context"`              // platform-architecture-configuration
	Outputs   map[string]string `json:"outputs"`              // normalized path -> hash
	DepInputs map[string]string `json:"dep_inputs,omitempty"` // path -> hash, or "missing"
}

// snapshot is the format of the index file.
type snapshot struct {
	Version int          `json:"version"`
	Entries []CacheEntry `json:"entries"`
}

// BuildCache maps cache keys to the recorded results of tasks. A lookup is a hit only when the
// recorded outputs are all still there, unchanged, and no discovered input changed: the cache
// stores no copies of outputs, it decides whether a task needs to run.
//
// Recording an entry appends one line to the journal (no fsync: a lost entry only makes its task
// run again). Close compacts: it writes the snapshot with the newest entry of each task and
// context, and removes the journal.
type BuildCache struct {
	cacheDir string
	mutex    sync.Mutex
	entries  map[string]CacheEntry // cache key -> entry
	newest   map[string]string     // task ID and context -> the key of its newest entry
	journal  *os.File              // opened for appending at the first Record
	dirty    bool                  // the snapshot is out of date: the journal has entries
}

// NewBuildCache opens the cache in cacheDir, creating it if needed.
func NewBuildCache(cacheDir string) (*BuildCache, error) {
	if cacheDir == "" {
		return nil, fmt.Errorf("no cache directory given")
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}
	bc := &BuildCache{
		cacheDir: cacheDir,
		entries:  make(map[string]CacheEntry),
		newest:   make(map[string]string),
	}
	if err := bc.loadSnapshot(); err != nil {
		return nil, err
	}
	if err := bc.replayJournal(); err != nil {
		return nil, err
	}
	return bc, nil
}

// CacheDir returns the cache directory path
func (bc *BuildCache) CacheDir() string {
	return bc.cacheDir
}

func (bc *BuildCache) add(entry CacheEntry) {
	bc.entries[entry.Key] = entry
	bc.newest[entry.TaskID+"\x00"+entry.Context] = entry.Key
}

func (bc *BuildCache) loadSnapshot() error {
	path := filepath.Join(bc.cacheDir, indexFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read the cache index %s: %w", path, err)
	}
	var version struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return fmt.Errorf("failed to parse the cache index %s (delete it to start a fresh cache): %w", path, err)
	}
	if version.Version != indexVersion {
		// The cache of another buildy version: its keys can't match. Start afresh.
		util.LogProgress("The cache in %s is from another version of buildy: every task runs again", bc.cacheDir)
		bc.dirty = true
		return nil
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("failed to parse the cache index %s (delete it to start a fresh cache): %w", path, err)
	}
	for _, entry := range snap.Entries {
		bc.add(entry)
	}
	return nil
}

// replayJournal adds the journal's entries, in order, over the snapshot's. A last line without
// its newline was cut short by a crash and is ignored; any other line that doesn't parse is an
// error.
func (bc *BuildCache) replayJournal() error {
	path := filepath.Join(bc.cacheDir, journalFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read the cache journal %s: %w", path, err)
	}
	bc.dirty = true
	for line := 1; len(data) > 0; line++ {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			util.LogInfo("Ignoring the incomplete last entry of the cache journal %s", path)
			return nil
		}
		var entry CacheEntry
		if err := json.Unmarshal(data[:end], &entry); err != nil || entry.Key == "" {
			return fmt.Errorf("the cache journal %s is damaged at line %d (delete it to start a fresh cache): %v", path, line, err)
		}
		bc.add(entry)
		data = data[end+1:]
	}
	return nil
}

// Close compacts the cache: it writes the snapshot with the newest entry of each task and
// context, then removes the journal. A run calls it once it has finished or stopped, and an
// interrupted run too; until then, the journal holds what the run recorded.
func (bc *BuildCache) Close() error {
	bc.mutex.Lock()
	defer bc.mutex.Unlock()
	if bc.journal != nil {
		if err := bc.journal.Close(); err != nil {
			return fmt.Errorf("failed to close the cache journal: %w", err)
		}
		bc.journal = nil
	}
	if !bc.dirty {
		return nil
	}

	snap := snapshot{Version: indexVersion, Entries: make([]CacheEntry, 0, len(bc.newest))}
	for _, key := range bc.newest {
		snap.Entries = append(snap.Entries, bc.entries[key])
	}
	sort.Slice(snap.Entries, func(i, j int) bool {
		a, b := snap.Entries[i], snap.Entries[j]
		if a.TaskID != b.TaskID {
			return a.TaskID < b.TaskID
		}
		return a.Context < b.Context
	})
	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("failed to encode the cache index: %w", err)
	}
	if err := writeFileSynced(filepath.Join(bc.cacheDir, indexFile), data); err != nil {
		return fmt.Errorf("failed to write the cache index: %w", err)
	}
	// The snapshot holds every entry now. Should buildy stop before the journal is gone, the next
	// run replays it over the snapshot, which changes nothing.
	if err := os.Remove(filepath.Join(bc.cacheDir, journalFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to remove the cache journal: %w", err)
	}

	// Drop the superseded entries from memory too
	kept := make(map[string]CacheEntry, len(snap.Entries))
	for _, entry := range snap.Entries {
		kept[entry.Key] = entry
	}
	bc.entries = kept
	bc.dirty = false
	return nil
}

// writeFileSynced replaces path with data: written to a temporary file, synced, then renamed over
// it, so path holds either the old content or the new.
func writeFileSynced(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// Lookup reports whether the task's recorded result is still valid, and if not, why. The task's
// inputs must be hashed (BuildTask.HashInputs). A hit needs an entry for the task's key, every
// declared output present with its recorded hash, and every input the dependency file reported
// unchanged (a discovered input that was missing must still be missing).
func (bc *BuildCache) Lookup(task *workspace.BuildTask) (bool, string) {
	bc.mutex.Lock()
	entry, exists := bc.entries[task.CacheKey]
	bc.mutex.Unlock()
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

// Record writes the cache entry for a task that just succeeded: it appends the entry to the
// journal. Every declared output must exist, and a declared dependency file must exist and parse;
// otherwise the task did not do what it declared, and Record fails without writing an entry.
// Relative paths in the dependency file are relative to workDir, where the task ran.
func (bc *BuildCache) Record(task *workspace.BuildTask, workDir string) error {
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

	entry := CacheEntry{
		Key:       task.CacheKey,
		TaskID:    task.TaskID,
		Context:   task.Platform + "-" + task.Architecture + "-" + task.Configuration,
		Outputs:   outputs,
		DepInputs: depInputs,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to encode the cache entry of %s: %w", task.TaskID, err)
	}
	line = append(line, '\n')

	bc.mutex.Lock()
	defer bc.mutex.Unlock()
	if bc.journal == nil {
		path := filepath.Join(bc.cacheDir, journalFile)
		if bc.journal, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644); err != nil {
			return fmt.Errorf("failed to open the cache journal: %w", err)
		}
	}
	// One write of a whole line: a crash leaves at most an incomplete last line
	if _, err := bc.journal.Write(line); err != nil {
		return fmt.Errorf("failed to append to the cache journal: %w", err)
	}
	bc.add(entry)
	bc.dirty = true
	return nil
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

// Entries returns how many tasks have an entry: the newest of each task and context.
func (bc *BuildCache) Entries() int {
	bc.mutex.Lock()
	defer bc.mutex.Unlock()
	return len(bc.newest)
}
