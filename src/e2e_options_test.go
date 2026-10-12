package main

// End-to-end tests of the Go and Rust target options: each one reaches the program buildy builds,
// a release build gets its own, and changing one changes the program.

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// output runs a program and returns its output, trimmed, with \n line ends.
func output(t *testing.T, program string) string {
	t.Helper()
	return strings.ReplaceAll(run(t, program), "\r\n", "\n")
}

// editFile replaces old with new in a file, which must hold old.
func editFile(t *testing.T, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s has no %q", path, old)
	}
	writeFile(t, path, strings.Replace(string(data), old, new, 1))
}

// containsAll fails the test for each of wants that text doesn't contain.
func containsAll(t *testing.T, what, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s has no %q:\n%s", what, want, text)
		}
	}
}

// The Go options reach the program: build tags select files, -X sets strings (one with a space,
// quoted for go inside its item), the gcflags and the build mode are what go recorded, and a
// release build is -trimpath with its filtered tags and ldflags. Changing an option changes the
// program, though go build always runs.
func TestE2EGoOptions(t *testing.T) {
	needTools(t, "go")
	dir := fixture(t, "go_options")

	plan := mustBuildy(t, dir, "--target", "app", "--dry-run", "-n", "3")
	containsAll(t, "the dry run", plan, "-buildmode=pie", "-tags fancy ", "main.version=1.2.3", "-gcflags=-N -l")

	mustBuildy(t, dir, "--target", "app")
	app := filepath.Join(outDir(dir, "debug"), "bin", "app"+exeSuffix())
	want := "version=1.2.3 msg=hello world mode=debug tags=fancy\n-buildmode=pie\n-gcflags=-N -l"
	if got := output(t, app); got != want {
		t.Errorf("debug app printed\n%s\nwant\n%s", got, want)
	}

	mustBuildy(t, dir, "--target", "app", "-c", "release")
	release := filepath.Join(outDir(dir, "release"), "bin", "app"+exeSuffix())
	want = "version=1.2.3 msg=hello world mode=release tags=extra,fancy\n-buildmode=pie\n-gcflags=-N -l\n-trimpath=true"
	if got := output(t, release); got != want {
		t.Errorf("release app printed\n%s\nwant\n%s", got, want)
	}

	config := filepath.Join(dir, "buildy.yaml")
	editFile(t, config, "build_tags: [fancy, {release: [extra]}]", "build_tags: [extra]")
	editFile(t, config, "-X main.version=${project_version}", "-X main.version=2.0")
	editFile(t, config, "buildmode: pie", "buildmode: exe")
	editFile(t, config, `gcflags: ["-N", "-l"]`, `gcflags: ["-l"]`)
	mustBuildy(t, dir, "--target", "app")
	want = "version=2.0 msg=hello world mode=debug tags=extra\n-buildmode=exe\n-gcflags=-l"
	if got := output(t, app); got != want {
		t.Errorf("after the edits, app printed\n%s\nwant\n%s", got, want)
	}
}

// A Go static library is a c-archive a C program links, with the header go writes beside it and
// its build tags; a shared library is a c-shared library that exports the functions.
func TestE2EGoLibraries(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping: a C program is linked against a Go archive on Linux only")
	}
	needTools(t, "go", "gcc")
	if cgo, err := exec.Command("go", "env", "CGO_ENABLED").Output(); err != nil || strings.TrimSpace(string(cgo)) != "1" {
		t.Skipf("skipping: Go libraries need cgo, and go env CGO_ENABLED is %q (%v)", strings.TrimSpace(string(cgo)), err)
	}
	dir := fixture(t, "go_options")
	lib := filepath.Join(outDir(dir, "debug"), "lib")

	// The C program's compile includes the archive's header: build the libraries first
	mustBuildy(t, dir, "--target", "carc", "--target", "cshared")
	for _, file := range []string{"libcarc.a", "libcarc.h", "libcshared.so", "libcshared.h"} {
		if !exists(filepath.Join(lib, file)) {
			t.Errorf("no %s in %s", file, lib)
		}
	}
	mustBuildy(t, dir, "--target", "cmain")
	if got := output(t, filepath.Join(outDir(dir, "debug"), "bin", "cmain")); got != "5 2" {
		t.Errorf("cmain printed %q, want %q (Add(2, 3), and Mode() with the tag 'tagged')", got, "5 2")
	}

	symbols := exportedSymbols(t, filepath.Join(lib, "libcshared.so"))
	for _, name := range []string{"Add", "Mode"} {
		if !symbols[name] {
			t.Errorf("libcshared.so doesn't export %s", name)
		}
	}
}

// exportedSymbols returns the names an ELF shared library exports.
func exportedSymbols(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := elf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.DynamicSymbols()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range syms {
		if s.Section != elf.SHN_UNDEF {
			names[s.Name] = true
		}
	}
	return names
}

// The Rust options reach the program: features enable cfgs (a configuration's too), --bin builds
// one binary, a release build is Cargo's release profile, and a library's features decide what it
// exports. Changing an option changes the program, though cargo build always runs.
func TestE2ERustOptions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping: the built-in Windows Rust toolchain cross-compiles from Linux")
	}
	needTools(t, "cargo")
	dir := fixture(t, "rust_options")
	cargo := func(config string) string { return filepath.Join(outDir(dir, config), "cargo", config) }

	plan := mustBuildy(t, dir, "--dry-run", "-n", "3")
	containsAll(t, "the dry run", plan, "--features fancy", "--bin other", "--lib", "--features double")

	mustBuildy(t, dir)
	if got := output(t, filepath.Join(cargo("debug"), "app")); got != "features=fancy profile=debug" {
		t.Errorf("debug app printed %q", got)
	}
	if got := output(t, filepath.Join(cargo("debug"), "other")); got != "other" {
		t.Errorf("tool printed %q", got)
	}
	if runtime.GOOS == "linux" && !exportedSymbols(t, filepath.Join(cargo("debug"), "libplugin.so"))["double_enabled"] {
		t.Errorf("the plugin built with the feature 'double' doesn't export double_enabled")
	}

	mustBuildy(t, dir, "-c", "release")
	if got := output(t, filepath.Join(cargo("release"), "app")); got != "features=fancy,extra profile=release" {
		t.Errorf("release app printed %q", got)
	}

	config := filepath.Join(dir, "buildy.yaml")
	editFile(t, config, "features: [fancy, {release: [extra]}]", "features: [extra]")
	editFile(t, config, "features: [double]", "features: []")
	mustBuildy(t, dir)
	if got := output(t, filepath.Join(cargo("debug"), "app")); got != "features=extra profile=debug" {
		t.Errorf("after the edit, app printed %q", got)
	}
	if runtime.GOOS == "linux" && exportedSymbols(t, filepath.Join(cargo("debug"), "libplugin.so"))["double_enabled"] {
		t.Errorf("the plugin built without the feature 'double' exports double_enabled")
	}
}
