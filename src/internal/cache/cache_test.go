package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"buildy/internal/resource"
	"buildy/internal/workspace"
	"buildy/pkg/util"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// compileTask is a compile task of dir/main.cpp to dir/main.o with a depfile dir/main.d.
func compileTask(t *testing.T, dir string) *workspace.BuildTask {
	t.Helper()
	task := workspace.NewBuildTask("compile_main_001", "compile",
		[]workspace.TaskInput{workspace.NewTaskInput(filepath.Join(dir, "main.cpp"))},
		[]string{filepath.Join(dir, "main.o"), filepath.Join(dir, "main.d")},
		nil, util.Command{Args: []string{"g++", "-c", "main.cpp", "-o", "main.o"}})
	task.DepFile = &workspace.DepFile{Path: filepath.Join(dir, "main.d"), Format: resource.DepFormatMake}
	return &task
}

// ran simulates a successful run: writes the outputs and records the entry.
func ran(t *testing.T, bc *BuildCache, task *workspace.BuildTask, dir string) {
	t.Helper()
	write(t, filepath.Join(dir, "main.o"), "object")
	write(t, filepath.Join(dir, "main.d"), "main.o: main.cpp config other.h\n")
	if err := task.HashInputs(util.HashFile); err != nil {
		t.Fatal(err)
	}
	if err := bc.Record(task, dir); err != nil {
		t.Fatal(err)
	}
}

func lookup(t *testing.T, bc *BuildCache, task *workspace.BuildTask) (bool, string) {
	t.Helper()
	if err := task.HashInputs(util.HashFile); err != nil {
		t.Fatal(err)
	}
	return bc.Lookup(task)
}

// closeAtEnd closes the cache when the test ends, as every run does: an open journal can't be
// removed on Windows, nor the temporary directory holding it.
func closeAtEnd(t *testing.T, bc *BuildCache) {
	t.Cleanup(func() {
		if err := bc.Close(); err != nil {
			t.Error(err)
		}
	})
}

func setup(t *testing.T) (*BuildCache, *workspace.BuildTask, string) {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.cpp"), "int main() {}")
	write(t, filepath.Join(dir, "config"), "#define VALUE 1")
	write(t, filepath.Join(dir, "other.h"), "")
	bc, err := NewBuildCache(filepath.Join(dir, ".buildy_cache"))
	if err != nil {
		t.Fatal(err)
	}
	closeAtEnd(t, bc)
	task := compileTask(t, dir)
	ran(t, bc, task, dir)
	return bc, task, dir
}

func TestLookupHitsWhenNothingChanged(t *testing.T) {
	bc, task, _ := setup(t)
	if hit, reason := lookup(t, bc, task); !hit {
		t.Fatalf("expected a hit, got a miss: %s", reason)
	}

	// The entry survives a reload from disk.
	reloaded, err := NewBuildCache(bc.CacheDir())
	if err != nil {
		t.Fatal(err)
	}
	if hit, reason := lookup(t, reloaded, task); !hit {
		t.Fatalf("expected a hit after reload, got a miss: %s", reason)
	}
}

// Finding 5: a deleted output is a miss, not a hit with the output absent.
func TestLookupMissesWhenAnOutputIsMissing(t *testing.T) {
	bc, task, dir := setup(t)
	os.Remove(filepath.Join(dir, "main.o"))
	if hit, _ := lookup(t, bc, task); hit {
		t.Fatal("hit with a declared output missing")
	}
}

func TestLookupMissesWhenAnOutputChanged(t *testing.T) {
	bc, task, dir := setup(t)
	write(t, filepath.Join(dir, "main.o"), "tampered")
	if hit, _ := lookup(t, bc, task); hit {
		t.Fatal("hit with a changed output")
	}
}

// Finding 6: an extensionless header from the depfile is tracked.
func TestLookupMissesWhenADiscoveredInputChanged(t *testing.T) {
	bc, task, dir := setup(t)
	write(t, filepath.Join(dir, "config"), "#define VALUE 2")
	hit, reason := lookup(t, bc, task)
	if hit {
		t.Fatal("hit after the extensionless header 'config' changed")
	}
	if !strings.Contains(reason, "config") {
		t.Errorf("reason %q doesn't name config", reason)
	}
}

func TestLookupMissesWhenADiscoveredInputWasRemoved(t *testing.T) {
	bc, task, dir := setup(t)
	os.Remove(filepath.Join(dir, "other.h"))
	if hit, _ := lookup(t, bc, task); hit {
		t.Fatal("hit after a header was removed")
	}
}

// A header the depfile listed but that didn't exist is recorded as missing: its appearance is a
// miss.
func TestLookupMissesWhenAMissingDiscoveredInputAppears(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.cpp"), "int main() {}")
	write(t, filepath.Join(dir, "config"), "")
	bc, err := NewBuildCache(filepath.Join(dir, ".buildy_cache"))
	if err != nil {
		t.Fatal(err)
	}
	closeAtEnd(t, bc)
	task := compileTask(t, dir)
	ran(t, bc, task, dir) // the depfile lists other.h, which doesn't exist
	if hit, reason := lookup(t, bc, task); !hit {
		t.Fatalf("expected a hit, got a miss: %s", reason)
	}
	write(t, filepath.Join(dir, "other.h"), "")
	if hit, _ := lookup(t, bc, task); hit {
		t.Fatal("hit after a missing header appeared")
	}
}

func TestLookupMissesWhenADeclaredInputChanged(t *testing.T) {
	bc, task, dir := setup(t)
	write(t, filepath.Join(dir, "main.cpp"), "int main() { return 1; }")
	if hit, _ := lookup(t, bc, task); hit {
		t.Fatal("hit after the source changed")
	}
}

// Finding 5: recording fails when a declared output is absent, and writes no entry.
func TestRecordFailsWithoutADeclaredOutput(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.cpp"), "int main() {}")
	bc, err := NewBuildCache(filepath.Join(dir, ".buildy_cache"))
	if err != nil {
		t.Fatal(err)
	}
	closeAtEnd(t, bc)
	task := compileTask(t, dir)
	write(t, filepath.Join(dir, "main.d"), "main.o: main.cpp\n")
	if err := task.HashInputs(util.HashFile); err != nil {
		t.Fatal(err)
	}
	err = bc.Record(task, dir)
	if err == nil || !strings.Contains(err.Error(), "main.o") {
		t.Fatalf("expected an error naming main.o, got %v", err)
	}
	if hit, _ := bc.Lookup(task); hit {
		t.Fatal("an entry was written")
	}
}

func TestRecordFailsOnAnUnparsableDepFile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.cpp"), "int main() {}")
	write(t, filepath.Join(dir, "main.o"), "object")
	write(t, filepath.Join(dir, "main.d"), "garbage without a separator\n")
	bc, err := NewBuildCache(filepath.Join(dir, ".buildy_cache"))
	if err != nil {
		t.Fatal(err)
	}
	closeAtEnd(t, bc)
	task := compileTask(t, dir)
	if err := task.HashInputs(util.HashFile); err != nil {
		t.Fatal(err)
	}
	if err := bc.Record(task, dir); err == nil {
		t.Fatal("expected an error for an unparsable dependency file")
	}
}

// The key covers the declared outputs: the same command writing elsewhere is another task.
func TestCacheKeyIncludesOutputs(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.cpp"), "int main() {}")
	a := compileTask(t, dir)
	b := compileTask(t, dir)
	b.Outputs = []string{filepath.Join(dir, "other.o")}
	if err := a.HashInputs(util.HashFile); err != nil {
		t.Fatal(err)
	}
	if err := b.HashInputs(util.HashFile); err != nil {
		t.Fatal(err)
	}
	if a.CacheKey == b.CacheKey {
		t.Error("tasks with different outputs share a cache key")
	}
}

// S11: the key covers where a task runs, not just its toolchain's name.
func TestCacheKeyIncludesExec(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.cpp"), "int main() {}")
	key := func(mutate func(*workspace.BuildTask)) string {
		task := compileTask(t, dir)
		mutate(task)
		if err := task.HashInputs(util.HashFile); err != nil {
			t.Fatal(err)
		}
		return task.CacheKey
	}
	native := key(func(*workspace.BuildTask) {})
	docker := key(func(task *workspace.BuildTask) { task.Exec = util.ExecSpec{Type: "docker", Image: "gcc:13"} })
	otherImage := key(func(task *workspace.BuildTask) { task.Exec = util.ExecSpec{Type: "docker", Image: "gcc:14"} })
	otherEnv := key(func(task *workspace.BuildTask) { task.Exec = util.ExecSpec{Env: map[string]string{"A": "1"}} })
	keys := map[string]bool{native: true, docker: true, otherImage: true, otherEnv: true}
	if len(keys) != 4 {
		t.Errorf("keys collide: native %s, docker %s, gcc:14 %s, environment %s", native, docker, otherImage, otherEnv)
	}
}

// Finding 1.3: the program a command runs and the environment variables its toolchain reads are
// in the key.
func TestCacheKeyIncludesToolAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.cpp"), "int main() {}")
	key := func(mutate func(*workspace.BuildTask)) string {
		task := compileTask(t, dir)
		task.Exec.ReadsEnv = []string{"CPATH"}
		mutate(task)
		if err := task.HashInputs(util.HashFile); err != nil {
			t.Fatal(err)
		}
		return task.CacheKey
	}
	t.Setenv("CPATH", "/one")
	one := key(func(*workspace.BuildTask) {})
	if again := key(func(*workspace.BuildTask) {}); again != one {
		t.Error("the key isn't stable")
	}
	t.Setenv("CPATH", "/two")
	if two := key(func(*workspace.BuildTask) {}); two == one {
		t.Error("changing CPATH kept the key")
	}
	os.Unsetenv("CPATH")
	unset := key(func(*workspace.BuildTask) {})
	t.Setenv("CPATH", "")
	if empty := key(func(*workspace.BuildTask) {}); empty == unset {
		t.Error("an empty CPATH has the key of an unset one")
	}
	// Exec.Env sets what the command sees
	if set := key(func(task *workspace.BuildTask) { task.Exec.Env = map[string]string{"CPATH": "/one"} }); set == unset {
		t.Error("the toolchain's own CPATH didn't change the key")
	}
	if other := key(func(task *workspace.BuildTask) { task.Tool = "/usr/bin/gcc abc" }); other == unset {
		t.Error("another tool kept the key")
	}
	// A Docker container sees none of the host's environment
	t.Setenv("CPATH", "/one")
	d1 := key(func(task *workspace.BuildTask) {
		task.Exec = util.ExecSpec{Type: "docker", Image: "x", ReadsEnv: []string{"CPATH"}}
	})
	t.Setenv("CPATH", "/two")
	if d2 := key(func(task *workspace.BuildTask) {
		task.Exec = util.ExecSpec{Type: "docker", Image: "x", ReadsEnv: []string{"CPATH"}}
	}); d1 != d2 {
		t.Error("the host's CPATH changed a Docker task's key")
	}
}
