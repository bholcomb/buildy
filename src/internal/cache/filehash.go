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

	"buildy/pkg/util"
)

const (
	fileHashesFile    = "file_hashes.json"
	fileHashesVersion = 1

	// racyWindow: a hash is kept for later runs only if the file's times were this much older
	// than the moment buildy read it. A write within the same timestamp tick as the one that set
	// the recorded times would leave them unchanged; 2 s covers the coarsest timestamps in use
	// (FAT's), and the clock ticks behind Linux's. Such a file is hashed again next run.
	racyWindow = 2 * time.Second
)

// hashContent reads and hashes a file (replaced in tests, to count reads).
var hashContent = util.HashFile

// fileStat is what tells that a file changed without reading it: its size, its modification and
// change times, and its identity (inode and device), where the platform has them (statOf). The
// change time can't be set back by tools that restore an mtime.
type fileStat struct {
	Size  int64  `json:"s"`
	Mtime int64  `json:"m"` // nanoseconds
	Ctime int64  `json:"c"` // nanoseconds; the creation time on Windows
	Ino   uint64 `json:"i,omitempty"`
	Dev   uint64 `json:"d,omitempty"`
}

type fileHash struct {
	Stat     fileStat `json:"stat"`
	Hash     string   `json:"hash"`
	HashedAt int64    `json:"at"` // when buildy took the stat and read the file, in Unix nanoseconds

	seen bool // used in this run
}

// trusted reports whether a later run may take the hash on the stat alone: no write could have
// changed the file since without changing its times.
func (h *fileHash) trusted() bool {
	newest := max(h.Stat.Mtime, h.Stat.Ctime)
	return h.HashedAt-newest > int64(racyWindow)
}

// FileHashes hashes files (SHA-256) and remembers each hash with the file's stat: within a run a
// file is read once, and across runs a file whose stat is unchanged is not read again. Any change
// of size, times, inode or device means a new read.
type FileHashes struct {
	path    string
	mu      sync.Mutex
	entries map[string]*fileHash // cleaned absolute path -> hash
	now     func() time.Time
}

func loadFileHashes(cacheDir string) (*FileHashes, error) {
	fh := &FileHashes{path: filepath.Join(cacheDir, fileHashesFile), entries: map[string]*fileHash{}, now: time.Now}
	data, err := os.ReadFile(fh.path)
	if errors.Is(err, fs.ErrNotExist) {
		return fh, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", fh.path, err)
	}
	var file struct {
		Version int                  `json:"version"`
		Files   map[string]*fileHash `json:"files"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("failed to parse %s (delete it; buildy hashes the files again): %w", fh.path, err)
	}
	if file.Version == fileHashesVersion {
		fh.entries = file.Files
	}
	return fh, nil
}

// Hash returns the SHA-256 of the file at path (following symlinks), read only if its stat
// changed since it was last hashed.
func (fh *FileHashes) Hash(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// The stat comes first: a write after it changes the stat the next lookup compares
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	stat := statOf(info)

	fh.mu.Lock()
	if e, ok := fh.entries[path]; ok && e.Stat == stat {
		e.seen = true
		fh.mu.Unlock()
		return e.Hash, nil
	}
	fh.mu.Unlock()

	hashedAt := fh.now().UnixNano()
	hash, err := hashContent(path)
	if err != nil {
		return "", err
	}
	fh.mu.Lock()
	fh.entries[path] = &fileHash{Stat: stat, Hash: hash, HashedAt: hashedAt, seen: true}
	fh.mu.Unlock()
	return hash, nil
}

// Forget drops what is known about path: a task is about to write it.
func (fh *FileHashes) Forget(path string) {
	if abs, err := filepath.Abs(path); err == nil {
		fh.mu.Lock()
		delete(fh.entries, abs)
		fh.mu.Unlock()
	}
}

// Save writes the hashes a later run can trust: those of the files used in this run whose times
// were old enough when they were read, and the earlier ones of files this run didn't use that are
// still there unchanged. The rest are dropped, so the file doesn't grow with deleted files.
func (fh *FileHashes) Save() error {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	keep := make(map[string]*fileHash, len(fh.entries))
	for path, e := range fh.entries {
		if !e.trusted() {
			continue
		}
		if !e.seen {
			info, err := os.Stat(path)
			if err != nil || info.IsDir() || statOf(info) != e.Stat {
				continue
			}
		}
		keep[path] = e
	}
	data, err := json.Marshal(map[string]any{"version": fileHashesVersion, "files": keep})
	if err != nil {
		return fmt.Errorf("failed to encode the file hashes: %w", err)
	}
	if err := writeFileSynced(fh.path, data); err != nil {
		return fmt.Errorf("failed to write %s: %w", fh.path, err)
	}
	return nil
}
