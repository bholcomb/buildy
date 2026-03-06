package util

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// GOOS is the operating system for path normalization (injectable for testing)
var GOOS = runtime.GOOS

// CopyFile copies a file from src to dst, preserving permissions
func CopyFile(src, dst string) error {
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

// NormalizePath normalizes a file path for consistent cache key generation.
// On Windows, this lowercases the entire path since the filesystem is case-insensitive.
// On all platforms, it cleans the path to remove redundant separators.
func NormalizePath(path string) string {
	// First, clean the path to normalize separators and remove redundant parts
	cleaned := filepath.Clean(path)

	// On Windows, lowercase the path for consistent comparisons
	// Windows filesystem is case-insensitive, so C:\Dev and c:\dev are the same
	if GOOS == "windows" {
		cleaned = strings.ToLower(cleaned)
	}

	return cleaned
}

// NormalizePathForCache is specifically for cache key calculation.
// It normalizes the path and also converts to forward slashes for cross-platform
// cache key consistency (though cache is typically not shared across platforms).
func NormalizePathForCache(path string) string {
	normalized := NormalizePath(path)
	// Convert to forward slashes for consistent cache keys
	return filepath.ToSlash(normalized)
}
