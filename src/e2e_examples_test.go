package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// example copies examples/<name> of the repository to a temporary directory.
func example(t *testing.T, name string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	copyTree(t, filepath.Join("..", "examples", name), dst)
	return dst
}

// The examples the readme lists build from a clean checkout. examples/math needs only a C++
// compiler, so it is built and run; the others fetch from the network (and need glslc, or Docker),
// so they are planned: --dry-run loads, generates and validates every task without fetching.
func TestE2EExamples(t *testing.T) {
	needCpp(t)
	t.Run("math", func(t *testing.T) {
		dir := example(t, "math")
		for _, config := range []string{"debug", "release"} {
			mustBuildy(t, dir, "-c", config)
			out := run(t, filepath.Join(outDir(dir, config), "bin", "calculator"))
			containsAll(t, "calculator ("+config+")", out, "5 + 3 = 8", "4 * 7 = 28")
		}
	})
	for _, name := range []string{"game_engine", "portable_app"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS != "linux" {
				t.Skip("skipping: the example's plan is checked on Linux")
			}
			dir := example(t, name)
			for _, config := range []string{"debug", "release"} {
				out := mustBuildy(t, dir, "--dry-run", "-c", config)
				if !strings.Contains(out, "Dry run: would fetch") {
					t.Errorf("the plan of %s (%s) fetches nothing:\n%s", name, config, out)
				}
			}
			if exists(filepath.Join(dir, ".buildy_cache", "deps")) {
				t.Errorf("planning %s fetched", name)
			}
		})
	}
}
