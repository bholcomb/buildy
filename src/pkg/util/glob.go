package util

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Glob is a file pattern, split into the directory it searches (Base, taken literally) and the
// pattern below it (Pattern, with forward slashes; "" when the whole pattern is a literal path).
//
// The syntax is doublestar's: '*' matches within a path component, '**' as a component matches
// any number of directories (none included: src/**/*.c matches src/top.c and src/a/b/deep.c), '?'
// one character, '[abc]' a class and '{a,b}' alternatives. Patterns match files, never
// directories.
type Glob struct {
	Base    string
	Pattern string
}

// isMeta reports whether a path component holds glob syntax. On Windows a backslash is a path
// separator; elsewhere it escapes the next character, which doublestar handles.
func isMeta(component string) bool {
	meta := "*?[{"
	if filepath.Separator != '\\' {
		meta += `\`
	}
	return strings.ContainsAny(component, meta)
}

// ParseGlob splits pattern, relative to dir (an absolute directory, never read as a pattern
// itself) unless it is absolute, into the literal directory it starts from and the rest.
func ParseGlob(dir, pattern string) (Glob, error) {
	if pattern == "" {
		return Glob{}, fmt.Errorf("empty file pattern")
	}
	base := dir
	rest := filepath.Clean(pattern)
	if filepath.IsAbs(rest) {
		volume := filepath.VolumeName(rest)
		base = volume + string(filepath.Separator)
		rest = strings.TrimLeft(rest[len(volume):], `/\`)
	} else if !filepath.IsAbs(dir) {
		return Glob{}, fmt.Errorf("internal error: pattern %q is relative to %q, which is not absolute", pattern, dir)
	}
	components := strings.Split(filepath.ToSlash(rest), "/")
	i := 0
	for ; i < len(components) && !isMeta(components[i]); i++ {
		base = filepath.Join(base, components[i])
	}
	g := Glob{Base: filepath.Clean(base), Pattern: strings.Join(components[i:], "/")}
	if g.Pattern != "" && !doublestar.ValidatePattern(g.Pattern) {
		return Glob{}, fmt.Errorf("invalid file pattern '%s'", pattern)
	}
	return g, nil
}

// Literal reports whether the pattern is a plain path: it names one file, Base.
func (g Glob) Literal() bool {
	return g.Pattern == ""
}

// Match reports whether path (absolute) is a file the pattern names.
func (g Glob) Match(p string) bool {
	p = filepath.Clean(p)
	if g.Literal() {
		return p == g.Base
	}
	rel, err := filepath.Rel(g.Base, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == "." {
		return false
	}
	return doublestar.MatchUnvalidated(g.Pattern, filepath.ToSlash(rel))
}

// Files returns the files on disk the pattern names: absolute, clean and sorted. A literal path
// names itself if it is a file.
func (g Glob) Files() ([]string, error) {
	if g.Literal() {
		if info, err := os.Stat(g.Base); err == nil && !info.IsDir() {
			return []string{g.Base}, nil
		}
		return nil, nil
	}
	if info, err := os.Stat(g.Base); err != nil || !info.IsDir() {
		return nil, nil
	}
	matches, err := doublestar.Glob(os.DirFS(g.Base), g.Pattern, doublestar.WithFilesOnly())
	if err != nil {
		return nil, fmt.Errorf("file pattern %s: %w", g, err)
	}
	files := make([]string, len(matches))
	for i, m := range matches {
		files[i] = filepath.Join(g.Base, filepath.FromSlash(m))
	}
	sort.Strings(files)
	return files, nil
}

// String is the pattern as one path.
func (g Glob) String() string {
	if g.Literal() {
		return g.Base
	}
	return filepath.Join(g.Base, filepath.FromSlash(g.Pattern))
}

// GlobFiles returns the files on disk that pattern, relative to dir unless absolute, names:
// absolute, clean and sorted.
func GlobFiles(dir, pattern string) ([]string, error) {
	g, err := ParseGlob(dir, pattern)
	if err != nil {
		return nil, err
	}
	return g.Files()
}
