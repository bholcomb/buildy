package main

// End-to-end tests: build the buildy binary once, copy a fixture from testdata/e2e/<case>/ to a
// temporary directory, and run buildy on it. A test skips when a tool it needs (g++, go, cargo,
// docker) is missing. C++ cases run on Linux and macOS.

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildyBinary is the binary under test, built by TestMain.
var buildyBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "buildy-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	buildyBinary = filepath.Join(dir, "buildy"+exeSuffix())
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go"+exeSuffix())
	build := exec.Command(goTool, "build", "-o", buildyBinary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building buildy for the end-to-end tests: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// needTools skips the test unless every tool is on the PATH.
func needTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("skipping: %s is not on the PATH", tool)
		}
	}
}

// needCpp skips C++ cases where they don't run yet (Windows) or g++ is missing.
func needCpp(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skipping: the C++ end-to-end cases run on Linux and macOS")
	}
	needTools(t, "g++")
}

// fixture copies testdata/e2e/<name> to a temporary directory and returns the copy.
func fixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata", "e2e", name)
	dst := filepath.Join(t.TempDir(), name)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("copying fixture %s: %v", name, err)
	}
	return dst
}

// buildy runs buildy in dir and returns its combined output and error.
func buildy(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(buildyBinary, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// mustBuildy runs buildy in dir and fails the test if it fails.
func mustBuildy(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := buildy(t, dir, args...)
	if err != nil {
		t.Fatalf("buildy %s in %s failed: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return out
}

// mustFailBuildy runs buildy in dir and fails the test if it succeeds.
func mustFailBuildy(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := buildy(t, dir, args...)
	if err == nil {
		t.Fatalf("buildy %s in %s succeeded, want a failure\n%s", strings.Join(args, " "), dir, out)
	}
	return out
}

// outDir is the default output directory for a configuration.
func outDir(dir, config string) string {
	return filepath.Join(dir, "build", fmt.Sprintf("%s-%s-%s", detectPlatform(), detectArchitecture(), config))
}

// run runs a program and returns its trimmed standard output.
func run(t *testing.T, program string, args ...string) string {
	t.Helper()
	out, err := exec.Command(program, args...).Output()
	if err != nil {
		t.Fatalf("running %s: %v", program, err)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// S3: removing a library source removes its member from the archive.
func TestE2EStaleArchive(t *testing.T) {
	needCpp(t)
	needTools(t, "ar")
	dir := fixture(t, "stale_archive")
	lib := filepath.Join(outDir(dir, "debug"), "lib", "libcalc.a")

	mustBuildy(t, dir)
	if members := run(t, "ar", "t", lib); !strings.Contains(members, "sub.o") {
		t.Fatalf("libcalc.a members %q lack sub.o", members)
	}

	if err := os.Remove(filepath.Join(dir, "lib", "sub.cpp")); err != nil {
		t.Fatal(err)
	}
	mustBuildy(t, dir)
	if members := run(t, "ar", "t", lib); strings.Contains(members, "sub.o") {
		t.Errorf("libcalc.a still holds sub.o after sub.cpp was removed: %q", members)
	}
}

// Finding 6 and S8: editing an extensionless header, or one under a directory with a space,
// rebuilds.
func TestE2EExtensionlessHeader(t *testing.T) {
	needCpp(t)
	dir := fixture(t, "extensionless_header")
	app := filepath.Join(outDir(dir, "debug"), "bin", "app")

	mustBuildy(t, dir)
	if got := run(t, app); got != "111" {
		t.Fatalf("app printed %q, want 111", got)
	}

	writeFile(t, filepath.Join(dir, "config"), "#define VALUE 2\n")
	mustBuildy(t, dir)
	if got := run(t, app); got != "211" {
		t.Errorf("after editing config, app printed %q, want 211", got)
	}

	writeFile(t, filepath.Join(dir, "inc dir", "v.h"), "#define V 2\n")
	mustBuildy(t, dir)
	if got := run(t, app); got != "212" {
		t.Errorf("after editing 'inc dir/v.h', app printed %q, want 212", got)
	}

	// Nothing changed: every task is a cache hit.
	out := mustBuildy(t, dir)
	if !strings.Contains(out, "0 executed") {
		t.Errorf("a no-op build executed tasks:\n%s", out)
	}
}

// Finding 2: two targets built from one source keep their own objects.
func TestE2ESharedSource(t *testing.T) {
	needCpp(t)
	dir := fixture(t, "shared_source")
	mustBuildy(t, dir)
	bin := filepath.Join(outDir(dir, "debug"), "bin")
	if got := run(t, filepath.Join(bin, "one")); got != "1" {
		t.Errorf("one printed %q, want 1", got)
	}
	if got := run(t, filepath.Join(bin, "two")); got != "2" {
		t.Errorf("two printed %q, want 2", got)
	}
}

// S5: "buildy ." and "cd sub && buildy .." build the same objects in the same places.
func TestE2ECwdIndependent(t *testing.T) {
	needCpp(t)
	dir := fixture(t, "cwd_independent")
	objects := func() []string {
		var files []string
		filepath.WalkDir(filepath.Join(dir, "build"), func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(dir, path)
				files = append(files, rel)
			}
			return nil
		})
		return files
	}

	mustBuildy(t, dir, ".")
	fromRoot := objects()
	for _, generated := range []string{"build", ".buildy_cache"} {
		if err := os.RemoveAll(filepath.Join(dir, generated)); err != nil {
			t.Fatal(err)
		}
	}

	mustBuildy(t, filepath.Join(dir, "sub"), "..")
	fromSub := objects()
	if strings.Join(fromRoot, "\n") != strings.Join(fromSub, "\n") {
		t.Errorf("outputs differ:\nbuildy .:\n  %s\ncd sub && buildy ..:\n  %s",
			strings.Join(fromRoot, "\n  "), strings.Join(fromSub, "\n  "))
	}
	for _, stray := range []string{"build", ".buildy_cache"} {
		if _, err := os.Stat(filepath.Join(dir, "sub", stray)); err == nil {
			t.Errorf("buildy .. created sub/%s", stray)
		}
	}
	want := filepath.Join("build", fmt.Sprintf("%s-%s-debug", detectPlatform(), detectArchitecture()), "obj", ".", "app", "src", "main.o")
	if !strings.Contains(strings.Join(fromRoot, "\n"), filepath.Clean(want)) {
		t.Errorf("no object at %s:\n  %s", filepath.Clean(want), strings.Join(fromRoot, "\n  "))
	}
}

// Finding 3: a changed static library relinks the executable.
func TestE2EStaticRelink(t *testing.T) {
	needCpp(t)
	dir := fixture(t, "static_relink")
	app := filepath.Join(outDir(dir, "debug"), "bin", "app")

	mustBuildy(t, dir)
	if got := run(t, app); got != "5" {
		t.Fatalf("app printed %q, want 5", got)
	}
	writeFile(t, filepath.Join(dir, "add.cpp"), "int add(int a, int b) { return a * b; }\n")
	mustBuildy(t, dir)
	if got := run(t, app); got != "6" {
		t.Errorf("after changing add(), app printed %q, want 6: it was not relinked", got)
	}
}

// Finding 4 and S6: editing a package imported from outside the target's directory, or a file it
// embeds, changes the build. Go runs every time and reports "ran", never a cache hit.
func TestE2EGoOutsideImport(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		t.Skip("skipping: there is no built-in Go toolchain for macOS arm64")
	}
	needTools(t, "go")
	dir := fixture(t, "go_outside_import")
	app := filepath.Join(outDir(dir, "debug"), "bin", "app"+exeSuffix())

	mustBuildy(t, dir)
	if got := run(t, app); got != "v1 m1" {
		t.Fatalf("app printed %q, want %q", got, "v1 m1")
	}

	greet := filepath.Join(dir, "internal", "greet", "greet.go")
	data, err := os.ReadFile(greet)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, greet, strings.Replace(string(data), `"v1 "`, `"v2 "`, 1))
	out := mustBuildy(t, dir)
	if got := run(t, app); got != "v2 m1" {
		t.Errorf("after editing internal/greet, app printed %q, want %q", got, "v2 m1")
	}
	if !strings.Contains(out, "0 cache hits") {
		t.Errorf("the Go build was a cache hit:\n%s", out)
	}

	writeFile(t, filepath.Join(dir, "internal", "greet", "message.txt"), "m2")
	mustBuildy(t, dir)
	if got := run(t, app); got != "v2 m2" {
		t.Errorf("after editing the embedded file, app printed %q, want %q", got, "v2 m2")
	}
}

// Finding 5 and S7: the declared Rust outputs are where Cargo writes them, and a deleted
// binary is rebuilt.
func TestE2ERustOutputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping: the built-in Windows Rust toolchain cross-compiles from Linux")
	}
	needTools(t, "cargo")
	dir := fixture(t, "rust_outputs")
	cargoDir := filepath.Join(outDir(dir, "debug"), "cargo", "debug")
	hello := filepath.Join(cargoDir, "hello")

	mustBuildy(t, dir)
	if got := run(t, hello); got != "hello" {
		t.Fatalf("hello printed %q", got)
	}
	if _, err := os.Stat(filepath.Join(cargoDir, "libcalc.a")); err != nil {
		t.Errorf("static library: %v", err)
	}

	if err := os.Remove(hello); err != nil {
		t.Fatal(err)
	}
	mustBuildy(t, dir)
	if _, err := os.Stat(hello); err != nil {
		t.Errorf("the deleted binary was not rebuilt: %v", err)
	}
}
