package workspace

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"buildy/pkg/util"
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
	// VirtualFiles are files that don't exist yet but will be created by artifacts
	// These are included in glob matching for targets that depend on artifacts
	VirtualFiles []string
}

// NewPathResolver creates a new PathResolver
func NewPathResolver(configFileDir, workspaceRoot string) *PathResolver {
	return &PathResolver{
		ConfigFileDir: configFileDir,
		WorkspaceRoot: workspaceRoot,
		VirtualFiles:  []string{},
	}
}

// AddVirtualFiles adds files that will be created by artifacts
// These files don't exist yet but should be included in glob matching
func (pr *PathResolver) AddVirtualFiles(files []string) {
	pr.VirtualFiles = append(pr.VirtualFiles, files...)
}

// ClearVirtualFiles removes all virtual files
func (pr *PathResolver) ClearVirtualFiles() {
	pr.VirtualFiles = []string{}
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
// Deprecated: Use ResolveSourcesWithContext for filtered sources
func (pr *PathResolver) ResolveSources(itemConfig map[string]any) ([]string, error) {
	return pr.ResolveSourcesWithContext(itemConfig, BuildContext{})
}

// ResolveSourcesForPlatform resolves source file paths with platform filtering
// Deprecated: Use ResolveSourcesWithContext instead
func (pr *PathResolver) ResolveSourcesForPlatform(itemConfig map[string]any, platform string) ([]string, error) {
	return pr.ResolveSourcesWithContext(itemConfig, BuildContext{Platform: platform})
}

// ResolveSourcesWithContext resolves source file paths with full filter support.
// Sources can be:
//   - Simple strings (always included)
//   - Filter maps (included when filter matches context)
//
// Example:
//
//	sources:
//	  - "src/common.c"        # Always included
//	  - linux:                # Included when platform=linux
//	      - "src/linux.c"
//	  - windows:              # Included when platform=windows
//	      - "src/windows.c"
func (pr *PathResolver) ResolveSourcesWithContext(itemConfig map[string]any, ctx BuildContext) ([]string, error) {
	sourcesRaw, ok := itemConfig["sources"]
	if !ok {
		return []string{}, nil
	}

	// Use ResolveFilteredList to get the filtered source patterns
	patterns := ResolveFilteredList(sourcesRaw, ctx)

	// Expand globs for each pattern
	var allSources []string
	for _, pattern := range patterns {
		// Check if this is an optional pattern (object form with optional: true)
		// For now, treat all patterns from filtered list as required
		resolved, err := pr.expandGlob(pattern, false)
		if err != nil {
			return nil, err
		}
		allSources = append(allSources, resolved...)
	}

	return allSources, nil
}

// expandGlob expands a glob pattern, relative to the config file directory, to a sorted list of
// absolute paths. Returns an error if the pattern matches no files and optional is false.
// Also includes virtual files (from artifact outputs) that match the pattern.
func (pr *PathResolver) expandGlob(pattern string, optional bool) ([]string, error) {
	searchPattern := pr.absolute(pattern)
	util.LogInfo("Expanding pattern '%s' as '%s'", pattern, searchPattern)

	results, err := filepath.Glob(searchPattern)
	if err != nil {
		return nil, fmt.Errorf("error expanding glob '%s': %w", pattern, err)
	}

	// Also match virtual files (artifact outputs) against the pattern
	virtualMatches := pr.matchVirtualFiles(searchPattern)
	if len(virtualMatches) > 0 {
		util.LogInfo("Pattern '%s' matched %d virtual files from artifacts", pattern, len(virtualMatches))
		results = append(results, virtualMatches...)
	}

	if len(results) == 0 {
		if optional {
			util.LogInfo("Optional glob pattern '%s' matched no files (skipped)", pattern)
			return []string{}, nil
		}
		return nil, fmt.Errorf("glob pattern '%s' (resolved to '%s') matched no files. "+
			"Use {pattern: \"%s\", optional: true} if this is intentional", pattern, searchPattern, pattern)
	}

	absResults := []string{}
	seen := make(map[string]bool) // deduplicate
	for _, result := range results {
		result = filepath.Clean(result)
		if !seen[result] {
			absResults = append(absResults, result)
			seen[result] = true
		}
	}

	sort.Strings(absResults)
	util.LogInfo("Pattern '%s' matched %d files: %v", pattern, len(absResults), absResults)
	return absResults, nil
}

// absolute resolves a path against the config file directory. Every path buildy derives from
// configuration is absolute, so nothing depends on the directory it was started from.
func (pr *PathResolver) absolute(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if !filepath.IsAbs(pr.ConfigFileDir) {
		panic(fmt.Sprintf("internal error: path resolver needs an absolute config directory, has %q", pr.ConfigFileDir))
	}
	return filepath.Join(pr.ConfigFileDir, p)
}

// matchVirtualFiles returns virtual files that match the given glob pattern
func (pr *PathResolver) matchVirtualFiles(pattern string) []string {
	var matches []string
	for _, vf := range pr.VirtualFiles {
		matched, err := filepath.Match(pattern, vf)
		if err == nil && matched {
			matches = append(matches, vf)
		}
	}
	return matches
}

// ResolveIncludeDirs resolves include directory paths relative to config file directory
func (pr *PathResolver) ResolveIncludeDirs(itemConfig map[string]any) ([]string, error) {
	includeDirsRaw, ok := itemConfig["include_dirs"]
	if !ok {
		return []string{}, nil
	}

	var includeDirs []string

	resolvePath := pr.absolute

	switch v := includeDirsRaw.(type) {
	case string:
		includeDirs = append(includeDirs, resolvePath(v))
	case []any:
		for _, item := range v {
			if str, ok := item.(string); ok {
				includeDirs = append(includeDirs, resolvePath(str))
			}
		}
	case []string:
		for _, str := range v {
			includeDirs = append(includeDirs, resolvePath(str))
		}
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

	resolvePath := pr.absolute

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

// ResolveRelativePath resolves a path relative to the config file directory to an absolute path
func (pr *PathResolver) ResolveRelativePath(path string) string {
	return pr.absolute(path)
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
