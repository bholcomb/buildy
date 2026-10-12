package workspace

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestComputeSourceRel(t *testing.T) {
	root := "/ws"
	if runtime.GOOS == "windows" {
		root = `C:\ws`
	}
	abs := func(p string) string { return filepath.Join(root, filepath.FromSlash(p)) }
	tests := []struct {
		source, moduleDir, want string
	}{
		{abs("core/window.cpp"), abs("core"), "window"},
		{abs("core/src/widgets/window.cpp"), abs("core"), "src/widgets/window"},
		{abs("main.cpp"), abs("."), "main"},
		{abs("shared/util.cpp"), abs("core"), "_up/shared/util"},
		{abs("a/b.c"), abs("x/y"), "_up/_up/a/b"},
		{abs("core/file.tar.cpp"), abs("core"), "file.tar"},
	}
	for _, tt := range tests {
		got, err := computeSourceRel(tt.source, tt.moduleDir)
		if err != nil {
			t.Errorf("computeSourceRel(%q, %q): %v", tt.source, tt.moduleDir, err)
			continue
		}
		if got != tt.want {
			t.Errorf("computeSourceRel(%q, %q) = %q, want %q", tt.source, tt.moduleDir, got, tt.want)
		}
	}

	if _, err := computeSourceRel("main.cpp", abs("core")); err == nil {
		t.Error("a relative source was accepted")
	}
}

// Finding 2: two targets compiling the same source get their own object files.
func TestObjectPathsAreDistinctPerTarget(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"main.cpp": "int main() { return VALUE; }",
		"buildy.yaml": `
project: {name: shared_source}
` + cppEnvironment + `
targets:
  executables:
    - {name: one, language: cpp, sources: ["main.cpp"], defines: ["VALUE=1"]}
    - {name: two, language: cpp, sources: ["main.cpp"], defines: ["VALUE=2"]}
`,
	})
	compiles := tasksOfType(mustGenerateTasks(t, root), "compile")
	if len(compiles) != 2 {
		t.Fatalf("got %d compile tasks, want 2", len(compiles))
	}
	// Task IDs name the source by its path in the module, not by where the workspace is
	for _, c := range compiles {
		if !strings.HasPrefix(c.TaskID, "compile_one_main_cpp_") && !strings.HasPrefix(c.TaskID, "compile_two_main_cpp_") {
			t.Errorf("task ID %s doesn't name main.cpp by its path in the module", c.TaskID)
		}
	}
	a, b := compiles[0].Outputs[0], compiles[1].Outputs[0]
	if a == b {
		t.Fatalf("both targets compile to %s", a)
	}
	for _, out := range []string{a, b} {
		if !strings.Contains(filepath.ToSlash(out), "/obj/") || !filepath.IsAbs(out) {
			t.Errorf("object %s is not an absolute path under obj/", out)
		}
	}
}

// S5: object paths don't depend on the directory buildy runs from, and a source outside the
// module stays under obj/.
func TestObjectPathsIndependentOfCwd(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"shared/util.cpp": "int util() { return 1; }",
		"core/main.cpp":   "int main() {}",
		"sub/.keep":       "",
		"buildy.yaml": `
project: {name: cwd}
` + cppEnvironment + `
workspace:
  modules: [core]
`,
		"core/buildy.yaml": `
project: {name: core}
targets:
  executables:
    - {name: app, language: cpp, sources: ["main.cpp", "../shared/util.cpp"]}
`,
	})

	outputsFrom := func(dir string) []string {
		t.Chdir(dir)
		var outs []string
		for _, task := range tasksOfType(mustGenerateTasks(t, root), "compile") {
			outs = append(outs, task.Outputs[0])
		}
		return outs
	}
	fromRoot := outputsFrom(root)
	fromSub := outputsFrom(filepath.Join(root, "sub"))
	if strings.Join(fromRoot, "|") != strings.Join(fromSub, "|") {
		t.Errorf("object paths depend on the current directory:\n  %v\n  %v", fromRoot, fromSub)
	}
	obj := filepath.Join(root, "build", "linux-x86_64-debug", "obj")
	want := filepath.Join(obj, "core", "app", "_up", "shared", "util.cpp.o")
	found := false
	for _, out := range fromRoot {
		if !strings.HasPrefix(out, obj+string(filepath.Separator)) {
			t.Errorf("object %s escapes %s", out, obj)
		}
		if out == want {
			found = true
		}
	}
	if !found {
		t.Errorf("no object at %s; got %v", want, fromRoot)
	}
}

// A module outside the workspace root ("../lib", as Monstro's CI workspace lists its library)
// keeps its objects under obj/; they used to land outside the output directory.
func TestObjectPathsOfModulesOutsideTheRoot(t *testing.T) {
	top := t.TempDir()
	root := filepath.Join(top, "ws")
	writeFiles(t, top, map[string]string{
		"ws/buildy.yaml":  "project: {name: ws}\n" + cppEnvironment + "workspace:\n  modules: [../lib]\n",
		"lib/buildy.yaml": "targets:\n  static_libraries:\n    - {name: calc, language: cpp, sources: [add.cpp]}\n",
		"lib/add.cpp":     "int add(int a, int b) { return a + b; }",
	})
	compiles := tasksOfType(mustGenerateTasks(t, root), "compile")
	if len(compiles) != 1 {
		t.Fatalf("got %d compiles", len(compiles))
	}
	want := filepath.Join(root, "build", "linux-x86_64-debug", "obj", "_up", "lib", "calc", "add.cpp.o")
	if got := compiles[0].Outputs[0]; got != want {
		t.Errorf("object at %s, want %s", got, want)
	}
}

// Objects keep their source's extension, as CMake's do: a.c and a.cpp in one target are two
// objects (they collided at obj/.../a.o, which graph validation rejected).
func TestObjectsKeepTheSourceExtension(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: ws}\n" + cppEnvironment + "    c_standard: c11\n" +
			"targets:\n  executables:\n    - {name: app, language: cpp, sources: [a.c, a.cpp]}\n",
		"a.c":   "int c(void) { return 1; }",
		"a.cpp": "int main() {}",
	})
	tasks := mustGenerateTasks(t, root)
	if err := graphOf(t, tasks...).Validate(); err != nil {
		t.Fatal(err)
	}
	var objects []string
	for _, task := range tasksOfType(tasks, "compile") {
		objects = append(objects, filepath.Base(task.Outputs[0])+" "+filepath.Base(task.DepFile.Path))
	}
	sort.Strings(objects)
	if strings.Join(objects, ", ") != "a.c.o a.c.d, a.cpp.o a.cpp.d" {
		t.Errorf("objects and dependency files: %v", objects)
	}
}
