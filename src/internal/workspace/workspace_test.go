package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"buildy/internal/config"
	"buildy/internal/resource"
	"buildy/pkg/util"
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
		"common/buildy.yaml":             module,
		"platform-linux/buildy.yaml":     module,
		"platform-win/buildy.yaml":       module,
		"arm/buildy.yaml":                module,
		"rel/buildy.yaml":                module,
		"parent/buildy.yaml":             "project: {name: parent}\nworkspace:\n  modules:\n    - linux: [child-linux]\n    - windows: [child-win]\n",
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
		// The loader rejects the file
		if _, err := NewWorkspace(root); err == nil || !strings.Contains(err.Error(), "workspace.modules") {
			t.Errorf("modules: %s: %v", modules, err)
		}
	}
}

// repoRoot is the buildy repository, which holds examples/ and buildy's own buildy.yaml.
const repoRoot = "../../.."

// The configs that ship with buildy pass the strict loader: the examples (their root configs,
// modules, and dependency override configs) and buildy's own buildy.yaml.
func TestShippedConfigsValidate(t *testing.T) {
	var configs []string
	err := filepath.WalkDir(filepath.Join(repoRoot, "examples"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "build" || d.Name() == ".buildy_cache") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".yaml") && !strings.Contains(filepath.ToSlash(path), "/buildy_config/") {
			configs = append(configs, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	overrides, _ := filepath.Glob(filepath.Join(repoRoot, "examples", "*", "buildy_config", "dependencies", "*_build.yaml"))
	configs = append(configs, overrides...)
	configs = append(configs, filepath.Join(repoRoot, "buildy.yaml"))
	if len(configs) < 5 {
		t.Fatalf("found only %v", configs)
	}

	for _, path := range configs {
		root := filepath.Base(filepath.Dir(path)) == "examples" || filepath.Dir(path) == repoRoot ||
			filepath.Base(filepath.Dir(filepath.Dir(path))) == "examples" || strings.HasSuffix(path, "_build.yaml")
		if _, err := config.LoadFile(path, root); err != nil {
			t.Errorf("%v", err)
		}
	}

	// The examples' dependency definitions
	examples, _ := filepath.Glob(filepath.Join(repoRoot, "examples", "*"))
	for _, example := range examples {
		deps, err := resource.NewDependencyResolver("linux", "x86_64", "", "debug", t.TempDir(), example, util.NewVariableEnvironment(nil))
		if err != nil {
			t.Fatal(err)
		}
		if err := deps.LoadDependencies(example, ""); err != nil {
			t.Errorf("%s: %v", example, err)
		}
	}
}

// Finding 11: an unknown key, at the top level or in a target, fails the load naming the key and
// the file.
func TestUnknownKeysFailToLoad(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml": "project: {name: ws}\nbogus_key: 1\n",
	})
	_, err := NewWorkspace(root)
	if err == nil || !strings.Contains(err.Error(), "bogus_key") || !strings.Contains(err.Error(), filepath.Join(root, "buildy.yaml")) {
		t.Fatalf("got %v", err)
	}

	writeFiles(t, root, map[string]string{
		"buildy.yaml":     "project: {name: ws}\nworkspace:\n  modules: [app]\n",
		"app/buildy.yaml": "targets:\n  executables:\n    - {name: app, language: cpp, sources: [main.cpp], defnies: [X=1]}\n",
	})
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ws.DiscoverModules(linuxDebug())
	if err == nil || !strings.Contains(err.Error(), "defnies") || !strings.Contains(err.Error(), "did you mean 'defines'") ||
		!strings.Contains(err.Error(), filepath.Join(root, "app", "buildy.yaml")) {
		t.Fatalf("got %v", err)
	}
}

// The schema matches what the generator reads: Go and Rust targets have a path, not sources.
func TestSchemaAcceptsWhatTheGeneratorReads(t *testing.T) {
	dir := t.TempDir()
	load := func(text string) error {
		path := filepath.Join(dir, "buildy.yaml")
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := config.LoadFile(path, true)
		return err
	}
	if err := load(`
project: {name: ok, toolchain: gcc-cpp-linux}
toolchain: gcc-cpp-linux
architectures: {x86_64: {defines: [X64]}}
configurations: {debug: {defines: [DBG]}}
environment:
  link: {flags: [-pthread]}
targets:
  executables:
    - {name: goapp, language: go, path: cmd/app, ldflags: [-s], gcflags: "-N", buildmode: pie}
    - {name: rustapp, language: rust, path: app, features: [a], bin: other}
    - name: cppapp
      language: cpp
      sources: [main.cpp]
      lib_dirs: [lib]
      frameworks: [Cocoa]
      output_prefix: ""
      remove_flags: [-O2]
      remove_defines: [X]
      compile: {flags: [-g], remove_flags: [-O0], defines: [Y]}
      link: {flags: [-s], remove_flags: [-g]}
  static_libraries:
    - {name: rustlib, language: rust, path: lib}
`); err != nil {
		t.Errorf("rejected: %v", err)
	}

	for _, bad := range []string{
		"project: {name: x}\ntargets:\n  executables:\n    - {name: a, language: cpp}\n",
		"project: {name: x}\ntargets:\n  executables:\n    - {name: a, language: go}\n",
		"project: {name: x}\nlibrary: {name: a}\n",
		"project: {name: x}\nenvironment:\n  dependency_builds: {}\n",
		"targets: {}\n",
	} {
		if err := load(bad); err == nil {
			t.Errorf("accepted:\n%s", bad)
		}
	}
}

// From inside a module the workspace root is the outermost buildy.yaml that includes it: listed,
// through child modules, under any filter key, or discovered. A buildy.yaml nothing above includes
// is its own workspace.
func TestFindWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml":                            "project: {name: outer}\nworkspace:\n  modules: [app, nested, {windows: [winmod]}]\n",
		"app/buildy.yaml":                        "targets: {}\n",
		"app/src/main.c":                         "",
		"nested/buildy.yaml":                     "project: {name: nested}\nworkspace:\n  modules: [inner]\n",
		"nested/inner/buildy.yaml":               "targets: {}\n",
		"winmod/buildy.yaml":                     "targets: {}\n",
		"other/buildy.yaml":                      "project: {name: other}\n",
		"other/sub/buildy.yaml":                  "targets: {}\n",
		"other/.buildy_cache/deps/d/buildy.yaml": "project: {name: d}\n",
	})
	for start, want := range map[string]string{
		".":                          ".",
		"app":                        ".",
		"app/src":                    ".",
		"nested/inner":               ".", // through nested's child modules
		"winmod":                     ".", // under a filter key
		"other":                      "other",
		"other/sub":                  "other", // discovery mode
		"other/.buildy_cache/deps/d": "other/.buildy_cache/deps/d",
	} {
		got, _, err := findWorkspaceRoot(filepath.Join(root, start))
		if err != nil {
			t.Fatalf("%s: %v", start, err)
		}
		if got != filepath.Join(root, want) {
			t.Errorf("from %s the root is %s, want %s", start, got, filepath.Join(root, want))
		}
	}
	if _, _, err := findWorkspaceRoot(filepath.Dir(root)); err == nil {
		t.Error("a directory without any buildy.yaml at or above it has a workspace")
	}
}

// A broken config above the current directory is an error, not something to skip.
func TestFindWorkspaceRootBrokenParent(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml":     "workspace:\n  modulez: [app]\n",
		"app/buildy.yaml": "targets: {}\n",
	})
	if _, _, err := findWorkspaceRoot(filepath.Join(root, "app")); err == nil || !strings.Contains(err.Error(), "modulez") {
		t.Errorf("got %v", err)
	}
}
