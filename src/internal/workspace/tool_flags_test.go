package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	"buildy/internal/resource"
)

// generateWithToolchains generates root's tasks with its buildy_config/toolchains.
func generateWithToolchains(t *testing.T, root, configuration string) []*BuildTask {
	t.Helper()
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := BuildContext{Platform: "linux", Architecture: "x86_64", Configuration: configuration}
	if _, err := ws.DiscoverModules(ctx); err != nil {
		t.Fatal(err)
	}
	toolchains, err := resource.NewToolchainManagerMulti([]string{filepath.Join(root, "buildy_config", "toolchains")})
	if err != nil {
		t.Fatal(err)
	}
	templates, err := NewBuildTemplateEngineMulti(nil)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := NewConfigParser(ctx, nil, toolchains, "", templates, ws, nil).GenerateWorkspaceTasks()
	if err != nil {
		t.Fatal(err)
	}
	tasks := generated.Tasks
	return tasks
}

// Finding 1.12: transform and generate tools get their common flags and the configuration's, as
// compile tools do, and the module's abstract keywords as their own toolchain maps them.
func TestTransformAndGenerateToolFlags(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": `project: {name: flags}
environment:
  compile:
    optimization: speed
artifacts:
  transform:
    - name: shaders
      tool: shaderc
      inputs: [a.vert]
      outputs: "${out_dir}/${basename}.spv"
  generate:
    - name: code
      tool: shaderc
      inputs: [b.idl]
      outputs: ["${gen_dir}/${basename}.h"]
`,
		"a.vert": "", "b.idl": "",
		"buildy_config/toolchains/shaderc.yaml": `toolchain:
  name: shaderc
  target: {platform: any, architecture: any}
  host: {platform: any, architecture: any}
  flag_mappings:
    optimization:
      none: ["-O0"]
      speed: ["-O"]
  tools:
    compile:
      action: transform
      command: "shaderc ${flags} ${input} -o ${output}"
      input_extensions: [".vert"]
      flags:
        common: ["--common"]
        debug: ["--debug"]
        release: ["--release"]
    gen:
      action: generate
      command: "idlgen ${flags} ${input} ${output}"
      input_extensions: [".idl"]
      flags:
        common: ["--gen-common"]
        debug: ["--gen-debug"]
`,
	})
	for _, c := range []struct{ configuration, transform, generate string }{
		{"debug", "shaderc -O --common --debug ", "idlgen -O --gen-common --gen-debug "},
		{"release", "shaderc -O --common --release ", "idlgen -O --gen-common "},
	} {
		tasks := generateWithToolchains(t, root, c.configuration)
		for _, want := range []struct{ taskType, prefix string }{{"transform", c.transform}, {"generate", c.generate}} {
			found := tasksOfType(tasks, want.taskType)
			if len(found) != 1 {
				t.Fatalf("%d %s tasks, want 1", len(found), want.taskType)
			}
			if got := strings.Join(found[0].Command.Args, " "); !strings.HasPrefix(got, want.prefix) {
				t.Errorf("%s %s runs %q, want it to start with %q", c.configuration, want.taskType, got, want.prefix)
			}
		}
	}
}

// A C++ target's frameworks reach its link on macOS (the C++ toolchains had no ${frameworks}).
func TestMacOSCppLinkHasFrameworks(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: mac}\n" + cppEnvironment + `targets:
  executables:
    - {name: app, language: cpp, sources: [main.cpp], frameworks: [Cocoa, IOKit]}
  shared_libraries:
    - {name: lib, language: cpp, sources: [lib.cpp], frameworks: [CoreVideo]}
`,
		"main.cpp": "int main() {}", "lib.cpp": "int f() { return 1; }",
	})
	for _, arch := range []string{"arm64", "x86_64"} {
		tasks, err := generateTasksFor(t, root, "macos", arch, "release")
		if err != nil {
			t.Fatal(err)
		}
		links := tasksOfType(tasks, "link")
		if len(links) != 2 {
			t.Fatalf("%d link tasks, want 2", len(links))
		}
		for _, link := range links {
			got := strings.Join(link.Command.Args, " ")
			want := "-framework Cocoa -framework IOKit -o "
			if link.Target == "lib" {
				want = "-framework CoreVideo -o "
			}
			if !strings.Contains(got, want) {
				t.Errorf("macos %s: %s links %q, want %q", arch, link.Target, got, want)
			}
		}
	}
}

// A staging task depends on the task that produces its file and no other: a generate step with two
// outputs per input staged its second output after the next input's task (outputs were matched to
// task IDs by index).
func TestStagingDependsOnItsProducer(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": `project: {name: stage}
artifacts:
  generate:
    - name: code
      tool: idl
      inputs: [a.idl, b.idl]
      outputs: ["${gen_dir}/${basename}.h", "${gen_dir}/${basename}.c"]
staging:
  contents:
    - folder: include
      artifacts: [code]
`,
		"a.idl": "", "b.idl": "",
		"buildy_config/toolchains/idl.yaml": `toolchain:
  name: idl
  target: {platform: any, architecture: any}
  host: {platform: any, architecture: any}
  tools:
    gen:
      action: generate
      command: "idlgen ${input} ${output}"
      input_extensions: [".idl"]
`,
	})
	tasks := generateWithToolchains(t, root, "debug")
	graph := graphOf(t, tasks...)
	if err := graph.Validate(); err != nil {
		t.Fatal(err)
	}
	producers := map[string]string{}
	for _, task := range tasks {
		for _, output := range task.Outputs {
			producers[output] = task.TaskID
		}
	}
	staged := 0
	for _, task := range tasks {
		if task.TaskType != NativeCopy && task.TaskType != NativeSymlink {
			continue
		}
		staged++
		want := producers[task.Inputs[0].Path]
		if want == "" || len(task.Dependencies) != 1 || task.Dependencies[0] != want {
			t.Errorf("%s stages %s: depends on %v, want [%s]", task.TaskID, task.Inputs[0].Path, task.Dependencies, want)
		}
	}
	if staged != 4 {
		t.Errorf("%d staging tasks, want 4", staged)
	}
}
