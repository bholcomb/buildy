package workspace

import (
	"fmt"
	"path/filepath"

	"buildy/internal/config"
	"buildy/pkg/util"
)

// PathResolver handles source file and include directory resolution
type PathResolver struct {
	ConfigFileDir string
	// Outputs are the outputs of the tasks generated so far, which source patterns match too
	// (generated sources), and the output directories, where only they count
	Outputs *DeclaredOutputs
}

// NewPathResolver creates a new PathResolver
func NewPathResolver(configFileDir string, outputs *DeclaredOutputs) *PathResolver {
	return &PathResolver{ConfigFileDir: configFileDir, Outputs: outputs}
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
// absolute files: those on disk outside the output directories, and the outputs of the tasks
// generated so far (generated sources) it matches. Returns an error if the pattern matches no
// files and optional is false.
func (pr *PathResolver) expandGlob(pattern string, optional bool) ([]string, error) {
	if !filepath.IsAbs(pr.ConfigFileDir) {
		panic(fmt.Sprintf("internal error: path resolver needs an absolute config directory, has %q", pr.ConfigFileDir))
	}
	glob, err := util.ParseGlob(pr.ConfigFileDir, pattern)
	if err != nil {
		return nil, err
	}
	files, outputDir, err := pr.Outputs.Files(glob)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		if optional {
			util.LogInfo("Optional glob pattern '%s' matched no files (skipped)", pattern)
			return []string{}, nil
		}
		if outputDir != "" {
			return nil, fmt.Errorf("source pattern '%s': %w", pattern, noOutputMatches(glob, outputDir))
		}
		return nil, fmt.Errorf("glob pattern '%s' (resolved to '%s') matched no files. "+
			"Use {pattern: \"%s\", optional: true} if this is intentional", pattern, glob, pattern)
	}
	util.LogInfo("Pattern '%s' matched %d files: %v", pattern, len(files), files)
	return files, nil
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
