package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"buildy/internal/resource"
)

// writeFiles writes files (path relative to root -> content) under root.
func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// generateTasks loads the workspace at root and generates its tasks for linux-x86_64-debug with
// the built-in templates and toolchains, as a build would before executing anything.
func generateTasks(t *testing.T, root string) ([]*BuildTask, error) {
	t.Helper()
	return generateTasksFor(t, root, "linux", "x86_64", "debug")
}

// generateTasksFor is generateTasks for another platform, architecture or configuration.
func generateTasksFor(t *testing.T, root, platform, arch, config string) ([]*BuildTask, error) {
	t.Helper()
	ws, err := NewWorkspace(root)
	if err != nil {
		return nil, err
	}
	ctx := BuildContext{Platform: platform, Architecture: arch, Configuration: config}
	if _, err := ws.DiscoverModules(ctx); err != nil {
		return nil, err
	}
	toolchains, err := resource.NewToolchainManagerMulti(nil)
	if err != nil {
		t.Fatal(err)
	}
	templates, err := NewBuildTemplateEngineMulti(nil)
	if err != nil {
		t.Fatal(err)
	}
	cp := NewConfigParser(ctx, nil, toolchains, "", templates, ws, nil)
	return cp.GenerateWorkspaceTasks()
}

func mustGenerateTasks(t *testing.T, root string) []*BuildTask {
	t.Helper()
	tasks, err := generateTasks(t, root)
	if err != nil {
		t.Fatalf("generating tasks: %v", err)
	}
	return tasks
}

// tasksOfType returns the tasks of one type.
func tasksOfType(tasks []*BuildTask, taskType string) []*BuildTask {
	var result []*BuildTask
	for _, task := range tasks {
		if task.TaskType == taskType {
			result = append(result, task)
		}
	}
	return result
}

const cppEnvironment = `
environment:
  compile:
    cpp_standard: "c++17"
`
