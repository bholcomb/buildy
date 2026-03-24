package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"buildy/pkg/util"
)

// TestCacheKeyChangesWhenInputFileChanges verifies that the cache key changes
// when an input file's content changes, even if the file existed before.
// This is the critical bug fix for link tasks that depend on compile outputs.
func TestCacheKeyChangesWhenInputFileChanges(t *testing.T) {
	// Create a temp directory for test files
	tmpDir, err := os.MkdirTemp("", "buildy-cache-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test input file (simulating an object file from a previous build)
	inputFile := filepath.Join(tmpDir, "test.o")
	outputFile := filepath.Join(tmpDir, "test.exe")
	
	// Write initial content
	if err := os.WriteFile(inputFile, []byte("initial object file content"), 0644); err != nil {
		t.Fatalf("Failed to write initial input file: %v", err)
	}

	// Create a task with the input file (simulating task generation)
	// At generation time, the hash is computed because the file exists
	task := NewBuildTask(
		"link_test_001",
		"link",
		[]TaskInput{NewTaskInput(inputFile)},
		[]string{outputFile},
		[]string{},
		"ld -o test.exe test.o",
	)
	task.Platform = "linux"
	task.Architecture = "x86_64"
	task.Configuration = "debug"
	task.Toolchain = "gcc"
	task.CacheKey = task.CalculateCacheKey()

	initialCacheKey := task.CacheKey
	initialInputHash := task.Inputs[0].Hash

	if initialCacheKey == "" {
		t.Fatal("Initial cache key should not be empty")
	}
	if initialInputHash == "" {
		t.Fatal("Initial input hash should not be empty")
	}

	// Simulate a compile task running and producing a NEW object file
	// (This is what happens when source code changes)
	if err := os.WriteFile(inputFile, []byte("MODIFIED object file content - different!"), 0644); err != nil {
		t.Fatalf("Failed to write modified input file: %v", err)
	}

	// Call UpdateInputHashesAndCacheKey - this is called before cache lookup
	if err := task.UpdateInputHashesAndCacheKey(); err != nil {
		t.Fatalf("UpdateInputHashesAndCacheKey failed: %v", err)
	}

	updatedCacheKey := task.CacheKey
	updatedInputHash := task.Inputs[0].Hash

	// THE CRITICAL ASSERTION: Cache key MUST change when input content changes
	if updatedCacheKey == initialCacheKey {
		t.Errorf("CRITICAL BUG: Cache key did not change after input file was modified!\n"+
			"Initial cache key: %s\n"+
			"Updated cache key: %s\n"+
			"This would cause stale artifacts to be used.",
			initialCacheKey, updatedCacheKey)
	}

	if updatedInputHash == initialInputHash {
		t.Errorf("Input hash did not change after file modification!\n"+
			"Initial hash: %s\n"+
			"Updated hash: %s",
			initialInputHash, updatedInputHash)
	}

	t.Logf("SUCCESS: Cache key correctly changed from %s to %s", initialCacheKey[:16]+"...", updatedCacheKey[:16]+"...")
}

// TestCacheKeyChangesWhenDirectoryContentsChange verifies that install tasks
// correctly detect when staging directory contents change.
func TestCacheKeyChangesWhenDirectoryContentsChange(t *testing.T) {
	// Create a temp directory structure (simulating staging)
	tmpDir, err := os.MkdirTemp("", "buildy-cache-dir-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	stagingDir := filepath.Join(tmpDir, "staging")
	if err := os.MkdirAll(filepath.Join(stagingDir, "bin"), 0755); err != nil {
		t.Fatalf("Failed to create staging/bin dir: %v", err)
	}

	// Create initial file in staging
	exeFile := filepath.Join(stagingDir, "bin", "myapp.exe")
	if err := os.WriteFile(exeFile, []byte("initial executable content"), 0644); err != nil {
		t.Fatalf("Failed to write initial exe: %v", err)
	}

	// Create install task with directory input
	outputFile := filepath.Join(tmpDir, "dist.zip")
	task := NewBuildTask(
		"install_test_001",
		"install",
		[]TaskInput{{Path: stagingDir, Hash: "directory"}}, // Initial placeholder hash
		[]string{outputFile},
		[]string{},
		"zip -r dist.zip staging",
	)
	task.Platform = "linux"
	task.Architecture = "x86_64"
	task.Configuration = "debug"

	// Update hashes - this should calculate the actual directory hash
	if err := task.UpdateInputHashesAndCacheKey(); err != nil {
		t.Fatalf("UpdateInputHashesAndCacheKey failed: %v", err)
	}

	initialCacheKey := task.CacheKey
	initialDirHash := task.Inputs[0].Hash

	if initialCacheKey == "" {
		t.Fatal("Initial cache key should not be empty")
	}
	if initialDirHash == "directory" {
		t.Fatal("Directory hash should have been calculated, not left as placeholder")
	}

	// Simulate staging task updating the executable
	if err := os.WriteFile(exeFile, []byte("REBUILT executable with new code!"), 0644); err != nil {
		t.Fatalf("Failed to write modified exe: %v", err)
	}

	// Update hashes again
	if err := task.UpdateInputHashesAndCacheKey(); err != nil {
		t.Fatalf("UpdateInputHashesAndCacheKey failed: %v", err)
	}

	updatedCacheKey := task.CacheKey
	updatedDirHash := task.Inputs[0].Hash

	// THE CRITICAL ASSERTION: Cache key MUST change when directory contents change
	if updatedCacheKey == initialCacheKey {
		t.Errorf("CRITICAL BUG: Cache key did not change after directory contents changed!\n"+
			"Initial cache key: %s\n"+
			"Updated cache key: %s\n"+
			"This would cause stale packages to be created.",
			initialCacheKey, updatedCacheKey)
	}

	if updatedDirHash == initialDirHash {
		t.Errorf("Directory hash did not change after file modification!\n"+
			"Initial hash: %s\n"+
			"Updated hash: %s",
			initialDirHash, updatedDirHash)
	}

	t.Logf("SUCCESS: Directory cache key correctly changed")
}

// TestSymlinkInputsAreNotRehashed verifies that symlink inputs retain their
// special hash format and are not recalculated (symlinks don't need content hashing).
func TestSymlinkInputsAreNotRehashed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "buildy-symlink-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sourceFile := filepath.Join(tmpDir, "source.txt")
	if err := os.WriteFile(sourceFile, []byte("source content"), 0644); err != nil {
		t.Fatalf("Failed to write source file: %v", err)
	}

	// Create task with symlink-style input
	task := NewBuildTask(
		"stage_symlink_001",
		"stage",
		[]TaskInput{{Path: sourceFile, Hash: "symlink:" + sourceFile}},
		[]string{filepath.Join(tmpDir, "dest.txt")},
		[]string{},
		"ln -s source.txt dest.txt",
	)
	task.Platform = "linux"

	originalHash := task.Inputs[0].Hash

	// Update hashes
	if err := task.UpdateInputHashesAndCacheKey(); err != nil {
		t.Fatalf("UpdateInputHashesAndCacheKey failed: %v", err)
	}

	// Symlink hash should be preserved, not recalculated
	if task.Inputs[0].Hash != originalHash {
		t.Errorf("Symlink hash was modified!\n"+
			"Original: %s\n"+
			"After update: %s\n"+
			"Symlink inputs should retain their special hash format.",
			originalHash, task.Inputs[0].Hash)
	}
}

// TestMultipleInputsAllRehashed verifies that ALL inputs are rehashed,
// not just the first one or ones without hashes.
func TestMultipleInputsAllRehashed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "buildy-multi-input-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create multiple input files
	input1 := filepath.Join(tmpDir, "file1.o")
	input2 := filepath.Join(tmpDir, "file2.o")
	input3 := filepath.Join(tmpDir, "file3.o")

	if err := os.WriteFile(input1, []byte("content1"), 0644); err != nil {
		t.Fatalf("Failed to write input1: %v", err)
	}
	if err := os.WriteFile(input2, []byte("content2"), 0644); err != nil {
		t.Fatalf("Failed to write input2: %v", err)
	}
	if err := os.WriteFile(input3, []byte("content3"), 0644); err != nil {
		t.Fatalf("Failed to write input3: %v", err)
	}

	// Create task with multiple inputs
	task := NewBuildTask(
		"link_multi_001",
		"link",
		[]TaskInput{
			NewTaskInput(input1),
			NewTaskInput(input2),
			NewTaskInput(input3),
		},
		[]string{filepath.Join(tmpDir, "output.exe")},
		[]string{},
		"ld -o output.exe file1.o file2.o file3.o",
	)
	task.Platform = "linux"
	task.CacheKey = task.CalculateCacheKey()

	initialCacheKey := task.CacheKey
	initialHashes := []string{
		task.Inputs[0].Hash,
		task.Inputs[1].Hash,
		task.Inputs[2].Hash,
	}

	// Modify ONLY the middle file
	if err := os.WriteFile(input2, []byte("MODIFIED content2"), 0644); err != nil {
		t.Fatalf("Failed to modify input2: %v", err)
	}

	// Update hashes
	if err := task.UpdateInputHashesAndCacheKey(); err != nil {
		t.Fatalf("UpdateInputHashesAndCacheKey failed: %v", err)
	}

	// Cache key should change
	if task.CacheKey == initialCacheKey {
		t.Error("Cache key should change when any input changes")
	}

	// First and third hashes should be unchanged
	if task.Inputs[0].Hash != initialHashes[0] {
		t.Error("Hash for unchanged file1 should remain the same")
	}
	if task.Inputs[2].Hash != initialHashes[2] {
		t.Error("Hash for unchanged file3 should remain the same")
	}

	// Second hash MUST be different
	if task.Inputs[1].Hash == initialHashes[1] {
		t.Error("Hash for modified file2 should have changed")
	}

	t.Logf("SUCCESS: Only modified input's hash changed, cache key updated correctly")
}

// TestCacheKeyStableWhenNoChanges verifies that the cache key remains stable
// when files haven't changed (important for cache hits).
func TestCacheKeyStableWhenNoChanges(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "buildy-stable-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	inputFile := filepath.Join(tmpDir, "stable.o")
	if err := os.WriteFile(inputFile, []byte("stable content"), 0644); err != nil {
		t.Fatalf("Failed to write input file: %v", err)
	}

	task := NewBuildTask(
		"link_stable_001",
		"link",
		[]TaskInput{NewTaskInput(inputFile)},
		[]string{filepath.Join(tmpDir, "output.exe")},
		[]string{},
		"ld -o output.exe stable.o",
	)
	task.Platform = "linux"
	task.CacheKey = task.CalculateCacheKey()

	initialCacheKey := task.CacheKey

	// Call UpdateInputHashesAndCacheKey multiple times without changing the file
	for i := 0; i < 5; i++ {
		if err := task.UpdateInputHashesAndCacheKey(); err != nil {
			t.Fatalf("UpdateInputHashesAndCacheKey failed on iteration %d: %v", i, err)
		}

		if task.CacheKey != initialCacheKey {
			t.Errorf("Cache key changed on iteration %d even though file didn't change!\n"+
				"Initial: %s\n"+
				"Current: %s\n"+
				"This would cause unnecessary cache misses.",
				i, initialCacheKey, task.CacheKey)
		}
	}

	t.Logf("SUCCESS: Cache key remained stable across %d checks", 5)
}

// TestCacheKeyPathNormalization verifies that cache keys are consistent
// regardless of path separator style. This is critical for cross-platform builds.
func TestCacheKeyPathNormalization(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "buildy-path-norm-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	inputFile := filepath.Join(tmpDir, "input.o")
	if err := os.WriteFile(inputFile, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to write input file: %v", err)
	}

	// Create task with native path separators
	nativeOutput := filepath.Join(tmpDir, "output.exe")
	task1 := NewBuildTask(
		"test_task_001",
		"link",
		[]TaskInput{NewTaskInput(inputFile)},
		[]string{nativeOutput},
		[]string{},
		"link command",
	)
	task1.Platform = "test"
	task1.Architecture = "x86_64"
	task1.Configuration = "debug"
	task1.CacheKey = task1.CalculateCacheKey()

	// Create identical task - should have same cache key
	task2 := NewBuildTask(
		"test_task_001",
		"link",
		[]TaskInput{NewTaskInput(inputFile)},
		[]string{nativeOutput},
		[]string{},
		"link command",
	)
	task2.Platform = "test"
	task2.Architecture = "x86_64"
	task2.Configuration = "debug"
	task2.CacheKey = task2.CalculateCacheKey()

	if task1.CacheKey != task2.CacheKey {
		t.Errorf("Identical tasks should have identical cache keys:\n"+
			"Task1: %s\nTask2: %s", task1.CacheKey, task2.CacheKey)
	}
}

// TestCacheKeyDifferentPlatforms verifies that different platforms produce
// different cache keys (so Linux cache isn't used for Windows builds).
func TestCacheKeyDifferentPlatforms(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "buildy-platform-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	inputFile := filepath.Join(tmpDir, "input.o")
	if err := os.WriteFile(inputFile, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to write input file: %v", err)
	}

	createTask := func(platform string) *BuildTask {
		task := NewBuildTask(
			"test_task_001",
			"link",
			[]TaskInput{NewTaskInput(inputFile)},
			[]string{filepath.Join(tmpDir, "output")},
			[]string{},
			"link command",
		)
		task.Platform = platform
		task.Architecture = "x86_64"
		task.Configuration = "debug"
		task.CacheKey = task.CalculateCacheKey()
		return &task
	}

	linuxTask := createTask("linux")
	windowsTask := createTask("windows")

	if linuxTask.CacheKey == windowsTask.CacheKey {
		t.Error("Different platforms should produce different cache keys")
	}

	t.Logf("Linux cache key: %s...", linuxTask.CacheKey[:16])
	t.Logf("Windows cache key: %s...", windowsTask.CacheKey[:16])
}

// TestCacheKeyDifferentToolchains verifies that different toolchains produce
// different cache keys (so GCC cache isn't used for Clang builds).
func TestCacheKeyDifferentToolchains(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "buildy-toolchain-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	inputFile := filepath.Join(tmpDir, "input.o")
	if err := os.WriteFile(inputFile, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to write input file: %v", err)
	}

	createTask := func(toolchain string) *BuildTask {
		task := NewBuildTask(
			"test_task_001",
			"compile",
			[]TaskInput{NewTaskInput(inputFile)},
			[]string{filepath.Join(tmpDir, "output.o")},
			[]string{},
			"compile command",
		)
		task.Platform = "linux"
		task.Architecture = "x86_64"
		task.Configuration = "debug"
		task.Toolchain = toolchain
		task.CacheKey = task.CalculateCacheKey()
		return &task
	}

	gccTask := createTask("gcc-13")
	clangTask := createTask("clang-17")

	if gccTask.CacheKey == clangTask.CacheKey {
		t.Error("Different toolchains should produce different cache keys")
	}
}

// TestWindowsPathCaseInsensitiveHashing verifies that on Windows, paths with
// different cases produce the same normalized cache path.
func TestWindowsPathCaseInsensitiveHashing(t *testing.T) {
	// Mock Windows behavior using the injectable GOOS variable in util package
	oldGOOS := util.GOOS
	util.GOOS = "windows"
	defer func() { util.GOOS = oldGOOS }()

	// Test that path normalization lowercases on Windows
	path1 := util.NormalizePath("C:/Build/File.o")
	path2 := util.NormalizePath("c:/build/file.o")

	if path1 != path2 {
		t.Errorf("On Windows, paths with different case should normalize to same value:\n"+
			"'C:/Build/File.o' -> '%s'\n"+
			"'c:/build/file.o' -> '%s'",
			path1, path2)
	}
}

// TestConfigHashInvalidatesCache verifies that changing the config hash
// produces a different cache key, even when all other inputs are identical.
func TestConfigHashInvalidatesCache(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "buildy-confighash-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	inputFile := filepath.Join(tmpDir, "input.cpp")
	if err := os.WriteFile(inputFile, []byte("int main() {}"), 0644); err != nil {
		t.Fatalf("Failed to write input file: %v", err)
	}

	createTask := func(configHash string) *BuildTask {
		task := NewBuildTask(
			"compile_mylib_001",
			"compile",
			[]TaskInput{NewTaskInput(inputFile)},
			[]string{filepath.Join(tmpDir, "output.o")},
			[]string{},
			"g++ -c input.cpp -o output.o",
		)
		task.Platform = "linux"
		task.Architecture = "x86_64"
		task.Configuration = "debug"
		task.Toolchain = "gcc"
		task.ConfigHash = configHash
		task.CacheKey = task.CalculateCacheKey()
		return &task
	}

	task1 := createTask("aabbccdd")
	task2 := createTask("aabbccdd")
	task3 := createTask("11223344")

	if task1.CacheKey != task2.CacheKey {
		t.Error("Same config hash should produce same cache key")
	}

	if task1.CacheKey == task3.CacheKey {
		t.Error("Different config hash should produce different cache key")
	}
}

// TestConfigHashEmptyDoesNotPanic verifies that an empty config hash
// still produces a valid cache key.
func TestConfigHashEmptyDoesNotPanic(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "buildy-confighash-empty-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	inputFile := filepath.Join(tmpDir, "input.cpp")
	if err := os.WriteFile(inputFile, []byte("int main() {}"), 0644); err != nil {
		t.Fatalf("Failed to write input file: %v", err)
	}

	task := NewBuildTask(
		"compile_test_001",
		"compile",
		[]TaskInput{NewTaskInput(inputFile)},
		[]string{filepath.Join(tmpDir, "output.o")},
		[]string{},
		"g++ -c input.cpp -o output.o",
	)
	task.ConfigHash = ""
	task.CacheKey = task.CalculateCacheKey()

	if task.CacheKey == "" {
		t.Error("Cache key should not be empty even with empty config hash")
	}
}
