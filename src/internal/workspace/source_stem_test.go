package workspace

import (
	"testing"
)

// TestComputeSourceStemPreservesSubdirectories verifies that source files
// in different subdirectories produce unique stems, preventing object file
// collisions.
func TestComputeSourceStemPreservesSubdirectories(t *testing.T) {
	testCases := []struct {
		source    string
		moduleDir string
		expected  string
	}{
		// Basic case - file directly in module
		{"src/window.cpp", "src", "window"},

		// File in subdirectory - must preserve subdir
		{"src/widgets/window.cpp", "src", "widgets/window"},

		// Deeper nesting
		{"src/ui/dialogs/window.cpp", "src", "ui/dialogs/window"},

		// Different module names
		{"examples/basic/main.cpp", "examples", "basic/main"},

		// Source at workspace root (no module prefix)
		{"main.cpp", "workspace", "main"},

		// Source at workspace root with empty module
		{"main.cpp", "", "main"},
	}

	for _, tc := range testCases {
		result := computeSourceStem(tc.source, tc.moduleDir)
		if result != tc.expected {
			t.Errorf("computeSourceStem(%q, %q) = %q, expected %q",
				tc.source, tc.moduleDir, result, tc.expected)
		}
	}
}

// TestComputeSourceStemCollisionPrevention is the specific regression test
// for the bug where src/window.cpp and src/widgets/window.cpp produced the
// same object file path.
func TestComputeSourceStemCollisionPrevention(t *testing.T) {
	stem1 := computeSourceStem("src/window.cpp", "src")
	stem2 := computeSourceStem("src/widgets/window.cpp", "src")

	if stem1 == stem2 {
		t.Errorf("CRITICAL: Source files in different directories produce the same stem!\n"+
			"src/window.cpp -> %q\n"+
			"src/widgets/window.cpp -> %q\n"+
			"This would cause object file collisions.",
			stem1, stem2)
	}

	t.Logf("src/window.cpp -> %q", stem1)
	t.Logf("src/widgets/window.cpp -> %q", stem2)
}

// TestComputeSourceStemBackslashes verifies Windows-style paths are handled.
func TestComputeSourceStemBackslashes(t *testing.T) {
	testCases := []struct {
		source    string
		moduleDir string
		expected  string
	}{
		// Windows paths with backslashes
		{"src\\window.cpp", "src", "window"},
		{"src\\widgets\\window.cpp", "src", "widgets/window"},

		// Mixed separators (can happen with variable resolution)
		{"src/widgets\\window.cpp", "src", "widgets/window"},
	}

	for _, tc := range testCases {
		result := computeSourceStem(tc.source, tc.moduleDir)
		if result != tc.expected {
			t.Errorf("computeSourceStem(%q, %q) = %q, expected %q",
				tc.source, tc.moduleDir, result, tc.expected)
		}
	}
}

// TestComputeSourceStemDifferentExtensions verifies extension stripping
// works for various source file types.
func TestComputeSourceStemDifferentExtensions(t *testing.T) {
	testCases := []struct {
		source   string
		expected string
	}{
		{"src/file.cpp", "file"},
		{"src/file.c", "file"},
		{"src/file.cc", "file"},
		{"src/file.cxx", "file"},
		{"src/file.C", "file"},
	}

	for _, tc := range testCases {
		result := computeSourceStem(tc.source, "src")
		if result != tc.expected {
			t.Errorf("computeSourceStem(%q, \"src\") = %q, expected %q",
				tc.source, result, tc.expected)
		}
	}
}
