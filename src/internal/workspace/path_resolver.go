package workspace

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SourceEntry represents a source file entry that can be either a simple string
// or an object with pattern and optional flag
type SourceEntry struct {
	Pattern  string
	Optional bool
}

// PathResolver handles source file and include directory resolution
type PathResolver struct {
	ConfigFileDir string
	WorkspaceRoot string
}

// NewPathResolver creates a new PathResolver
func NewPathResolver(configFileDir, workspaceRoot string) *PathResolver {
	return &PathResolver{
		ConfigFileDir: configFileDir,
		WorkspaceRoot: workspaceRoot,
	}
}

// ParseSourceEntries parses source entries from config, handling both string and object forms
// Supports:
//   - Simple string: "src/*.cpp"
//   - Object form: {pattern: "src/*.cpp", optional: true}
func (pr *PathResolver) ParseSourceEntries(sourcesRaw any) ([]SourceEntry, error) {
	entries := []SourceEntry{}

	switch v := sourcesRaw.(type) {
	case string:
		entries = append(entries, SourceEntry{Pattern: v, Optional: false})
	case []any:
		for i, item := range v {
			switch s := item.(type) {
			case string:
				entries = append(entries, SourceEntry{Pattern: s, Optional: false})
			case map[string]any:
				pattern, ok := s["pattern"].(string)
				if !ok {
					return nil, fmt.Errorf("source entry %d: missing 'pattern' field", i)
				}
				optional := false
				if opt, ok := s["optional"].(bool); ok {
					optional = opt
				}
				entries = append(entries, SourceEntry{Pattern: pattern, Optional: optional})
			default:
				return nil, fmt.Errorf("source entry %d: must be a string or object with 'pattern' field", i)
			}
		}
	case []string:
		for _, s := range v {
			entries = append(entries, SourceEntry{Pattern: s, Optional: false})
		}
	default:
		return nil, fmt.Errorf("sources must be a string or list")
	}

	return entries, nil
}

// ResolveSources resolves source file paths from config, expanding globs
// Returns an error if a non-optional glob matches no files
func (pr *PathResolver) ResolveSources(itemConfig map[string]any) ([]string, error) {
	sourcesRaw, ok := itemConfig["sources"]
	if !ok {
		return []string{}, nil
	}

	entries, err := pr.ParseSourceEntries(sourcesRaw)
	if err != nil {
		return nil, err
	}

	var sources []string
	for _, entry := range entries {
		resolved, err := pr.expandGlob(entry.Pattern, entry.Optional)
		if err != nil {
			return nil, err
		}
		sources = append(sources, resolved...)
	}

	return sources, nil
}

// expandGlob expands a glob pattern to sorted file list
// Returns an error if the pattern matches no files and optional is false
func (pr *PathResolver) expandGlob(pattern string, optional bool) ([]string, error) {
	var searchPattern string
	if pr.ConfigFileDir == "" {
		log.Printf("WARNING: Config file directory not set, using current directory for pattern '%s'", pattern)
		searchPattern = pattern
	} else {
		// Make pattern relative to config file directory
		searchPattern = filepath.Join(pr.ConfigFileDir, pattern)
		log.Printf("Expanding pattern '%s' as '%s'", pattern, searchPattern)
	}

	results, err := filepath.Glob(searchPattern)
	if err != nil {
		return nil, fmt.Errorf("error expanding glob '%s': %w", pattern, err)
	}

	if len(results) == 0 {
		if optional {
			log.Printf("Optional glob pattern '%s' matched no files (skipped)", pattern)
			return []string{}, nil
		}
		return nil, fmt.Errorf("glob pattern '%s' (resolved to '%s') matched no files. "+
			"Use {pattern: \"%s\", optional: true} if this is intentional", pattern, searchPattern, pattern)
	}

	// Convert absolute paths back to relative paths from current directory
	cwd, _ := os.Getwd()
	relativeResults := []string{}
	for _, result := range results {
		relPath, err := filepath.Rel(cwd, result)
		if err != nil {
			// If it's not relative to cwd, use absolute path
			absPath, _ := filepath.Abs(result)
			relativeResults = append(relativeResults, absPath)
		} else {
			relativeResults = append(relativeResults, relPath)
		}
	}

	sort.Strings(relativeResults)
	log.Printf("Pattern '%s' matched %d files: %v", pattern, len(relativeResults), relativeResults)
	return relativeResults, nil
}

// ResolveIncludeDirs resolves include directory paths relative to config file directory
func (pr *PathResolver) ResolveIncludeDirs(itemConfig map[string]any) ([]string, error) {
	includeDirsRaw, ok := itemConfig["include_dirs"]
	if !ok {
		return []string{}, nil
	}

	var includeDirs []string

	// Helper to resolve a single path
	resolvePath := func(inc string) string {
		// Skip absolute paths (start with / or have a drive letter on Windows)
		if filepath.IsAbs(inc) {
			return inc
		}
		// Only resolve relative paths with the config file directory
		if pr.ConfigFileDir != "" && pr.ConfigFileDir != "." {
			absIncPath := filepath.Join(pr.ConfigFileDir, inc)
			cwd, _ := os.Getwd()
			relIncPath, err := filepath.Rel(cwd, absIncPath)
			if err != nil {
				return absIncPath
			}
			return relIncPath
		}
		return inc
	}

	// Helper to extract paths from a value
	extractPaths := func(value any) []string {
		result := []string{}
		switch v := value.(type) {
		case string:
			result = append(result, resolvePath(v))
		case []any:
			for _, item := range v {
				if str, ok := item.(string); ok {
					result = append(result, resolvePath(str))
				}
			}
		case []string:
			for _, str := range v {
				result = append(result, resolvePath(str))
			}
		}
		return result
	}

	switch v := includeDirsRaw.(type) {
	case map[string]any:
		// New format with public/private subsections
		if publicDirs, ok := v["public"]; ok {
			includeDirs = append(includeDirs, extractPaths(publicDirs)...)
		}
		if privateDirs, ok := v["private"]; ok {
			includeDirs = append(includeDirs, extractPaths(privateDirs)...)
		}
	case []any:
		// Legacy format: simple list
		includeDirs = extractPaths(v)
	case []string:
		// Legacy format: simple list
		includeDirs = extractPaths(v)
	}

	return includeDirs, nil
}

// ResolveLibDirs resolves library directory paths
func (pr *PathResolver) ResolveLibDirs(itemConfig map[string]any) ([]string, error) {
	libDirsRaw, ok := itemConfig["lib_dirs"]
	if !ok {
		return []string{}, nil
	}

	return pr.resolvePathList(libDirsRaw)
}

// resolvePathList resolves a list of paths
func (pr *PathResolver) resolvePathList(pathsRaw any) ([]string, error) {
	var paths []string

	resolvePath := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		if pr.ConfigFileDir != "" && pr.ConfigFileDir != "." {
			absPath := filepath.Join(pr.ConfigFileDir, p)
			cwd, _ := os.Getwd()
			relPath, err := filepath.Rel(cwd, absPath)
			if err != nil {
				return absPath
			}
			return relPath
		}
		return p
	}

	switch v := pathsRaw.(type) {
	case string:
		paths = append(paths, resolvePath(v))
	case []any:
		for _, item := range v {
			if str, ok := item.(string); ok {
				paths = append(paths, resolvePath(str))
			}
		}
	case []string:
		for _, str := range v {
			paths = append(paths, resolvePath(str))
		}
	}

	return paths, nil
}

// ResolveRelativePath resolves a path relative to the config file directory
func (pr *PathResolver) ResolveRelativePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if pr.ConfigFileDir != "" {
		absPath := filepath.Join(pr.ConfigFileDir, path)
		cwd, _ := os.Getwd()
		relPath, err := filepath.Rel(cwd, absPath)
		if err != nil {
			return absPath
		}
		return relPath
	}
	return path
}

// ExtractStringList extracts a string slice from various input types
func ExtractStringList(value any) []string {
	result := []string{}
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			result = append(result, v)
		}
	case []any:
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
	case []string:
		result = v
	}
	return result
}
