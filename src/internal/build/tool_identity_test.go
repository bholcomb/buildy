package build

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"buildy/pkg/util"
)

// writeProgram writes an executable file.
func writeProgram(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Finding 1.3: another program of the same name earlier on the PATH, or a changed one, is
// another tool.
func TestToolIdentityFollowsThePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping: the programs are shell scripts")
	}
	dir := t.TempDir()
	writeProgram(t, filepath.Join(dir, "a", "cc"), "#!/bin/sh\necho a\n")
	writeProgram(t, filepath.Join(dir, "b", "cc"), "#!/bin/sh\necho b\n")
	command := util.Command{Args: []string{"cc", "-c", "x.c"}}
	identify := func() string {
		t.Helper()
		id, err := newToolIdentities(util.HashFile).of(context.Background(), util.ExecSpec{}, command, dir)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	t.Setenv("PATH", filepath.Join(dir, "a")+string(os.PathListSeparator)+filepath.Join(dir, "b"))
	a := identify()
	t.Setenv("PATH", filepath.Join(dir, "b")+string(os.PathListSeparator)+filepath.Join(dir, "a"))
	b := identify()
	if a == b {
		t.Errorf("cc in a and cc in b have one identity: %s", a)
	}
	// Same path, new content (an upgrade)
	writeProgram(t, filepath.Join(dir, "b", "cc"), "#!/bin/sh\necho b2\n")
	if upgraded := identify(); upgraded == b {
		t.Error("an upgraded program kept its identity")
	}
	// A symlink to a program is that program
	os.Symlink(filepath.Join(dir, "a", "cc"), filepath.Join(dir, "c-cc"))
	command = util.Command{Args: []string{"./c-cc"}}
	if linked := identify(); linked != a {
		t.Errorf("a link to a's cc is %s, want %s", linked, a)
	}
}

// A program that isn't there fails the identity, naming it.
func TestToolIdentityOfAMissingProgram(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := newToolIdentities(util.HashFile).of(context.Background(), util.ExecSpec{}, util.Command{Args: []string{"no-such-cc"}}, t.TempDir())
	if err == nil {
		t.Fatal("expected an error")
	}
}

// Each program is resolved once per run.
func TestToolIdentityIsResolvedOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping: the programs are shell scripts")
	}
	dir := t.TempDir()
	writeProgram(t, filepath.Join(dir, "cc"), "#!/bin/sh\n")
	t.Setenv("PATH", dir)
	hashes := 0
	ti := newToolIdentities(func(path string) (string, error) { hashes++; return util.HashFile(path) })
	for range 3 {
		if _, err := ti.of(context.Background(), util.ExecSpec{}, util.Command{Args: []string{"cc"}}, dir); err != nil {
			t.Fatal(err)
		}
	}
	if hashes != 1 {
		t.Errorf("hashed %d times, want 1", hashes)
	}
}
