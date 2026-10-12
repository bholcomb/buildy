package main

// End-to-end tests of fetched dependencies: configurations, build markers, git refs
// (doc/design/dependencies.md). The remotes are local git repositories; nothing uses the network.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// gitIn runs git in dir and returns its trimmed output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitRepo makes dir a git repository whose main branch has one commit of its files.
func gitRepo(t *testing.T, dir string) {
	t.Helper()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitCommit(t, dir, "first")
}

// gitCommit commits every change in dir and returns the commit.
func gitCommit(t *testing.T, dir, message string) string {
	t.Helper()
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", message)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

// fileURL is a file:// URL of a local path.
func fileURL(path string) string {
	return "file://" + filepath.ToSlash(path)
}

// depsMarkers returns the build markers of a fetched dependency.
func depsMarkers(t *testing.T, dir, name string) []string {
	t.Helper()
	markers, err := filepath.Glob(filepath.Join(dir, ".buildy_cache", "deps", name, "_build", "*.built.json"))
	if err != nil {
		t.Fatal(err)
	}
	return markers
}

// Findings 1.1, 4.3 and 6.2: a dependency is built for the configuration being built, and only
// for it; a release build links the release build; cmake gets one CMAKE_BUILD_TYPE; the install
// phase fills ${dep_install_dir}; a custom configuration needs a mapping; a no-op build runs no
// command of the dependency's; a changed argument rebuilds it, and a failed build leaves no
// marker.
func TestE2EDependencyConfigurations(t *testing.T) {
	needCpp(t)
	needTools(t, "git", "cmake", "gcc")
	dir := fixture(t, "dep_configurations")
	repo := filepath.Join(t.TempDir(), "dv")
	if err := os.Rename(filepath.Join(dir, "dv"), repo); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, repo)
	deps := filepath.Join(dir, "buildy_config", "dependencies.yaml")
	writeDeps := func(build string) {
		writeFile(t, deps, fmt.Sprintf(`dv:
  git: %q
  build: %s
  include_dirs: ["${dep_install_dir}/include"]
  lib_dirs: ["${dep_install_dir}/lib"]
  libs: [dv]
`, fileURL(repo), build))
	}
	writeDeps("{system: cmake}")
	app := func(config string) string {
		return run(t, filepath.Join(outDir(dir, config), "bin", "app"))
	}

	out := mustBuildy(t, dir, "-c", "release", "-n", "3")
	if got := app("release"); got != "222" {
		t.Errorf("the release app printed %s, want 222 (the release build of dv)", got)
	}
	if n := strings.Count(out, "-DCMAKE_BUILD_TYPE="); n != 1 || !strings.Contains(out, "-DCMAKE_BUILD_TYPE=Release") {
		t.Errorf("cmake got CMAKE_BUILD_TYPE %d times, want once, Release:\n%s", n, out)
	}
	if strings.Contains(out, "(debug, using cmake)") {
		t.Errorf("a release build built the dependency's debug configuration:\n%s", out)
	}
	key := fmt.Sprintf("%s-%s-release", detectPlatform(), detectArchitecture())
	for _, f := range []string{"include/dv.h", "lib/libdv.a"} {
		if _, err := os.Stat(filepath.Join(dir, ".buildy_cache", "deps", "dv", "_install", key, f)); err != nil {
			t.Errorf("the install phase didn't install %s: %v", f, err)
		}
	}

	// A no-op build runs nothing of the dependency's
	out = mustBuildy(t, dir, "-c", "release", "-n", "3")
	for _, unwanted := range []string{"Building dependency", "Executing", "Fetching"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a no-op build did %q:\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, "Dependency dv (release) is up to date") {
		t.Errorf("a no-op build doesn't say the dependency is up to date:\n%s", out)
	}

	mustBuildy(t, dir)
	if got := app("debug"); got != "111" {
		t.Errorf("the debug app printed %s, want 111", got)
	}

	// A configuration cmake doesn't know needs a mapping
	out = mustFailBuildy(t, dir, "-c", "portable")
	if !strings.Contains(out, "no configuration for buildy's 'portable'") || !strings.Contains(out, "build: {configurations: {portable: Release}}") {
		t.Errorf("an unmapped configuration:\n%s", out)
	}
	writeDeps("{system: cmake, configurations: {portable: Release}}")
	mustBuildy(t, dir, "-c", "portable")
	if got := app("portable"); got != "222" {
		t.Errorf("the portable app printed %s, want 222", got)
	}

	// A changed argument rebuilds; a failed build leaves no marker, and the next build runs again
	writeDeps(`{system: cmake, args: ["-DCMAKE_C_FLAGS=-DBROKEN"]}`)
	out = mustFailBuildy(t, dir, "-c", "release")
	if !strings.Contains(out, "broken on purpose") {
		t.Errorf("the broken build's output isn't shown:\n%s", out)
	}
	for _, m := range depsMarkers(t, dir, "dv") {
		if strings.Contains(m, "-release.") {
			t.Errorf("a failed build left its marker %s", m)
		}
	}
	writeDeps("{system: cmake}")
	if out = mustBuildy(t, dir, "-c", "release"); !strings.Contains(out, "Building dependency: dv (release") {
		t.Errorf("the build after a failed one didn't build the dependency:\n%s", out)
	}
	if got := app("release"); got != "222" {
		t.Errorf("the release app printed %s, want 222", got)
	}
	// --force rebuilds it
	if out = mustBuildy(t, dir, "-c", "release", "-f"); !strings.Contains(out, "Building dependency: dv (release") {
		t.Errorf("--force didn't rebuild the dependency:\n%s", out)
	}
}

// Finding 1.10: a git dependency is fetched once and then used offline; a branch moves only with
// --update-deps, and then the build uses its new commit; a short commit ID and a branch named like
// a tag work on the first clone; a changed URL doesn't reuse the old clone.
func TestE2EGitDependencies(t *testing.T) {
	needCpp(t)
	needTools(t, "git", "make", "gcc")
	repo := filepath.Join(t.TempDir(), "lib")
	writeFile(t, filepath.Join(repo, "Makefile"), "all:\n\tprintf '#define VALUE %s\\n' \"$$(cat value)\" > value.h\nclean:\n\trm -f value.h\n")
	writeFile(t, filepath.Join(repo, "value"), "1\n")
	gitRepo(t, repo)
	first := gitIn(t, repo, "rev-parse", "HEAD")
	gitIn(t, repo, "branch", "v2-dev")
	writeFile(t, filepath.Join(repo, "value"), "2\n")
	second := gitCommit(t, repo, "second")

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "buildy.yaml"), `project:
  name: gitdeps
environment:
  compile:
    c_standard: c11
targets:
  executables:
    - name: app
      language: c
      sources: [main.c]
      deps: [lib]
`)
	writeFile(t, filepath.Join(dir, "main.c"), "#include <stdio.h>\n#include \"value.h\"\nint main(void) { printf(\"%d\\n\", VALUE); return 0; }\n")
	deps := filepath.Join(dir, "buildy_config", "dependencies.yaml")
	useRef := func(url, ref string) {
		writeFile(t, deps, fmt.Sprintf("lib:\n  git: %q\n  ref: %q\n  build: {system: make}\n  include_dirs: [\"${dep_dir}\"]\n", url, ref))
	}
	value := func() string {
		return run(t, filepath.Join(outDir(dir, "debug"), "bin", "app"))
	}

	// A short commit ID, and a branch named like a tag, on a first clone
	for _, c := range []struct{ ref, want string }{{first[:8], "1"}, {"v2-dev", "1"}, {"main", "2"}} {
		os.RemoveAll(filepath.Join(dir, ".buildy_cache"))
		useRef(fileURL(repo), c.ref)
		mustBuildy(t, dir)
		if got := value(); got != c.want {
			t.Errorf("ref %s: the app printed %s, want %s", c.ref, got, c.want)
		}
	}

	// Fetched: the build works with the remote gone, and doesn't fetch
	hidden := repo + ".away"
	if err := os.Rename(repo, hidden); err != nil {
		t.Fatal(err)
	}
	if out := mustBuildy(t, dir, "-n", "3"); strings.Contains(out, "Fetching git") {
		t.Errorf("a build of a fetched dependency fetched:\n%s", out)
	}
	if err := os.Rename(hidden, repo); err != nil {
		t.Fatal(err)
	}

	// A new commit on the branch is used only after --update-deps
	writeFile(t, filepath.Join(repo, "value"), "3\n")
	third := gitCommit(t, repo, "third")
	mustBuildy(t, dir)
	if got := value(); got != "2" {
		t.Errorf("without --update-deps the app printed %s, want 2 (commit %s)", got, second)
	}
	out := mustBuildy(t, dir, "--update-deps", "-n", "3")
	if got := value(); got != "3" {
		t.Errorf("after --update-deps the app printed %s, want 3", got)
	}
	if !strings.Contains(out, "is commit "+third) {
		t.Errorf("the build doesn't log the commit it used (%s):\n%s", third, out)
	}

	// Another URL is another clone, even under the same name
	other := filepath.Join(t.TempDir(), "other")
	writeFile(t, filepath.Join(other, "Makefile"), "all:\n\techo '#define VALUE 42' > value.h\nclean:\n\trm -f value.h\n")
	gitRepo(t, other)
	useRef(fileURL(other), "main")
	mustBuildy(t, dir)
	if got := value(); got != "42" {
		t.Errorf("after changing the URL the app printed %s, want 42", got)
	}
}

// A make dependency builds in its source tree: building another configuration cleans it first,
// and ${config} in build.args reaches make.
func TestE2EInSourceDependency(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping: the case uses make and sh")
	}
	needTools(t, "git", "make")
	repo := filepath.Join(t.TempDir(), "dep")
	writeFile(t, filepath.Join(repo, "Makefile"), "all:\n\techo built $(CFG) >> log\nclean:\n\techo cleaned >> log\n")
	gitRepo(t, repo)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "buildy.yaml"), "project:\n  name: insource\n")
	writeFile(t, filepath.Join(dir, "buildy_config", "dependencies.yaml"),
		fmt.Sprintf("dep:\n  git: %q\n  build: {system: make, args: [\"CFG=${config}\"]}\n", fileURL(repo)))
	mustBuildy(t, dir)
	mustBuildy(t, dir)
	mustBuildy(t, dir, "-c", "release")
	data, err := os.ReadFile(filepath.Join(dir, ".buildy_cache", "deps", "dep", "log"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(strings.ReplaceAll(string(data), "\n", " ")), []string{"built", "debug", "cleaned", "built", "release"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("the dependency's make ran %q, want %q", got, want)
	}
}
