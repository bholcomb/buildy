package workspace

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"buildy/internal/resource"
)

// commandsOf returns every task's ID and command, in order.
func commandsOf(tasks []*BuildTask) []string {
	var result []string
	for _, task := range tasks {
		result = append(result, task.TaskID+": "+task.Command.String())
	}
	return result
}

// Generation is deterministic and leaves the loaded configs as they were: generating twice from
// one loaded workspace gives the same tasks, and the configs serialise the same before and after
// (finding 6.4: getMergedConfig used to write into the parsed maps).
func TestGenerationDoesNotChangeTheLoadedConfig(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": `project: {name: ws}
variables: {V: "1"}
workspace:
  modules: [app]
environment:
  compile:
    cpp_standard: c++17
    defines: [ROOT]
    flags: [{linux: [-DL], debug: [-DD], x86_64: [-DX]}]
  configurations:
    debug: {optimization: none, defines: [DBG]}
`,
		"app/buildy.yaml": `environment:
  compile:
    defines: ["APP=${V}"]
targets:
  static_libraries:
    - {name: lib, language: cpp, sources: [lib.cpp], defines: [{linux: [LIBL], debug: [LIBD]}]}
  executables:
    - name: app
      language: cpp
      sources: [main.cpp]
      libs: [lib, {linux: [m], debug: [dl], x86_64: [pthread]}]
platforms:
  linux:
    targets:
      executables:
        - {name: app, compile: {defines: [ONLINUX]}}
`,
		"app/lib.cpp":  "",
		"app/main.cpp": "",
	})
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := linuxDebug()
	if _, err := ws.DiscoverModules(ctx); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for path, m := range ws.Modules {
		data, _ := json.Marshal(m.File)
		before[path] = string(data)
	}
	rootBefore, _ := json.Marshal(ws.Config.File)

	toolchains, _ := resource.NewToolchainManagerMulti(nil)
	templates, _ := NewBuildTemplateEngineMulti(nil)
	var first []string
	for i := 0; i < 20; i++ {
		tasks, err := NewConfigParser(ctx, nil, toolchains, "", templates, ws, nil).GenerateWorkspaceTasks()
		if err != nil {
			t.Fatal(err)
		}
		commands := commandsOf(tasks)
		if first == nil {
			first = commands
		} else if !reflect.DeepEqual(commands, first) {
			t.Fatalf("generation %d differs:\n%q\n%q", i, commands, first)
		}
	}
	joined := strings.Join(first, "\n")
	for _, want := range []string{
		"-O0 -DL -DD -DX -DROOT -DAPP=1 -DDBG -DONLINUX",
		"-DROOT -DAPP=1 -DDBG -DLIBL -DLIBD",
		"-llib -lm -ldl -lpthread",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("no command has %q:\n%s", want, joined)
		}
	}

	for path, m := range ws.Modules {
		if data, _ := json.Marshal(m.File); string(data) != before[path] {
			t.Errorf("generation changed the loaded config of module %s", path)
		}
	}
	if rootAfter, _ := json.Marshal(ws.Config.File); string(rootAfter) != string(rootBefore) {
		t.Error("generation changed the loaded root config")
	}
}

// Staging files take the documented {source, use_symlink} form; it was read as a filter map with
// keys nothing matched, and dropped.
func TestStagingFileEntries(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": `project: {name: ws}
staging:
  name: product
  use_symlinks: false
  contents:
    - folder: scripts
      files:
        - a.lua
        - {source: "b.lua", use_symlink: true}
        - linux: [{source: "c.lua", use_symlink: true}]
`,
		"a.lua": "", "b.lua": "", "c.lua": "",
	})
	tasks := mustGenerateTasks(t, root)
	kinds := map[string]string{}
	for _, task := range tasks {
		kinds[filepath.Base(task.Outputs[0])] = task.TaskType
	}
	if want := map[string]string{"a.lua": "copy", "b.lua": "symlink", "c.lua": "symlink"}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("staging tasks %v, want %v", kinds, want)
	}
}

// Finding 5.4: filter keys and abstract keywords are checked against what the build could
// match, with the configurations the workspace declares.
func TestNamesAreChecked(t *testing.T) {
	for _, c := range []struct {
		config string
		want   string // "" for accepted
	}{
		{"defines: [{linxu: [X]}]", "filter key 'linxu'"},
		{"defines: [{portable: [X]}]", "filter key 'portable'"},
		{"defines: [{portable: [X]}]\nconfigurations: {portable: {}}", ""},
		{"defines: [{darwin: [X]}, {aarch64: [Y]}, {gcc-cpp-linux: [Z]}, {release: [R]}]", ""},
		{"optimisation: full", "unknown key 'environment.compile.optimisation' (did you mean 'optimization'?)"},
		{"vectorize: avx", "unknown key 'environment.compile.vectorize'"},
	} {
		root := t.TempDir()
		settings, rest, _ := strings.Cut(c.config, "\n")
		writeFiles(t, root, map[string]string{
			"buildy.yaml": "project: {name: ws}\nenvironment:\n  compile:\n    cpp_standard: c++17\n    " + settings + "\n" + rest + "\n" +
				"targets:\n  executables:\n    - {name: app, language: cpp, sources: [main.cpp]}\n",
			"main.cpp": "",
		})
		_, err := generateTasks(t, root)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.config, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want %q", c.config, err, c.want)
		}
	}
}
