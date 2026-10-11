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
