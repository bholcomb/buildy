package resource

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"buildy/internal/config"
	"buildy/pkg/util"
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

// BuildConfig is a fetched dependency's build section.
type BuildConfig = config.DependencyBuild

// DependencyConfig is a dependency definition.
type DependencyConfig = config.Dependency

// DependencySection is the fields of one section of a dependency, or of its sections merged.
type DependencySection = config.DependencySection

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

	// Timeout is how long a dependency build phase may run (0: no limit)
	Timeout time.Duration
}

// NewDependencyResolver creates a new DependencyResolver
func NewDependencyResolver(platform, architecture, toolchain, configuration, cacheDir, workspaceRoot string, varEnv *util.VariableEnvironment) (*DependencyResolver, error) {
	bsm, err := NewBuildSystemManager()
	if err != nil {
		return nil, err
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
	}, nil
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

	// A file some dependency names as its build.override is a buildy config for building it, not
	// definitions; override configs are named relative to buildy_config/dependencies/
	loaded := make(map[string][]*config.Dependency, len(files))
	failed := make(map[string]error, len(files))
	overrides := map[string]bool{}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read dependencies file %s: %w", path, err)
		}
		deps, err := config.LoadDependencies(data, path)
		if err != nil {
			failed[path] = err
			continue
		}
		loaded[path] = deps
		for _, dep := range deps {
			for _, override := range dep.Overrides() {
				overrides[filepath.Join(depsDir, override)] = true
			}
		}
	}

	definedIn := map[string]string{}
	for _, path := range files {
		if overrides[path] {
			continue
		}
		if err := failed[path]; err != nil {
			return err
		}
		for _, dep := range loaded[path] {
			if other, ok := definedIn[dep.Name]; ok {
				return fmt.Errorf("dependency '%s' is defined in both %s and %s", dep.Name, other, path)
			}
			definedIn[dep.Name] = path
			dr.configs[dep.Name] = dep
		}
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ResolveAll resolves all loaded dependencies: fetches and builds them. With plan (--dry-run), it
// fetches and builds nothing, reports what it would do, and resolves what is already on disk.
// ctx stops the fetches and builds (an interrupt).
func (dr *DependencyResolver) ResolveAll(ctx context.Context, plan bool) error {
	dr.plan = plan
	names := make([]string, 0, len(dr.configs))
	for name := range dr.configs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := dr.resolveDependency(ctx, name, dr.configs[name]); err != nil {
			return err
		}
	}
	return nil
}

// resolveDependency resolves a single dependency
func (dr *DependencyResolver) resolveDependency(ctx context.Context, name string, dep *DependencyConfig) error {
	// Merge the sections: the direct fields, common, platform, platform-arch
	merged := dep.For(dr.platform, dr.architecture)

	// Create variable environment for this dependency
	depVarEnv := dr.varEnv.CreateChild()
	depVarEnv.SetVariable("name", name, "dependency")
	depVarEnv.SetVariable("version", dep.Version, "dependency")
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
		if err := dr.resolveFetchDependency(ctx, name, dep, merged, depVarEnv, resolved); err != nil {
			return err
		}
	case DepTypeSystem:
		if err := dr.resolveSystemDependency(ctx, name, merged, depVarEnv, resolved); err != nil {
			return err
		}
	}

	resolved.Resolved = true
	dr.resolved[name] = resolved
	return nil
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
func (dr *DependencyResolver) resolveSystemDependency(ctx context.Context, name string, section DependencySection, varEnv *util.VariableEnvironment, resolved *ResolvedDependency) error {
	// If pkg_config is specified, use it
	if section.PkgConfig != "" {
		// Check if pkg-config is available
		if _, err := exec.LookPath("pkg-config"); err != nil {
			return fmt.Errorf("pkg_config specified for '%s' but pkg-config is not available", name)
		}

		if err := dr.resolvePkgConfig(ctx, section.PkgConfig, resolved); err != nil {
			return fmt.Errorf("pkg-config failed for '%s': %w", name, err)
		}

		// Still apply any additional defines from config
		return dr.resolveAll(&resolved.Defines, section.Defines, varEnv, name, "defines")
	}

	// Resolve root if specified
	if section.Root != "" {
		resolvedRoot, err := dr.resolveVariables(section.Root, varEnv, name, "root")
		if err != nil {
			return err
		}

		// Check if root path exists
		if _, err := os.Stat(resolvedRoot); os.IsNotExist(err) {
			return fmt.Errorf("root path does not exist for '%s': %s", name, resolvedRoot)
		}

		// Set root variable for path resolution
		varEnv.SetVariable("root", resolvedRoot, "dependency")
	}

	return dr.resolveProvided(name, section, varEnv, resolved)
}

// resolveProvided resolves what a dependency provides: include and lib dirs, libs, frameworks
// and defines.
func (dr *DependencyResolver) resolveProvided(name string, section DependencySection, varEnv *util.VariableEnvironment, resolved *ResolvedDependency) error {
	for _, l := range []struct {
		dst    *[]string
		values []string
		field  string
	}{
		{&resolved.IncludeDirs, section.IncludeDirs, "include_dirs"},
		{&resolved.LibDirs, section.LibDirs, "lib_dirs"},
		{&resolved.Libs, section.Libs, "libs"},
		{&resolved.Frameworks, section.Frameworks, "frameworks"},
		{&resolved.Defines, section.Defines, "defines"},
	} {
		if err := dr.resolveAll(l.dst, l.values, varEnv, name, l.field); err != nil {
			return err
		}
	}
	return nil
}

// resolveFetchDependency resolves a fetch dependency
func (dr *DependencyResolver) resolveFetchDependency(ctx context.Context, name string, dep *DependencyConfig, section DependencySection, varEnv *util.VariableEnvironment, resolved *ResolvedDependency) error {
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
		err = dr.fetchManager.FetchGit(ctx, section.Git, section.Ref, dest)
	} else if section.URL != "" {
		err = dr.fetchManager.FetchURL(ctx, section.URL, section.Checksum, dest)
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
		if err := dr.buildFetchedDep(ctx, name, section, dest, resolved); err != nil {
			return fmt.Errorf("failed to build '%s': %w", name, err)
		}
	}

	return dr.resolveProvided(name, section, varEnv, resolved)
}

// buildFetchedDep builds a fetched dependency using its build system
// Builds both debug and release configurations so users can link against either
func (dr *DependencyResolver) buildFetchedDep(ctx context.Context, name string, section DependencySection, dest string, resolved *ResolvedDependency) error {
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

		// The build's additional variables
		variables := map[string]string{
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
			variables["buildy_exe"] = buildyExe

			// If override config is specified, pass it as the config file argument
			// The source_dir remains the fetched dependency directory
			if section.Build.Override != "" {
				overrideConfig := filepath.Join(dr.workspaceRoot, "buildy_config", "dependencies", section.Build.Override)
				variables["config_file"] = overrideConfig
			} else {
				// No override - use the default buildy.yaml in the source directory
				variables["config_file"] = ""
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
		if err := dr.buildSystemManager.ExecuteWithContext(ctx, bsConfig, dest, buildDir, installDir, execSpec, mounts, phases, extraArgs, variables, dr.Timeout); err != nil {
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
func (dr *DependencyResolver) resolvePkgConfig(ctx context.Context, pkgName string, dep *ResolvedDependency) error {
	// Get cflags
	cflagsCmd := exec.CommandContext(ctx, "pkg-config", "--cflags", pkgName)
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
	libsCmd := exec.CommandContext(ctx, "pkg-config", "--libs", pkgName)
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

// resolveVariables resolves the variables in a dependency's field, or fails naming them.
func (dr *DependencyResolver) resolveVariables(text string, varEnv *util.VariableEnvironment, name, field string) (string, error) {
	var errors []string
	resolved := varEnv.ResolveString(text, &errors, 10)
	if len(errors) > 0 {
		return "", fmt.Errorf("dependency '%s': %s of %q (a dependency sees ${name}, ${version}, ${platform}, ${arch}, ${config}, ${toolchain}, ${root}, ${dep_dir}, ${workspace_root} and the workspace's variables)",
			name, strings.Join(errors, "; "), field)
	}
	return resolved, nil
}

// resolveAll appends values to dst with their variables resolved.
func (dr *DependencyResolver) resolveAll(dst *[]string, values []string, varEnv *util.VariableEnvironment, name, field string) error {
	for _, v := range values {
		resolved, err := dr.resolveVariables(v, varEnv, name, field)
		if err != nil {
			return err
		}
		*dst = append(*dst, resolved)
	}
	return nil
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
