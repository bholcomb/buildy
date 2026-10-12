package util

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A junction is resolved too: the tool cache of a GitHub Actions Windows runner is one, through
// which filepath.EvalSymlinks fails (Go 1.23 and later), and every task's program was unusable.
func TestRealPathResolvesAJunction(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "go.exe"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "junction")
	if out, err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v\n%s", err, out)
	}
	want, err := RealPath(filepath.Join(target, "go.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := RealPath(filepath.Join(link, "go.exe")); err != nil || got != want {
		t.Errorf("RealPath through the junction = %s (%v), want %s", got, err, want)
	}
}
