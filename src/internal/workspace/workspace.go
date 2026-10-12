package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"buildy/internal/config"
	"buildy/pkg/util"

	"github.com/bmatcuk/doublestar/v4"
)

// ModuleInfo is a loaded module
type ModuleInfo struct {
	Path         string       `json:"path"`
	RelativePath string       `json:"relative_path"`
	File         *config.File `json:"-"`
	Targets      []string     `json:"targets"`
}

// WorkspaceConfig is the workspace's root config and what buildy reads from it before loading
// the modules
type WorkspaceConfig struct {
	RootDir          string             `json:"root"`
	DiscoverPatterns []string           `json:"discover_patterns"` // without workspace.modules: the configs to discover
	ExcludePatterns  []string           `json:"exclude_patterns"`
	Modules          *config.ModuleList `json:"-"` // workspace.modules as written
	DependenciesFile string             `json:"dependencies_file"`
	File             *config.File       `json:"-"`
}

// resolveModuleEntries returns the modules a workspace.modules section selects for ctx. It is
// a filtered list like any other: plain entries ("core" or {path: core, config: core.yaml}) are
// always included, and a filter map ({linux: [...]}, {x86_64: [...]}, {release: [...]}) includes
// its entries when the key matches the platform, architecture or configuration. The legacy map
// form ({common: [...], linux: [...]}) includes "common" and the keys that match.
func resolveModuleEntries(modules *config.ModuleList, ctx BuildContext) []config.ModuleEntry {
	if modules == nil {
		return nil
	}
	return modules.Entries.Resolve(ctx)
}

// DefaultCacheDir is the cache directory (--cache-dir) unless one is given: relative to the
// workspace root.
const DefaultCacheDir = ".buildy_cache"

// cacheDirOf returns the cache directory of a workspace at root: cacheDir (--cache-dir) relative to
// the root, or absolute.
func cacheDirOf(root, cacheDir string) string {
	if !filepath.IsAbs(cacheDir) {
		cacheDir = filepath.Join(root, cacheDir)
	}
	return filepath.Clean(cacheDir)
}

// Workspace manages multi-module workspace with buildy.yaml files
type Workspace struct {
	RootDir    string
	ConfigPath string                  // Explicit config file path (empty for default buildy.yaml discovery)
	CacheDir   string                  // the cache directory, absolute: discovery never looks in it
	Config     *WorkspaceConfig
	Modules    map[string]*ModuleInfo // relative_path -> ModuleInfo
	discovered bool
}

// NewWorkspace loads the workspace at rootDir, whose cache directory is cacheDir (relative to it,
// or absolute).
func NewWorkspace(rootDir, cacheDir string) (*Workspace, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("internal error: NewWorkspace needs a directory (DiscoverWorkspace finds one)")
	}
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}

	ws := &Workspace{
		RootDir:    rootDir,
		CacheDir:   cacheDirOf(rootDir, cacheDir),
		Modules:    make(map[string]*ModuleInfo),
		discovered: false,
	}

	config, err := ws.loadWorkspaceConfig("")
	if err != nil {
		return nil, err
	}
	ws.Config = config

	return ws, nil
}

// NewWorkspaceWithConfig loads a standalone config file as a workspace rooted at rootDir (the
// config of a dependency's build), whose cache directory is cacheDir.
func NewWorkspaceWithConfig(rootDir, configPath, cacheDir string) (*Workspace, error) {
	// Every path buildy derives is absolute, so the result never depends on the current directory
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	if configPath, err = filepath.Abs(configPath); err != nil {
		return nil, err
	}
	ws := &Workspace{
		RootDir:    rootDir,
		ConfigPath: configPath, // Store explicit config path
		CacheDir:   cacheDirOf(rootDir, cacheDir),
		Modules:    make(map[string]*ModuleInfo),
		discovered: false,
	}

	config, err := ws.loadWorkspaceConfig(configPath)
	if err != nil {
		return nil, err
	}
	ws.Config = config

	return ws, nil
}

// findWorkspaceRoot finds the workspace that start (a directory) belongs to, as Cargo does: the
// nearest directory at or above start with a buildy.yaml is a module, and its workspace root is
// the outermost directory above it whose buildy.yaml includes it as a module (listed in
// workspace.modules, directly or through child modules, under any filter key; or, in discovery
// mode, discovered, outside its cache directory cacheDir). A buildy.yaml that no config above
// includes is its own workspace. The second result says why the root was chosen.
func findWorkspaceRoot(start, cacheDir string) (string, string, error) {
	start, err := filepath.Abs(start)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve path: %w", err)
	}
	var candidates []string // directories with a buildy.yaml, nearest first
	for dir := start; ; dir = filepath.Dir(dir) {
		if IsWorkspaceRoot(dir) {
			candidates = append(candidates, dir)
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if len(candidates) == 0 {
		return "", "", fmt.Errorf("no buildy.yaml in %s or any directory above it", start)
	}
	nearest := candidates[0]
	for i := len(candidates) - 1; i > 0; i-- {
		included, err := includesModule(candidates[i], nearest, cacheDir)
		if err != nil {
			return "", "", err
		}
		if included {
			return candidates[i], fmt.Sprintf("its buildy.yaml includes %s as a module", nearest), nil
		}
	}
	if len(candidates) > 1 {
		return nearest, fmt.Sprintf("no buildy.yaml above it includes it as a module (%s)", strings.Join(candidates[1:], ", ")), nil
	}
	return nearest, "the only buildy.yaml at or above " + start, nil
}

// includesModule reports whether the workspace whose root config is in root includes dir as a
// module, for any build: explicit modules are followed through their child modules, whatever
// their filter keys.
func includesModule(root, dir, cacheDir string) (bool, error) {
	section, err := config.LoadWorkspaceSection(filepath.Join(root, "buildy.yaml"))
	if err != nil {
		return false, err
	}
	if section == nil || section.Modules == nil {
		var discover, exclude []string
		if section != nil {
			discover, exclude = section.Discover, section.Exclude
		}
		return discovers(root, dir, cacheDirOf(root, cacheDir), discover, exclude), nil
	}
	return listsModule(root, section.Modules, dir, map[string]bool{})
}

// listsModule reports whether modules (a config's workspace.modules, in parent) or their child
// modules include dir.
func listsModule(parent string, modules *config.ModuleList, dir string, seen map[string]bool) (bool, error) {
	for _, entry := range modules.Entries.All() {
		module := filepath.Join(parent, entry.Path)
		if util.NormalizePath(module) == util.NormalizePath(dir) {
			return true, nil
		}
		configPath := filepath.Join(module, entry.Config)
		if seen[configPath] {
			continue
		}
		seen[configPath] = true
		if _, err := os.Stat(configPath); err != nil {
			continue // loading the workspace reports it
		}
		children, err := config.LoadWorkspaceSection(configPath)
		if err != nil {
			return false, err
		}
		if children != nil && children.Modules != nil {
			if found, err := listsModule(module, children.Modules, dir, seen); found || err != nil {
				return found, err
			}
		}
	}
	return false, nil
}

// defaultExcludes are the directories discovery never looks in, with the cache directory (whose
// fetched dependencies have configs of their own).
var defaultExcludes = []string{"venv/**", ".git/**", "**/.git/**"}

// discovers reports whether a workspace at root in discovery mode, with this cache directory and
// these discover and exclude patterns, finds dir's buildy.yaml.
func discovers(root, dir, cacheDir string, discover, exclude []string) bool {
	if !util.PathWithin(dir, root) || util.PathWithin(dir, cacheDir) {
		return false
	}
	rel, err := filepath.Rel(root, filepath.Join(dir, "buildy.yaml"))
	if err != nil {
		return false
	}
	excludes := append(append([]string{}, defaultExcludes...), exclude...)
	for d := filepath.Dir(rel); d != "."; d = filepath.Dir(d) {
		if excludedDir(d, excludes) {
			return false
		}
	}
	if len(discover) == 0 {
		discover = []string{"**/buildy.yaml"}
	}
	for _, pattern := range discover {
		if pattern = strings.TrimSpace(pattern); pattern != "" && matchPattern(rel, pattern) {
			return true
		}
	}
	return false
}

// excludedDir reports whether discovery skips the directory rel (relative to the root).
func excludedDir(rel string, excludes []string) bool {
	for _, pattern := range excludes {
		if pattern = strings.TrimSpace(pattern); pattern != "" && matchPattern(rel, pattern) {
			return true
		}
	}
	return false
}

// loadWorkspaceConfig loads workspace configuration from buildy.yaml
// If configPath is empty, uses buildy.yaml in the workspace root
func (ws *Workspace) loadWorkspaceConfig(configPath string) (*WorkspaceConfig, error) {
	configFile := configPath
	if configFile == "" {
		configFile = filepath.Join(ws.RootDir, "buildy.yaml")
	}

	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("workspace config not found: %s", configFile)
	}

	file, err := config.LoadFile(configFile, true)
	if err != nil {
		return nil, err
	}

	// Explicit modules (resolved against the build context at discovery); without them, every
	// buildy.yaml under the root is discovered
	var modules *config.ModuleList
	var discover, exclude []string
	if file.Workspace != nil {
		modules = file.Workspace.Modules
		discover, exclude = file.Workspace.Discover, file.Workspace.Exclude
	}
	hasExplicitModules := modules != nil

	discoverPatterns := []string{}
	excludePatterns := append([]string{}, defaultExcludes...)
	if !hasExplicitModules {
		discoverPatterns = []string{"**/buildy.yaml"}
		if len(discover) > 0 {
			discoverPatterns = discover
		}
		excludePatterns = append(excludePatterns, exclude...)
	}

	dependenciesFile := ""
	if file.Dependencies != nil {
		dependenciesFile = file.Dependencies.File
	}

	config := &WorkspaceConfig{
		RootDir:          ws.RootDir,
		DiscoverPatterns: discoverPatterns,
		ExcludePatterns:  excludePatterns,
		Modules:          modules,
		DependenciesFile: dependenciesFile,
		File:             file,
	}

	if hasExplicitModules {
		util.LogDebug("Loaded workspace from %s (explicit modules)", ws.RootDir)
	} else {
		util.LogDebug("Loaded workspace from %s (discovery mode)", ws.RootDir)
		util.LogDebug("Discovery patterns: %v", discoverPatterns)
		util.LogDebug("Exclude patterns: %v", excludePatterns)
	}

	return config, nil
}

// DiscoverModules loads the workspace's modules for the build context: the explicit
// workspace.modules (and the modules those name, recursively) that ctx selects, or every
// buildy.yaml under the root in discovery mode. It runs once; later calls return the same modules.
func (ws *Workspace) DiscoverModules(ctx BuildContext) (map[string]*ModuleInfo, error) {
	if ws.discovered {
		return ws.Modules, nil
	}

	util.LogVerbose("Discovering modules in workspace...")
	ws.Modules = make(map[string]*ModuleInfo)

	// If we have an explicit config path (e.g., dependency build with override),
	// load it directly as the root module instead of discovering
	if ws.ConfigPath != "" {
		moduleInfo, err := ws.loadModule(ws.ConfigPath, ".")
		if err != nil {
			return nil, fmt.Errorf("failed to load module from config %s: %w", ws.ConfigPath, err)
		}
		ws.Modules["."] = moduleInfo
		ws.discovered = true
		util.LogVerbose("Loaded root module from explicit config: %s (%d targets)", ws.ConfigPath, len(moduleInfo.Targets))
		return ws.Modules, nil
	}

	// Check if we have explicit modules defined
	if ws.Config.Modules != nil {
		return ws.loadExplicitModules(ctx)
	}

	// Fall back to legacy discovery mode
	discoveredFiles, err := ws.findModuleFiles()
	if err != nil {
		return nil, err
	}

	// Load each module
	for _, modulePath := range discoveredFiles {
		relPath, err := filepath.Rel(ws.RootDir, modulePath)
		if err != nil {
			return nil, fmt.Errorf("module %s: %w", modulePath, err)
		}

		moduleDir := filepath.Dir(relPath)

		// Root buildy.yaml is module "."
		if modulePath == filepath.Join(ws.RootDir, "buildy.yaml") {
			moduleDir = "."
		}

		moduleInfo, err := ws.loadModule(modulePath, moduleDir)
		if err != nil {
			return nil, err
		}

		ws.Modules[moduleDir] = moduleInfo
		util.LogDebug("Discovered module: %s (%d targets)", moduleDir, len(moduleInfo.Targets))
	}

	ws.discovered = true
	totalTargets := ws.countTotalTargets()
	util.LogVerbose("Discovered %d modules with %d total targets", len(ws.Modules), totalTargets)

	return ws.Modules, nil
}

// loadExplicitModules loads the modules workspace.modules selects for ctx
func (ws *Workspace) loadExplicitModules(ctx BuildContext) (map[string]*ModuleInfo, error) {
	modulesToLoad := resolveModuleEntries(ws.Config.Modules, ctx)

	// Load each module
	for _, entry := range modulesToLoad {
		moduleDir := filepath.Clean(entry.Path)
		configFile := filepath.Join(ws.RootDir, moduleDir, entry.Config)

		// Check if config file exists
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			return nil, fmt.Errorf("module config not found: %s (referenced in workspace.modules)", configFile)
		}

		moduleInfo, err := ws.loadModule(configFile, moduleDir)
		if err != nil {
			return nil, fmt.Errorf("failed to load module %s: %w", moduleDir, err)
		}

		ws.Modules[moduleDir] = moduleInfo
		util.LogDebug("Loaded explicit module: %s (%d targets)", moduleDir, len(moduleInfo.Targets))

		// Recursively load child modules if this module has workspace.modules
		// Pass loading stack to detect circular dependencies
		if err := ws.loadChildModulesWithCycleDetection(moduleDir, moduleInfo, ctx, nil); err != nil {
			return nil, err
		}
	}

	ws.discovered = true
	totalTargets := ws.countTotalTargets()
	util.LogVerbose("Loaded %d explicit modules with %d total targets", len(ws.Modules), totalTargets)

	return ws.Modules, nil
}

// configFile is the workspace's root config file.
func (ws *Workspace) configFile() string {
	if ws.ConfigPath != "" {
		return ws.ConfigPath
	}
	return filepath.Join(ws.RootDir, "buildy.yaml")
}

// loadChildModulesWithCycleDetection recursively loads child modules with circular dependency detection
func (ws *Workspace) loadChildModulesWithCycleDetection(parentDir string, parent *ModuleInfo, ctx BuildContext, loadingStack []string) error {
	if parent.File.Workspace == nil {
		return nil // No workspace section, this is a leaf module
	}

	// Child modules are selected exactly like the root's
	childModules := resolveModuleEntries(parent.File.Workspace.Modules, ctx)

	// Load each child module
	for _, entry := range childModules {
		// Child paths are relative to parent module
		moduleDir := filepath.Join(parentDir, entry.Path)
		configFile := filepath.Join(ws.RootDir, moduleDir, entry.Config)

		// Check for circular dependency
		if hasString(loadingStack, moduleDir) {
			cyclePath := append(append([]string{}, loadingStack...), moduleDir)
			return fmt.Errorf("circular module dependency detected: %s", strings.Join(cyclePath, " -> "))
		}

		// Check if config file exists
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			return fmt.Errorf("child module config not found: %s (referenced in %s)", configFile, parent.Path)
		}

		// Skip if already loaded (but not currently loading - that would be a cycle)
		if _, exists := ws.Modules[moduleDir]; exists {
			continue
		}

		moduleInfo, err := ws.loadModule(configFile, moduleDir)
		if err != nil {
			return fmt.Errorf("failed to load child module %s: %w", moduleDir, err)
		}

		ws.Modules[moduleDir] = moduleInfo
		util.LogDebug("Loaded child module: %s (%d targets)", moduleDir, len(moduleInfo.Targets))

		// Recursively load grandchild modules
		if err := ws.loadChildModulesWithCycleDetection(moduleDir, moduleInfo, ctx, append(append([]string{}, loadingStack...), moduleDir)); err != nil {
			return err
		}
	}

	return nil
}

// findModuleFiles finds all buildy.yaml files matching discovery patterns
func (ws *Workspace) findModuleFiles() ([]string, error) {
	discovered := make(map[string]bool)

	err := filepath.Walk(ws.RootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(ws.RootDir, path)
		if err != nil {
			return err
		}

		// Check if this directory should be excluded
		if info.IsDir() {
			if util.NormalizePath(path) == util.NormalizePath(ws.CacheDir) || excludedDir(relPath, ws.Config.ExcludePatterns) {
				return filepath.SkipDir
			}
			return nil
		}

		// Check for buildy.yaml
		if filepath.Base(path) == "buildy.yaml" {
			// Check if it matches any discovery pattern
			for _, discoverPattern := range ws.Config.DiscoverPatterns {
				pattern := strings.TrimSpace(discoverPattern)
				if pattern == "" {
					continue
				}
				if matchPattern(relPath, pattern) {
					discovered[path] = true
					break
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory tree: %w", err)
	}

	// Convert to sorted slice
	result := make([]string, 0, len(discovered))
	for path := range discovered {
		result = append(result, path)
	}
	sort.Strings(result)

	return result, nil
}

// matchPattern matches a path against a discover or exclude pattern, which loading the config
// checked.
func matchPattern(path, pattern string) bool {
	return doublestar.MatchUnvalidated(pattern, path)
}

// loadModule loads and validates a module configuration file
func (ws *Workspace) loadModule(modulePath, relativeDir string) (*ModuleInfo, error) {
	file, err := config.LoadFile(modulePath, filepath.Clean(modulePath) == filepath.Clean(ws.configFile()))
	if err != nil {
		return nil, err
	}
	return &ModuleInfo{
		Path:         modulePath,
		RelativePath: relativeDir,
		File:         file,
		Targets:      file.Targets.Names(),
	}, nil
}

// countTotalTargets counts total number of targets across all modules
func (ws *Workspace) countTotalTargets() int {
	total := 0
	for _, module := range ws.Modules {
		total += len(module.Targets)
	}
	return total
}

// IsWorkspaceRoot checks if directory is a workspace root
func IsWorkspaceRoot(directory string) bool {
	configFile := filepath.Join(directory, "buildy.yaml")
	_, err := os.Stat(configFile)
	return err == nil
}

// DiscoverWorkspace loads the workspace startDir belongs to (findWorkspaceRoot): from inside a
// module, the whole workspace that includes it. cacheDir is --cache-dir: relative to the root, or
// absolute.
func DiscoverWorkspace(startDir, cacheDir string) (*Workspace, error) {
	root, why, err := findWorkspaceRoot(startDir, cacheDir)
	if err != nil {
		return nil, err
	}
	util.LogInfo("Workspace root: %s (%s)", root, why)
	return NewWorkspace(root, cacheDir)
}
