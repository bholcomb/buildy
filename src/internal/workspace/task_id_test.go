package workspace

import (
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

// TestTaskIDRegistryClear verifies the registry can be cleared.
func TestTaskIDRegistryClear(t *testing.T) {
	registry := NewTaskIDRegistry()
	registry.RegisterTarget("target1", "task1", "mod1")

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

