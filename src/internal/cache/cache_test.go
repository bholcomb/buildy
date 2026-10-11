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
	if err := task.HashInputs(); err != nil {
		t.Fatal(err)
	}
	if err := bc.Record(task, dir); err != nil {
		t.Fatal(err)
	}
}

func lookup(t *testing.T, bc *BuildCache, task *workspace.BuildTask) (bool, string) {
	t.Helper()
	if err := task.HashInputs(); err != nil {
		t.Fatal(err)
	}
	return bc.Lookup(task)
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
	task := compileTask(t, dir)
	write(t, filepath.Join(dir, "main.d"), "main.o: main.cpp\n")
	if err := task.HashInputs(); err != nil {
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
	task := compileTask(t, dir)
	if err := task.HashInputs(); err != nil {
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
	if err := a.HashInputs(); err != nil {
		t.Fatal(err)
	}
	if err := b.HashInputs(); err != nil {
		t.Fatal(err)
	}
	if a.CacheKey == b.CacheKey {
		t.Error("tasks with different outputs share a cache key")
	}
}

// S11: the key covers where a task runs and its toolchain's content, not just its name.
func TestCacheKeyIncludesExecAndToolchainHash(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.cpp"), "int main() {}")
	key := func(mutate func(*workspace.BuildTask)) string {
		task := compileTask(t, dir)
		mutate(task)
		if err := task.HashInputs(); err != nil {
			t.Fatal(err)
		}
		return task.CacheKey
	}
	native := key(func(*workspace.BuildTask) {})
	docker := key(func(task *workspace.BuildTask) { task.Exec = util.ExecSpec{Type: "docker", Image: "gcc:13"} })
	otherImage := key(func(task *workspace.BuildTask) { task.Exec = util.ExecSpec{Type: "docker", Image: "gcc:14"} })
	otherToolchain := key(func(task *workspace.BuildTask) { task.ToolchainHash = "changed" })
	keys := map[string]bool{native: true, docker: true, otherImage: true, otherToolchain: true}
	if len(keys) != 4 {
		t.Errorf("keys collide: native %s, docker %s, gcc:14 %s, changed toolchain %s", native, docker, otherImage, otherToolchain)
	}
}
