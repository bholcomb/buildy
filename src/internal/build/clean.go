package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"buildy/internal/workspace"
	"buildy/pkg/util"
)

// within reports whether path is strictly inside dir.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Clean removes what buildy produced for the workspace, inside the workspace root only:
//
//   - the output directories (OutputRoots: build/ by default, every configuration's outputs, with
//     the staging and install directories inside them, as by default);
//   - with all, the cache directory too (fetched dependencies and their builds).
//
// What staging, installs and copies put outside the output directory stays: such a directory can
// be the workspace itself, where a staged copy can stand on a file of the user's (Mobius stages
// src/packages/x/x.mob onto its tracked packages/x/x.mob). An output directory that holds a
// module's config, or a cache directory outside the workspace root for all, is an error before
// anything is removed. Load the workspace's modules first.
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
	if all {
		remove = append(remove, cacheDir)
	}
	for _, dir := range remove {
		if _, err := os.Lstat(dir); err != nil {
			continue
		}
		util.LogProgress("Removing %s", dir)
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return nil
}
