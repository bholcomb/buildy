package util

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNormalizePathConsistency ensures path normalization is deterministic.
func TestNormalizePathConsistency(t *testing.T) {
	paths := []string{
		"/home/user/project/build/linux-x86_64-debug/bin/myapp",
		"./relative/path/to/file.cpp",
		"../parent/file.o",
	}

	for _, path := range paths {
		norm1 := NormalizePath(path)
		norm2 := NormalizePath(path)
		if norm1 != norm2 {
			t.Errorf("NormalizePath not consistent: '%s' vs '%s' for input '%s'",
				norm1, norm2, path)
		}
	}
}

// TestNormalizePathForCacheConsistency ensures cache path normalization is deterministic.
func TestNormalizePathForCacheConsistency(t *testing.T) {
	paths := []string{
		"/home/user/project/build/obj/main.o",
		"build/windows-x86_64-debug/bin/app.exe",
	}

	for _, path := range paths {
		norm1 := NormalizePathForCache(path)
		norm2 := NormalizePathForCache(path)
		if norm1 != norm2 {
			t.Errorf("NormalizePathForCache not consistent: '%s' vs '%s' for input '%s'",
				norm1, norm2, path)
		}
	}
}

// TestNormalizePathForCacheProducesForwardSlashes verifies cache keys use forward slashes.
func TestNormalizePathForCacheProducesForwardSlashes(t *testing.T) {
	// Use filepath.Join to create a path with native separators
	path := filepath.Join("build", "platform", "bin", "app.exe")
	normalized := NormalizePathForCache(path)

	// Result should use forward slashes regardless of platform
	for _, c := range normalized {
		if c == '\\' {
			t.Errorf("NormalizePathForCache should produce forward slashes: input='%s' output='%s'",
				path, normalized)
			break
		}
	}

	expected := "build/platform/bin/app.exe"
	if normalized != expected {
		t.Errorf("Expected '%s', got '%s'", expected, normalized)
	}
}

// TestNormalizePathCleansRedundantElements ensures ./  and // are cleaned.
func TestNormalizePathCleansRedundantElements(t *testing.T) {
	// Use forward slashes - filepath.Clean handles these on all platforms
	testCases := []struct {
		input    string
		expected string
	}{
		{"path/./to/file", "path/to/file"},
		{"path//to//file", "path/to/file"},
		{"./path/to/file", "path/to/file"},
	}

	for _, tc := range testCases {
		// NormalizePathForCache gives us forward slashes on all platforms
		normalized := NormalizePathForCache(tc.input)
		if normalized != tc.expected {
			t.Errorf("NormalizePathForCache('%s') = '%s', expected '%s'",
				tc.input, normalized, tc.expected)
		}
	}
}

// TestNormalizePathWindowsCaseInsensitive tests Windows case handling.
func TestNormalizePathWindowsCaseInsensitive(t *testing.T) {
	// Mock Windows behavior
	oldGOOS := GOOS
	GOOS = "windows"
	defer func() { GOOS = oldGOOS }()

	// Use forward slashes which work on both platforms for testing
	path1 := "C:/Dev/Project/Build/App.exe"
	path2 := "c:/dev/project/build/app.exe"

	norm1 := NormalizePath(path1)
	norm2 := NormalizePath(path2)

	if norm1 != norm2 {
		t.Errorf("On Windows, paths should normalize to same value regardless of case:\n"+
			"'%s' -> '%s'\n'%s' -> '%s'",
			path1, norm1, path2, norm2)
	}
}

// TestNormalizePathLinuxCaseSensitive tests Linux case handling.
func TestNormalizePathLinuxCaseSensitive(t *testing.T) {
	// Mock Linux behavior
	oldGOOS := GOOS
	GOOS = "linux"
	defer func() { GOOS = oldGOOS }()

	path1 := "/home/user/Project/Build/App"
	path2 := "/home/user/project/build/app"

	norm1 := NormalizePath(path1)
	norm2 := NormalizePath(path2)

	if norm1 == norm2 {
		t.Errorf("On Linux, paths should preserve case:\n"+
			"'%s' -> '%s'\n'%s' -> '%s'",
			path1, norm1, path2, norm2)
	}
}

// TestCacheKeyPathIndependence verifies that the same logical path produces
// the same cache key regardless of how the path separators are written.
// This is critical for cross-platform builds.
func TestCacheKeyPathIndependence(t *testing.T) {
	// These represent the same logical path
	pathForward := "build/platform/obj/main.o"
	pathNative := filepath.Join("build", "platform", "obj", "main.o")

	norm1 := NormalizePathForCache(pathForward)
	norm2 := NormalizePathForCache(pathNative)

	if norm1 != norm2 {
		t.Errorf("Same logical path should produce same normalized result:\n"+
			"Forward slashes: '%s' -> '%s'\n"+
			"Native (filepath.Join): '%s' -> '%s'",
			pathForward, norm1, pathNative, norm2)
	}
}

// TestCopyFilePreservesPermissions tests that file permissions are preserved.
func TestCopyFilePreservesPermissions(t *testing.T) {
	if GOOS == "windows" {
		t.Skip("Permission test not reliable on Windows")
	}

	// Create temp files
	src := t.TempDir() + "/source"
	dst := t.TempDir() + "/dest"

	// Write source with executable permission
	if err := writeFileWithMode(src, []byte("content"), 0755); err != nil {
		t.Fatalf("Failed to create source: %v", err)
	}

	// Copy
	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}

	// Check destination permissions
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("Failed to stat dest: %v", err)
	}

	// Mode should have executable bit
	mode := info.Mode().Perm()
	if mode&0100 == 0 {
		t.Errorf("CopyFile should preserve executable permission: got %o", mode)
	}
}

// writeFileWithMode writes a file with specific permissions
func writeFileWithMode(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	f.Close()
	return err
}
