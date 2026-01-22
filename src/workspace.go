package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// ModuleInfo represents information about a discovered module
type ModuleInfo struct {
	Path         string         `json:"path"`
	RelativePath string         `json:"relative_path"`
	Config       map[string]any `json:"-"` // Not serialized to cache
	Targets      []string       `json:"targets"`
}

// WorkspaceConfig represents workspace configuration from root buildy.yaml
type WorkspaceConfig struct {
	RootDir          string                       `json:"root"`
	DiscoverPatterns []string                     `json:"discover_patterns"`  // Legacy: for backward compat
	ExcludePatterns  []string                     `json:"exclude_patterns"`   // Legacy: for backward compat
	ExplicitModules  map[string][]ModuleEntry     `json:"explicit_modules"`   // New: platform -> module entries
	Variables        map[string]any               `json:"variables"`
	PackagePaths     []string                     `json:"package_paths"`
	Packages         map[string]map[string]string `json:"packages"` // package_name -> {version, custom_vars}
	DependenciesFile string                       `json:"dependencies_file"`  // Path to external dependencies file
	RawConfig        map[string]any               `json:"-"`
}

// ModuleEntry represents a module in the workspace.modules section
type ModuleEntry struct {
	Path   string `json:"path"`   // Relative path to module directory
	Config string `json:"config"` // Custom config filename (default: buildy.yaml)
}

// Workspace manages multi-module workspace with buildy.yaml files
type Workspace struct {
	RootDir    string
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

	ws := &Workspace{
		RootDir:    rootDir,
		Modules:    make(map[string]*ModuleInfo),
		discovered: false,
	}

	config, err := ws.loadWorkspaceConfig()
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
			log.Printf("Found workspace root: %s", current)
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

// loadWorkspaceConfig loads workspace configuration from root buildy.yaml
func (ws *Workspace) loadWorkspaceConfig() (*WorkspaceConfig, error) {
	configFile := filepath.Join(ws.RootDir, "buildy.yaml")

	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("workspace config not found: %s", configFile)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace config: %w", err)
	}

	var rawConfig map[string]any
	if err := yaml.Unmarshal(data, &rawConfig); err != nil {
		return nil, fmt.Errorf("invalid YAML in %s: %w", configFile, err)
	}

	// Extract workspace section
	workspaceSection := map[string]any{}
	if wsSection, ok := rawConfig["workspace"].(map[string]any); ok {
		workspaceSection = wsSection
	}

	// Check for new explicit modules format first
	explicitModules := make(map[string][]ModuleEntry)
	hasExplicitModules := false
	if modules, ok := workspaceSection["modules"].(map[string]any); ok {
		hasExplicitModules = true
		for platform, moduleList := range modules {
			entries := []ModuleEntry{}
			switch v := moduleList.(type) {
			case []any:
				for _, item := range v {
					switch entry := item.(type) {
					case string:
						// Simple string path
						entries = append(entries, ModuleEntry{Path: entry, Config: "buildy.yaml"})
					case map[string]any:
						// Object with path and optional config
						path := ""
						configName := "buildy.yaml"
						if p, ok := entry["path"].(string); ok {
							path = p
						}
						if c, ok := entry["config"].(string); ok {
							configName = c
						}
						if path != "" {
							entries = append(entries, ModuleEntry{Path: path, Config: configName})
						}
					}
				}
			}
			explicitModules[platform] = entries
		}
	}

	// Legacy: Get discovery patterns (only if no explicit modules)
	discoverPatterns := []string{}
	excludePatterns := []string{".buildy_cache/**", "venv/**", ".git/**", "**/.git/**"}
	
	if !hasExplicitModules {
		discoverPatterns = []string{"**/buildy.yaml"}
		if discover, ok := workspaceSection["discover"]; ok {
			switch v := discover.(type) {
			case string:
				discoverPatterns = []string{v}
			case []any:
				discoverPatterns = []string{}
				for _, item := range v {
					if str, ok := item.(string); ok {
						discoverPatterns = append(discoverPatterns, str)
					}
				}
			}
		}

		if exclude, ok := workspaceSection["exclude"]; ok {
			switch v := exclude.(type) {
			case string:
				excludePatterns = append(excludePatterns, v)
			case []any:
				for _, item := range v {
					if str, ok := item.(string); ok {
						excludePatterns = append(excludePatterns, str)
					}
				}
			}
		}
	}

	// Get workspace variables
	variables := map[string]any{}
	if vars, ok := rawConfig["variables"].(map[string]any); ok {
		variables = vars
	}

	// Get package paths
	packagePaths := []string{}
	if paths, ok := workspaceSection["package_paths"]; ok {
		switch v := paths.(type) {
		case string:
			packagePaths = []string{v}
		case []any:
			for _, item := range v {
				if str, ok := item.(string); ok {
					packagePaths = append(packagePaths, str)
				}
			}
		}
	}

	// Get package configurations
	packages := make(map[string]map[string]string)
	if pkgs, ok := workspaceSection["packages"].(map[string]any); ok {
		for pkgName, pkgData := range pkgs {
			pkgVars := make(map[string]string)
			if pkgMap, ok := pkgData.(map[string]any); ok {
				for key, value := range pkgMap {
					if strVal, ok := value.(string); ok {
						pkgVars[key] = strVal
					}
				}
			}
			packages[pkgName] = pkgVars
		}
	}

	// Get dependencies file path
	dependenciesFile := ""
	if deps, ok := rawConfig["dependencies"].(map[string]any); ok {
		if file, ok := deps["file"].(string); ok {
			dependenciesFile = file
		}
	}

	config := &WorkspaceConfig{
		RootDir:          ws.RootDir,
		DiscoverPatterns: discoverPatterns,
		ExcludePatterns:  excludePatterns,
		ExplicitModules:  explicitModules,
		Variables:        variables,
		PackagePaths:     packagePaths,
		Packages:         packages,
		DependenciesFile: dependenciesFile,
		RawConfig:        rawConfig,
	}

	if hasExplicitModules {
		log.Printf("Loaded workspace from %s (explicit modules)", ws.RootDir)
		for platform, modules := range explicitModules {
			log.Printf("  %s: %d modules", platform, len(modules))
		}
	} else {
		log.Printf("Loaded workspace from %s (discovery mode)", ws.RootDir)
		log.Printf("Discovery patterns: %v", discoverPatterns)
		log.Printf("Exclude patterns: %v", excludePatterns)
	}

	return config, nil
}

// DiscoverModules discovers all buildy.yaml module files in workspace
func (ws *Workspace) DiscoverModules(force bool) (map[string]*ModuleInfo, error) {
	return ws.DiscoverModulesForPlatform(force, "")
}

// DiscoverModulesForPlatform discovers modules filtered by platform
func (ws *Workspace) DiscoverModulesForPlatform(force bool, platform string) (map[string]*ModuleInfo, error) {
	if ws.discovered && !force {
		return ws.Modules, nil
	}

	log.Printf("Discovering modules in workspace...")
	ws.Modules = make(map[string]*ModuleInfo)

	// Check if we have explicit modules defined
	if len(ws.Config.ExplicitModules) > 0 {
		return ws.loadExplicitModules(platform)
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
			log.Printf("WARNING: Failed to get relative path for %s: %v", modulePath, err)
			continue
		}

		moduleDir := filepath.Dir(relPath)

		// Root buildy.yaml is module "."
		if modulePath == filepath.Join(ws.RootDir, "buildy.yaml") {
			moduleDir = "."
		}

		moduleInfo, err := ws.loadModule(modulePath, moduleDir)
		if err != nil {
			log.Printf("WARNING: Failed to load module %s: %v", modulePath, err)
			continue
		}

		ws.Modules[moduleDir] = moduleInfo
		log.Printf("Discovered module: %s (%d targets)", moduleDir, len(moduleInfo.Targets))
	}

	ws.discovered = true
	totalTargets := ws.countTotalTargets()
	log.Printf("Discovered %d modules with %d total targets", len(ws.Modules), totalTargets)

	return ws.Modules, nil
}

// loadExplicitModules loads modules from explicit workspace.modules configuration
func (ws *Workspace) loadExplicitModules(platform string) (map[string]*ModuleInfo, error) {
	log.Printf("Loading explicit modules for platform: %s", platform)

	// Collect modules from "common" and platform-specific sections
	modulesToLoad := []ModuleEntry{}

	// Add common modules first
	if commonModules, ok := ws.Config.ExplicitModules["common"]; ok {
		modulesToLoad = append(modulesToLoad, commonModules...)
	}

	// Add platform-specific modules
	if platform != "" {
		if platformModules, ok := ws.Config.ExplicitModules[platform]; ok {
			modulesToLoad = append(modulesToLoad, platformModules...)
		}
	}

	// Load each module
	for _, entry := range modulesToLoad {
		moduleDir := entry.Path
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
		log.Printf("Loaded explicit module: %s (%d targets)", moduleDir, len(moduleInfo.Targets))

		// Recursively load child modules if this module has workspace.modules
		if err := ws.loadChildModules(moduleDir, moduleInfo.Config, platform); err != nil {
			return nil, err
		}
	}

	ws.discovered = true
	totalTargets := ws.countTotalTargets()
	log.Printf("Loaded %d explicit modules with %d total targets", len(ws.Modules), totalTargets)

	return ws.Modules, nil
}

// loadChildModules recursively loads child modules defined in a module's workspace.modules
func (ws *Workspace) loadChildModules(parentDir string, config map[string]any, platform string) error {
	workspaceSection, ok := config["workspace"].(map[string]any)
	if !ok {
		return nil // No workspace section, this is a leaf module
	}

	modulesSection, ok := workspaceSection["modules"].(map[string]any)
	if !ok {
		return nil // No modules section
	}

	// Collect child modules from "common" and platform-specific sections
	childModules := []ModuleEntry{}

	for sectionName, moduleList := range modulesSection {
		// Only process "common" or matching platform
		if sectionName != "common" && sectionName != platform {
			continue
		}

		switch v := moduleList.(type) {
		case []any:
			for _, item := range v {
				switch entry := item.(type) {
				case string:
					childModules = append(childModules, ModuleEntry{Path: entry, Config: "buildy.yaml"})
				case map[string]any:
					path := ""
					configName := "buildy.yaml"
					if p, ok := entry["path"].(string); ok {
						path = p
					}
					if c, ok := entry["config"].(string); ok {
						configName = c
					}
					if path != "" {
						childModules = append(childModules, ModuleEntry{Path: path, Config: configName})
					}
				}
			}
		}
	}

	// Load each child module
	for _, entry := range childModules {
		// Child paths are relative to parent module
		moduleDir := filepath.Join(parentDir, entry.Path)
		configFile := filepath.Join(ws.RootDir, moduleDir, entry.Config)

		// Check if config file exists
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			return fmt.Errorf("child module config not found: %s (referenced in %s/buildy.yaml)", configFile, parentDir)
		}

		// Skip if already loaded
		if _, exists := ws.Modules[moduleDir]; exists {
			continue
		}

		moduleInfo, err := ws.loadModule(configFile, moduleDir)
		if err != nil {
			return fmt.Errorf("failed to load child module %s: %w", moduleDir, err)
		}

		ws.Modules[moduleDir] = moduleInfo
		log.Printf("Loaded child module: %s (%d targets)", moduleDir, len(moduleInfo.Targets))

		// Recursively load grandchild modules
		if err := ws.loadChildModules(moduleDir, moduleInfo.Config, platform); err != nil {
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
		log.Printf("Pattern match error for %s: %v", pattern, err)
		return false
	}
	return matched
}

// loadModule loads a module configuration file
func (ws *Workspace) loadModule(modulePath, relativeDir string) (*ModuleInfo, error) {
	data, err := os.ReadFile(modulePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read module file: %w", err)
	}

	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("invalid YAML in %s: %w", modulePath, err)
	}

	// Extract target names from various sections
	targets := []string{}
	
	// Helper to extract names from a list of target maps
	extractNames := func(items []any) {
		for _, item := range items {
			if itemMap, ok := item.(map[string]any); ok {
				if name, ok := itemMap["name"].(string); ok {
					targets = append(targets, name)
				}
			}
		}
	}

	// New format: targets.libraries and targets.executables
	if targetsSection, ok := config["targets"].(map[string]any); ok {
		if libs, ok := targetsSection["libraries"].([]any); ok {
			extractNames(libs)
		}
		if exes, ok := targetsSection["executables"].([]any); ok {
			extractNames(exes)
		}
		if goMods, ok := targetsSection["go_modules"].([]any); ok {
			extractNames(goMods)
		}
	}

	// Legacy format: 'library' section (singular)
	if libSection, ok := config["library"]; ok {
		switch v := libSection.(type) {
		case map[string]any:
			if name, ok := v["name"].(string); ok {
				targets = append(targets, name)
			}
		case []any:
			extractNames(v)
		}
	}

	// Legacy format: 'executable' section (singular)
	if exeSection, ok := config["executable"]; ok {
		switch v := exeSection.(type) {
		case map[string]any:
			if name, ok := v["name"].(string); ok {
				targets = append(targets, name)
			}
		case []any:
			extractNames(v)
		}
	}

	// Also check legacy 'tasks' sections
	if section, ok := config["tasks"].([]any); ok {
		extractNames(section)
	}

	return &ModuleInfo{
		Path:         modulePath,
		RelativePath: relativeDir,
		Config:       config,
		Targets:      targets,
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

// GetModule gets a module by relative path
func (ws *Workspace) GetModule(relativePath string) *ModuleInfo {
	if !ws.discovered {
		ws.DiscoverModules(false)
	}
	return ws.Modules[relativePath]
}

// ListModules gets list of all module paths
func (ws *Workspace) ListModules() []string {
	if !ws.discovered {
		ws.DiscoverModules(false)
	}

	paths := make([]string, 0, len(ws.Modules))
	for path := range ws.Modules {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// ListAllTargets gets all targets organized by module
func (ws *Workspace) ListAllTargets() map[string][]string {
	if !ws.discovered {
		ws.DiscoverModules(false)
	}

	result := make(map[string][]string)
	for modulePath, module := range ws.Modules {
		result[modulePath] = module.Targets
	}
	return result
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

// CalculateConfigFilesHash calculates a combined hash of all buildy.yaml files and package files
func (ws *Workspace) CalculateConfigFilesHash() (string, error) {
	var filesToHash []string

	// Add root buildy.yaml
	rootConfig := filepath.Join(ws.RootDir, "buildy.yaml")
	if _, err := os.Stat(rootConfig); err == nil {
		filesToHash = append(filesToHash, rootConfig)
	}

	// Add all module buildy.yaml files
	for _, module := range ws.Modules {
		if _, err := os.Stat(module.Path); err == nil {
			filesToHash = append(filesToHash, module.Path)
		}
	}

	// Add dependencies file if referenced
	depsFile := filepath.Join(ws.RootDir, "buildy", "dependencies.yaml")
	if _, err := os.Stat(depsFile); err == nil {
		filesToHash = append(filesToHash, depsFile)
	}

	// Add all package files
	packagesDir := filepath.Join(ws.RootDir, "buildy", "packages")
	if info, err := os.Stat(packagesDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(packagesDir)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && (filepath.Ext(entry.Name()) == ".yaml" || filepath.Ext(entry.Name()) == ".yml") {
					filesToHash = append(filesToHash, filepath.Join(packagesDir, entry.Name()))
				}
			}
		}
	}

	// Sort for deterministic ordering
	sort.Strings(filesToHash)

	// Calculate combined hash
	h := sha256.New()
	for _, file := range filesToHash {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("failed to read %s: %w", file, err)
		}
		// Include filename in hash to detect renames
		h.Write([]byte(file))
		h.Write(data)
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// SaveDiscoveryCache saves workspace discovery results to cache
func (ws *Workspace) SaveDiscoveryCache(cacheDir string) error {
	cacheFile := filepath.Join(cacheDir, "workspace.json")

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Calculate hash of all config files
	configHash, err := ws.CalculateConfigFilesHash()
	if err != nil {
		log.Printf("WARNING: Failed to calculate config hash: %v", err)
		configHash = ""
	}

	// Serialize workspace info
	cacheData := map[string]any{
		"root":        ws.RootDir,
		"config_hash": configHash,
		"config": map[string]any{
			"discover_patterns": ws.Config.DiscoverPatterns,
			"exclude_patterns":  ws.Config.ExcludePatterns,
			"variables":         ws.Config.Variables,
		},
		"modules": map[string]any{},
	}

	modules := make(map[string]any)
	for relPath, module := range ws.Modules {
		modules[relPath] = map[string]any{
			"path":          module.Path,
			"relative_path": module.RelativePath,
			"targets":       module.Targets,
		}
	}
	cacheData["modules"] = modules

	data, err := json.MarshalIndent(cacheData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize cache data: %w", err)
	}

	if err := os.WriteFile(cacheFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	log.Printf("Saved workspace discovery cache to %s", cacheFile)
	return nil
}

// LoadDiscoveryCache loads workspace discovery results from cache
func LoadDiscoveryCache(cacheDir string) (*Workspace, error) {
	cacheFile := filepath.Join(cacheDir, "workspace.json")

	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		return nil, nil // Cache doesn't exist
	}

	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}

	var cacheData map[string]any
	if err := json.Unmarshal(data, &cacheData); err != nil {
		return nil, fmt.Errorf("failed to parse cache file: %w", err)
	}

	rootDir, ok := cacheData["root"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid cache: missing root")
	}

	// Validate that workspace root still exists
	if !IsWorkspaceRoot(rootDir) {
		log.Printf("Cached workspace root no longer valid")
		return nil, nil
	}

	// Create workspace instance
	workspace, err := NewWorkspace(rootDir)
	if err != nil {
		return nil, err
	}

	// Validate config files hash to detect changes in buildy.yaml or package files
	cachedHash, _ := cacheData["config_hash"].(string)
	if cachedHash != "" {
		currentHash, err := workspace.CalculateConfigFilesHash()
		if err != nil {
			log.Printf("Failed to calculate config hash, invalidating cache: %v", err)
			return nil, nil
		}
		if currentHash != cachedHash {
			log.Printf("Config files changed, invalidating workspace cache")
			return nil, nil
		}
	}

	// Restore modules
	modulesData, ok := cacheData["modules"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid cache: missing modules")
	}

	for relPath, moduleDataRaw := range modulesData {
		moduleData, ok := moduleDataRaw.(map[string]any)
		if !ok {
			continue
		}

		modulePath, ok := moduleData["path"].(string)
		if !ok {
			continue
		}

		// Validate module file still exists
		if _, err := os.Stat(modulePath); os.IsNotExist(err) {
			log.Printf("Cached module no longer exists: %s", modulePath)
			return nil, nil
		}

		// Reload module config
		moduleInfo, err := workspace.loadModule(modulePath, relPath)
		if err != nil {
			log.Printf("Failed to reload module config: %v", err)
			return nil, nil
		}

		workspace.Modules[relPath] = moduleInfo
	}

	workspace.discovered = true
	log.Printf("Loaded workspace from cache: %d modules", len(workspace.Modules))
	return workspace, nil
}

// TargetReference represents a reference to a target in the workspace
type TargetReference struct {
	Name       string
	ModulePath string
	ModuleInfo *ModuleInfo
}

// FullName returns the fully qualified target name (module:target)
func (tr *TargetReference) FullName() string {
	return fmt.Sprintf("%s:%s", tr.ModulePath, tr.Name)
}

// TargetRegistry manages all targets in a workspace
type TargetRegistry struct {
	workspace       *Workspace
	targetsByName   map[string][]*TargetReference
	targetsByModule map[string]map[string]*TargetReference
	initialized     bool
}

// NewTargetRegistry creates a new TargetRegistry
func NewTargetRegistry(workspace *Workspace) *TargetRegistry {
	return &TargetRegistry{
		workspace:       workspace,
		targetsByName:   make(map[string][]*TargetReference),
		targetsByModule: make(map[string]map[string]*TargetReference),
		initialized:     false,
	}
}

// Initialize initializes the registry by discovering and indexing all targets
func (tr *TargetRegistry) Initialize() error {
	if tr.initialized {
		return nil
	}

	log.Printf("Initializing target registry...")

	// Discover all modules
	modules, err := tr.workspace.DiscoverModules(false)
	if err != nil {
		return err
	}

	// Index all targets
	for modulePath, moduleInfo := range modules {
		tr.targetsByModule[modulePath] = make(map[string]*TargetReference)

		for _, targetName := range moduleInfo.Targets {
			// Create target reference
			ref := &TargetReference{
				Name:       targetName,
				ModulePath: modulePath,
				ModuleInfo: moduleInfo,
			}

			// Index by module
			tr.targetsByModule[modulePath][targetName] = ref

			// Index by name (for simple name lookup)
			tr.targetsByName[targetName] = append(tr.targetsByName[targetName], ref)
		}
	}

	tr.initialized = true
	log.Printf("Indexed %d unique target names", len(tr.targetsByName))
	return nil
}

// ResolveDependency resolves a dependency string to a target reference
func (tr *TargetRegistry) ResolveDependency(depString, currentModule string) (*TargetReference, error) {
	if !tr.initialized {
		if err := tr.Initialize(); err != nil {
			return nil, err
		}
	}

	// Local reference: ":target"
	if strings.HasPrefix(depString, ":") {
		return tr.resolveLocal(depString[1:], currentModule)
	}

	// Scoped reference: "path:target"
	if strings.Contains(depString, ":") {
		return tr.resolveScoped(depString)
	}

	// Simple name: "target"
	return tr.resolveSimple(depString, currentModule)
}

// resolveLocal resolves local reference (:target) within same module
func (tr *TargetRegistry) resolveLocal(targetName, currentModule string) (*TargetReference, error) {
	if currentModule == "" {
		return nil, fmt.Errorf(
			"local reference ':%s' used but current module unknown. "+
				"Local references can only be used within a module",
			targetName,
		)
	}

	// Look up in current module
	moduleTargets := tr.targetsByModule[currentModule]
	if ref, ok := moduleTargets[targetName]; ok {
		log.Printf("Resolved local reference :%s to %s:%s", targetName, currentModule, targetName)
		return ref, nil
	}

	return nil, fmt.Errorf(
		"target '%s' not found (local reference in module '%s')",
		targetName, currentModule,
	)
}

// resolveScoped resolves scoped reference (path:target)
func (tr *TargetRegistry) resolveScoped(depString string) (*TargetReference, error) {
	if strings.Count(depString, ":") != 1 {
		return nil, fmt.Errorf(
			"invalid scoped reference '%s'. Expected format: 'path:target' (exactly one colon)",
			depString,
		)
	}

	parts := strings.SplitN(depString, ":", 2)
	modulePath := strings.Trim(parts[0], string(filepath.Separator))
	targetName := parts[1]

	// Look up in specified module
	moduleTargets := tr.targetsByModule[modulePath]
	if ref, ok := moduleTargets[targetName]; ok {
		log.Printf("Resolved scoped reference %s", depString)
		return ref, nil
	}

	// Target not found - provide helpful error
	if _, exists := tr.targetsByModule[modulePath]; !exists {
		return nil, fmt.Errorf(
			"target '%s' not found: module '%s' does not exist",
			targetName, modulePath,
		)
	}

	available := []string{}
	for name := range moduleTargets {
		available = append(available, name)
	}
	return nil, fmt.Errorf(
		"target '%s' not found in module '%s' (available targets: %v)",
		targetName, modulePath, available,
	)
}

// resolveSimple resolves simple name reference by searching workspace
func (tr *TargetRegistry) resolveSimple(targetName, currentModule string) (*TargetReference, error) {
	// Get all matches
	matches := tr.targetsByName[targetName]

	if len(matches) == 0 {
		return nil, fmt.Errorf("target '%s' not found", targetName)
	}

	// If only one match, return it
	if len(matches) == 1 {
		log.Printf("Resolved simple reference %s to %s", targetName, matches[0].FullName())
		return matches[0], nil
	}

	// Multiple matches - try to resolve by proximity
	if currentModule != "" {
		// 1. Check current module first
		for _, match := range matches {
			if match.ModulePath == currentModule {
				log.Printf("Resolved simple reference %s to %s (current module)", targetName, match.FullName())
				return match, nil
			}
		}

		// 2. Check parent modules (walk up directory tree)
		currentParts := strings.Split(currentModule, string(filepath.Separator))
		for i := len(currentParts) - 1; i > 0; i-- {
			parentPath := strings.Join(currentParts[:i], string(filepath.Separator))
			for _, match := range matches {
				if match.ModulePath == parentPath {
					log.Printf("Resolved simple reference %s to %s (parent module)", targetName, match.FullName())
					return match, nil
				}
			}
		}
	}

	// Still ambiguous - build error message
	matchList := []string{}
	for _, ref := range matches {
		matchList = append(matchList, "    - "+ref.FullName())
	}
	suggestion := fmt.Sprintf("Use scoped name to disambiguate:\n  dependencies:\n    - %s", matches[0].FullName())

	return nil, fmt.Errorf(
		"ambiguous dependency '%s'\n  Found in:\n%s\n\n  %s",
		targetName, strings.Join(matchList, "\n"), suggestion,
	)
}

// GetTarget gets a specific target by module and name
func (tr *TargetRegistry) GetTarget(modulePath, targetName string) *TargetReference {
	if !tr.initialized {
		tr.Initialize()
	}

	if moduleTargets, ok := tr.targetsByModule[modulePath]; ok {
		return moduleTargets[targetName]
	}
	return nil
}

// ListTargets lists all targets, optionally filtered by module
func (tr *TargetRegistry) ListTargets(modulePath string) []*TargetReference {
	if !tr.initialized {
		tr.Initialize()
	}

	if modulePath != "" {
		if moduleTargets, ok := tr.targetsByModule[modulePath]; ok {
			result := make([]*TargetReference, 0, len(moduleTargets))
			for _, ref := range moduleTargets {
				result = append(result, ref)
			}
			return result
		}
		return []*TargetReference{}
	}

	// Return all targets
	allTargets := []*TargetReference{}
	for _, moduleTargets := range tr.targetsByModule {
		for _, ref := range moduleTargets {
			allTargets = append(allTargets, ref)
		}
	}
	return allTargets
}

// GetTargetNames gets set of all target names in workspace
func (tr *TargetRegistry) GetTargetNames() []string {
	if !tr.initialized {
		tr.Initialize()
	}

	names := make([]string, 0, len(tr.targetsByName))
	for name := range tr.targetsByName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// FindTargetsByName finds all targets with given name
func (tr *TargetRegistry) FindTargetsByName(targetName string) []*TargetReference {
	if !tr.initialized {
		tr.Initialize()
	}

	return tr.targetsByName[targetName]
}
