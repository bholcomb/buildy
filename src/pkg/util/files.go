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
	_, err = io.Copy(destFile, sourceFile)
	// A write can fail only when the file is closed (a full disk, a network file system)
	if closeErr := destFile.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	// Copy file permissions
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	return os.Chmod(dst, sourceInfo.Mode())
}

// FillTemplate writes the file src to dst with every @KEY@ and ${KEY} replaced by its value, one
// substitution after the other in the order given. dst gets src's permission bits.
func FillTemplate(src, dst string, substitutions [][2]string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	text := string(data)
	for _, s := range substitutions {
		text = strings.ReplaceAll(text, "@"+s[0]+"@", s[1])
		text = strings.ReplaceAll(text, "${"+s[0]+"}", s[1])
	}
	return os.WriteFile(dst, []byte(text), info.Mode().Perm())
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

// PathWithin reports whether path is dir or lies inside it, comparing as NormalizePath does.
// Both are absolute, or both relative to one directory.
func PathWithin(path, dir string) bool {
	rel, err := filepath.Rel(NormalizePath(dir), NormalizePath(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// NormalizePathForCache is specifically for cache key calculation.
// It normalizes the path and also converts to forward slashes for cross-platform
// cache key consistency (though cache is typically not shared across platforms).
func NormalizePathForCache(path string) string {
	normalized := NormalizePath(path)
	// Convert to forward slashes for consistent cache keys
	return filepath.ToSlash(normalized)
}
