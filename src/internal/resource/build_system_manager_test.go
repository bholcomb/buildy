package resource

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const plainSystem = `build_system:
  name: %s
  description: "%s"
  detection: {marker_files: [build.sh], priority: %s}
  phases:
    build: {command: "sh build.sh"}
`

// writeSystem writes a definition of a build system name to dir/file, its description the file's
// name.
func writeSystem(t *testing.T, dir, file, name, priority string) string {
	t.Helper()
	path := filepath.Join(dir, file)
	text := fmt.Sprintf(plainSystem, name, file, priority)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// newManager loads the built-in build systems, then those of dirs.
func newManager(t *testing.T, dirs ...string) (*BuildSystemManager, error) {
	t.Helper()
	bsm, err := NewBuildSystemManager()
	if err != nil {
		t.Fatal(err)
	}
	return bsm, bsm.LoadFilesystemSystems(dirs)
}

// A data directory's definition replaces a built-in one or an earlier directory's of the same
// name; its content hash is its build records' (an edit rebuilds); a missing directory is
// skipped.
func TestLoadFilesystemSystems(t *testing.T) {
	root := t.TempDir()
	low, high := filepath.Join(root, "low"), filepath.Join(root, "high")
	writeSystem(t, low, "plain.yaml", "plain", "10")
	writeSystem(t, low, "cmake.yaml", "cmake", "10")
	mine := writeSystem(t, high, "mine.yaml", "plain", "10")

	bsm, err := newManager(t, low, filepath.Join(root, "missing"), high)
	if err != nil {
		t.Fatal(err)
	}
	if got := bsm.GetBuildSystem("plain").Description; got != "mine.yaml" || bsm.sources["plain"] != mine {
		t.Errorf("plain is %q from %s, want the later directory's", got, bsm.sources["plain"])
	}
	if got := bsm.GetBuildSystem("cmake"); got.Description != "cmake.yaml" || got.Phases["install"] != nil {
		t.Errorf("cmake wasn't replaced: %+v", got)
	}
	if bsm.GetBuildSystem("meson") == nil {
		t.Error("the built-in meson is gone")
	}

	before := bsm.definition("plain")
	writeSystem(t, high, "mine.yaml", "plain", "11")
	bsm, err = newManager(t, low, high)
	if err != nil {
		t.Fatal(err)
	}
	if after := bsm.definition("plain"); after == before || after == "" {
		t.Errorf("editing the definition left its hash %s", after)
	}
}

// Two definitions of a name in one directory, and a definition that doesn't load, are errors
// naming the files.
func TestLoadFilesystemSystemsErrors(t *testing.T) {
	dir := t.TempDir()
	a := writeSystem(t, dir, "a.yaml", "plain", "10")
	b := writeSystem(t, dir, "b.yaml", "plain", "10")
	if _, err := newManager(t, dir); err == nil || !strings.Contains(err.Error(), a) || !strings.Contains(err.Error(), b) {
		t.Errorf("two plains in one directory: err = %v", err)
	}

	dir = t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("build_system:\n  name: bad\n  phases: {build: {command: make}}\n  output: {lib_pattern: [x]}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newManager(t, dir); err == nil || !strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), "unknown key 'build_system.output.lib_pattern' (did you mean 'lib_patterns'?)") {
		t.Errorf("an unknown key: err = %v", err)
	}
}

// Detection considers every definition: the highest priority wins; a tie is an error naming
// the build systems, not the first by name.
func TestDetect(t *testing.T) {
	tree := t.TempDir()
	for _, f := range []string{"build.sh", "meson.build"} {
		if err := os.WriteFile(filepath.Join(tree, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	defs := t.TempDir()
	writeSystem(t, defs, "plain.yaml", "plain", "10")
	bsm, err := newManager(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	_, err = bsm.Detect(tree)
	if err == nil || !strings.Contains(err.Error(), "build systems meson (built-in build_systems/meson.yaml, markers meson.build) and plain (") ||
		!strings.Contains(err.Error(), "the same priority (10)") {
		t.Errorf("a tie: err = %v", err)
	}

	writeSystem(t, defs, "plain.yaml", "plain", "11")
	if bsm, err = newManager(t, defs); err != nil {
		t.Fatal(err)
	}
	if bs, err := bsm.Detect(tree); err != nil || bs.Name != "plain" {
		t.Errorf("Detect = %v, %v; want plain", bs, err)
	}
	if bs, err := bsm.Detect(t.TempDir()); bs != nil || err != nil {
		t.Errorf("an empty tree: Detect = %v, %v", bs, err)
	}

	// Two built-ins tie too
	both := t.TempDir()
	for _, f := range []string{"CMakeLists.txt", "meson.build"} {
		if err := os.WriteFile(filepath.Join(both, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := bsm.Detect(both); err == nil || !strings.Contains(err.Error(), "cmake (") || !strings.Contains(err.Error(), "meson (") {
		t.Errorf("cmake and meson: err = %v", err)
	}
}
