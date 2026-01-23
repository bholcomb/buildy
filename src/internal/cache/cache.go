package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"buildy/internal/workspace"
)

// CacheEntry represents a cached build task result
type CacheEntry struct {
	TaskID             string            `json:"task_id"`
	TaskType           string            `json:"task_type"`
	CachedAt           float64           `json:"cached_at"`
	ExecutionTime      float64           `json:"execution_time"`
	Outputs            map[string]string `json:"outputs"`             // path -> hash
	HeaderDependencies []string          `json:"header_dependencies"` // list of header paths
	HeaderHashes       map[string]string `json:"header_hashes"`       // path -> hash
	Platform           string            `json:"platform"`
	Architecture       string            `json:"architecture"`
	Configuration      string            `json:"configuration"`
}

// BuildCache provides content-addressable build cache with thread-safe operations
type BuildCache struct {
	cacheDir       string
	cacheIndexFile string
	cacheLockFile  string
	objectsDir     string
	cacheIndex     map[string]CacheEntry
	mutex          sync.RWMutex // For thread-safe cache index access
}

// NewBuildCache creates a new build cache
func NewBuildCache(cacheDir string) (*BuildCache, error) {
	if cacheDir == "" {
		cacheDir = ".buildy_cache"
	}

	cache := &BuildCache{
		cacheDir:       cacheDir,
		cacheIndexFile: filepath.Join(cacheDir, "cache_index.json"),
		cacheLockFile:  filepath.Join(cacheDir, "cache.lock"),
		objectsDir:     filepath.Join(cacheDir, "objects"),
		cacheIndex:     make(map[string]CacheEntry),
	}

	// Create cache directory structure
	if err := os.MkdirAll(cache.cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	if err := os.MkdirAll(cache.objectsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create objects directory: %w", err)
	}

	// Load cache index
	if err := cache.loadCacheIndex(); err != nil {
		log.Printf("Warning: failed to load cache index: %v", err)
	}

	return cache, nil
}

// CacheDir returns the cache directory path
func (bc *BuildCache) CacheDir() string {
	return bc.cacheDir
}

// getCachePath returns sharded cache directory path using Git-style sharding
// Uses first 2 hex characters as subdirectory to distribute files
// across 256 directories (00-ff) for better filesystem performance.
func (bc *BuildCache) getCachePath(cacheKey string) string {
	if len(cacheKey) < 2 {
		return filepath.Join(bc.objectsDir, cacheKey)
	}
	shard := cacheKey[:2] // First 2 hex characters (00-ff)
	return filepath.Join(bc.objectsDir, shard, cacheKey)
}

// sanitizeCacheRelPath produces a cache-internal relative path for a build output.
// We preserve the original directory structure to avoid filename collisions, but
// strip drive letters and leading separators so the path remains relative.
func sanitizeCacheRelPath(outputPath string) string {
	cleaned := filepath.Clean(outputPath)

	// Drop drive letters (Windows) to avoid creating top-level drive dirs
	cleaned = strings.ReplaceAll(cleaned, ":", "")

	// Make absolute paths relative to root so they can live inside cache dir
	if filepath.IsAbs(cleaned) {
		cleaned = strings.TrimPrefix(cleaned, string(filepath.Separator))
	}

	return cleaned
}

// loadCacheIndex loads cache index from disk
func (bc *BuildCache) loadCacheIndex() error {
	bc.mutex.Lock()
	defer bc.mutex.Unlock()

	if _, err := os.Stat(bc.cacheIndexFile); os.IsNotExist(err) {
		return nil // No cache index yet, that's fine
	}

	data, err := os.ReadFile(bc.cacheIndexFile)
	if err != nil {
		return fmt.Errorf("failed to read cache index: %w", err)
	}

	if err := json.Unmarshal(data, &bc.cacheIndex); err != nil {
		return fmt.Errorf("failed to parse cache index: %w", err)
	}

	return nil
}

// saveCacheIndex saves cache index to disk atomically (acquires lock)
func (bc *BuildCache) saveCacheIndex() error {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	return bc.saveCacheIndexLocked()
}

// saveCacheIndexLocked saves cache index to disk atomically
// IMPORTANT: Caller must hold at least a read lock on bc.mutex
func (bc *BuildCache) saveCacheIndexLocked() error {
	// Ensure cache directory exists
	if err := os.MkdirAll(bc.cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

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

	// Sync and close
	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		os.Remove(tempPath)
		return fmt.Errorf("failed to sync temp file: %w", err)
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

// HasCacheEntry returns true if a cache entry exists for the given key
func (bc *BuildCache) HasCacheEntry(cacheKey string) bool {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	_, exists := bc.cacheIndex[cacheKey]
	return exists
}

// RestoreCachedResult restores cached task outputs
func (bc *BuildCache) RestoreCachedResult(task *workspace.BuildTask) error {
	bc.mutex.RLock()
	_, exists := bc.cacheIndex[task.CacheKey]
	bc.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("cache entry not found")
	}

	cacheFilesDir := bc.getCachePath(task.CacheKey)
	if _, err := os.Stat(cacheFilesDir); os.IsNotExist(err) {
		return fmt.Errorf("cache directory not found")
	}

	// Restore output files from cache (skip directories)
	for _, outputPath := range task.Outputs {
		// Skip directories - they should be created by the task if needed
		if strings.HasSuffix(outputPath, string(filepath.Separator)) {
			if err := os.MkdirAll(outputPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", outputPath, err)
			}
			continue
		}

		relPath := sanitizeCacheRelPath(outputPath)
		cachedFile := filepath.Join(cacheFilesDir, relPath)
		if _, err := os.Stat(cachedFile); err == nil {
			outputDir := filepath.Dir(outputPath)
			if outputDir != "" && outputDir != "." {
				if err := os.MkdirAll(outputDir, 0755); err != nil {
					return fmt.Errorf("failed to create output directory: %w", err)
				}
			}

			if err := copyFile(cachedFile, outputPath); err != nil {
				return fmt.Errorf("failed to copy cached file: %w", err)
			}
		}
	}

	log.Printf("✓ %s - cache hit, restored outputs", task.TaskID)
	return nil
}

// CacheTaskResult caches task result after successful execution
func (bc *BuildCache) CacheTaskResult(task *workspace.BuildTask, executionTime float64, success bool) error {
	if !success {
		return nil
	}

	cacheFilesDir := bc.getCachePath(task.CacheKey)
	if err := os.MkdirAll(cacheFilesDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Cache output files (skip directories)
	outputHashes := make(map[string]string)
	var depFile string

	for _, outputPath := range task.Outputs {
		if _, err := os.Stat(outputPath); err == nil {
			// Skip directories - they can't be cached as files
			info, err := os.Stat(outputPath)
			if err == nil && info.IsDir() {
				outputHashes[outputPath] = "directory"
				continue
			}

			// Track .d file for header dependency parsing
			if strings.HasSuffix(outputPath, ".d") {
				depFile = outputPath
			}

			relPath := sanitizeCacheRelPath(outputPath)
			cachedFile := filepath.Join(cacheFilesDir, relPath)
			if err := os.MkdirAll(filepath.Dir(cachedFile), 0755); err != nil {
				return fmt.Errorf("failed to create cached output directory: %w", err)
			}

			if err := copyFile(outputPath, cachedFile); err != nil {
				return fmt.Errorf("failed to cache file: %w", err)
			}

			hash, err := bc.calculateFileHash(outputPath)
			if err != nil {
				return fmt.Errorf("failed to hash output: %w", err)
			}
			outputHashes[outputPath] = hash
		}
	}

	// Parse header dependencies from .d file if this is a compile task
	var headerDeps []string
	headerHashes := make(map[string]string)

	if depFile != "" && task.TaskType == "compile" {
		headerDeps = bc.parseDependencyFile(depFile)
		// Calculate and store hashes for all header dependencies
		for _, headerPath := range headerDeps {
			if _, err := os.Stat(headerPath); err == nil {
				hash, err := bc.calculateFileHash(headerPath)
				if err == nil {
					headerHashes[headerPath] = hash
				}
			} else {
				log.Printf("Header dependency not found: %s", headerPath)
			}
		}
	}

	// Update cache index and save atomically while holding the lock
	bc.mutex.Lock()
	bc.cacheIndex[task.CacheKey] = CacheEntry{
		TaskID:             task.TaskID,
		TaskType:           task.TaskType,
		CachedAt:           float64(time.Now().Unix()),
		ExecutionTime:      executionTime,
		Outputs:            outputHashes,
		HeaderDependencies: headerDeps,
		HeaderHashes:       headerHashes,
		Platform:           task.Platform,
		Architecture:       task.Architecture,
		Configuration:      task.Configuration,
	}
	err := bc.saveCacheIndexLocked()
	bc.mutex.Unlock()

	return err
}

// calculateFileHash calculates SHA-256 hash of file efficiently using chunked reading
func (bc *BuildCache) calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()

	// Read in 8KB chunks for memory efficiency with large files
	buf := make([]byte, 8192)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			hash.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// parseDependencyFile parses compiler-generated dependency file
func (bc *BuildCache) parseDependencyFile(depFile string) []string {
	if _, err := os.Stat(depFile); os.IsNotExist(err) {
		return nil
	}

	// Detect format by extension
	if strings.HasSuffix(depFile, ".json") {
		return bc.parseMSVCJSONDeps(depFile)
	}
	return bc.parseMakefileDeps(depFile)
}

// parseMakefileDeps parses GCC/Clang Makefile-style .d file
func (bc *BuildCache) parseMakefileDeps(depFile string) []string {
	data, err := os.ReadFile(depFile)
	if err != nil {
		log.Printf("Warning: failed to read dependency file %s: %v", depFile, err)
		return nil
	}

	content := string(data)

	// Remove target (everything before and including ':')
	idx := strings.Index(content, ":")
	if idx == -1 {
		return nil
	}
	content = content[idx+1:]

	// Remove line continuations (backslash + newline)
	content = strings.ReplaceAll(content, "\\\n", " ")
	content = strings.ReplaceAll(content, "\\", "")

	// Split on whitespace
	allDeps := strings.Fields(content)

	// Filter to only header files (skip .cpp, .c source files)
	// Include .inl (inline implementation files) and .inc (include files)
	var headers []string
	for _, dep := range allDeps {
		dep = strings.TrimSpace(dep)
		if dep != "" && (strings.HasSuffix(dep, ".h") ||
			strings.HasSuffix(dep, ".hpp") ||
			strings.HasSuffix(dep, ".hxx") ||
			strings.HasSuffix(dep, ".hh") ||
			strings.HasSuffix(dep, ".H") ||
			strings.HasSuffix(dep, ".inl") ||
			strings.HasSuffix(dep, ".inc")) {
			headers = append(headers, dep)
		}
	}

	log.Printf("Parsed %d header dependencies from %s (Makefile format)", len(headers), depFile)
	return headers
}

// parseMSVCJSONDeps parses MSVC /sourceDependencies JSON format
func (bc *BuildCache) parseMSVCJSONDeps(depFile string) []string {
	data, err := os.ReadFile(depFile)
	if err != nil {
		log.Printf("Warning: failed to read dependency file %s: %v", depFile, err)
		return nil
	}

	var jsonData struct {
		Version string `json:"Version"`
		Data    struct {
			Source   string   `json:"Source"`
			Includes []string `json:"Includes"`
		} `json:"Data"`
	}

	if err := json.Unmarshal(data, &jsonData); err != nil {
		log.Printf("Warning: failed to parse JSON dependency file %s: %v", depFile, err)
		return nil
	}

	// Filter to only user headers (skip system headers in common system paths)
	var headers []string
	for _, inc := range jsonData.Data.Includes {
		// Normalize path separators
		incNormalized := strings.ReplaceAll(inc, "\\", "/")
		incLower := strings.ToLower(incNormalized)

		// Skip system headers in common locations
		if strings.HasPrefix(incLower, "c:/program files") ||
			strings.HasPrefix(incLower, "c:/windows") ||
			strings.HasPrefix(incLower, "/usr/include") ||
			strings.HasPrefix(incLower, "/usr/local/include") {
			continue
		}

		// Only include files with header extensions
		if strings.HasSuffix(incNormalized, ".h") ||
			strings.HasSuffix(incNormalized, ".hpp") ||
			strings.HasSuffix(incNormalized, ".hxx") ||
			strings.HasSuffix(incNormalized, ".hh") ||
			strings.HasSuffix(incNormalized, ".H") ||
			strings.HasSuffix(incNormalized, ".inl") ||
			strings.HasSuffix(incNormalized, ".inc") {
			headers = append(headers, filepath.FromSlash(incNormalized))
		}
	}

	log.Printf("Parsed %d header dependencies from %s (MSVC JSON format)", len(headers), depFile)
	return headers
}

// GetCacheStats returns cache statistics
func (bc *BuildCache) GetCacheStats() map[string]interface{} {
	bc.mutex.RLock()
	totalEntries := len(bc.cacheIndex)
	bc.mutex.RUnlock()

	var totalSize int64

	// Iterate through sharded cache directories
	filepath.Walk(bc.objectsDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})

	return map[string]interface{}{
		"total_entries":   totalEntries,
		"total_size_mb":   float64(totalSize) / (1024 * 1024),
		"cache_directory": bc.cacheDir,
	}
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	// Copy file permissions
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	return os.Chmod(dst, sourceInfo.Mode())
}
