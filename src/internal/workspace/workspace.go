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

// Workspace manages multi-module workspace with buildy.yaml files
type Workspace struct {
	RootDir    string
	ConfigPath string                  // Explicit config file path (empty for default buildy.yaml discovery)
	Config     *WorkspaceConfig
	Modules    map[string]*ModuleInfo // relative_path -> ModuleInfo
	discovered bool
}

// NewWorkspace creates a new Workspace
func NewWorkspace(rootDir string) (*Workspace, error) {
	if rootDir == "" {
		// Find workspace root by walking up directory tree
		foundRoot, err := findWorkspaceRoot("")
		if err != nil {
			return nil, err
		}
		rootDir = foundRoot
	}
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}

	ws := &Workspace{
		RootDir:    rootDir,
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

// NewWorkspaceWithConfig creates a new Workspace with a custom config file path
// This is used when building dependencies with override config files
func NewWorkspaceWithConfig(rootDir string, configPath string) (*Workspace, error) {
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

// findWorkspaceRoot finds workspace root by walking up directory tree
func findWorkspaceRoot(startDir string) (string, error) {
	if startDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current directory: %w", err)
		}
		startDir = cwd
	}

	current, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	// Walk up directory tree
	for {
		candidate := filepath.Join(current, "buildy.yaml")
		if _, err := os.Stat(candidate); err == nil {
			util.LogDebug("Found workspace root: %s", current)
			return current, nil
		}

		// Check if we've reached filesystem root
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf(
				"no buildy.yaml found in current directory or any parent directory. " +
					"Please create a buildy.yaml file in your project root",
			)
		}

		current = parent
	}
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
	excludePatterns := []string{".buildy_cache/**", "venv/**", ".git/**", "**/.git/**"}
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
			for _, excludePattern := range ws.Config.ExcludePatterns {
				pattern := strings.TrimSpace(excludePattern)
				if pattern == "" {
					continue
				}
				if matchPattern(relPath, pattern) {
					return filepath.SkipDir
				}
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

// matchPattern matches a path against a glob-like pattern
func matchPattern(path, pattern string) bool {
	matched, err := doublestar.Match(pattern, path)
	if err != nil {
		util.LogWarning("Pattern match error for %s: %v", pattern, err)
		return false
	}
	return matched
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

// DiscoverWorkspace discovers workspace starting from a directory
func DiscoverWorkspace(startDir string) (*Workspace, error) {
	root, err := findWorkspaceRoot(startDir)
	if err != nil {
		return nil, err
	}
	return NewWorkspace(root)
}
