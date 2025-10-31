package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	RootDir          string         `json:"root"`
	DiscoverPatterns []string       `json:"discover_patterns"`
	ExcludePatterns  []string       `json:"exclude_patterns"`
	Variables        map[string]any `json:"variables"`
	RawConfig        map[string]any `json:"-"`
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
	if ws, ok := rawConfig["workspace"].(map[string]any); ok {
		workspaceSection = ws
	}

	// Get discovery patterns
	discoverPatterns := []string{"**/buildy.yaml"}
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

	// Get exclude patterns
	excludePatterns := []string{".buildy_cache/**", "venv/**", ".git/**", "**/.git/**"}
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

	// Get workspace variables
	variables := map[string]any{}
	if vars, ok := rawConfig["variables"].(map[string]any); ok {
		variables = vars
	}

	config := &WorkspaceConfig{
		RootDir:          ws.RootDir,
		DiscoverPatterns: discoverPatterns,
		ExcludePatterns:  excludePatterns,
		Variables:        variables,
		RawConfig:        rawConfig,
	}

	log.Printf("Loaded workspace from %s", ws.RootDir)
	log.Printf("Discovery patterns: %v", discoverPatterns)
	log.Printf("Exclude patterns: %v", excludePatterns)

	return config, nil
}

// DiscoverModules discovers all buildy.yaml module files in workspace
func (ws *Workspace) DiscoverModules(force bool) (map[string]*ModuleInfo, error) {
	if ws.discovered && !force {
		return ws.Modules, nil
	}

	log.Printf("Discovering modules in workspace...")
	ws.Modules = make(map[string]*ModuleInfo)

	// Find all buildy.yaml files
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
				pattern := strings.TrimPrefix(excludePattern, "**/")
				if matchPattern(relPath, pattern) || matchPattern(filepath.Join(relPath, ""), pattern) {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Check for buildy.yaml
		if filepath.Base(path) == "buildy.yaml" {
			// Check if it matches any discovery pattern
			for _, discoverPattern := range ws.Config.DiscoverPatterns {
				pattern := strings.TrimPrefix(discoverPattern, "**/")
				if matchPattern(relPath, pattern) || matchPattern(relPath, discoverPattern) {
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
	matched, err := filepath.Match(pattern, path)
	if err != nil {
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

	// Extract target names from library and executable sections
	targets := []string{}

	// Check for 'library' section (singular)
	if libSection, ok := config["library"]; ok {
		switch v := libSection.(type) {
		case map[string]any:
			if name, ok := v["name"].(string); ok {
				targets = append(targets, name)
			}
		case []any:
			for _, item := range v {
				if libMap, ok := item.(map[string]any); ok {
					if name, ok := libMap["name"].(string); ok {
						targets = append(targets, name)
					}
				}
			}
		}
	}

	// Check for 'executable' section (singular)
	if exeSection, ok := config["executable"]; ok {
		switch v := exeSection.(type) {
		case map[string]any:
			if name, ok := v["name"].(string); ok {
				targets = append(targets, name)
			}
		case []any:
			for _, item := range v {
				if exeMap, ok := item.(map[string]any); ok {
					if name, ok := exeMap["name"].(string); ok {
						targets = append(targets, name)
					}
				}
			}
		}
	}

	// Also check legacy 'tasks' and 'targets' sections
	for _, taskSection := range []string{"tasks", "targets"} {
		if section, ok := config[taskSection].([]any); ok {
			for _, item := range section {
				if taskMap, ok := item.(map[string]any); ok {
					if name, ok := taskMap["name"].(string); ok {
						targets = append(targets, name)
					}
				}
			}
		}
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

// SaveDiscoveryCache saves workspace discovery results to cache
func (ws *Workspace) SaveDiscoveryCache(cacheDir string) error {
	cacheFile := filepath.Join(cacheDir, "workspace.json")

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Serialize workspace info
	cacheData := map[string]any{
		"root": ws.RootDir,
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
	workspace        *Workspace
	targetsByName    map[string][]*TargetReference
	targetsByModule  map[string]map[string]*TargetReference
	initialized      bool
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

