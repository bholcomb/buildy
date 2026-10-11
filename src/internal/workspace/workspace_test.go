package workspace

import (
	"sort"
	"strings"
	"testing"
)

// moduleNames loads the workspace at root for ctx and returns its module paths.
func moduleNames(t *testing.T, root string, ctx BuildContext) []string {
	t.Helper()
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	modules, err := ws.DiscoverModules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func linuxDebug() BuildContext {
	return BuildContext{Platform: "linux", Architecture: "x86_64", Configuration: "debug"}
}

// Finding 7: filtered module lists select modules by platform, architecture and configuration,
// in the list form and the legacy map form, at the top level and in child modules.
func TestModuleSelectionUsesTheBuildContext(t *testing.T) {
	module := "project: {name: m}\n"
	files := map[string]string{
		"common/buildy.yaml":         module,
		"platform-linux/buildy.yaml": module,
		"platform-win/buildy.yaml":   module,
		"arm/buildy.yaml":            module,
		"rel/buildy.yaml":            module,
		"parent/buildy.yaml":         "project: {name: parent}\nworkspace:\n  modules:\n    - linux: [child-linux]\n    - windows: [child-win]\n",
		"parent/child-linux/buildy.yaml": module,
		"parent/child-win/buildy.yaml":   module,
		"legacy/buildy.yaml":             "project: {name: legacy}\nworkspace:\n  modules:\n    common: [shared]\n    linux: [lin]\n    windows: [win]\n",
		"legacy/shared/buildy.yaml":      module,
		"legacy/lin/buildy.yaml":         module,
		"legacy/win/buildy.yaml":         module,
	}

	listForm := `
project: {name: ws}
workspace:
  modules:
    - common
    - parent
    - legacy
    - linux: [platform-linux]
    - windows: [platform-win]
    - arm64: [arm]
    - release: [rel]
`
	mapForm := `
project: {name: ws}
workspace:
  modules:
    common: [common, parent, legacy]
    linux: [platform-linux]
    windows: [platform-win]
    arm64: [arm]
    release: [rel]
`
	for name, rootConfig := range map[string]string{"list": listForm, "map": mapForm} {
		root := t.TempDir()
		files["buildy.yaml"] = rootConfig
		writeFiles(t, root, files)

		for _, tc := range []struct {
			ctx  BuildContext
			want string
		}{
			{linuxDebug(), "common legacy legacy/lin legacy/shared parent parent/child-linux platform-linux"},
			{BuildContext{Platform: "windows", Architecture: "x86_64", Configuration: "debug"},
				"common legacy legacy/shared legacy/win parent parent/child-win platform-win"},
			{BuildContext{Platform: "linux", Architecture: "arm64", Configuration: "release"},
				"arm common legacy legacy/lin legacy/shared parent parent/child-linux platform-linux rel"},
		} {
			got := strings.Join(moduleNames(t, root, tc.ctx), " ")
			if got != tc.want {
				t.Errorf("%s form, %s-%s-%s:\n  got  %s\n  want %s", name, tc.ctx.Platform, tc.ctx.Architecture, tc.ctx.Configuration, got, tc.want)
			}
		}
	}
}

// Finding 7 through generation: a module listed under linux generates its target for linux only.
func TestPlatformModuleGeneratesOnlyForItsPlatform(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml":                "project: {name: ws}\n" + cppEnvironment + "workspace:\n  modules:\n    - linux: [platform-linux]\n",
		"platform-linux/buildy.yaml": "project: {name: pl}\ntargets:\n  executables:\n    - {name: linuxonly, language: cpp, sources: [main.cpp]}\n",
		"platform-linux/main.cpp":    "int main() {}",
	})
	tasks := mustGenerateTasks(t, root)
	if len(tasksOfType(tasks, "link")) != 1 {
		t.Errorf("linux: got %d link tasks, want 1", len(tasksOfType(tasks, "link")))
	}
	tasks, err := generateTasksFor(t, root, "windows", "x86_64", "debug")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Errorf("windows: got %d tasks, want none", len(tasks))
	}
}

func TestModuleListErrors(t *testing.T) {
	for _, modules := range []string{
		"[{path: 3}]",
		"[[nested]]",
		"7",
	} {
		root := t.TempDir()
		writeFiles(t, root, map[string]string{"buildy.yaml": "project: {name: ws}\nworkspace:\n  modules: " + modules + "\n"})
		ws, err := NewWorkspace(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ws.DiscoverModules(linuxDebug()); err == nil {
			t.Errorf("modules: %s was accepted", modules)
		}
	}
}
