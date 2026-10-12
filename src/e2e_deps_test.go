package main

// End-to-end tests of fetched dependencies: git refs. The remotes are local git repositories; nothing uses the network.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
