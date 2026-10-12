package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"buildy/internal/cache"
	"buildy/internal/workspace"
	"buildy/pkg/util"
)

// within reports whether path is strictly inside dir.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Clean removes what buildy produced for the workspace, and nothing else:
//
//   - the output directories (OutputRoots: build/ by default, every configuration's outputs);
//   - every other file the cache records a task producing (staging, installs and copies outside
//     the output directory), with the directories that leaves empty;
//   - with all, the cache directory too (fetched dependencies and their builds).
//
// Only what lies inside the workspace root is removed. An output directory that holds a module's
// config, or a cache directory outside the workspace root for all, is an error before anything is
// removed. Load the workspace's modules first.
func Clean(ws *workspace.Workspace, ctx workspace.BuildContext, cliDefines map[string]string, cacheDir string, all bool) error {
	root := ws.RootDir
	if all && !within(root, cacheDir) {
		return fmt.Errorf("the cache directory %s is outside the workspace root %s: buildy removes only what is inside it", cacheDir, root)
	}
	outputRoots, err := ws.OutputRoots(ctx, cliDefines)
	if err != nil {
		return err
	}
	configs := []string{filepath.Join(root, "buildy.yaml")}
	for _, m := range ws.Modules {
		configs = append(configs, m.Path)
	}
	var remove []string
	for _, dir := range outputRoots {
		if !within(root, dir) {
			util.LogWarning("Not removing the output directory %s: it is outside the workspace root %s", dir, root)
			continue
		}
		for _, c := range configs {
			if within(dir, c) {
				return fmt.Errorf("the output directory %s holds the config %s: not removing it", dir, c)
			}
		}
		remove = append(remove, dir)
	}

	// The files the cache records outside those directories
	var recorded []string
	if info, err := os.Stat(cacheDir); err == nil && info.IsDir() {
		bc, err := cache.NewBuildCache(cacheDir)
		if err != nil {
			return err
		}
		for _, output := range bc.RecordedOutputs() {
			inside := !within(root, output) || within(cacheDir, output)
			for _, dir := range remove {
				inside = inside || within(dir, output)
			}
			if !inside {
				recorded = append(recorded, output)
			}
		}
	}

	removed := 0
	for _, file := range recorded {
		info, err := os.Lstat(file)
		if err != nil || info.IsDir() {
			continue // gone, or no longer what buildy wrote
		}
		if err := os.Remove(file); err != nil {
			return err
		}
		removed++
		// The directories that leaves empty, up to the workspace root
		for dir := filepath.Dir(file); within(root, dir); dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil {
				break // not empty
			}
		}
	}
	if removed > 0 {
		util.LogProgress("Removed %d files buildy staged, installed or copied outside the output directory", removed)
	}
	for _, dir := range remove {
		if _, err := os.Lstat(dir); err != nil {
			continue
		}
		util.LogProgress("Removing the output directory %s", dir)
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	if all {
		if _, err := os.Lstat(cacheDir); err == nil {
			util.LogProgress("Removing the cache directory %s", cacheDir)
			if err := os.RemoveAll(cacheDir); err != nil {
				return err
			}
		}
	}
	return nil
}
