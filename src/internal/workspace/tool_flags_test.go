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
	tasks, err := NewConfigParser(ctx, nil, toolchains, "", templates, ws, nil).GenerateWorkspaceTasks()
	if err != nil {
		t.Fatal(err)
	}
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
