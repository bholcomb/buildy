package resource

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	ConfigFile  string // For fetch deps: buildy config file to use
	NeedsBuildy bool   // True if this fetch dep should be built via buildy config
}

// BuildConfig represents the build section for fetch dependencies
type BuildConfig struct {
	System   string   `yaml:"system"`   // cmake, meson, make, autoconf, buildy, none
	Override string   `yaml:"override"` // Custom build file to use instead of default
	Args     []string `yaml:"args"`     // Arguments passed to the build system
}

// ExecutionConfig represents execution environment for dependency builds
type ExecutionConfig struct {
	Type       string   `yaml:"type"`        // native, docker
	Image      string   `yaml:"image"`       // Docker image
	Volumes    []string `yaml:"volumes"`     // Docker volumes
	WorkingDir string   `yaml:"working_dir"` // Working directory in container
	User       string   `yaml:"user"`        // User in container
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

	// Execution environment
	Execution *ExecutionConfig `yaml:"execution"`
}

// DependencyResolver resolves all dependency types
type DependencyResolver struct {
	platform           string
	architecture       string
	toolchain          string
	varEnv             *util.VariableEnvironment
	cacheDir           string
	workspaceRoot      string
	fetchManager       *FetchManager
	buildSystemManager *BuildSystemManager
	defaultExecEnv     *util.ExecutionEnvironment
	resolved           map[string]*ResolvedDependency
	configs            map[string]*DependencyConfig
}

// NewDependencyResolver creates a new DependencyResolver
func NewDependencyResolver(platform, architecture, toolchain, cacheDir, workspaceRoot string, varEnv *util.VariableEnvironment) *DependencyResolver {
	bsm, err := NewBuildSystemManager()
	if err != nil {
		util.LogWarning("Failed to create BuildSystemManager: %v", err)
	}

	return &DependencyResolver{
		platform:           platform,
		architecture:       architecture,
		toolchain:          toolchain,
		varEnv:             varEnv,
		cacheDir:           cacheDir,
		workspaceRoot:      workspaceRoot,
		fetchManager:       NewFetchManager(cacheDir),
		buildSystemManager: bsm,
		defaultExecEnv:     util.NewNativeExecution(),
		resolved:           make(map[string]*ResolvedDependency),
		configs:            make(map[string]*DependencyConfig),
	}
}

// SetDefaultExecution sets the default execution environment for dependency builds
func (dr *DependencyResolver) SetDefaultExecution(execEnv *util.ExecutionEnvironment) {
	dr.defaultExecEnv = execEnv
}

// LoadDependencies loads dependencies from the workspace
func (dr *DependencyResolver) LoadDependencies(workspaceRoot string) error {
	// Check for dependencies.yaml file
	depsFile := filepath.Join(workspaceRoot, "buildy_config", "dependencies.yaml")
	if _, err := os.Stat(depsFile); err == nil {
		if err := dr.loadDependenciesFile(depsFile); err != nil {
			return err
		}
	}

	// Check for dependencies/*.yaml directory
	depsDir := filepath.Join(workspaceRoot, "buildy_config", "dependencies")
	if info, err := os.Stat(depsDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(depsDir)
		if err != nil {
			return fmt.Errorf("failed to read dependencies directory: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".yaml") || strings.HasSuffix(entry.Name(), ".yml")) {
				filePath := filepath.Join(depsDir, entry.Name())
				if err := dr.loadDependenciesFile(filePath); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// loadDependenciesFile loads dependencies from a single YAML file
func (dr *DependencyResolver) loadDependenciesFile(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read dependencies file %s: %w", filePath, err)
	}

	var rawConfig map[string]any
	if err := yaml.Unmarshal(data, &rawConfig); err != nil {
		return fmt.Errorf("failed to parse dependencies file %s: %w", filePath, err)
	}

	// Each top-level key is a dependency name
	for depName, depData := range rawConfig {
		depMap, ok := depData.(map[string]any)
		if !ok {
			continue
		}

		config, err := dr.parseDependencyConfig(depName, depMap)
		if err != nil {
			return fmt.Errorf("failed to parse dependency '%s': %w", depName, err)
		}

		dr.configs[depName] = config
	}

	return nil
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
		exec := &ExecutionConfig{}
		if t, ok := execData["type"].(string); ok {
			exec.Type = t
		}
		if image, ok := execData["image"].(string); ok {
			exec.Image = image
		}
		exec.Volumes = util.ExtractStringSlice(execData["volumes"])
		if wd, ok := execData["working_dir"].(string); ok {
			exec.WorkingDir = wd
		}
		if user, ok := execData["user"].(string); ok {
			exec.User = user
		}
		section.Execution = exec
	}

	return section, nil
}

// ResolveAll resolves all loaded dependencies
func (dr *DependencyResolver) ResolveAll() error {
	for name, config := range dr.configs {
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
	if section.Git != "" {
		err = dr.fetchManager.FetchGit(section.Git, section.Ref, dest)
	} else if section.URL != "" {
		err = dr.fetchManager.FetchURL(section.URL, section.Checksum, dest)
	}

	if err != nil {
		return fmt.Errorf("failed to fetch '%s': %w", name, err)
	}

	resolved.Path = dest

	// Set dep_dir variable for path resolution
	varEnv.SetVariable("dep_dir", dest, "dependency")

	// Handle build configuration
	if section.Build != nil {
		if section.Build.System == "buildy" {
			// Will be built via buildy - store config for later
			if section.Build.Override != "" {
				// Override file is relative to the dependencies config directory
				configPath := filepath.Join(dr.workspaceRoot, "buildy_config", "dependencies", section.Build.Override)
				resolved.ConfigFile = configPath
			} else {
				// Look for buildy.yaml in the fetched source
				resolved.ConfigFile = filepath.Join(dest, "buildy.yaml")
			}
			resolved.NeedsBuildy = true
			util.LogInfo("Fetch dependency '%s' will be built using buildy config: %s", name, resolved.ConfigFile)
		} else if section.Build.System != "" && section.Build.System != "none" {
			// Build using external build system
			if err := dr.buildFetchedDep(name, section, dest, resolved); err != nil {
				return fmt.Errorf("failed to build '%s': %w", name, err)
			}
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
func (dr *DependencyResolver) buildFetchedDep(name string, section DependencySection, dest string, resolved *ResolvedDependency) error {
	if dr.buildSystemManager == nil {
		return fmt.Errorf("BuildSystemManager not initialized")
	}

	buildSystemName := section.Build.System

	var bsConfig *BuildSystemConfig
	if buildSystemName == "auto" || buildSystemName == "" {
		bsConfig = dr.buildSystemManager.Detect(dest)
		if bsConfig == nil {
			util.LogWarning("Could not detect build system for %s, skipping build", name)
			return nil
		}
		util.LogInfo("Auto-detected build system: %s for %s", bsConfig.Name, name)
	} else {
		bsConfig = dr.buildSystemManager.GetBuildSystem(buildSystemName)
		if bsConfig == nil {
			return fmt.Errorf("unknown build system '%s' for %s", buildSystemName, name)
		}
	}

	// Set up directories
	buildDir := filepath.Join(dest, "_build")
	installDir := filepath.Join(dest, "_install")
	os.MkdirAll(buildDir, 0755)
	os.MkdirAll(installDir, 0755)

	// Determine execution environment
	execEnv := dr.defaultExecEnv
	if section.Execution != nil && section.Execution.Type != "" {
		execEnv = dr.createExecutionEnv(section.Execution)
	}

	// Default phases
	phases := []string{"configure", "build"}

	// Execute the build
	util.LogInfo("Building %s with %s (phases: %v)", name, bsConfig.Name, phases)
	if err := dr.buildSystemManager.Execute(bsConfig, dest, buildDir, installDir, execEnv, phases, section.Build.Args); err != nil {
		return fmt.Errorf("build failed for %s: %w", name, err)
	}

	// Get output paths from build system config
	includeDirs, libDirs := dr.buildSystemManager.GetOutputPaths(bsConfig, dest, buildDir, installDir)
	resolved.IncludeDirs = append(resolved.IncludeDirs, includeDirs...)
	resolved.LibDirs = append(resolved.LibDirs, libDirs...)

	util.LogInfo("Build completed for %s", name)
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

// createExecutionEnv creates an ExecutionEnvironment from config
func (dr *DependencyResolver) createExecutionEnv(config *ExecutionConfig) *util.ExecutionEnvironment {
	switch config.Type {
	case "docker":
		dockerConfig := map[string]any{
			"image": config.Image,
		}
		if len(config.Volumes) > 0 {
			dockerConfig["volumes"] = config.Volumes
		}
		if config.WorkingDir != "" {
			dockerConfig["working_dir"] = config.WorkingDir
		}
		if config.User != "" {
			dockerConfig["user"] = config.User
		}
		return util.NewDockerExecution(dockerConfig)
	default:
		return util.NewNativeExecution()
	}
}

// GetDependency returns a resolved dependency by name
func (dr *DependencyResolver) GetDependency(name string) *ResolvedDependency {
	return dr.resolved[name]
}

// GetAllDependencies returns all resolved dependencies
func (dr *DependencyResolver) GetAllDependencies() map[string]*ResolvedDependency {
	return dr.resolved
}

// GetBuildyDependencies returns fetch dependencies that need to be built via buildy config
func (dr *DependencyResolver) GetBuildyDependencies() map[string]struct{ SourcePath, ConfigFile string } {
	result := make(map[string]struct{ SourcePath, ConfigFile string })
	for name, dep := range dr.resolved {
		if dep.NeedsBuildy && dep.ConfigFile != "" {
			result[name] = struct{ SourcePath, ConfigFile string }{
				SourcePath: dep.Path,
				ConfigFile: dep.ConfigFile,
			}
		}
	}
	return result
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
