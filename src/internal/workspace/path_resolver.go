package workspace

import (
	"fmt"
	"path/filepath"
	"sort"

	"buildy/internal/config"
	"buildy/pkg/util"
)

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

// ResolveSources expands a target's source patterns, in order, to absolute files. A pattern that
// matches no file is an error unless it is optional ({pattern: ..., optional: true}).
func (pr *PathResolver) ResolveSources(sources []config.Source) ([]string, error) {
	var all []string
	for _, source := range sources {
		resolved, err := pr.expandGlob(source.Pattern, source.Optional)
		if err != nil {
			return nil, err
		}
		all = append(all, resolved...)
	}
	return all, nil
}

// expandGlob expands a pattern, relative to the config file directory, to a sorted list of
// absolute files (util.Glob: ** is recursive). Returns an error if the pattern matches no files
// and optional is false. Also includes virtual files (from artifact outputs) that match the pattern.
func (pr *PathResolver) expandGlob(pattern string, optional bool) ([]string, error) {
	if !filepath.IsAbs(pr.ConfigFileDir) {
		panic(fmt.Sprintf("internal error: path resolver needs an absolute config directory, has %q", pr.ConfigFileDir))
	}
	glob, err := util.ParseGlob(pr.ConfigFileDir, pattern)
	if err != nil {
		return nil, err
	}
	util.LogInfo("Expanding pattern '%s' as '%s'", pattern, glob)

	results, err := glob.Files()
	if err != nil {
		return nil, err
	}

	// Also match virtual files (artifact outputs) against the pattern
	for _, vf := range pr.VirtualFiles {
		if glob.Match(vf) {
			results = append(results, vf)
		}
	}

	if len(results) == 0 {
		if optional {
			util.LogInfo("Optional glob pattern '%s' matched no files (skipped)", pattern)
			return []string{}, nil
		}
		return nil, fmt.Errorf("glob pattern '%s' (resolved to '%s') matched no files. "+
			"Use {pattern: \"%s\", optional: true} if this is intentional", pattern, glob, pattern)
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

// ResolveIncludeDirs makes include directories absolute, relative to the config file's directory
func (pr *PathResolver) ResolveIncludeDirs(dirs []string) []string {
	result := make([]string, len(dirs))
	for i, dir := range dirs {
		result[i] = pr.absolute(dir)
	}
	return result
}

// ResolveRelativePath resolves a path relative to the config file directory to an absolute path
func (pr *PathResolver) ResolveRelativePath(path string) string {
	return pr.absolute(path)
}
