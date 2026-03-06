package workspace

import (
	"path/filepath"
	"testing"
)

// TestTaskIDGeneratorUniqueness ensures task IDs are unique within a module.
func TestTaskIDGeneratorUniqueness(t *testing.T) {
	gen := NewTaskIDGenerator("my_module")

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := gen.Next("compile", "source")
		if seen[id] {
			t.Errorf("Duplicate task ID generated: %s", id)
		}
		seen[id] = true
	}

	t.Logf("Generated %d unique task IDs", len(seen))
}

// TestTaskIDGeneratorModulePrefix ensures different modules get different prefixes.
func TestTaskIDGeneratorModulePrefix(t *testing.T) {
	gen1 := NewTaskIDGenerator("src")
	gen2 := NewTaskIDGenerator("examples")

	id1 := gen1.Next("compile", "main")
	id2 := gen2.Next("compile", "main")

	if id1 == id2 {
		t.Errorf("Different modules should generate different IDs: %s vs %s", id1, id2)
	}

	// Both should contain their module prefix
	if id1[len(id1)-5:] != "__src" {
		t.Errorf("Expected id1 to end with __src: %s", id1)
	}
	if id2[len(id2)-10:] != "__examples" {
		t.Errorf("Expected id2 to end with __examples: %s", id2)
	}
}

// TestTaskIDRegistryTargetResolution tests the core target → task ID resolution.
func TestTaskIDRegistryTargetResolution(t *testing.T) {
	registry := NewTaskIDRegistry()

	// Register some targets
	registry.RegisterTargetWithOutput("mylib", "link_mylib_001__src", "src", "/build/lib/mylib.so")
	registry.RegisterTargetWithOutput("myapp", "link_myapp_001__examples", "examples", "/build/bin/myapp")

	// Test resolution by target name
	taskID, ok := registry.GetLinkTaskID("mylib")
	if !ok {
		t.Error("Should find mylib target")
	}
	if taskID != "link_mylib_001__src" {
		t.Errorf("Expected link_mylib_001__src, got %s", taskID)
	}

	// Test reverse lookup
	targetName, ok := registry.GetTargetName("link_myapp_001__examples")
	if !ok {
		t.Error("Should find target for task ID")
	}
	if targetName != "myapp" {
		t.Errorf("Expected myapp, got %s", targetName)
	}

	// Test output path lookup
	outputPath, ok := registry.GetTargetOutputPath("mylib")
	if !ok {
		t.Error("Should find output path for mylib")
	}
	if outputPath != "/build/lib/mylib.so" {
		t.Errorf("Expected /build/lib/mylib.so, got %s", outputPath)
	}
}

// TestTaskIDRegistryResolveDependency tests resolving both target names and task IDs.
func TestTaskIDRegistryResolveDependency(t *testing.T) {
	registry := NewTaskIDRegistry()
	registry.RegisterTarget("vulkease", "link_vulkease_001__src", "src")

	// Target name should resolve to task ID
	taskID, ok := registry.ResolveDependency("vulkease")
	if !ok {
		t.Error("Should resolve vulkease target")
	}
	if taskID != "link_vulkease_001__src" {
		t.Errorf("Expected link_vulkease_001__src, got %s", taskID)
	}

	// Task ID should pass through unchanged
	taskID, ok = registry.ResolveDependency("compile_foo_001__bar")
	if !ok {
		t.Error("Should recognize task ID format")
	}
	if taskID != "compile_foo_001__bar" {
		t.Errorf("Task ID should pass through unchanged: %s", taskID)
	}

	// Unknown target should not resolve
	_, ok = registry.ResolveDependency("unknown_target")
	if ok {
		t.Error("Should not resolve unknown target")
	}
}

// TestTaskIDRegistryOutputPathLookup tests looking up tasks by output path.
func TestTaskIDRegistryOutputPathLookup(t *testing.T) {
	registry := NewTaskIDRegistry()

	// Register target with multiple outputs (DLL + import library)
	registry.RegisterTargetWithOutput("mylib", "link_mylib_001__src", "src", "/build/bin/mylib.dll")
	registry.RegisterTargetOutputs("link_mylib_001__src", []string{
		"/build/bin/mylib.dll",
		"/build/lib/mylib.lib",
	})

	// Primary output should resolve
	taskID, ok := registry.GetTaskIDByOutputPath("/build/bin/mylib.dll")
	if !ok {
		t.Error("Should find task by primary output path")
	}
	if taskID != "link_mylib_001__src" {
		t.Errorf("Expected link_mylib_001__src, got %s", taskID)
	}

	// Secondary output (import library) should also resolve
	taskID, ok = registry.GetTaskIDByOutputPath("/build/lib/mylib.lib")
	if !ok {
		t.Error("Should find task by secondary output path (import library)")
	}
	if taskID != "link_mylib_001__src" {
		t.Errorf("Expected link_mylib_001__src for .lib, got %s", taskID)
	}

	// Unknown path should not resolve
	_, ok = registry.GetTaskIDByOutputPath("/build/bin/unknown.exe")
	if ok {
		t.Error("Should not find task for unknown output path")
	}
}

// TestTaskIDRegistryClear verifies the registry can be cleared.
func TestTaskIDRegistryClear(t *testing.T) {
	registry := NewTaskIDRegistry()
	registry.RegisterTarget("target1", "task1", "mod1")
	registry.RegisterTargetOutputs("task1", []string{"/out/file1"})

	// Verify it's populated
	_, ok := registry.GetLinkTaskID("target1")
	if !ok {
		t.Error("Registry should have target1")
	}

	// Clear it
	registry.Clear()

	// Verify it's empty
	_, ok = registry.GetLinkTaskID("target1")
	if ok {
		t.Error("Registry should be empty after Clear()")
	}

	_, ok = registry.GetTaskIDByOutputPath("/out/file1")
	if ok {
		t.Error("Output path mapping should be cleared")
	}
}

// TestTaskIDGeneratorSpecialCharacters tests handling of special characters in paths.
func TestTaskIDGeneratorSpecialCharacters(t *testing.T) {
	// Paths with special characters should be sanitized
	testCases := []string{
		"src/module",
		"src\\module",      // Windows path
		"src-module",
		"src.module",
		"@fetch:glfw3",     // Special prefix
		"my module",        // Space
	}

	for _, path := range testCases {
		gen := NewTaskIDGenerator(path)
		id := gen.Next("compile", "test")

		// ID should only contain alphanumeric and underscore
		for _, c := range id {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
				t.Errorf("Task ID contains invalid character '%c' for path '%s': %s", c, path, id)
			}
		}
	}
}

// TestTaskIDRegistryOutputPathLookupCrossplatform verifies that output path lookups
// work correctly regardless of path separator style. This is critical for staging
// tasks that need to find which task produces a given file.
func TestTaskIDRegistryOutputPathLookupCrossplatform(t *testing.T) {
	registry := NewTaskIDRegistry()

	// Register with a path using native separators (as would happen during task generation)
	nativePath := filepath.Join("build", "platform", "bin", "myapp.exe")
	registry.RegisterTargetWithOutput("myapp", "link_myapp_001", "src", nativePath)
	registry.RegisterTargetOutputs("link_myapp_001", []string{nativePath})

	// Lookup with the same native path should work
	taskID, ok := registry.GetTaskIDByOutputPath(nativePath)
	if !ok {
		t.Errorf("Should find task by native path: %s", nativePath)
	}
	if taskID != "link_myapp_001" {
		t.Errorf("Expected link_myapp_001, got %s", taskID)
	}

	// Lookup with forward slashes should also work (cross-platform compatibility)
	forwardPath := "build/platform/bin/myapp.exe"
	taskID, ok = registry.GetTaskIDByOutputPath(forwardPath)
	if !ok {
		t.Errorf("Should find task by forward-slash path: %s (registered as: %s)", forwardPath, nativePath)
	}
	if taskID != "link_myapp_001" {
		t.Errorf("Expected link_myapp_001, got %s", taskID)
	}
}

// TestTaskIDRegistryWindowsCaseInsensitive verifies that on Windows,
// path lookups are case-insensitive.
func TestTaskIDRegistryWindowsCaseInsensitive(t *testing.T) {
	// Mock Windows behavior by setting the goos variable
	oldGoos := goos
	goos = "windows"
	defer func() { goos = oldGoos }()

	registry := NewTaskIDRegistry()

	// Register with mixed case (using forward slashes which work on both platforms)
	registry.RegisterTargetWithOutput("myapp", "link_myapp_001", "src", "C:/Build/Bin/MyApp.exe")
	registry.RegisterTargetOutputs("link_myapp_001", []string{"C:/Build/Bin/MyApp.exe"})

	// Lookup with different case should work on Windows
	taskID, ok := registry.GetTaskIDByOutputPath("c:/build/bin/myapp.exe")
	if !ok {
		t.Error("Windows path lookup should be case-insensitive")
	}
	if taskID != "link_myapp_001" {
		t.Errorf("Expected link_myapp_001, got %s", taskID)
	}

	// Also test with uppercase lookup
	taskID, ok = registry.GetTaskIDByOutputPath("C:/BUILD/BIN/MYAPP.EXE")
	if !ok {
		t.Error("Windows path lookup should be case-insensitive (uppercase)")
	}
	if taskID != "link_myapp_001" {
		t.Errorf("Expected link_myapp_001, got %s", taskID)
	}
}

// TestTaskIDRegistryLinuxCaseSensitive verifies that on Linux,
// path lookups are case-sensitive.
func TestTaskIDRegistryLinuxCaseSensitive(t *testing.T) {
	// Mock Linux behavior
	oldGoos := goos
	goos = "linux"
	defer func() { goos = oldGoos }()

	registry := NewTaskIDRegistry()

	// Register with specific case
	registry.RegisterTargetWithOutput("myapp", "link_myapp_001", "src", "/home/user/Build/Bin/MyApp")
	registry.RegisterTargetOutputs("link_myapp_001", []string{"/home/user/Build/Bin/MyApp"})

	// Exact case should work
	taskID, ok := registry.GetTaskIDByOutputPath("/home/user/Build/Bin/MyApp")
	if !ok {
		t.Error("Exact case lookup should work")
	}
	if taskID != "link_myapp_001" {
		t.Errorf("Expected link_myapp_001, got %s", taskID)
	}

	// Different case should NOT match on Linux
	_, ok = registry.GetTaskIDByOutputPath("/home/user/build/bin/myapp")
	if ok {
		t.Error("Linux path lookup should be case-sensitive - different case should not match")
	}
}
