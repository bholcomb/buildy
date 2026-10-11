package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"buildy/pkg/util"
)

// countReads counts the files hashContent reads during the test.
func countReads(t *testing.T) *int {
	t.Helper()
	reads := 0
	hashContent = func(path string) (string, error) {
		reads++
		return util.HashFile(path)
	}
	t.Cleanup(func() { hashContent = util.HashFile })
	return &reads
}

func mustHash(t *testing.T, fh *FileHashes, path string) string {
	t.Helper()
	h, err := fh.Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// Finding 4.4: within a run, a file is read once, however many tasks hash it.
func TestAFileIsReadOncePerRun(t *testing.T) {
	reads := countReads(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "common.h")
	write(t, path, "int x;")
	fh, err := loadFileHashes(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := mustHash(t, fh, path)
	for range 3 {
		if h := mustHash(t, fh, path); h != first {
			t.Fatal("the hash changed")
		}
	}
	if *reads != 1 {
		t.Errorf("read %d times, want 1", *reads)
	}
}

// A change of size or time reads the file again, and Forget does.
func TestAChangedStatReadsAgain(t *testing.T) {
	reads := countReads(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.c")
	write(t, path, "one")
	fh, _ := loadFileHashes(dir)
	before := mustHash(t, fh, path)

	write(t, path, "three")
	three := mustHash(t, fh, path)
	if three == before {
		t.Error("a file of another size kept its hash")
	}
	// Same size, and the mtime put back (as tools that restore times do): the change time differs.
	// Windows has no change time.
	if runtime.GOOS == "windows" {
		return
	}
	info, _ := os.Stat(path)
	time.Sleep(10 * time.Millisecond)
	write(t, path, "seven")
	os.Chtimes(path, info.ModTime(), info.ModTime())
	if h := mustHash(t, fh, path); h == three {
		t.Error("a rewritten file of the same size and mtime kept its hash")
	}
	fh.Forget(path)
	mustHash(t, fh, path)
	if *reads != 4 {
		t.Errorf("read %d times, want 4", *reads)
	}
}

// Across runs, a file whose stat is unchanged is not read; a file written just before it was
// read is, as its stat can't tell a write in the same timestamp tick.
func TestHashesPersistUnlessRacy(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	os.MkdirAll(cacheDir, 0o755)
	settled := filepath.Join(dir, "settled.h")
	fresh := filepath.Join(dir, "fresh.o")
	write(t, settled, "old")
	write(t, fresh, "new")
	old := time.Now().Add(-time.Minute)
	os.Chtimes(settled, old, old)

	fh, _ := loadFileHashes(cacheDir)
	// The change time of settled.h is now; pretend the run reads it a minute later
	later := time.Now().Add(time.Minute)
	fh.now = func() time.Time { return later }
	mustHash(t, fh, settled)
	fh.now = time.Now
	mustHash(t, fh, fresh)
	if err := fh.Save(); err != nil {
		t.Fatal(err)
	}

	reads := countReads(t)
	next, err := loadFileHashes(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	mustHash(t, next, settled)
	if *reads != 0 {
		t.Errorf("an unchanged settled file was read again")
	}
	mustHash(t, next, fresh)
	if *reads != 1 {
		t.Errorf("a file written just before it was hashed was trusted: %d reads, want 1", *reads)
	}
}

// Save keeps the hashes of files this run didn't use while they are unchanged, and drops the
// others.
func TestSaveDropsDeletedFiles(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	os.MkdirAll(cacheDir, 0o755)
	kept := filepath.Join(dir, "kept.h")
	gone := filepath.Join(dir, "gone.h")
	write(t, kept, "k")
	write(t, gone, "g")
	fh, _ := loadFileHashes(cacheDir)
	later := time.Now().Add(time.Minute)
	fh.now = func() time.Time { return later }
	mustHash(t, fh, kept)
	mustHash(t, fh, gone)
	fh.Save()

	// A run that uses neither; gone.h is deleted
	os.Remove(gone)
	second, _ := loadFileHashes(cacheDir)
	second.Save()
	third, _ := loadFileHashes(cacheDir)
	if _, ok := third.entries[kept]; !ok {
		t.Error("the hash of an unchanged file this run didn't use was dropped")
	}
	if _, ok := third.entries[gone]; ok {
		t.Error("the hash of a deleted file was kept")
	}
}
