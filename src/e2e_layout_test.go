package main

// End-to-end tests of review phase 5: globs, staging and install, --clean, the workspace root,
// and the default configurations.

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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

// Finding 1.8: an install archive holds exactly what the config stages this run. A file removed
// from the staged set leaves the archive (and reruns the install), and what else is in the
// staging directory is never shipped.
func TestE2EInstallShipsOnlyWhatIsStaged(t *testing.T) {
	dir := fixture(t, "stale_staging")
	out := outDir(dir, "debug")
	archive := filepath.Join(out, "dist", "pkg.tar.gz")

	mustBuildy(t, dir)
	if got, want := tarNames(t, archive), []string{"docs/", "docs/a.txt", "docs/b.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the archive holds %v, want %v", got, want)
	}

	if err := os.Remove(filepath.Join(dir, "files", "b.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(out, "staging", "docs", "mine.txt"), "not staged by buildy")
	mustBuildy(t, dir)
	if got, want := tarNames(t, archive), []string{"docs/", "docs/a.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after b.txt left the staged set the archive holds %v, want %v", got, want)
	}
	if out := mustBuildy(t, dir); !strings.Contains(out, "0 executed") {
		t.Errorf("a no-op build executed tasks:\n%s", out)
	}
}

// Staging links on a host that can, whatever the platform being built for: building for Windows on
// Linux or macOS links too. (On a Windows host it always copies.)
func TestE2EStagingSymlinksFollowTheHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping: a Windows host copies")
	}
	dir := fixture(t, "symlink_staging")
	for _, platform := range []string{"linux", "windows"} {
		mustBuildy(t, dir, "--platform", platform)
		staged := filepath.Join(dir, "build", platform+"-"+detectArchitecture()+"-debug", "staging", "docs", "readme.txt")
		info, err := os.Lstat(staged)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("building for %s staged a copy, want a symlink", platform)
		}
	}
}

// Finding 5.3: buildy run inside a module, or below one, builds the workspace that includes it:
// the root's variables and settings, the other modules' targets, one cache at the root.
func TestE2EBuildFromInsideAModule(t *testing.T) {
	needCpp(t)
	needTools(t, "gcc", "ar")
	dir := fixture(t, "module_dir")
	for _, start := range []string{"app", filepath.Join("app", "src")} {
		mustBuildy(t, filepath.Join(dir, start))
		if got := run(t, filepath.Join(outDir(dir, "debug"), "bin", "app")); got != "42" {
			t.Errorf("built from %s, app printed %q, want 42", start, got)
		}
		for _, stray := range []string{".buildy_cache", "build"} {
			if exists(filepath.Join(dir, "app", stray)) {
				t.Errorf("built from %s: app/%s exists, the module was built as a workspace of its own", start, stray)
			}
		}
	}
}

// Finding 5.2: --clean, run anywhere in the workspace, removes the output directory and nothing it
// didn't produce: directories named build, dist or staging that buildy never configured stay. What
// staging and installs put outside the output directory stays too (it may stand on the user's
// files). With no workspace it refuses. --clean-all removes the cache too.
func TestE2ECleanRemovesOnlyWhatBuildyMade(t *testing.T) {
	dir := fixture(t, "clean_layout")
	theirs := []string{
		"app/build/keep.txt", "app/dist/keep.txt", "app/staging/keep.txt",
		"staging/keep.txt", "dist/mine.txt", "docs/build/keep.txt",
	}
	for _, f := range theirs {
		writeFile(t, filepath.Join(dir, f), "not buildy's")
	}
	mustBuildy(t, dir)
	data := "build/" + filepath.Base(outDir(dir, "debug")) + "/data/a.dat"
	for _, f := range []string{"dist/docs/readme.txt", "dist/pkg.tar.gz", data} {
		if !exists(filepath.Join(dir, f)) {
			t.Fatalf("the build didn't make %s", f)
		}
	}

	mustBuildy(t, filepath.Join(dir, "app"), "--clean")
	for _, f := range append(theirs, "dist/docs/readme.txt", "dist/pkg.tar.gz") {
		if !exists(filepath.Join(dir, f)) {
			t.Errorf("--clean removed %s, outside the output directory", f)
		}
	}
	if exists(filepath.Join(dir, "build")) {
		t.Errorf("--clean left the output directory")
	}
	if !exists(filepath.Join(dir, ".buildy_cache")) {
		t.Error("--clean removed the cache directory")
	}
	if exists(filepath.Join(dir, "app", ".buildy_cache")) {
		t.Error("--clean made a cache directory in the module")
	}

	mustBuildy(t, dir, "--clean-all")
	if exists(filepath.Join(dir, ".buildy_cache")) {
		t.Error("--clean-all left the cache directory")
	}

	// Outside any workspace: refuse, remove nothing
	elsewhere := t.TempDir()
	writeFile(t, filepath.Join(elsewhere, "build", "keep.txt"), "not buildy's")
	writeFile(t, filepath.Join(elsewhere, "dist", "keep.txt"), "not buildy's")
	if out := mustFailBuildy(t, elsewhere, "--clean"); !strings.Contains(out, "Nothing to clean") {
		t.Errorf("--clean outside a workspace:\n%s", out)
	}
	for _, f := range []string{"build/keep.txt", "dist/keep.txt"} {
		if !exists(filepath.Join(elsewhere, f)) {
			t.Errorf("--clean outside a workspace removed %s", f)
		}
	}
}

// --cache-dir: a relative directory is relative to the workspace root, an absolute one is used as
// it is.
func TestE2ECacheDir(t *testing.T) {
	dir := fixture(t, "stale_staging")
	abs := filepath.Join(t.TempDir(), "cache")
	mustBuildy(t, dir, "--cache-dir", abs)
	if !exists(filepath.Join(abs, "cache_index.json")) {
		t.Errorf("an absolute --cache-dir wasn't used as it is")
	}
	if exists(filepath.Join(dir, strings.TrimPrefix(abs, filepath.VolumeName(abs)))) {
		t.Errorf("an absolute --cache-dir was joined onto the workspace root")
	}
	if err := os.MkdirAll(filepath.Join(dir, "files", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustBuildy(t, filepath.Join(dir, "files"), "--cache-dir", "rel-cache")
	if !exists(filepath.Join(dir, "rel-cache", "cache_index.json")) {
		t.Errorf("a relative --cache-dir isn't relative to the workspace root")
	}
}

// Finding 1.11: without configurations: in the config, -c release differs from -c debug: buildy's
// own debug is optimization: none and symbols: true, its release optimization: speed and NDEBUG.
func TestE2EDefaultConfigurations(t *testing.T) {
	needCpp(t)
	needTools(t, "gcc")
	dir := fixture(t, "default_configurations")
	for config, want := range map[string]string{"debug": "asserts unoptimized", "release": "ndebug optimized"} {
		mustBuildy(t, dir, "-c", config)
		if got := run(t, filepath.Join(outDir(dir, config), "bin", "app")); got != want {
			t.Errorf("-c %s: app printed %q, want %q", config, got, want)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, ".buildy_cache", "tasks_"+filepath.Base(outDir(dir, "debug"))+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"-O0"`) || !strings.Contains(string(data), `"-g"`) {
		t.Errorf("the debug compile lacks -O0 or -g")
	}
}
