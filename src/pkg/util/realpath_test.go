package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrimExtendedPrefix(t *testing.T) {
	for in, want := range map[string]string{
		`\\?\C:\Users\runneradmin\go.exe`: `C:\Users\runneradmin\go.exe`,
		`\\?\UNC\server\share\dir`:        `\\server\share\dir`,
		`C:\plain`:                        `C:\plain`,
		`/usr/bin/cc`:                     `/usr/bin/cc`,
	} {
		if got := trimExtendedPrefix(in); got != want {
			t.Errorf("trimExtendedPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

// RealPath resolves a symlink to what it points to, and gives the path the file system knows
// the file by: the same for every spelling of it.
func TestRealPathResolvesASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		if GOOS == "windows" {
			t.Skipf("skipping: this Windows account can't make symlinks (it needs developer mode or administrator rights): %v", err)
		}
		t.Fatal(err)
	}
	want, err := RealPath(filepath.Join(target, "file"))
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(want) || filepath.Base(want) != "file" || filepath.Base(filepath.Dir(want)) != "real" {
		t.Errorf("RealPath of real/file is %s", want)
	}
	if got, err := RealPath(filepath.Join(link, "file")); err != nil || got != want {
		t.Errorf("RealPath of link/file = %s (%v), want %s", got, err, want)
	}
	if _, err := RealPath(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Errorf("RealPath of a missing file: %v, want a not-exist error", err)
	}
}
