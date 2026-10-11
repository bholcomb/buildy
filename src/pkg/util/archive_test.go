package util

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stagingTree makes a staging directory with a file, an executable in a subdirectory, and (where
// symlinks work) a symlink to a file outside it.
func stagingTree(t *testing.T) (dir string, hasLink bool) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "stage dir")
	write := func(path, content string, mode os.FileMode) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "readme.txt"), "hello\n", 0o644)
	write(filepath.Join(dir, "bin", "app"), "#!/bin/sh\n", 0o755)
	outside := filepath.Join(root, "lib.so")
	write(outside, "library", 0o644)
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(dir, "bin", "lib.so")); err != nil {
			t.Fatal(err)
		}
		hasLink = true
	}
	return dir, hasLink
}

type entry struct {
	name, content, link string
	mode                os.FileMode
}

func readTar(t *testing.T, path string, gzipped bool) []entry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r io.Reader = bytes.NewReader(data)
	if gzipped {
		gz, err := gzip.NewReader(r)
		if err != nil {
			t.Fatal(err)
		}
		r = gz
	}
	tr := tar.NewReader(r)
	var entries []entry
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(tr)
		if !h.ModTime.Equal(archiveTime) {
			t.Errorf("%s has mtime %v, want %v", h.Name, h.ModTime, archiveTime)
		}
		entries = append(entries, entry{h.Name, string(content), h.Linkname, os.FileMode(h.Mode).Perm()})
	}
}

func readZip(t *testing.T, path string) []entry {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var entries []entry
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(rc)
		rc.Close()
		e := entry{name: f.Name, content: string(content), mode: f.Mode().Perm()}
		if f.Mode()&os.ModeSymlink != 0 {
			e.link, e.content = e.content, ""
		}
		entries = append(entries, e)
	}
	return entries
}

func names(entries []entry) string {
	var n []string
	for _, e := range entries {
		n = append(n, e.name)
	}
	return strings.Join(n, " ")
}

// Install archives are written by buildy itself: sorted entries without "./", the same bytes for
// the same tree, executables stay executable, and follow_symlinks decides how a link is stored.
func TestWriteArchive(t *testing.T) {
	dir, hasLink := stagingTree(t)
	out := t.TempDir()
	for _, format := range ArchiveFormats {
		for _, follow := range []bool{true, false} {
			if !follow && !hasLink {
				continue
			}
			path := filepath.Join(out, "pkg."+format)
			if err := WriteArchive(dir, path, format, follow); err != nil {
				t.Fatalf("%s: %v", format, err)
			}
			first, _ := os.ReadFile(path)
			if err := WriteArchive(dir, path, format, follow); err != nil {
				t.Fatal(err)
			}
			if second, _ := os.ReadFile(path); !bytes.Equal(first, second) {
				t.Errorf("%s: two archives of one tree differ", format)
			}

			var entries []entry
			if format == "zip" {
				entries = readZip(t, path)
			} else {
				entries = readTar(t, path, format == "tar.gz")
			}
			want := "bin/ bin/app readme.txt"
			if hasLink {
				want = "bin/ bin/app bin/lib.so readme.txt"
			}
			if got := names(entries); got != want {
				t.Errorf("%s: entries %q, want %q", format, got, want)
			}
			for _, e := range entries {
				switch e.name {
				case "bin/app":
					if runtime.GOOS != "windows" && e.mode != 0o755 {
						t.Errorf("%s: bin/app has mode %v, want 0755", format, e.mode)
					}
				case "readme.txt":
					if e.content != "hello\n" {
						t.Errorf("%s: readme.txt = %q", format, e.content)
					}
				case "bin/lib.so":
					if follow && (e.content != "library" || e.link != "") {
						t.Errorf("%s following symlinks: lib.so = %+v, want the file", format, e)
					}
					if !follow && (e.link == "" || e.content != "") {
						t.Errorf("%s keeping symlinks: lib.so = %+v, want a link", format, e)
					}
				}
			}
		}
	}
}

func TestWriteArchiveRejects(t *testing.T) {
	dir, _ := stagingTree(t)
	if err := WriteArchive(dir, filepath.Join(dir, "pkg.tar"), "tar", true); err == nil {
		t.Error("an archive inside the directory it packs was accepted")
	}
	if err := WriteArchive(dir, filepath.Join(t.TempDir(), "pkg.rar"), "rar", true); err == nil {
		t.Error("an unknown format was accepted")
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(dir, filepath.Join(dir, "bin", "loop")); err != nil {
			t.Fatal(err)
		}
		err := WriteArchive(dir, filepath.Join(t.TempDir(), "pkg.tar"), "tar", true)
		if err == nil || !strings.Contains(err.Error(), "loop") {
			t.Errorf("a symlink loop gave %v", err)
		}
	}
}

// Template generation substitutes @KEY@ and ${KEY} in order, whatever the values hold: slashes,
// quotes, ampersands and spaces broke the sed and PowerShell commands it replaces.
func TestFillTemplate(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "version.h.in")
	dst := filepath.Join(dir, "out dir", "version.h")
	if err := os.WriteFile(src, []byte("#define V \"@VERSION@\"\n#define P \"${PATH_}\"\n#define N @NESTED@\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	subs := [][2]string{
		{"VERSION", `1.0 "beta" & co`},
		{"PATH_", `C:\it's/a path`},
		{"NESTED", "@VERSION@"}, // substituted after VERSION: stays as written
	}
	if err := FillTemplate(src, dst, subs); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	want := "#define V \"1.0 \"beta\" & co\"\n#define P \"C:\\it's/a path\"\n#define N @VERSION@\n"
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
