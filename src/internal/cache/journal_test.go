package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reopen(t *testing.T, bc *BuildCache) *BuildCache {
	t.Helper()
	reloaded, err := NewBuildCache(bc.CacheDir())
	if err != nil {
		t.Fatal(err)
	}
	return reloaded
}

// Finding 4.1: recording appends one line to the journal and rewrites no index; an interrupted
// run (no Close) keeps the entries of the tasks that finished.
func TestRecordAppendsToTheJournal(t *testing.T) {
	bc, task, _ := setup(t)
	if _, err := os.Stat(filepath.Join(bc.CacheDir(), indexFile)); err == nil {
		t.Error("Record wrote the index")
	}
	data, err := os.ReadFile(filepath.Join(bc.CacheDir(), journalFile))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "\n"); n != 1 {
		t.Errorf("the journal has %d lines, want 1", n)
	}
	// No Close: the run was interrupted
	if hit, reason := lookup(t, reopen(t, bc), task); !hit {
		t.Fatalf("the entry of a finished task was lost: %s", reason)
	}
}

// A crash in the middle of an append leaves an incomplete last line, which is ignored.
func TestReplayIgnoresAnIncompleteLastLine(t *testing.T) {
	bc, task, _ := setup(t)
	f, err := os.OpenFile(filepath.Join(bc.CacheDir(), journalFile), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"key":"abc","task_id":"cut sh`)
	f.Close()
	if hit, reason := lookup(t, reopen(t, bc), task); !hit {
		t.Fatalf("expected a hit, got a miss: %s", reason)
	}
}

func TestReplayFailsOnADamagedLine(t *testing.T) {
	bc, _, _ := setup(t)
	path := filepath.Join(bc.CacheDir(), journalFile)
	data, _ := os.ReadFile(path)
	os.WriteFile(path, append([]byte("not json\n"), data...), 0o644)
	if _, err := NewBuildCache(bc.CacheDir()); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("expected an error naming line 1, got %v", err)
	}
}

// Close writes the index with the newest entry of each task and context, and removes the journal.
func TestCloseKeepsTheNewestEntryPerTaskAndContext(t *testing.T) {
	bc, task, dir := setup(t)
	oldKey := task.CacheKey

	// The source changes and the task runs again: a second entry for the same task
	write(t, filepath.Join(dir, "main.cpp"), "int main() { return 2; }")
	ran(t, bc, task, dir)
	if task.CacheKey == oldKey {
		t.Fatal("the key didn't change")
	}
	// The same task in another configuration is another task
	releaseDir := filepath.Join(dir, "release")
	write(t, filepath.Join(releaseDir, "main.cpp"), "int main() {}")
	release := compileTask(t, releaseDir)
	release.Configuration = "release"
	ran(t, bc, release, releaseDir)

	if err := bc.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bc.CacheDir(), journalFile)); err == nil {
		t.Error("Close left the journal")
	}
	reloaded := reopen(t, bc)
	if n := reloaded.Entries(); n != 2 {
		t.Errorf("%d entries, want 2 (debug and release)", n)
	}
	if _, ok := reloaded.entries[oldKey]; ok {
		t.Error("the superseded entry was kept")
	}
	if hit, reason := lookup(t, reloaded, task); !hit {
		t.Fatalf("expected a hit, got a miss: %s", reason)
	}
}

// Should buildy stop between writing the index and removing the journal, replaying the journal
// over the index changes nothing.
func TestReplayOverACompactedIndex(t *testing.T) {
	bc, task, _ := setup(t)
	journal, _ := os.ReadFile(filepath.Join(bc.CacheDir(), journalFile))
	if err := bc.Close(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(bc.CacheDir(), journalFile), journal, 0o644)
	reloaded := reopen(t, bc)
	if hit, reason := lookup(t, reloaded, task); !hit {
		t.Fatalf("expected a hit, got a miss: %s", reason)
	}
	if n := reloaded.Entries(); n != 1 {
		t.Errorf("%d entries, want 1", n)
	}
}

// An index from an earlier buildy (a map of keys to entries) is discarded, not an error.
func TestAnOldIndexIsDiscarded(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".buildy_cache")
	write(t, filepath.Join(cacheDir, indexFile), `{"abc": {"task_id": "x", "outputs": {}}}`)
	bc, err := NewBuildCache(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if n := bc.Entries(); n != 0 {
		t.Errorf("%d entries, want 0", n)
	}
	if err := bc.Close(); err != nil {
		t.Fatal(err)
	}
	if n := reopen(t, bc).Entries(); n != 0 {
		t.Errorf("%d entries after Close, want 0", n)
	}
}
