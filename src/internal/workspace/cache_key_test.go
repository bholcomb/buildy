package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"buildy/internal/resource"
)

// keysOf generates the workspace at root (with its buildy_config/toolchains) and returns each
// task's cache key, its inputs given a stand-in hash: what the configuration puts in the key.
func keysOf(t *testing.T, root string) map[string]string {
	t.Helper()
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := BuildContext{Platform: "linux", Architecture: "x86_64", Configuration: "debug"}
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
	keys := make(map[string]string, len(tasks))
	for _, task := range tasks {
		for i := range task.Inputs {
			task.Inputs[i].Hash = "content of " + filepath.Base(task.Inputs[i].Path)
		}
		keys[task.TaskID] = task.CalculateCacheKey()
	}
	return keys
}

// keyWorkspace is a module with a static library and two executables, and a copy of the GCC C
// toolchain as the workspace's own.
func keyWorkspace(t *testing.T, buildyYAML string) string {
	t.Helper()
	root := t.TempDir()
	toolchain, err := resource.GetEmbeddedFile("toolchains/gcc-c-linux.yaml")
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, map[string]string{
		"buildy.yaml": buildyYAML,
		"buildy_config/toolchains/my-gcc.yaml": strings.Replace(string(toolchain),
			`name: "gcc-c-linux"`, `name: "my-gcc"`, 1),
		"lib.c":   "int lib(void) { return 1; }",
		"a.c":     "int main(void) { return 0; }",
		"b.c":     "int main(void) { return 0; }",
		"inc/x.h": "",
	})
	return root
}

const keyBase = `project:
  name: keys
toolchain: my-gcc
environment:
  compile:
    c_standard: c11
targets:
  static_libraries:
    - name: lib
      language: c
      sources: [lib.c]
  executables:
    - name: a
      language: c
      sources: [a.c]
      libs: [lib]
    - name: b
      language: c
      sources: [b.c]
      libs: [lib]
`

// keysAfter rewrites root's buildy.yaml and returns the keys then.
func keysAfter(t *testing.T, root, buildyYAML string) map[string]string {
	t.Helper()
	writeFiles(t, root, map[string]string{"buildy.yaml": buildyYAML})
	return keysOf(t, root)
}

// idsWith returns the IDs of the keys whose task belongs to the target, by ID.
func idsWith(keys map[string]string, part string) []string {
	var ids []string
	for id := range keys {
		if strings.Contains(id, part) {
			ids = append(ids, id)
		}
	}
	return ids
}

// Finding 4.2: changing one target's config, or adding a target, changes no other task's key.
func TestEditingOneTargetChangesOnlyItsKeys(t *testing.T) {
	edits := map[string]string{
		"a define on a": strings.Replace(keyBase, "sources: [a.c]", "sources: [a.c]\n      defines: [A=1]", 1),
		"a flag on a":   strings.Replace(keyBase, "sources: [a.c]", "sources: [a.c]\n      flags: [-O1]", 1),
		"a new target c": keyBase + `    - name: c
      language: c
      sources: [b.c]
`,
	}
	for name, yaml := range edits {
		t.Run(name, func(t *testing.T) {
			root := keyWorkspace(t, keyBase)
			base := keysOf(t, root)
			if len(base) != 6 {
				t.Fatalf("%d tasks, want 6", len(base))
			}
			edited := keysAfter(t, root, yaml)
			for id, key := range base {
				changed := edited[id] != key
				ofA := strings.Contains(id, "_a_") || strings.HasSuffix(id, "_a")
				if changed && !(ofA && name != "a new target c") {
					t.Errorf("%s changed the key of %s", name, id)
				}
			}
			if name != "a new target c" {
				compiles := idsWith(base, "compile")
				changedA := false
				for _, id := range compiles {
					if edited[id] != base[id] && strings.Contains(id, "_a_") {
						changedA = true
					}
				}
				if !changedA {
					t.Errorf("%s didn't change the key of a's compile (%v)", name, compiles)
				}
			}
		})
	}
}

// Every change of config that changes what a task produces changes its key.
func TestConfigChangesThatChangeOutputsChangeKeys(t *testing.T) {
	replace := func(old, new string) string {
		if !strings.Contains(keyBase, old) {
			t.Fatalf("%q is not in the base config", old)
		}
		return strings.Replace(keyBase, old, new, 1)
	}
	cases := []struct {
		name, yaml, task string // the task whose key must change: a substring of its ID
	}{
		{"define", replace("sources: [lib.c]", "sources: [lib.c]\n      defines: [X=1]"), "compile_"},
		{"flag", replace("sources: [lib.c]", "sources: [lib.c]\n      flags: [-O2]"), "compile_"},
		{"include dir", replace("sources: [lib.c]", "sources: [lib.c]\n      include_dirs: [inc]"), "compile_"},
		{"standard", replace("c_standard: c11", "c_standard: c99"), "compile_"},
		{"module flag", replace("c_standard: c11", "c_standard: c11\n    flags: [-Wall]"), "compile_"},
		{"output prefix", replace("sources: [lib.c]", "sources: [lib.c]\n      output_prefix: \"x\""), "link_"},
		{"link flag", replace("sources: [a.c]", "sources: [a.c]\n      link:\n        flags: [-s]"), "link_"},
		{"system lib", replace("libs: [lib]\n    - name: b", "libs: [lib, m]\n    - name: b"), "link_"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := keyWorkspace(t, keyBase)
			base := keysOf(t, root)
			edited := keysAfter(t, root, c.yaml)
			changed := 0
			for _, id := range idsWith(base, c.task) {
				if edited[id] != "" && edited[id] != base[id] {
					changed++
				}
			}
			if changed == 0 {
				t.Errorf("no %s task's key changed", c.task)
			}
		})
	}

	// The toolchain's YAML: a flag it adds to every compile
	t.Run("toolchain", func(t *testing.T) {
		root := keyWorkspace(t, keyBase)
		base := keysOf(t, root)
		path := filepath.Join(root, "buildy_config", "toolchains", "my-gcc.yaml")
		data, _ := os.ReadFile(path)
		edited := strings.Replace(string(data), "      flags:\n        common: []", "      flags:\n        common: [\"-fno-common\"]", 1)
		if edited == string(data) {
			t.Fatal("the toolchain's compile flags weren't found")
		}
		os.WriteFile(path, []byte(edited), 0o644)
		keys := keysOf(t, root)
		for _, id := range idsWith(base, "compile_") {
			if keys[id] == base[id] {
				t.Errorf("the toolchain's new flag didn't change the key of %s", id)
			}
		}
	})
}
