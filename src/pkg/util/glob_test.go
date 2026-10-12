package util

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func touch(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func abs(root string, names ...string) []string {
	var result []string
	for _, n := range names {
		result = append(result, filepath.Join(root, filepath.FromSlash(n)))
	}
	return result
}

func TestGlobFiles(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "src/top.c", "src/a/mid.c", "src/a/b/deep.c", "src/a/b/deep.h", "src/z.c", "other/x.c")
	if err := os.MkdirAll(filepath.Join(root, "src", "dir.c"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		// ** matches no directory, one, or several
		{"src/**/*.c", abs(root, "src/a/b/deep.c", "src/a/mid.c", "src/top.c", "src/z.c")},
		{"src/*.c", abs(root, "src/top.c", "src/z.c")},
		{"src/**", abs(root, "src/a/b/deep.c", "src/a/b/deep.h", "src/a/mid.c", "src/top.c", "src/z.c")},
		{"src/a/b/*.{c,h}", abs(root, "src/a/b/deep.c", "src/a/b/deep.h")},
		{"src/../other/*.c", abs(root, "other/x.c")},
		{"src/top.c", abs(root, "src/top.c")},
		// A literal path that is missing or a directory names nothing
		{"src/missing.c", nil},
		{"src/dir.c", nil},
		{"nowhere/*.c", nil},
		{filepath.Join(root, "src", "*.c"), abs(root, "src/top.c", "src/z.c")},
	} {
		got, err := GlobFiles(root, tc.pattern)
		if err != nil {
			t.Fatalf("%s: %v", tc.pattern, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %v\nwant %v", tc.pattern, got, tc.want)
		}
	}

	if _, err := GlobFiles(root, "src/[a.c"); err == nil {
		t.Error("an invalid pattern is accepted")
	}
}

// The directory a pattern is relative to is a literal path, even when its name holds glob syntax.
func TestGlobBaseIsLiteral(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj [v1] {x}")
	touch(t, root, "src/a.c")
	got, err := GlobFiles(root, "src/*.c")
	if err != nil {
		t.Fatal(err)
	}
	if want := abs(root, "src/a.c"); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGlobMatch(t *testing.T) {
	root := t.TempDir()
	g, err := ParseGlob(root, "build/bin/**/*.so")
	if err != nil {
		t.Fatal(err)
	}
	if g.Base != filepath.Join(root, "build", "bin") || g.Pattern != "**/*.so" {
		t.Errorf("split into %q and %q", g.Base, g.Pattern)
	}
	for p, want := range map[string]bool{
		"build/bin/a.so":       true,
		"build/bin/x/y/b.so":   true,
		"build/bin/a.dll":      false,
		"build/lib/a.so":       false,
		"build/bin/../a.so":    false,
		"other/build/bin/a.so": false,
	} {
		if got := g.Match(filepath.Join(root, filepath.FromSlash(p))); got != want {
			t.Errorf("Match(%s) = %v, want %v", p, got, want)
		}
	}
	lit, err := ParseGlob(root, "build/bin/app")
	if err != nil {
		t.Fatal(err)
	}
	if !lit.Literal() || !lit.Match(filepath.Join(root, "build", "bin", "app")) || lit.Match(filepath.Join(root, "build", "bin", "app2")) {
		t.Errorf("literal pattern %+v", lit)
	}
}
