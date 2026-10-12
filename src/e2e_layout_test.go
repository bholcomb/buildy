package main

// End-to-end tests of review phase 5: globs, staging and install, --clean, the workspace root,
// and the default configurations.

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// exists reports whether path exists (a symlink counts, whatever it points to).
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// filesUnder lists the files (and symlinks) under dir, relative to it with forward slashes, sorted.
func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

// Finding 1.7: ** matches any number of directories, none included, in sources, copies and
// staging.
func TestE2ERecursiveGlobs(t *testing.T) {
	needCpp(t)
	needTools(t, "gcc")
	dir := fixture(t, "recursive_glob")
	out := outDir(dir, "debug")
	mustBuildy(t, dir)
	if got := run(t, filepath.Join(out, "bin", "app")); got != "123" {
		t.Errorf("app printed %q, want 123 (main.c, a/mid.c and a/b/deep.c)", got)
	}
	if got, want := filesUnder(t, filepath.Join(out, "data")), []string{"deep.dat", "top.dat"}; !reflect.DeepEqual(got, want) {
		t.Errorf("copied %v, want %v", got, want)
	}
	if got, want := filesUnder(t, filepath.Join(out, "staging")), []string{"docs/guide.txt", "docs/top.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("staged %v, want %v", got, want)
	}
}

// The root's output: reaches the modules (and the root's own staging): one output layout per
// workspace.
func TestE2ERootOutputReachesModules(t *testing.T) {
	needCpp(t)
	needTools(t, "gcc")
	dir := fixture(t, "root_output")
	mustBuildy(t, dir)
	ctx := filepath.Base(outDir(dir, "debug"))
	for _, p := range []string{"bin/app", "staging/bin/app"} {
		if !exists(filepath.Join(dir, "out", ctx, p)) {
			t.Errorf("out/%s/%s is missing", ctx, p)
		}
	}
	if exists(filepath.Join(dir, "build")) {
		t.Errorf("build/ exists: a module ignored the root's output.base_dir")
	}
}

// Finding 1.9: staging and copy patterns over build outputs name the build's declared outputs, on
// a clean build too, and never a stale file in the output directory. A pattern into the output
// directory that names no output fails before anything runs.
func TestE2EPatternsOverOutputs(t *testing.T) {
	needCpp(t)
	needTools(t, "gcc")
	dir := fixture(t, "output_globs")
	out := outDir(dir, "debug")
	// A file a previous build (of another config) left in bin/
	writeFile(t, filepath.Join(out, "bin", "stale"), "stale")

	mustBuildy(t, dir)
	if got, want := filesUnder(t, filepath.Join(out, "staging")), []string{"bin/app", "copied/app"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a clean build staged %v, want %v", got, want)
	}
	if got, want := filesUnder(t, filepath.Join(out, "copied")), []string{"app"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a clean build copied %v, want %v", got, want)
	}

	config := filepath.Join(dir, "buildy.yaml")
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, config, string(data)+"    - folder: lib\n      files: [\"${out_dir}/lib/*.so\"]\n")
	out2 := mustFailBuildy(t, dir, "--dry-run")
	if !strings.Contains(out2, "matches no output of the build") || !strings.Contains(out2, "lib/*.so") {
		t.Errorf("a pattern into the output directory that names nothing doesn't fail as it should:\n%s", tail(out2, 10))
	}
}
