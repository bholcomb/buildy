package resource

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tarEntry describes one entry of a test tar archive.
type tarEntry struct {
	name     string
	typ      byte
	body     string
	linkname string
	mode     int64
}

func writeTarGz(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Linkname: e.linkname, Mode: mode, Size: int64(len(e.body))}
		if e.typ == tar.TypeXGlobalHeader {
			h = &tar.Header{Typeflag: e.typ, PAXRecords: map[string]string{"comment": "abc123"}}
		}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, path string, files map[string]string, order []string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range order {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s exists; the archive wrote outside the extraction directory", path)
	}
}

// assertNoExtraction checks a failed extraction left neither dest nor a temporary directory.
func assertNoExtraction(t *testing.T, dest string) {
	t.Helper()
	assertAbsent(t, dest)
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), extractTempPrefix(dest)+"*"))
	if len(leftovers) != 0 {
		t.Errorf("temporary extraction directories left behind: %v", leftovers)
	}
}

// Finding 1: a ZIP entry with ".." components escaped the extraction directory.
func TestExtractZipRejectsDotDot(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "a", "b", "dest")
	archive := filepath.Join(root, "evil.zip")
	writeZip(t, archive, map[string]string{"pkg/README": "hi", "pkg/../../zip_escape.txt": "x"},
		[]string{"pkg/README", "pkg/../../zip_escape.txt"})

	err := extractArchive(archive, "evil.zip", dest)
	if err == nil || !strings.Contains(err.Error(), "'..'") {
		t.Fatalf("expected a '..' error, got %v", err)
	}
	assertAbsent(t, filepath.Join(root, "a", "zip_escape.txt"))
	assertNoExtraction(t, dest)
}

// Finding 1: tar "pkg/../x" escaped too.
func TestExtractTarRejectsDotDot(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	archive := filepath.Join(root, "evil.tar.gz")
	writeTarGz(t, archive, []tarEntry{
		{name: "pkg/", typ: tar.TypeDir, mode: 0o755},
		{name: "pkg/../tar_dotdot.txt", typ: tar.TypeReg, body: "x"},
	})

	if err := extractArchive(archive, "evil.tar.gz", dest); err == nil {
		t.Fatal("expected an error for a '..' entry")
	}
	assertAbsent(t, filepath.Join(root, "tar_dotdot.txt"))
	assertNoExtraction(t, dest)
}

// Finding 1: a symlink to a directory outside, then a file written through it.
func TestExtractTarRejectsSymlinkWriteThrough(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "dest")
	archive := filepath.Join(root, "evil.tar.gz")
	writeTarGz(t, archive, []tarEntry{
		{name: "pkg/", typ: tar.TypeDir, mode: 0o755},
		{name: "pkg/link", typ: tar.TypeSymlink, linkname: outside},
		{name: "pkg/link/tar_escape.txt", typ: tar.TypeReg, body: "x"},
	})

	if err := extractArchive(archive, "evil.tar.gz", dest); err == nil {
		t.Fatal("expected an error for a symlink leaving the extraction directory")
	}
	assertAbsent(t, filepath.Join(outside, "tar_escape.txt"))
	assertNoExtraction(t, dest)
}

// A relative symlink that climbs out is rejected as well, even before anything uses it.
func TestExtractTarRejectsEscapingRelativeSymlink(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	archive := filepath.Join(root, "evil.tar.gz")
	writeTarGz(t, archive, []tarEntry{
		{name: "pkg/sub/", typ: tar.TypeDir, mode: 0o755},
		{name: "pkg/sub/up", typ: tar.TypeSymlink, linkname: "../../.."},
	})
	if err := extractArchive(archive, "evil.tar.gz", dest); err == nil {
		t.Fatal("expected an error for a symlink climbing out")
	}
	assertNoExtraction(t, dest)
}

// Hard links must stay inside, and are created (they used to be dropped).
func TestExtractTarHardLinks(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	archive := filepath.Join(root, "ok.tar.gz")
	writeTarGz(t, archive, []tarEntry{
		{name: "pkg/a.txt", typ: tar.TypeReg, body: "A"},
		{name: "pkg/b.txt", typ: tar.TypeLink, linkname: "pkg/a.txt"},
		{name: "pkg/inner", typ: tar.TypeSymlink, linkname: "a.txt"},
	})
	if err := extractArchive(archive, "ok.tar.gz", dest); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "b.txt")); err != nil || string(data) != "A" {
		t.Errorf("hard link b.txt: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "inner")); err != nil || string(data) != "A" {
		t.Errorf("symlink inner: %q, %v", data, err)
	}

	evil := filepath.Join(root, "evil.tar.gz")
	writeTarGz(t, evil, []tarEntry{
		{name: "pkg/a.txt", typ: tar.TypeReg, body: "A"},
		{name: "pkg/b.txt", typ: tar.TypeLink, linkname: "../../etc/passwd"},
	})
	dest2 := filepath.Join(root, "dest2")
	if err := extractArchive(evil, "evil.tar.gz", dest2); err == nil {
		t.Fatal("expected an error for a hard link leaving the extraction directory")
	}
	assertNoExtraction(t, dest2)
}

// A duplicate entry replaces the first; a symlink planted by the first is not written through.
func TestExtractDuplicateEntries(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	archive := filepath.Join(root, "dup.tar.gz")
	writeTarGz(t, archive, []tarEntry{
		{name: "pkg/target.txt", typ: tar.TypeReg, body: "original"},
		{name: "pkg/f", typ: tar.TypeSymlink, linkname: "target.txt"},
		{name: "pkg/f", typ: tar.TypeReg, body: "second"},
		{name: "pkg/g", typ: tar.TypeReg, body: "one"},
		{name: "pkg/g", typ: tar.TypeReg, body: "two", mode: 0o4755},
	})
	if err := extractArchive(archive, "dup.tar.gz", dest); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dest, "target.txt")); string(data) != "original" {
		t.Errorf("target.txt was written through the duplicate's symlink: %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(dest, "f")); string(data) != "second" {
		t.Errorf("f = %q, want the second entry", data)
	}
	info, err := os.Stat(filepath.Join(dest, "g"))
	if err != nil {
		t.Fatal(err)
	}
	// Windows keeps no Unix permission bits
	if runtime.GOOS != "windows" && (info.Mode()&os.ModeSetuid != 0 || info.Mode().Perm() != 0o755) {
		t.Errorf("g mode = %v, want 0755 without setuid", info.Mode())
	}
}

// The common top-level directory is stripped; a git archive's pax header doesn't prevent it.
func TestExtractStripsCommonPrefix(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	archive := filepath.Join(root, "p.tar.gz")
	writeTarGz(t, archive, []tarEntry{
		{name: "pax_global_header", typ: tar.TypeXGlobalHeader},
		{name: "project-1.0/", typ: tar.TypeDir, mode: 0o755},
		{name: "project-1.0/include/x.h", typ: tar.TypeReg, body: "h"},
		{name: "project-1.0/README", typ: tar.TypeReg, body: "r"},
	})
	if err := extractArchive(archive, "p.tar.gz", dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "include", "x.h")); err != nil {
		t.Errorf("prefix not stripped: %v", err)
	}

	// Two top-level entries: nothing is stripped.
	mixed := filepath.Join(root, "m.zip")
	writeZip(t, mixed, map[string]string{"a/x": "1", "b/y": "2"}, []string{"a/x", "b/y"})
	dest2 := filepath.Join(root, "dest2")
	if err := extractArchive(mixed, "m.zip", dest2); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a/x", "b/y"} {
		if _, err := os.Stat(filepath.Join(dest2, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

// S2: dest is only ever a complete extraction. A failure leaves the previous extraction alone
// and no partial tree; a leftover from an interrupted run is not accepted.
func TestFetchURLNeverAcceptsPartialExtraction(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "good.tar.gz")
	writeTarGz(t, good, []tarEntry{
		{name: "pkg/a.txt", typ: tar.TypeReg, body: "A"},
		{name: "pkg/b.txt", typ: tar.TypeReg, body: "B"},
	})
	data, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer server.Close()

	cache := filepath.Join(root, "cache")
	fm := NewFetchManager(cache)
	dest := filepath.Join(cache, "deps", "pkg")

	// An interrupted run left a partial tree and a temporary directory, and no marker.
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "a.txt"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(filepath.Dir(dest), extractTempPrefix(dest)+"123")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := fm.FetchURL(server.URL+"/v1/pkg.tar.gz", "", dest); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "a.txt")); string(got) != "A" {
		t.Errorf("a.txt = %q: the partial extraction was accepted", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "b.txt")); err != nil {
		t.Errorf("b.txt missing: %v", err)
	}
	assertAbsent(t, stale)

	// A second fetch reuses the recorded extraction.
	if err := os.WriteFile(filepath.Join(dest, "local.txt"), []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fm.FetchURL(server.URL+"/v1/pkg.tar.gz", "", dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "local.txt")); err != nil {
		t.Errorf("a recorded extraction was redone: %v", err)
	}
}

// S1: two URLs with the same basename are separate downloads.
func TestFetchURLKeysDownloadsByWholeURL(t *testing.T) {
	root := t.TempDir()
	archives := map[string][]byte{}
	for _, who := range []string{"foo", "bar"} {
		p := filepath.Join(root, who+".tar.gz")
		writeTarGz(t, p, []tarEntry{{name: who + "-1.0/WHO", typ: tar.TypeReg, body: strings.ToUpper(who)}})
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		archives["/"+who+"/v1.0.tar.gz"] = data
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := archives[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	defer server.Close()

	cache := filepath.Join(root, "cache")
	fm := NewFetchManager(cache)
	for _, who := range []string{"foo", "bar"} {
		dest := filepath.Join(cache, "deps", who)
		if err := fm.FetchURL(server.URL+"/"+who+"/v1.0.tar.gz", "", dest); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dest, "WHO"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != strings.ToUpper(who) {
			t.Errorf("deps/%s/WHO = %q, want %q", who, got, strings.ToUpper(who))
		}
	}
}
