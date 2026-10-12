package workspace

import (
	"context"
	"path/filepath"
	"testing"

	"buildy/internal/resource"
	"buildy/pkg/util"
)

// linkTask returns the link task of a target.
func linkTask(t *testing.T, tasks []*BuildTask, target string) *BuildTask {
	t.Helper()
	for _, task := range tasks {
		if task.TaskType == "link" && task.Target == target {
			return task
		}
	}
	t.Fatalf("no link task for target %s", target)
	return nil
}

func inputPaths(task *BuildTask) []string {
	var paths []string
	for _, in := range task.Inputs {
		paths = append(paths, in.Path)
	}
	return paths
}

// Finding 3: a link's inputs include the workspace libraries it links, across modules, so a
// changed library is in the key; validation orders the link after the library.
func TestLinkInputsIncludeWorkspaceLibraries(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml":        "project: {name: ws}\n" + cppEnvironment + "workspace:\n  modules: [libmod, appmod]\n",
		"libmod/buildy.yaml": "project: {name: libmod}\ntargets:\n  static_libraries:\n    - {name: calc, language: cpp, sources: [add.cpp]}\n",
		"appmod/buildy.yaml": "project: {name: appmod}\ntargets:\n  executables:\n    - {name: app, language: cpp, sources: [main.cpp], libs: [calc, pthread]}\n",
		"libmod/add.cpp":     "int add(int a, int b) { return a + b; }",
		"appmod/main.cpp":    "int main() {}",
	})
	tasks := mustGenerateTasks(t, root)
	link := linkTask(t, tasks, "app")
	lib := filepath.Join(root, "build", "linux-x86_64-debug", "lib", "libcalc.a")
	if !hasString(inputPaths(link), lib) {
		t.Fatalf("link inputs %v lack %s", inputPaths(link), lib)
	}

	if err := graphOf(t, tasks...).Validate(); err != nil {
		t.Fatal(err)
	}
	calcLink := linkTask(t, tasks, "calc")
	if !hasString(link.Dependencies, calcLink.TaskID) {
		t.Errorf("link_app does not depend on %s: %v", calcLink.TaskID, link.Dependencies)
	}
}

// A fetched or system dependency's libraries found in its lib dirs are link inputs too;
// libraries that aren't there are system libraries and stay unhashed.
func TestLinkInputsIncludeDependencyLibraries(t *testing.T) {
	root := t.TempDir()
	depLib := filepath.Join(root, "third_party", "lib", "libfoo.a")
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: ws}\n" + cppEnvironment + "targets:\n  executables:\n    - {name: app, language: cpp, sources: [main.cpp], deps: [foo]}\n",
		"main.cpp":    "int main() {}",
		"buildy_config/dependencies.yaml": `
foo:
  lib_dirs: ["${workspace_root}/third_party/lib"]
  libs: [foo, m]
`,
		"third_party/lib/libfoo.a": "archive",
	})

	tasks := generateWithDependencies(t, root)
	link := linkTask(t, tasks, "app")
	if !hasString(inputPaths(link), depLib) {
		t.Errorf("link inputs %v lack %s", inputPaths(link), depLib)
	}
	for _, in := range inputPaths(link) {
		if filepath.Base(in) == "libm.so" || filepath.Base(in) == "libm.a" {
			t.Errorf("system library %s became an input", in)
		}
	}
}

// generateWithDependencies generates a Linux debug build's tasks with the workspace's
// buildy_config/dependencies.yaml resolved.
func generateWithDependencies(t *testing.T, root string) []*BuildTask {
	t.Helper()
	ws, err := NewWorkspace(root, DefaultCacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.DiscoverModules(BuildContext{Platform: "linux", Architecture: "x86_64", Configuration: "debug"}); err != nil {
		t.Fatal(err)
	}
	deps, err := resource.NewDependencyResolver("linux", "x86_64", "", "debug", filepath.Join(root, ".buildy_cache"), root, util.NewVariableEnvironment(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.LoadDependencies(root, ""); err != nil {
		t.Fatal(err)
	}
	if err := deps.ResolveAll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	toolchains, err := resource.NewToolchainManagerMulti(nil)
	if err != nil {
		t.Fatal(err)
	}
	templates, err := NewBuildTemplateEngineMulti(nil)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := NewConfigParser(BuildContext{Platform: "linux", Architecture: "x86_64", Configuration: "debug"}, nil, toolchains, "", templates, ws, deps).GenerateWorkspaceTasks()
	if err != nil {
		t.Fatal(err)
	}
	return generated.Tasks
}

// A dependency's library named like a workspace target is still the dependency's (or a system
// library): a shared library "glfw" built over a dependency that links -lglfw (macOS's Homebrew
// GLFW) must not take its own output as an input of its link.
func TestDependencyLibraryNamedLikeAWorkspaceTargetIsNotThatTarget(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: ws}\n" + cppEnvironment + `targets:
  shared_libraries:
    - {name: glfw, language: cpp, output_prefix: "", sources: [plugin.cpp], deps: [glfw3]}
  executables:
    - {name: app, language: cpp, sources: [main.cpp], deps: [glfw3]}
`,
		"plugin.cpp": "int plugin() { return 0; }",
		"main.cpp":   "int main() {}",
		"buildy_config/dependencies.yaml": `
glfw3:
  libs: [glfw]
`,
	})
	tasks := generateWithDependencies(t, root)
	glfw := linkTask(t, tasks, "glfw")
	for _, out := range glfw.Outputs {
		if hasString(inputPaths(glfw), out) {
			t.Errorf("link of glfw takes its own output %s as an input: %v", out, inputPaths(glfw))
		}
	}
	app := linkTask(t, tasks, "app")
	for _, out := range glfw.Outputs {
		if hasString(inputPaths(app), out) {
			t.Errorf("link of app takes glfw's output %s, for its dependency's -lglfw: %v", out, inputPaths(app))
		}
	}
	if err := graphOf(t, tasks...).Validate(); err != nil {
		t.Fatal(err)
	}
}
