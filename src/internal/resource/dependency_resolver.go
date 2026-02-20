package resource

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"buildy/pkg/util"

	"gopkg.in/yaml.v3"
)

// DependencyType represents the type of dependency
type DependencyType string

const (
	DepTypeSystem  DependencyType = "system"
	DepTypePath    DependencyType = "path"
	DepTypeFetch   DependencyType = "fetch"
	DepTypePackage DependencyType = "package"
)

// Dependency represents a resolved dependency with all its paths and flags
type Dependency struct {
	Name        string
	Type        DependencyType
	IncludeDirs []string
	LibDirs     []string
	Libs        []string
	Frameworks  []string
	Defines     []string
	Sources     []string
	Path        string // For path/fetch deps, the resolved path
	Resolved    bool
	Warning     string // Reproducibility warning for system deps
	ConfigFile  string // For fetch deps: buildy config file to use instead of build_system
	NeedsBuildy bool   // True if this fetch dep should be built via buildy config
}

// SystemDependencyConfig represents a system dependency from the config
type SystemDependencyConfig struct {
	Name       string   `yaml:"name"`
	PkgConfig  string   `yaml:"pkg_config"`
	Libs       []string `yaml:"libs"`
	Frameworks []string `yaml:"frameworks"`
}

// PathDependencyConfig represents a path-based dependency from the config
type PathDependencyConfig struct {
	Name        string   `yaml:"name"`
	Path        string   `yaml:"path"`
	IncludeDirs []string `yaml:"include_dirs"`
	LibDirs     []string `yaml:"lib_dirs"`
	Libs        []string `yaml:"libs"`
}

// ExecutionConfig represents execution environment for dependency builds
type ExecutionConfig struct {
	Type    string `yaml:"type"`    // native, docker
	Image   string `yaml:"image"`   // Docker image
	Volumes []string `yaml:"volumes"` // Docker volumes
}

// FetchDependencyConfig represents a fetched dependency from the config
type FetchDependencyConfig struct {
	Name        string          `yaml:"name"`
	Git         string          `yaml:"git"`
	URL         string          `yaml:"url"`
	Ref         string          `yaml:"ref"`
	Checksum    string          `yaml:"checksum"`
	Dest        string          `yaml:"dest"`
	Config      string          `yaml:"config"`       // buildy config file to use instead of build_system
	BuildSystem string          `yaml:"build_system"` // auto, cmake, meson, make, none
	BuildPhases []string        `yaml:"build_phases"` // phases to run: configure, build, test, install
	BuildArgs   []string        `yaml:"build_args"`
	CMakeArgs   []string        `yaml:"cmake_args"`   // deprecated: use build_args
	Type        string          `yaml:"type"`         // source, header_only
	IncludeDirs []string        `yaml:"include_dirs"`
	Execution   *ExecutionConfig `yaml:"execution"`   // per-dependency execution override
}

// DependenciesConfig represents the full dependencies section
type DependenciesConfig struct {
	File    string                           `yaml:"file"`
	System  map[string][]SystemDependencyConfig `yaml:"system"`
	Paths   []PathDependencyConfig           `yaml:"paths"`
	Fetch   []FetchDependencyConfig          `yaml:"fetch"`
}

// DependencyResolver resolves all dependency types
type DependencyResolver struct {
	platform           string
	architecture       string
	varEnv             *util.VariableEnvironment
	cacheDir           string
	fetchManager       *FetchManager
	buildSystemManager *BuildSystemManager
	defaultExecEnv     *util.ExecutionEnvironment
	resolved           map[string]*Dependency
	warnings           []string
}

// NewDependencyResolver creates a new DependencyResolver
func NewDependencyResolver(platform, architecture, cacheDir string, varEnv *util.VariableEnvironment) *DependencyResolver {
	bsm, err := NewBuildSystemManager()
	if err != nil {
		log.Printf("WARNING: Failed to create BuildSystemManager: %v", err)
	}

	return &DependencyResolver{
		platform:           platform,
		architecture:       architecture,
		varEnv:             varEnv,
		cacheDir:           cacheDir,
		fetchManager:       NewFetchManager(cacheDir),
		buildSystemManager: bsm,
		defaultExecEnv:     util.NewNativeExecution(),
		resolved:           make(map[string]*Dependency),
		warnings:           []string{},
	}
}

// SetDefaultExecution sets the default execution environment for dependency builds
func (dr *DependencyResolver) SetDefaultExecution(execEnv *util.ExecutionEnvironment) {
	dr.defaultExecEnv = execEnv
}

// LoadDependencies loads and parses dependencies from config
func (dr *DependencyResolver) LoadDependencies(config map[string]any, workspaceRoot string) (*DependenciesConfig, error) {
	depsSection, ok := config["dependencies"].(map[string]any)
	if !ok {
		return nil, nil // No dependencies section
	}

	// Check for external file reference
	if file, ok := depsSection["file"].(string); ok {
		return dr.loadDependenciesFromFile(filepath.Join(workspaceRoot, file))
	}

	// Parse inline dependencies
	return dr.parseDependenciesSection(depsSection)
}

// loadDependenciesFromFile loads dependencies from an external YAML file
func (dr *DependencyResolver) loadDependenciesFromFile(filePath string) (*DependenciesConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read dependencies file %s: %w", filePath, err)
	}

	var rawConfig map[string]any
	if err := yaml.Unmarshal(data, &rawConfig); err != nil {
		return nil, fmt.Errorf("failed to parse dependencies file %s: %w", filePath, err)
	}

	return dr.parseDependenciesSection(rawConfig)
}

// parseDependenciesSection parses the dependencies section into config structs
func (dr *DependencyResolver) parseDependenciesSection(section map[string]any) (*DependenciesConfig, error) {
	config := &DependenciesConfig{
		System: make(map[string][]SystemDependencyConfig),
		Paths:  []PathDependencyConfig{},
		Fetch:  []FetchDependencyConfig{},
	}

	// Parse system dependencies (platform-keyed)
	if system, ok := section["system"].(map[string]any); ok {
		for platform, deps := range system {
			depsSlice := []SystemDependencyConfig{}
			if depsList, ok := deps.([]any); ok {
				for _, depRaw := range depsList {
					if depMap, ok := depRaw.(map[string]any); ok {
						dep := SystemDependencyConfig{}
						if name, ok := depMap["name"].(string); ok {
							dep.Name = name
						}
						if pkgConfig, ok := depMap["pkg_config"].(string); ok {
							dep.PkgConfig = pkgConfig
						}
						dep.Libs = util.ExtractStringSlice(depMap["libs"])
						dep.Frameworks = util.ExtractStringSlice(depMap["frameworks"])
						depsSlice = append(depsSlice, dep)
					}
				}
			}
			config.System[platform] = depsSlice
		}
	}

	// Parse path dependencies
	if paths, ok := section["paths"].([]any); ok {
		for _, pathRaw := range paths {
			if pathMap, ok := pathRaw.(map[string]any); ok {
				dep := PathDependencyConfig{}
				if name, ok := pathMap["name"].(string); ok {
					dep.Name = name
				}
				if path, ok := pathMap["path"].(string); ok {
					dep.Path = path
				}
				dep.IncludeDirs = util.ExtractStringSlice(pathMap["include_dirs"])
				dep.LibDirs = util.ExtractStringSlice(pathMap["lib_dirs"])
				dep.Libs = util.ExtractStringSlice(pathMap["libs"])
				config.Paths = append(config.Paths, dep)
			}
		}
	}

	// Parse fetch dependencies
	if fetch, ok := section["fetch"].([]any); ok {
		for _, fetchRaw := range fetch {
			if fetchMap, ok := fetchRaw.(map[string]any); ok {
				dep := FetchDependencyConfig{}
				if name, ok := fetchMap["name"].(string); ok {
					dep.Name = name
				}
				if git, ok := fetchMap["git"].(string); ok {
					dep.Git = git
				}
				if url, ok := fetchMap["url"].(string); ok {
					dep.URL = url
				}
				if ref, ok := fetchMap["ref"].(string); ok {
					dep.Ref = ref
				}
				if checksum, ok := fetchMap["checksum"].(string); ok {
					dep.Checksum = checksum
				}
			if dest, ok := fetchMap["dest"].(string); ok {
				dep.Dest = dest
			}
			if configFile, ok := fetchMap["config"].(string); ok {
				dep.Config = configFile
			}
			if buildSystem, ok := fetchMap["build_system"].(string); ok {
				dep.BuildSystem = buildSystem
			}
				if depType, ok := fetchMap["type"].(string); ok {
					dep.Type = depType
				}
				dep.BuildArgs = util.ExtractStringSlice(fetchMap["build_args"])
				dep.CMakeArgs = util.ExtractStringSlice(fetchMap["cmake_args"])
				dep.IncludeDirs = util.ExtractStringSlice(fetchMap["include_dirs"])
				config.Fetch = append(config.Fetch, dep)
			}
		}
	}

	return config, nil
}

// ResolveAll resolves all dependencies and returns them
func (dr *DependencyResolver) ResolveAll(config *DependenciesConfig) (map[string]*Dependency, error) {
	if config == nil {
		return dr.resolved, nil
	}

	// Resolve system dependencies
	if err := dr.resolveSystemDeps(config.System); err != nil {
		return nil, err
	}

	// Resolve path dependencies
	if err := dr.resolvePathDeps(config.Paths); err != nil {
		return nil, err
	}

	// Resolve fetch dependencies
	if err := dr.resolveFetchDeps(config.Fetch); err != nil {
		return nil, err
	}

	return dr.resolved, nil
}

// resolveSystemDeps resolves system dependencies using pkg-config or explicit paths
func (dr *DependencyResolver) resolveSystemDeps(systemDeps map[string][]SystemDependencyConfig) error {
	// Collect deps from "common" and platform-specific sections
	depsToResolve := []SystemDependencyConfig{}

	if common, ok := systemDeps["common"]; ok {
		depsToResolve = append(depsToResolve, common...)
	}
	if platformDeps, ok := systemDeps[dr.platform]; ok {
		depsToResolve = append(depsToResolve, platformDeps...)
	}

	for _, dep := range depsToResolve {
		resolved := &Dependency{
			Name:     dep.Name,
			Type:     DepTypeSystem,
			Resolved: false,
		}

		// Try pkg-config first
		if dep.PkgConfig != "" {
			if err := dr.resolvePkgConfig(dep.PkgConfig, resolved); err != nil {
				log.Printf("WARNING: pkg-config failed for %s: %v", dep.PkgConfig, err)
			} else {
				resolved.Resolved = true
				resolved.Warning = fmt.Sprintf("%s: using system library via pkg-config", dep.Name)
			}
		}

		// Use explicit libs if pkg-config failed or wasn't specified
		if !resolved.Resolved && len(dep.Libs) > 0 {
			resolved.Libs = dep.Libs
			resolved.Resolved = true
			resolved.Warning = fmt.Sprintf("%s: using explicit system library", dep.Name)
		}

		// Add frameworks (macOS)
		if len(dep.Frameworks) > 0 {
			resolved.Frameworks = dep.Frameworks
			resolved.Resolved = true
		}

		if resolved.Resolved {
			dr.resolved[dep.Name] = resolved
			if resolved.Warning != "" {
				dr.warnings = append(dr.warnings, resolved.Warning)
			}
		} else {
			return fmt.Errorf("failed to resolve system dependency: %s", dep.Name)
		}
	}

	return nil
}

// resolvePkgConfig uses pkg-config to get include dirs, lib dirs, and libs
func (dr *DependencyResolver) resolvePkgConfig(pkgName string, dep *Dependency) error {
	// Check if pkg-config exists
	if _, err := exec.LookPath("pkg-config"); err != nil {
		return fmt.Errorf("pkg-config not found")
	}

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

// resolvePathDeps resolves path-based dependencies
func (dr *DependencyResolver) resolvePathDeps(pathDeps []PathDependencyConfig) error {
	for _, dep := range pathDeps {
		resolved := &Dependency{
			Name:     dep.Name,
			Type:     DepTypePath,
			Resolved: false,
		}

		// Resolve path with variable expansion
		errors := []string{}
		resolvedPath := dr.varEnv.ResolveString(dep.Path, &errors, 10)
		if len(errors) > 0 {
			return fmt.Errorf("failed to resolve path for %s: %v", dep.Name, errors)
		}

		// Check if path exists
		if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
			return fmt.Errorf("path dependency %s not found: %s", dep.Name, resolvedPath)
		}

		resolved.Path = resolvedPath

		// Resolve include dirs (may reference ${path})
		dr.varEnv.SetVariable("path", resolvedPath, "dependency")
		for _, incDir := range dep.IncludeDirs {
			resolvedInc := dr.varEnv.ResolveString(incDir, nil, 10)
			resolved.IncludeDirs = append(resolved.IncludeDirs, resolvedInc)
		}

		// Resolve lib dirs
		for _, libDir := range dep.LibDirs {
			resolvedLib := dr.varEnv.ResolveString(libDir, nil, 10)
			resolved.LibDirs = append(resolved.LibDirs, resolvedLib)
		}

		resolved.Libs = dep.Libs
		resolved.Resolved = true

		dr.resolved[dep.Name] = resolved
	}

	return nil
}

// resolveFetchDeps resolves fetched dependencies
func (dr *DependencyResolver) resolveFetchDeps(fetchDeps []FetchDependencyConfig) error {
	for _, dep := range fetchDeps {
		resolved := &Dependency{
			Name:     dep.Name,
			Type:     DepTypeFetch,
			Resolved: false,
		}

		// Determine destination
		dest := dep.Dest
		if dest == "" {
			dest = filepath.Join(dr.cacheDir, "deps", dep.Name)
		} else {
			// Resolve variables in dest
			errors := []string{}
			dest = dr.varEnv.ResolveString(dest, &errors, 10)
		}

		// Fetch the dependency
		var err error
		if dep.Git != "" {
			err = dr.fetchManager.FetchGit(dep.Git, dep.Ref, dest)
		} else if dep.URL != "" {
			err = dr.fetchManager.FetchURL(dep.URL, dep.Checksum, dest)
		}

		if err != nil {
			return fmt.Errorf("failed to fetch %s: %w", dep.Name, err)
		}

		resolved.Path = dest

		// Set include dirs
		dr.varEnv.SetVariable("dest", dest, "dependency")
		if len(dep.IncludeDirs) > 0 {
			for _, incDir := range dep.IncludeDirs {
				resolvedInc := dr.varEnv.ResolveString(incDir, nil, 10)
				resolved.IncludeDirs = append(resolved.IncludeDirs, resolvedInc)
			}
		} else if dep.Type == "header_only" {
			// Default to dest for header-only deps
			resolved.IncludeDirs = []string{dest}
		}

		// If config is specified, this dep will be built via buildy (stored for later)
		// Otherwise, use the build_system approach
		if dep.Config != "" {
			resolved.ConfigFile = dep.Config
			resolved.NeedsBuildy = true
			log.Printf("Fetch dependency '%s' will be built using buildy config: %s", dep.Name, dep.Config)
		} else if dep.BuildSystem != "" && dep.BuildSystem != "none" && dep.Type != "header_only" {
			if err := dr.buildFetchedDep(dep, dest, resolved); err != nil {
				return fmt.Errorf("failed to build %s: %w", dep.Name, err)
			}
		}

		resolved.Resolved = true
		dr.resolved[dep.Name] = resolved
	}

	return nil
}

// buildFetchedDep builds a fetched dependency using its build system
func (dr *DependencyResolver) buildFetchedDep(dep FetchDependencyConfig, dest string, resolved *Dependency) error {
	if dr.buildSystemManager == nil {
		return fmt.Errorf("BuildSystemManager not initialized")
	}

	// Determine build system config
	var bsConfig *BuildSystemConfig
	buildSystemName := dep.BuildSystem

	if buildSystemName == "auto" || buildSystemName == "" {
		// Auto-detect build system
		bsConfig = dr.buildSystemManager.Detect(dest)
		if bsConfig == nil {
			log.Printf("WARNING: Could not detect build system for %s, skipping build", dep.Name)
			return nil
		}
		log.Printf("Auto-detected build system: %s for %s", bsConfig.Name, dep.Name)
	} else if buildSystemName == "none" {
		log.Printf("Build system set to 'none' for %s, skipping build", dep.Name)
		return nil
	} else {
		// Use specified build system
		bsConfig = dr.buildSystemManager.GetBuildSystem(buildSystemName)
		if bsConfig == nil {
			return fmt.Errorf("unknown build system '%s' for %s", buildSystemName, dep.Name)
		}
	}

	// Set up directories
	buildDir := filepath.Join(dest, "_build")
	installDir := filepath.Join(dest, "_install")
	os.MkdirAll(buildDir, 0755)
	os.MkdirAll(installDir, 0755)

	// Determine execution environment
	execEnv := dr.defaultExecEnv
	if dep.Execution != nil && dep.Execution.Type != "" {
		execEnv = dr.createExecutionEnv(dep.Execution)
	}

	// Determine phases to run
	phases := dep.BuildPhases
	if len(phases) == 0 {
		phases = []string{"configure", "build"}
	}

	// Combine build args (support legacy CMakeArgs)
	extraArgs := dep.BuildArgs
	if len(dep.CMakeArgs) > 0 && bsConfig.Name == "cmake" {
		extraArgs = append(extraArgs, dep.CMakeArgs...)
	}

	// Execute the build
	log.Printf("Building %s with %s (phases: %v)", dep.Name, bsConfig.Name, phases)
	if err := dr.buildSystemManager.Execute(bsConfig, dest, buildDir, installDir, execEnv, phases, extraArgs); err != nil {
		return fmt.Errorf("build failed for %s: %w", dep.Name, err)
	}

	// Get output paths from build system config
	includeDirs, libDirs := dr.buildSystemManager.GetOutputPaths(bsConfig, dest, buildDir, installDir)
	resolved.IncludeDirs = append(resolved.IncludeDirs, includeDirs...)
	resolved.LibDirs = append(resolved.LibDirs, libDirs...)

	log.Printf("Build completed for %s", dep.Name)
	return nil
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
		return util.NewDockerExecution(dockerConfig)
	default:
		return util.NewNativeExecution()
	}
}

// GetDependency returns a resolved dependency by name
func (dr *DependencyResolver) GetDependency(name string) *Dependency {
	return dr.resolved[name]
}

// GetBuildyDependencies returns fetch dependencies that need to be built via buildy config
// Returns a map of dependency name -> (source path, config file path)
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

// GetWarnings returns reproducibility warnings
func (dr *DependencyResolver) GetWarnings() []string {
	return dr.warnings
}

