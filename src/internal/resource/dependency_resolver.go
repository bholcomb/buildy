package resource

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"buildy/pkg/util"

	"gopkg.in/yaml.v3"
)

// DependencyType represents the type of dependency (inferred from fields)
type DependencyType string

const (
	DepTypeSystem DependencyType = "system" // pkg_config, root, or explicit paths
	DepTypeFetch  DependencyType = "fetch"  // git or url
)

// ResolvedDependency represents a fully resolved dependency with all paths and flags
type ResolvedDependency struct {
	Name        string
	Type        DependencyType
	IncludeDirs []string
	LibDirs     []string
	Libs        []string
	Frameworks  []string
	Defines     []string
	Sources     []string
	Path        string // For fetch deps, the resolved path
	Resolved    bool
	SourceURL   string // For URL fetch deps: the original URL
	SourceGit   string // For git fetch deps: the git URL
	SourceRef   string // For git fetch deps: the ref (tag/branch)
}

// BuildConfig represents the build section for fetch dependencies
type BuildConfig struct {
	System   string   `yaml:"system"`   // cmake, meson, make, autoconf, buildy, none
	Override string   `yaml:"override"` // Custom build file to use instead of default
	Args     []string `yaml:"args"`     // Arguments passed to the build system
}

// DependencyConfig represents a single dependency's configuration (parsed from YAML)
// This is the new unified format with platform sections
type DependencyConfig struct {
	Name        string
	Description string
	Version     string

	// Platform-specific sections (common, linux, windows, macos, etc.)
	// Each section can contain any of the fields below
	Sections map[string]DependencySection
}

// DependencySection represents one platform section of a dependency
type DependencySection struct {
	// Source fields (determine type)
	Git       string `yaml:"git"`        // Git repository URL -> fetch type
	URL       string `yaml:"url"`        // Archive URL -> fetch type
	PkgConfig string `yaml:"pkg_config"` // pkg-config name -> system type
	Root      string `yaml:"root"`       // Base path for library -> system type

	// Fetch-specific fields
	Ref      string       `yaml:"ref"`      // Git ref (tag/branch/commit)
	Checksum string       `yaml:"checksum"` // SHA256 for archives
	Extract  bool         `yaml:"extract"`  // Whether to extract archive
	Build    *BuildConfig `yaml:"build"`    // Build configuration

	// Path fields
	IncludeDirs []string `yaml:"include_dirs"`
	LibDirs     []string `yaml:"lib_dirs"`
	Libs        []string `yaml:"libs"`
	Frameworks  []string `yaml:"frameworks"`
	Defines     []string `yaml:"defines"`

	// Execution environment (native or docker) for the dependency's build
	Execution *util.ExecSpec `yaml:"execution"`
}

// DependencyResolver resolves all dependency types
type DependencyResolver struct {
	platform           string
	architecture       string
	toolchain          string
	configuration      string
	varEnv             *util.VariableEnvironment
	cacheDir           string
	workspaceRoot      string
	fetchManager       *FetchManager
	buildSystemManager *BuildSystemManager
	resolved           map[string]*ResolvedDependency
	configs            map[string]*DependencyConfig
	plan               bool // Dry run: report fetches and builds instead of doing them
}

// NewDependencyResolver creates a new DependencyResolver
func NewDependencyResolver(platform, architecture, toolchain, configuration, cacheDir, workspaceRoot string, varEnv *util.VariableEnvironment) *DependencyResolver {
	bsm, err := NewBuildSystemManager()
	if err != nil {
		util.LogWarning("Failed to create BuildSystemManager: %v", err)
	}

	return &DependencyResolver{
		platform:           platform,
		architecture:       architecture,
		toolchain:          toolchain,
		configuration:      configuration,
		varEnv:             varEnv,
		cacheDir:           cacheDir,
		workspaceRoot:      workspaceRoot,
		fetchManager:       NewFetchManager(cacheDir),
		buildSystemManager: bsm,
		resolved:           make(map[string]*ResolvedDependency),
		configs:            make(map[string]*DependencyConfig),
	}
}

// LoadDependencies loads the dependency definitions: file (dependencies.file in buildy.yaml, by
// default buildy_config/dependencies.yaml) and every buildy_config/dependencies/*.yaml. A file
// some dependency names as its build.override is a buildy config for building that dependency,
// not a list of definitions, and is skipped. Every definition is validated.
func (dr *DependencyResolver) LoadDependencies(workspaceRoot, file string) error {
	var files []string
	if file != "" {
		if !filepath.IsAbs(file) {
			file = filepath.Join(workspaceRoot, file)
		}
		if _, err := os.Stat(file); err != nil {
			return fmt.Errorf("dependencies.file: %w", err)
		}
		files = append(files, file)
	} else if defaultFile := filepath.Join(workspaceRoot, "buildy_config", "dependencies.yaml"); fileExists(defaultFile) {
		files = append(files, defaultFile)
	}

	depsDir := filepath.Join(workspaceRoot, "buildy_config", "dependencies")
	if info, err := os.Stat(depsDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(depsDir)
		if err != nil {
			return fmt.Errorf("failed to read dependencies directory: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".yaml") || strings.HasSuffix(entry.Name(), ".yml")) {
				files = append(files, filepath.Join(depsDir, entry.Name()))
			}
		}
	}

	parsed := make(map[string]map[string]any, len(files))
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read dependencies file %s: %w", path, err)
		}
		var raw map[string]any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("failed to parse dependencies file %s: %w", path, err)
		}
		parsed[path] = raw
	}

	// Override configs are named relative to buildy_config/dependencies/
	overrides := map[string]bool{}
	for _, raw := range parsed {
		for _, depData := range raw {
			for _, override := range buildOverrides(depData) {
				overrides[filepath.Join(depsDir, override)] = true
			}
		}
	}

	definedIn := map[string]string{}
	for _, path := range files {
		if overrides[path] {
			continue
		}
		if err := dr.loadDependenciesFile(path, parsed[path], definedIn); err != nil {
			return err
		}
	}

	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// buildOverrides returns the build.override files a dependency definition names, in any section.
func buildOverrides(depData any) []string {
	dep, ok := depData.(map[string]any)
	if !ok {
		return nil
	}
	var overrides []string
	sections := []map[string]any{dep}
	for _, value := range dep {
		if section, ok := value.(map[string]any); ok {
			sections = append(sections, section)
		}
	}
	for _, section := range sections {
		if build, ok := section["build"].(map[string]any); ok {
			if override, ok := build["override"].(string); ok && override != "" {
				overrides = append(overrides, override)
			}
		}
	}
	return overrides
}

// loadDependenciesFile validates and registers the dependencies defined in one file
func (dr *DependencyResolver) loadDependenciesFile(filePath string, rawConfig map[string]any, definedIn map[string]string) error {
	names := make([]string, 0, len(rawConfig))
	for name := range rawConfig {
		names = append(names, name)
	}
	sort.Strings(names)

	// Each top-level key is a dependency name
	for _, depName := range names {
		depMap, ok := rawConfig[depName].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: dependency '%s' must be a mapping", filePath, depName)
		}
		if problems := validateDependency(depMap); len(problems) > 0 {
			return fmt.Errorf("%s: dependency '%s' is not valid:\n  - %s", filePath, depName, strings.Join(problems, "\n  - "))
		}
		if other, ok := definedIn[depName]; ok {
			return fmt.Errorf("dependency '%s' is defined in both %s and %s", depName, other, filePath)
		}
		definedIn[depName] = filePath

		config, err := dr.parseDependencyConfig(depName, depMap)
		if err != nil {
			return fmt.Errorf("%s: dependency '%s': %w", filePath, depName, err)
		}

		dr.configs[depName] = config
	}

	return nil
}

// The keys a dependency definition may have: metadata, platform sections, and the fields a
// section holds (directly, when there are no sections).
var (
	dependencyMetadataKeys = map[string]bool{"description": true, "version": true}
	dependencyFieldKeys    = map[string]bool{
		"git": true, "url": true, "ref": true, "checksum": true, "extract": true,
		"pkg_config": true, "root": true,
		"include_dirs": true, "lib_dirs": true, "libs": true, "frameworks": true, "defines": true,
		"build": true, "execution": true,
	}
	dependencyBuildKeys = map[string]bool{"system": true, "override": true, "args": true}
)

// isDependencySection reports whether key names a platform section: common, a platform, or
// platform-architecture.
func isDependencySection(key string) bool {
	platforms := []string{"common", "linux", "windows", "macos", "darwin", "android", "ios"}
	archs := []string{"x86_64", "arm64", "x86", "arm", "aarch64"}
	for _, p := range platforms {
		if key == p {
			return true
		}
		for _, a := range archs {
			if key == p+"-"+a {
				return true
			}
		}
	}
	return false
}

// validateDependency checks one dependency definition against the schema.
func validateDependency(dep map[string]any) []string {
	var problems []string
	checkFields := func(section map[string]any, where string) {
		for _, key := range sortedMapKeys(section) {
			if !dependencyFieldKeys[key] {
				problems = append(problems, fmt.Sprintf("unknown key '%s%s'", where, key))
			}
		}
		if build, ok := section["build"]; ok {
			buildMap, ok := build.(map[string]any)
			if !ok {
				problems = append(problems, fmt.Sprintf("'%sbuild' must be a mapping", where))
			}
			for _, key := range sortedMapKeys(buildMap) {
				if !dependencyBuildKeys[key] {
					problems = append(problems, fmt.Sprintf("unknown key '%sbuild.%s' (expected system, override, args)", where, key))
				}
			}
		}
		if execution, ok := section["execution"]; ok {
			execMap, ok := execution.(map[string]any)
			if !ok {
				problems = append(problems, fmt.Sprintf("'%sexecution' must be a mapping", where))
			} else if _, err := execSpecFromMap(execMap); err != nil {
				problems = append(problems, fmt.Sprintf("%s%v", where, err))
			}
		}
	}

	direct := map[string]any{}
	for _, key := range sortedMapKeys(dep) {
		value := dep[key]
		switch {
		case dependencyMetadataKeys[key]:
		case isDependencySection(key):
			section, ok := value.(map[string]any)
			if !ok {
				problems = append(problems, fmt.Sprintf("section '%s' must be a mapping", key))
				continue
			}
			checkFields(section, key+".")
		default:
			direct[key] = value
		}
	}
	checkFields(direct, "")
	return problems
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// parseDependencyConfig parses a single dependency configuration
func (dr *DependencyResolver) parseDependencyConfig(name string, data map[string]any) (*DependencyConfig, error) {
	config := &DependencyConfig{
		Name:     name,
		Sections: make(map[string]DependencySection),
	}

	// Extract top-level metadata
	if desc, ok := data["description"].(string); ok {
		config.Description = desc
	}
	if version, ok := data["version"].(string); ok {
		config.Version = version
	}

	// Known platform section names
	platformSections := []string{"common", "linux", "windows", "macos", "darwin", "android", "ios"}
	archSections := []string{"x86_64", "arm64", "x86", "arm", "aarch64"}

	// Build list of all possible section names (including platform-arch combos)
	allSectionNames := make(map[string]bool)
	for _, p := range platformSections {
		allSectionNames[p] = true
		for _, a := range archSections {
			allSectionNames[p+"-"+a] = true
		}
	}

	// Check if we have any platform sections
	hasPlatformSections := false
	for key := range data {
		if allSectionNames[key] {
			hasPlatformSections = true
			break
		}
	}

	// If we have platform sections, extract non-platform fields into a "common" section
	// This allows top-level fields like "defines" to be shared across platforms
	if hasPlatformSections {
		topLevelData := make(map[string]any)
		hasTopLevelFields := false
		for key, value := range data {
			// Skip metadata
			if key == "description" || key == "version" {
				continue
			}
			// Skip platform sections (they have map values and are in allSectionNames)
			if allSectionNames[key] {
				continue
			}
			// Everything else is a top-level dependency field
			topLevelData[key] = value
			hasTopLevelFields = true
		}
		if hasTopLevelFields {
			section, err := dr.parseDependencySection(topLevelData)
			if err != nil {
				return nil, fmt.Errorf("failed to parse top-level fields: %w", err)
			}
			config.Sections["common"] = section
		}
	}

	// Parse each platform section
	for key, value := range data {
		// Skip metadata fields
		if key == "description" || key == "version" {
			continue
		}

		// Check if this is a platform section
		if !allSectionNames[key] {
			// If no platform sections, treat entire data as a "common" section
			if !hasPlatformSections && len(config.Sections) == 0 {
				section, err := dr.parseDependencySection(data)
				if err != nil {
					return nil, err
				}
				config.Sections["common"] = section
				return config, nil
			}
			continue
		}

		sectionData, ok := value.(map[string]any)
		if !ok {
			continue
		}

		section, err := dr.parseDependencySection(sectionData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse section '%s': %w", key, err)
		}

		config.Sections[key] = section
	}

	return config, nil
}

// parseDependencySection parses a single platform section
func (dr *DependencyResolver) parseDependencySection(data map[string]any) (DependencySection, error) {
	section := DependencySection{}

	// Source fields
	if git, ok := data["git"].(string); ok {
		section.Git = git
	}
	if url, ok := data["url"].(string); ok {
		section.URL = url
	}
	if pkgConfig, ok := data["pkg_config"].(string); ok {
		section.PkgConfig = pkgConfig
	}
	if root, ok := data["root"].(string); ok {
		section.Root = root
	}

	// Fetch fields
	if ref, ok := data["ref"].(string); ok {
		section.Ref = ref
	}
	if checksum, ok := data["checksum"].(string); ok {
		section.Checksum = checksum
	}
	if extract, ok := data["extract"].(bool); ok {
		section.Extract = extract
	}

	// Build section
	if buildData, ok := data["build"].(map[string]any); ok {
		build := &BuildConfig{}
		if system, ok := buildData["system"].(string); ok {
			build.System = system
		}
		if override, ok := buildData["override"].(string); ok {
			build.Override = override
		}
		build.Args = util.ExtractStringSlice(buildData["args"])
		section.Build = build
	}

	// Path fields
	section.IncludeDirs = util.ExtractStringSlice(data["include_dirs"])
	section.LibDirs = util.ExtractStringSlice(data["lib_dirs"])
	section.Libs = util.ExtractStringSlice(data["libs"])
	section.Frameworks = util.ExtractStringSlice(data["frameworks"])
	section.Defines = util.ExtractStringSlice(data["defines"])

	// Execution config
	if execData, ok := data["execution"].(map[string]any); ok {
		spec, err := execSpecFromMap(execData)
		if err != nil {
			return section, err
		}
		section.Execution = &spec
	}

	return section, nil
}

// ResolveAll resolves all loaded dependencies: fetches and builds them. With plan (--dry-run), it
// fetches and builds nothing, reports what it would do, and resolves what is already on disk.
func (dr *DependencyResolver) ResolveAll(plan bool) error {
	dr.plan = plan
	names := make([]string, 0, len(dr.configs))
	for name := range dr.configs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		config := dr.configs[name]
		if err := dr.resolveDependency(name, config); err != nil {
			return err
		}
	}
	return nil
}

// resolveDependency resolves a single dependency
func (dr *DependencyResolver) resolveDependency(name string, config *DependencyConfig) error {
	// Merge platform sections: common -> platform -> platform-arch
	merged := dr.mergePlatformSections(config)

	// Create variable environment for this dependency
	depVarEnv := dr.varEnv.CreateChild()
	depVarEnv.SetVariable("name", name, "dependency")
	depVarEnv.SetVariable("version", config.Version, "dependency")
	depVarEnv.SetVariable("platform", dr.platform, "dependency")
	depVarEnv.SetVariable("arch", dr.architecture, "dependency")
	depVarEnv.SetVariable("toolchain", dr.toolchain, "dependency")
	depVarEnv.SetVariable("config", dr.configuration, "dependency")
	depVarEnv.SetVariable("workspace_root", dr.workspaceRoot, "dependency")

	// Infer dependency type from fields
	depType, err := dr.inferDependencyType(merged)
	if err != nil {
		return fmt.Errorf("dependency '%s': %w", name, err)
	}

	resolved := &ResolvedDependency{
		Name:     name,
		Type:     depType,
		Resolved: false,
	}

	switch depType {
	case DepTypeFetch:
		if err := dr.resolveFetchDependency(name, config, merged, depVarEnv, resolved); err != nil {
			return err
		}
	case DepTypeSystem:
		if err := dr.resolveSystemDependency(name, merged, depVarEnv, resolved); err != nil {
			return err
		}
	}

	resolved.Resolved = true
	dr.resolved[name] = resolved
	return nil
}

// mergePlatformSections merges common, platform, and platform-arch sections
func (dr *DependencyResolver) mergePlatformSections(config *DependencyConfig) DependencySection {
	merged := DependencySection{}

	// Helper to merge a section into merged
	mergeSection := func(section DependencySection) {
		if section.Git != "" {
			merged.Git = section.Git
		}
		if section.URL != "" {
			merged.URL = section.URL
		}
		if section.PkgConfig != "" {
			merged.PkgConfig = section.PkgConfig
		}
		if section.Root != "" {
			merged.Root = section.Root
		}
		if section.Ref != "" {
			merged.Ref = section.Ref
		}
		if section.Checksum != "" {
			merged.Checksum = section.Checksum
		}
		if section.Extract {
			merged.Extract = section.Extract
		}
		if section.Build != nil {
			merged.Build = section.Build
		}
		if section.Execution != nil {
			merged.Execution = section.Execution
		}

		// Append list fields
		merged.IncludeDirs = append(merged.IncludeDirs, section.IncludeDirs...)
		merged.LibDirs = append(merged.LibDirs, section.LibDirs...)
		merged.Libs = append(merged.Libs, section.Libs...)
		merged.Frameworks = append(merged.Frameworks, section.Frameworks...)
		merged.Defines = append(merged.Defines, section.Defines...)
	}

	// Apply in order: common -> platform -> platform-arch
	if common, ok := config.Sections["common"]; ok {
		mergeSection(common)
	}

	// Normalize platform name (darwin -> macos)
	platform := dr.platform
	if platform == "darwin" {
		platform = "macos"
	}

	if platformSection, ok := config.Sections[platform]; ok {
		mergeSection(platformSection)
	}
	// Also check darwin if we're on macos
	if platform == "macos" {
		if darwinSection, ok := config.Sections["darwin"]; ok {
			mergeSection(darwinSection)
		}
	}

	// Platform-arch combination
	platformArch := platform + "-" + dr.architecture
	if platformArchSection, ok := config.Sections[platformArch]; ok {
		mergeSection(platformArchSection)
	}

	return merged
}

// inferDependencyType determines the dependency type from its fields
func (dr *DependencyResolver) inferDependencyType(section DependencySection) (DependencyType, error) {
	hasFetch := section.Git != "" || section.URL != ""
	hasSystemSource := section.PkgConfig != "" || section.Root != ""

	// Fetch takes priority - include_dirs/libs are allowed with fetch to specify build output locations
	if hasFetch && hasSystemSource {
		return "", fmt.Errorf("conflicting fields: cannot have both fetch (git/url) and system source (pkg_config/root) fields")
	}

	if hasFetch {
		return DepTypeFetch, nil
	}

	// Default to system for explicit paths or pkg_config
	return DepTypeSystem, nil
}

// resolveSystemDependency resolves a system dependency
func (dr *DependencyResolver) resolveSystemDependency(name string, section DependencySection, varEnv *util.VariableEnvironment, resolved *ResolvedDependency) error {
	// If pkg_config is specified, use it
	if section.PkgConfig != "" {
		// Check if pkg-config is available
		if _, err := exec.LookPath("pkg-config"); err != nil {
			return fmt.Errorf("pkg_config specified for '%s' but pkg-config is not available", name)
		}

		if err := dr.resolvePkgConfig(section.PkgConfig, resolved); err != nil {
			return fmt.Errorf("pkg-config failed for '%s': %w", name, err)
		}

		// Still apply any additional defines from config
		for _, def := range section.Defines {
			resolvedDef := dr.resolveVariables(def, varEnv)
			resolved.Defines = append(resolved.Defines, resolvedDef)
		}

		return nil
	}

	// Resolve root if specified
	if section.Root != "" {
		resolvedRoot := dr.resolveVariables(section.Root, varEnv)

		// Check if root path exists
		if _, err := os.Stat(resolvedRoot); os.IsNotExist(err) {
			return fmt.Errorf("root path does not exist for '%s': %s", name, resolvedRoot)
		}

		// Set root variable for path resolution
		varEnv.SetVariable("root", resolvedRoot, "dependency")
	}

	// Resolve include dirs
	for _, incDir := range section.IncludeDirs {
		resolvedInc := dr.resolveVariables(incDir, varEnv)
		resolved.IncludeDirs = append(resolved.IncludeDirs, resolvedInc)
	}

	// Resolve lib dirs
	for _, libDir := range section.LibDirs {
		resolvedLib := dr.resolveVariables(libDir, varEnv)
		resolved.LibDirs = append(resolved.LibDirs, resolvedLib)
	}

	// Set libs (no path resolution needed)
	resolved.Libs = append(resolved.Libs, section.Libs...)

	// Set frameworks (macOS)
	resolved.Frameworks = append(resolved.Frameworks, section.Frameworks...)

	// Resolve defines
	for _, def := range section.Defines {
		resolvedDef := dr.resolveVariables(def, varEnv)
		resolved.Defines = append(resolved.Defines, resolvedDef)
	}

	return nil
}

// resolveFetchDependency resolves a fetch dependency
func (dr *DependencyResolver) resolveFetchDependency(name string, config *DependencyConfig, section DependencySection, varEnv *util.VariableEnvironment, resolved *ResolvedDependency) error {
	// Determine destination
	dest := filepath.Join(dr.cacheDir, "deps", name)

	// Fetch the dependency
	var err error
	source := section.URL
	if section.Git != "" {
		source = section.Git
		if section.Ref != "" {
			source += " @ " + section.Ref
		}
	}
	if dr.plan {
		if fileExists(dest) {
			util.LogProgress("Dry run: would fetch %s from %s (using what is in %s)", name, source, dest)
		} else {
			util.LogProgress("Dry run: would fetch %s from %s into %s (not on disk yet: its include and lib dirs are missing from this plan)", name, source, dest)
		}
	} else if section.Git != "" {
		err = dr.fetchManager.FetchGit(section.Git, section.Ref, dest)
	} else if section.URL != "" {
		err = dr.fetchManager.FetchURL(section.URL, section.Checksum, dest)
	}
	resolved.SourceGit = section.Git
	resolved.SourceRef = section.Ref
	resolved.SourceURL = section.URL

	if err != nil {
		return fmt.Errorf("failed to fetch '%s': %w", name, err)
	}

	resolved.Path = dest

	// Set dep_dir variable for path resolution
	varEnv.SetVariable("dep_dir", dest, "dependency")

	// Handle build configuration
	if section.Build != nil && section.Build.System != "" && section.Build.System != "none" {
		// Build using the specified build system (cmake, make, meson, buildy, etc.)
		if err := dr.buildFetchedDep(name, section, dest, resolved); err != nil {
			return fmt.Errorf("failed to build '%s': %w", name, err)
		}
	}

	// Resolve include dirs
	for _, incDir := range section.IncludeDirs {
		resolvedInc := dr.resolveVariables(incDir, varEnv)
		resolved.IncludeDirs = append(resolved.IncludeDirs, resolvedInc)
	}

	// Resolve lib dirs
	for _, libDir := range section.LibDirs {
		resolvedLib := dr.resolveVariables(libDir, varEnv)
		resolved.LibDirs = append(resolved.LibDirs, resolvedLib)
	}

	// Set libs
	resolved.Libs = append(resolved.Libs, section.Libs...)

	// Resolve defines
	for _, def := range section.Defines {
		resolvedDef := dr.resolveVariables(def, varEnv)
		resolved.Defines = append(resolved.Defines, resolvedDef)
	}

	return nil
}

// buildFetchedDep builds a fetched dependency using its build system
// Builds both debug and release configurations so users can link against either
func (dr *DependencyResolver) buildFetchedDep(name string, section DependencySection, dest string, resolved *ResolvedDependency) error {
	if dr.buildSystemManager == nil {
		return fmt.Errorf("BuildSystemManager not initialized")
	}

	buildSystemName := section.Build.System

	if dr.plan && !fileExists(dest) {
		util.LogProgress("Dry run: would build %s (debug, release) with %s", name, buildSystemName)
		return nil
	}

	var bsConfig *BuildSystemConfig
	if buildSystemName == "auto" || buildSystemName == "" {
		bsConfig = dr.buildSystemManager.Detect(dest)
		if bsConfig == nil {
			return fmt.Errorf("could not detect the build system of %s in %s; set build.system", name, dest)
		}
		util.LogInfo("Auto-detected build system: %s for %s", bsConfig.Name, name)
	} else {
		bsConfig = dr.buildSystemManager.GetBuildSystem(buildSystemName)
		if bsConfig == nil {
			return fmt.Errorf("unknown build system '%s' for %s", buildSystemName, name)
		}
	}

	if dr.plan {
		util.LogProgress("Dry run: would build %s (debug, release) with %s", name, bsConfig.Name)
		includeDirs, libDirs := dr.buildSystemManager.GetOutputPaths(bsConfig, dest,
			filepath.Join(dest, "_build", "debug"), filepath.Join(dest, "_install", "debug"))
		resolved.IncludeDirs = append(resolved.IncludeDirs, includeDirs...)
		resolved.LibDirs = append(resolved.LibDirs, libDirs...)
		return nil
	}

	util.LogProgress("=== Building dependency: %s (using %s) ===", name, bsConfig.Name)

	// Determine execution environment: natively, or in Docker with the workspace and the cache
	// directory mounted at their host paths
	execSpec := util.ExecSpec{}
	if section.Execution != nil {
		execSpec = *section.Execution
	}
	mounts := dr.dockerMounts()

	// Build both debug and release configurations
	configurations := []string{"debug", "release"}

	for _, config := range configurations {
		// Set up per-configuration directories
		buildDir := filepath.Join(dest, "_build", config)
		installDir := filepath.Join(dest, "_install", config)
		os.MkdirAll(buildDir, 0755)
		os.MkdirAll(installDir, 0755)

		// Build context with additional variables
		context := map[string]string{
			"config":   config,
			"platform": dr.platform,
			"arch":     dr.architecture,
		}

		// For buildy build system, add the executable path and handle override config
		if bsConfig.Name == "buildy" {
			buildyExe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to find buildy executable: %w", err)
			}
			context["buildy_exe"] = buildyExe

			// If override config is specified, pass it as the config file argument
			// The source_dir remains the fetched dependency directory
			if section.Build.Override != "" {
				overrideConfig := filepath.Join(dr.workspaceRoot, "buildy_config", "dependencies", section.Build.Override)
				context["config_file"] = overrideConfig
			} else {
				// No override - use the default buildy.yaml in the source directory
				context["config_file"] = ""
			}
		}

		// Determine phases - buildy only has "build", others have "configure" and "build"
		var phases []string
		if bsConfig.Name == "buildy" {
			phases = []string{"build"}
		} else {
			phases = []string{"configure", "build"}
		}

		// Build configuration-specific args
		extraArgs := append([]string{}, section.Build.Args...)
		if bsConfig.Name == "cmake" {
			// Override CMAKE_BUILD_TYPE for each configuration
			extraArgs = append(extraArgs, fmt.Sprintf("-DCMAKE_BUILD_TYPE=%s", strings.Title(config)))
		}

		util.LogInfo("Building %s (%s) with %s", name, config, bsConfig.Name)
		if err := dr.buildSystemManager.ExecuteWithContext(bsConfig, dest, buildDir, installDir, execSpec, mounts, phases, extraArgs, context); err != nil {
			return fmt.Errorf("build failed for %s (%s): %w", name, config, err)
		}

		util.LogInfo("Build completed for %s (%s)", name, config)
	}

	// Get output paths from the debug build (headers are typically the same)
	// Library paths will be per-configuration in the dependency config
	debugBuildDir := filepath.Join(dest, "_build", "debug")
	debugInstallDir := filepath.Join(dest, "_install", "debug")
	includeDirs, libDirs := dr.buildSystemManager.GetOutputPaths(bsConfig, dest, debugBuildDir, debugInstallDir)
	resolved.IncludeDirs = append(resolved.IncludeDirs, includeDirs...)
	resolved.LibDirs = append(resolved.LibDirs, libDirs...)

	util.LogProgress("=== Dependency %s built successfully ===", name)
	return nil
}

// resolvePkgConfig uses pkg-config to get include dirs, lib dirs, and libs
func (dr *DependencyResolver) resolvePkgConfig(pkgName string, dep *ResolvedDependency) error {
	// Get cflags
	cflagsCmd := exec.Command("pkg-config", "--cflags", pkgName)
	cflagsOut, err := cflagsCmd.Output()
	if err != nil {
		return fmt.Errorf("pkg-config --cflags failed: %w", err)
	}

	// Parse cflags for include dirs
	cflags := strings.Fields(string(cflagsOut))
	for _, flag := range cflags {
		if strings.HasPrefix(flag, "-I") {
			dep.IncludeDirs = append(dep.IncludeDirs, strings.TrimPrefix(flag, "-I"))
		} else if strings.HasPrefix(flag, "-D") {
			dep.Defines = append(dep.Defines, strings.TrimPrefix(flag, "-D"))
		}
	}

	// Get libs
	libsCmd := exec.Command("pkg-config", "--libs", pkgName)
	libsOut, err := libsCmd.Output()
	if err != nil {
		return fmt.Errorf("pkg-config --libs failed: %w", err)
	}

	// Parse libs for lib dirs and library names
	libs := strings.Fields(string(libsOut))
	for _, flag := range libs {
		if strings.HasPrefix(flag, "-L") {
			dep.LibDirs = append(dep.LibDirs, strings.TrimPrefix(flag, "-L"))
		} else if strings.HasPrefix(flag, "-l") {
			dep.Libs = append(dep.Libs, strings.TrimPrefix(flag, "-l"))
		}
	}

	return nil
}

// resolveVariables resolves variables in a string
func (dr *DependencyResolver) resolveVariables(text string, varEnv *util.VariableEnvironment) string {
	errors := []string{}
	resolved := varEnv.ResolveString(text, &errors, 10)

	if len(errors) > 0 {
		// Error on unresolved variables
		util.LogFatal("Variable resolution failed: %v in '%s'", errors, text)
	}

	return resolved
}

// dockerMounts returns the directories a Docker build mounts at their host paths: the workspace
// root, and the cache directory when it lies outside it.
func (dr *DependencyResolver) dockerMounts() []string {
	return DockerMounts(dr.workspaceRoot, dr.cacheDir)
}

// DockerMounts returns the workspace root, and the cache directory when it lies outside it.
func DockerMounts(workspaceRoot, cacheDir string) []string {
	mounts := []string{workspaceRoot}
	if rel, err := filepath.Rel(workspaceRoot, cacheDir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		mounts = append(mounts, cacheDir)
	}
	return mounts
}

// GetDependency returns a resolved dependency by name
func (dr *DependencyResolver) GetDependency(name string) *ResolvedDependency {
	return dr.resolved[name]
}

// GetAllDependencies returns all resolved dependencies
func (dr *DependencyResolver) GetAllDependencies() map[string]*ResolvedDependency {
	return dr.resolved
}

// ResolveDependencies resolves a list of dependency names and returns merged settings
// This is used by TaskGenerator to get the combined include_dirs, libs, etc.
func (dr *DependencyResolver) ResolveDependencies(depNames []string) (*ResolvedDependency, error) {
	merged := &ResolvedDependency{
		Name:        "merged",
		IncludeDirs: []string{},
		LibDirs:     []string{},
		Libs:        []string{},
		Frameworks:  []string{},
		Defines:     []string{},
		Sources:     []string{},
	}

	for _, name := range depNames {
		dep := dr.resolved[name]
		if dep == nil {
			return nil, fmt.Errorf("dependency '%s' not found", name)
		}

		merged.IncludeDirs = append(merged.IncludeDirs, dep.IncludeDirs...)
		merged.LibDirs = append(merged.LibDirs, dep.LibDirs...)
		merged.Libs = append(merged.Libs, dep.Libs...)
		merged.Frameworks = append(merged.Frameworks, dep.Frameworks...)
		merged.Defines = append(merged.Defines, dep.Defines...)
		merged.Sources = append(merged.Sources, dep.Sources...)
	}

	return merged, nil
}

// GetDependencySearchPaths returns the search paths for dependency files
func GetDependencySearchPaths(workspaceRoot string) []string {
	paths := []string{}

	// 1. Workspace buildy_config/dependencies
	if workspaceRoot != "" {
		paths = append(paths, filepath.Join(workspaceRoot, "buildy_config", "dependencies"))
		paths = append(paths, filepath.Join(workspaceRoot, "buildy_config"))
	}

	// 2. System locations
	paths = append(paths, getSystemDependencyDirs()...)

	return paths
}

// getSystemDependencyDirs returns platform-specific system dependency directories
func getSystemDependencyDirs() []string {
	switch runtime.GOOS {
	case "linux":
		return []string{
			"/usr/local/lib/buildy/dependencies",
			"/usr/lib/buildy/dependencies",
			"/opt/buildy/dependencies",
		}
	case "darwin":
		return []string{
			"/usr/local/lib/buildy/dependencies",
			"/opt/buildy/dependencies",
		}
	case "windows":
		return []string{
			filepath.Join(os.Getenv("ProgramFiles"), "buildy", "dependencies"),
			"C:\\buildy\\dependencies",
		}
	default:
		return []string{}
	}
}

// execSpecFromMap reads an execution section: type, image, volumes, user, env.
func execSpecFromMap(config map[string]any) (util.ExecSpec, error) {
	spec := util.ExecSpec{}
	for key, value := range config {
		switch key {
		case "type":
			spec.Type, _ = value.(string)
		case "image":
			spec.Image, _ = value.(string)
		case "user":
			spec.User, _ = value.(string)
		case "volumes":
			spec.Volumes = util.ExtractStringList(value)
		case "env":
			env, ok := value.(map[string]any)
			if !ok {
				return spec, fmt.Errorf("execution.env must be a map")
			}
			spec.Env = map[string]string{}
			for k, v := range env {
				spec.Env[k] = fmt.Sprint(v)
			}
		case "working_dir":
			return spec, fmt.Errorf("execution.working_dir is not supported: commands run in the workspace root, which Docker mounts at its host path")
		default:
			return spec, fmt.Errorf("unknown execution key %q (expected type, image, volumes, user, env)", key)
		}
	}
	if err := spec.Validate(); err != nil {
		return spec, fmt.Errorf("execution: %w", err)
	}
	return spec, nil
}
