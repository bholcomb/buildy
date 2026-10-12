package workspace

import (
	"strings"
	"testing"
)

// S4: two modules may not define targets of the same name; the error names both modules.
func TestSameNamedTargetsInTwoModulesAreRejected(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml":    "project: {name: ws}\n" + cppEnvironment + "workspace:\n  modules: [m1, m2]\n",
		"m1/buildy.yaml": "project: {name: m1}\ntargets:\n  executables:\n    - {name: tool, language: cpp, sources: [main.cpp]}\n",
		"m2/buildy.yaml": "project: {name: m2}\ntargets:\n  executables:\n    - {name: tool, language: cpp, sources: [main.cpp]}\n",
		"m1/main.cpp":    "int main() {}",
		"m2/main.cpp":    "int main() {}",
	})
	_, err := generateTasks(t, root)
	if err == nil {
		t.Fatal("two targets named 'tool' were accepted")
	}
	for _, want := range []string{"'tool'", "module m1", "module m2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// S12: a C++ target without cpp_standard used to compile with "-std=${std}".
func TestMissingCppStandardIsAnError(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: nostd}\ntargets:\n  executables:\n    - {name: app, language: cpp, sources: [main.cpp]}\n",
		"main.cpp":    "int main() {}",
	})
	_, err := generateTasks(t, root)
	if err == nil {
		t.Fatal("a compile with an unresolved ${std} was generated")
	}
	for _, want := range []string{"target 'app'", "${std}", "cpp_standard"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestDependsOnUnknownTargetIsAnError(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: deps}\n" + cppEnvironment + `
targets:
  executables:
    - name: app
      language: cpp
      sources: [main.cpp]
      depends_on:
        targets: [nosuch]
`,
		"main.cpp": "int main() {}",
	})
	tasks, err := generateTasks(t, root)
	if err == nil {
		err = graphOf(t, tasks...).Validate()
	}
	if err == nil || !strings.Contains(err.Error(), "nosuch") {
		t.Fatalf("got %v", err)
	}
}

// S15: compiles of a target that depends on an artifact run after the artifact's tasks, for
// generated headers (an explicit edge) as for generated sources (an input).
func TestArtifactDependenciesReachCompiles(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: gen}\n" + cppEnvironment + `
artifacts:
  generate:
    - name: version_header
      template: version.h.in
      output: "${gen_dir}/version.h"
targets:
  executables:
    - name: app
      language: cpp
      sources: [main.cpp, util.cpp]
      include_dirs: ["${output_dir}/gen"]
      depends_on:
        artifacts: [version_header]
`,
		"version.h.in": "#define VERSION 1",
		"main.cpp":     "int main() {}",
		"util.cpp":     "int util() { return 0; }",
	})
	tasks := mustGenerateTasks(t, root)
	gens := tasksOfType(tasks, "generate")
	if len(gens) != 1 {
		t.Fatalf("got %d generate tasks", len(gens))
	}
	compiles := tasksOfType(tasks, "compile")
	if len(compiles) != 2 {
		t.Fatalf("got %d compile tasks", len(compiles))
	}
	for _, c := range compiles {
		if !hasString(c.Dependencies, gens[0].TaskID) {
			t.Errorf("%s does not depend on %s: %v", c.TaskID, gens[0].TaskID, c.Dependencies)
		}
	}

	// A target naming an artifact that doesn't exist is an error.
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: gen}\n" + cppEnvironment + `
targets:
  executables:
    - {name: app, language: cpp, sources: [main.cpp], depends_on: {artifacts: [nosuch]}}
`,
	})
	if _, err := generateTasks(t, root); err == nil || !strings.Contains(err.Error(), "nosuch") {
		t.Fatalf("got %v", err)
	}
}

// Every task of a target, compiles included, runs after the final task of each target it depends
// on: a compile may include a header that target writes (a Go library's), which is no declared
// input. The dependency's own tasks don't wait for the target.
func TestTargetDependenciesReachEveryTask(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: deps}\n" + cppEnvironment + `
targets:
  executables:
    - {name: gen, language: cpp, sources: [gen.cpp, gen2.cpp]}
    - {name: app, language: cpp, sources: [main.cpp, util.cpp], depends_on: [gen]}
    - {name: tool, language: go, path: tool, depends_on: {targets: [app]}}
`,
		"gen.cpp":  "int main() {}",
		"gen2.cpp": "int gen2() { return 0; }",
		"main.cpp": "int main() {}",
		"util.cpp": "int util() { return 0; }",
	})
	tasks := mustGenerateTasks(t, root)
	final := map[string]string{} // target -> its final task
	for _, task := range tasks {
		if task.TaskType == "link" || task.TaskType == "build" {
			final[task.Target] = task.TaskID
		}
	}
	for _, task := range tasks {
		var want, notWant []string
		switch task.Target {
		case "gen":
			notWant = []string{final["app"], final["tool"]}
		case "app":
			want, notWant = []string{final["gen"]}, []string{final["tool"]}
		case "tool":
			want = []string{final["app"]}
		}
		for _, id := range want {
			if !hasString(task.Dependencies, id) {
				t.Errorf("%s does not depend on %s: %v", task.TaskID, id, task.Dependencies)
			}
		}
		for _, id := range notWant {
			if hasString(task.Dependencies, id) {
				t.Errorf("%s depends on %s", task.TaskID, id)
			}
		}
	}
	if n := len(tasksOfType(tasks, "compile")); n != 4 {
		t.Errorf("got %d compile tasks, want 4", n)
	}
	if err := graphOf(t, tasks...).Validate(); err != nil {
		t.Fatal(err)
	}
}

// Setup tasks are gone: their shared directory outputs broke the one-producer rule.
func TestNoSetupTasks(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: s}\n" + cppEnvironment + "targets:\n  executables:\n    - {name: app, language: cpp, sources: [main.cpp]}\n",
		"main.cpp":    "int main() {}",
	})
	tasks := mustGenerateTasks(t, root)
	if setup := tasksOfType(tasks, "setup"); len(setup) != 0 {
		t.Errorf("found setup tasks: %v", setup[0].TaskID)
	}
	if err := graphOf(t, tasks...).Validate(); err != nil {
		t.Fatal(err)
	}
}

// The root config's staging is generated once, also when the root is a module (it used to be
// generated twice, two producers of every staged file).
func TestRootStagingIsGeneratedOnce(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: s}\n" + cppEnvironment + `
targets:
  executables:
    - {name: app, language: cpp, sources: [main.cpp]}
staging:
  contents:
    - {folder: bin, targets: [app]}
`,
		"main.cpp": "int main() {}",
	})
	tasks := mustGenerateTasks(t, root)
	if err := graphOf(t, tasks...).Validate(); err != nil {
		t.Fatal(err)
	}
	if staged := tasksOfType(tasks, "copy"); len(staged) != 1 {
		t.Errorf("got %d staging copies, want 1", len(staged))
	}
}

// S12: configuration errors found while generating stop the build instead of being skipped with
// a warning.
func TestGenerationConfigErrors(t *testing.T) {
	cases := map[string]struct {
		config string
		files  map[string]string
		want   string
	}{
		"transform with an unknown toolchain": {
			config: "artifacts:\n  transform:\n    - {name: shaders, tool: nosuch, inputs: \"*.vert\", outputs: \"${output_dir}/${basename}.spv\"}\n",
			files:  map[string]string{"a.vert": ""},
			want:   "toolchain 'nosuch'",
		},
		"transform whose inputs match nothing": {
			config: "artifacts:\n  transform:\n    - {name: shaders, tool: glslc, inputs: \"*.vert\", outputs: \"${output_dir}/${basename}.spv\"}\n",
			want:   "no files match",
		},
		"staging an unknown target": {
			config: "staging:\n  contents:\n    - {folder: bin, targets: [nosuch]}\n",
			want:   "'nosuch' is not a target",
		},
		"staging an unknown artifact": {
			config: "staging:\n  contents:\n    - {folder: data, artifacts: [nosuch]}\n",
			want:   "artifact 'nosuch'",
		},
		"install of an unknown staging area": {
			config: "staging:\n  name: one\n  contents: []\ninstall:\n  - {name: pkg, staging: two}\n",
			want:   "staging 'two'",
		},
		"copy of an unknown target": {
			config: "artifacts:\n  copy:\n    - {target: nosuch, destination: out}\n",
			want:   "target 'nosuch'",
		},
		"unknown environment toolchain": {
			config: "environment:\n  toolchains: {default: nosuch}\n",
			want:   "toolchain 'nosuch' (from environment.toolchains.default)",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{"buildy.yaml": "project: {name: c}\n" + tc.config}
			for k, v := range tc.files {
				files[k] = v
			}
			writeFiles(t, root, files)
			_, err := generateTasks(t, root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// Workspace-level staging assembles artifacts of the modules (the game_engine example stages
// its shader module's outputs); it used to find none and skip them with a warning.
func TestWorkspaceStagingOfModuleArtifacts(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": `
project: {name: ws}
workspace:
  modules: [shaders]
staging:
  contents:
    - {folder: shaders, artifacts: [vertex_shaders]}
`,
		"shaders/buildy.yaml": `
artifacts:
  transform:
    - {name: vertex_shaders, tool: glslc, inputs: "src/*.vert", outputs: "${output_dir}/shaders/${basename}.vert.spv"}
`,
		"shaders/src/basic.vert": "",
	})
	tasks := mustGenerateTasks(t, root)
	if err := graphOf(t, tasks...).Validate(); err != nil {
		t.Fatal(err)
	}
	transforms := tasksOfType(tasks, "transform")
	copies := tasksOfType(tasks, "copy")
	if len(transforms) != 1 || len(copies) != 1 {
		t.Fatalf("got %d transforms and %d copies, want 1 each", len(transforms), len(copies))
	}
	if !hasString(copies[0].Dependencies, transforms[0].TaskID) {
		t.Errorf("the staging copy doesn't depend on the transform: %v", copies[0].Dependencies)
	}

	// Two modules defining one artifact name is an error naming both.
	writeFiles(t, root, map[string]string{
		"buildy.yaml":         "project: {name: ws}\nworkspace:\n  modules: [shaders, more]\n",
		"more/buildy.yaml":    "artifacts:\n  transform:\n    - {name: vertex_shaders, tool: glslc, inputs: \"src/*.vert\", outputs: \"${output_dir}/more/${basename}.spv\"}\n",
		"more/src/other.vert": "",
	})
	_, err := generateTasks(t, root)
	if err == nil || !strings.Contains(err.Error(), "module more") || !strings.Contains(err.Error(), "module shaders") {
		t.Fatalf("got %v", err)
	}
}
