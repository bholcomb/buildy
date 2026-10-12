package workspace

import (
	"strings"
	"sync"
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

// Artifact names are unique across the workspace, within a module too.
func TestArtifactNamesAreUnique(t *testing.T) {
	names := newGeneratedNames()
	if err := names.addArtifact("shaders", "a", []string{"/o/x.spv"}, []string{"t1"}); err != nil {
		t.Fatal(err)
	}
	if err := names.addArtifact("shaders", "a", nil, nil); err == nil || !strings.Contains(err.Error(), "defined twice in module a") {
		t.Errorf("same module: got %v", err)
	}
	if err := names.addArtifact("shaders", "b", nil, nil); err == nil || !strings.Contains(err.Error(), "in module a and in module b") {
		t.Errorf("two modules: got %v", err)
	}
}

// Each generation owns what it names: generations running at the same time, of different
// workspaces, each see their own targets only.
func TestGenerationsShareNoState(t *testing.T) {
	roots := map[string]string{}
	for _, name := range []string{"one", "two", "three"} {
		root := t.TempDir()
		writeFiles(t, root, map[string]string{
			"buildy.yaml": "project: {name: " + name + "}\n" + cppEnvironment +
				"targets:\n  executables:\n    - {name: app_" + name + ", language: cpp, sources: [main.cpp]}\n",
			"main.cpp": "int main() {}",
		})
		roots[name] = root
	}
	var wg sync.WaitGroup
	for name, root := range roots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				generated, err := generate(t, root, "linux", "x86_64", "debug")
				if err != nil {
					t.Error(err)
					return
				}
				if len(generated.Targets) != 1 || generated.Targets["app_"+name] == "" {
					t.Errorf("workspace %s: targets %v", name, generated.Targets)
				}
			}
		}()
	}
	wg.Wait()
}

// TestTaskIDGeneratorSpecialCharacters tests handling of special characters in paths.
func TestTaskIDGeneratorSpecialCharacters(t *testing.T) {
	// Paths with special characters should be sanitized
	testCases := []string{
		"src/module",
		"src\\module", // Windows path
		"src-module",
		"src.module",
		"@fetch:glfw3", // Special prefix
		"my module",    // Space
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
