package cache

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"buildy/internal/resource"
)

// parseDepFile returns every path a dependency file lists as an input of its target. Nothing is
// filtered by extension: a header without one ("config") is as much an input as "config.h".
func parseDepFile(data []byte, format string) ([]string, error) {
	switch format {
	case resource.DepFormatMake:
		return parseMakeDepFile(string(data))
	case resource.DepFormatMSVCJSON:
		return parseMSVCDepFile(data)
	}
	return nil, fmt.Errorf("unknown dependency file format %q", format)
}

// parseMakeDepFile parses the Makefile rules gcc and clang write with -MD/-MMD/-MP:
//
//	obj/main.o: src/main.cpp include/config \
//	  inc\ dir/v.h
//	include/config:
//
// It returns the prerequisites of every rule, in order and without duplicates; -MP's empty
// phony rules add nothing. Continuation lines are joined; "\ " is a space and "\#" a '#' inside a
// path; "$$" is '$'. A rule's targets end at the first ':' followed by whitespace or the end of
// the line, so a drive letter ("C:/src/main.o") is not mistaken for the separator.
func parseMakeDepFile(content string) ([]string, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\\\n", " ")

	var prereqs []string
	seen := map[string]bool{}
	for lineNo, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		sep := -1
		for i := 0; i < len(line); i++ {
			if line[i] == '\\' {
				i++ // an escaped character is never the separator
				continue
			}
			if line[i] == ':' && (i+1 == len(line) || line[i+1] == ' ' || line[i+1] == '\t') {
				sep = i
				break
			}
		}
		if sep < 0 {
			return nil, fmt.Errorf("line %d has no 'target:' separator: %q", lineNo+1, line)
		}
		for _, p := range splitMakeWords(line[sep+1:]) {
			if !seen[p] {
				seen[p] = true
				prereqs = append(prereqs, p)
			}
		}
	}
	return prereqs, nil
}

// splitMakeWords splits a prerequisite list on unescaped whitespace and unescapes each word.
func splitMakeWords(s string) []string {
	var words []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '#'):
			word.WriteByte(s[i+1])
			i++
		case c == '$' && i+1 < len(s) && s[i+1] == '$':
			word.WriteByte('$')
			i++
		case c == ' ' || c == '\t':
			flush()
		default:
			word.WriteByte(c)
		}
	}
	flush()
	return words
}

// parseMSVCDepFile parses cl.exe's /sourceDependencies JSON. Headers under the Windows SDK and
// Visual Studio install are left out, as gcc's -MMD leaves out system headers.
func parseMSVCDepFile(data []byte) ([]string, error) {
	var doc struct {
		Data struct {
			Includes []string `json:"Includes"`
		} `json:"Data"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var includes []string
	for _, inc := range doc.Data.Includes {
		lower := strings.ToLower(strings.ReplaceAll(inc, "\\", "/"))
		if strings.HasPrefix(lower, "c:/program files") ||
			strings.HasPrefix(lower, "c:/windows/") ||
			strings.HasPrefix(lower, "/usr/include/") ||
			strings.HasPrefix(lower, "/usr/local/include/") {
			continue
		}
		includes = append(includes, filepath.FromSlash(strings.ReplaceAll(inc, "\\", "/")))
	}
	return includes, nil
}
