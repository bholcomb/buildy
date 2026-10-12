package main

// End-to-end tests of review phase 5: globs, staging and install, --clean, the workspace root,
// and the default configurations.

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
