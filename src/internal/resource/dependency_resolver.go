package resource

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
	tools              *util.ToolIdentities // What each build phase runs, for build markers
	plan               bool                 // Dry run: report fetches and builds instead of doing them

	// Timeout is how long a dependency build phase may run (0: no limit)
	Timeout time.Duration
	// Force builds every fetched dependency again, whatever its marker says (--force)
	Force bool
	// Update fetches the dependencies that can move again: git branches and tags, and archives
	// without a checksum (--update-deps)
	Update bool
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
		tools:              util.NewToolIdentities(util.HashFile),
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
		var values []string
		if err := dr.resolveAll(&values, l.values, varEnv, name, l.field); err != nil {
			return err
		}
		if l.field == "include_dirs" || l.field == "lib_dirs" {
			*l.dst = appendNew(*l.dst, values...)
		} else {
			*l.dst = append(*l.dst, values...)
		}
	}
	return nil
}

// resolveFetchDependency fetches a dependency, builds it if it has a build system, and resolves
// what it provides.
func (dr *DependencyResolver) resolveFetchDependency(ctx context.Context, name string, dep *DependencyConfig, section DependencySection, varEnv *util.VariableEnvironment, resolved *ResolvedDependency) error {
	dest := filepath.Join(dr.cacheDir, "deps", name)
	resolved.SourceGit = section.Git
	resolved.SourceRef = section.Ref
	resolved.SourceURL = section.URL
	resolved.Path = dest

	// The fetched tree's identity, for its build's marker: its commit, or its archive's hash
	var source string
	switch {
	case dr.plan:
		from := section.URL
		if section.Git != "" {
			from = section.Git + " @ " + refName(section.Ref)
			if commit := dr.fetchManager.FetchedGit(section.Git, section.Ref, dest); commit != "" && (!dr.Update || isCommitID(section.Ref)) {
				source = "git " + commit
			}
		} else if hash := dr.fetchManager.ExtractedURL(section.URL, dest); hash != "" && !(dr.Update && section.Checksum == "") {
			source = "sha256 " + hash
		}
		if source == "" {
			util.LogProgress("Dry run: would fetch %s from %s into %s", name, from, dest)
		}
	case section.Git != "":
		commit, err := dr.fetchManager.FetchGit(ctx, section.Git, section.Ref, dest, dr.Update)
		if err != nil {
			return fmt.Errorf("failed to fetch '%s': %w", name, err)
		}
		util.LogInfo("Dependency %s: %s @ %s is commit %s", name, section.Git, refName(section.Ref), commit)
		source = "git " + commit
	default:
		hash, err := dr.fetchManager.FetchURL(ctx, section.URL, section.Checksum, dest, dr.Update)
		if err != nil {
			return fmt.Errorf("failed to fetch '%s': %w", name, err)
		}
		source = "sha256 " + hash
	}

	varEnv.SetVariable("dep_dir", dest, "dependency")
	var includeDirs, libDirs []string
	if section.Build != nil && section.Build.System != "" && section.Build.System != "none" {
		var err error
		if includeDirs, libDirs, err = dr.buildFetchedDep(ctx, name, section, dest, source, varEnv); err != nil {
			return fmt.Errorf("failed to build '%s': %w", name, err)
		}
	} else if section.Build != nil && (section.Build.Override != "" || len(section.Build.Args) > 0 || len(section.Build.Configurations) > 0) {
		return fmt.Errorf("dependency '%s': build.override, build.args and build.configurations need a build.system", name)
	}

	// The dirs the definition names, then the build system's output dirs it doesn't name
	if err := dr.resolveProvided(name, section, varEnv, resolved); err != nil {
		return err
	}
	resolved.IncludeDirs = appendNew(resolved.IncludeDirs, includeDirs...)
	resolved.LibDirs = appendNew(resolved.LibDirs, libDirs...)
	return nil
}

// buildFetchedDep builds the configuration being built of a fetched dependency with its build
// system, unless its marker says that build is done, and returns the build system's output
// directories that exist. source is the fetched tree's identity ("" in a dry run when it isn't
// fetched yet).
func (dr *DependencyResolver) buildFetchedDep(ctx context.Context, name string, section DependencySection, dest, source string, varEnv *util.VariableEnvironment) (includeDirs, libDirs []string, err error) {
	build := section.Build
	if dr.plan && source == "" {
		util.LogProgress("Dry run: would build %s (%s) with %s", name, dr.configuration, build.System)
		return nil, nil, nil
	}

	var bs *BuildSystemConfig
	if build.System == "auto" {
		bs = dr.buildSystemManager.Detect(dest)
		if bs == nil {
			return nil, nil, fmt.Errorf("could not detect the build system of %s in %s; set build.system", name, dest)
		}
		util.LogInfo("Auto-detected build system: %s for %s", bs.Name, name)
	} else if bs = dr.buildSystemManager.GetBuildSystem(build.System); bs == nil {
		return nil, nil, fmt.Errorf("unknown build system '%s' (known: %s)", build.System, strings.Join(dr.buildSystemManager.ListBuildSystems(), ", "))
	}

	// The build system's name for the configuration being built
	buildType := ""
	if bs.Configurations != nil {
		buildType = build.Configurations[dr.configuration]
		if buildType == "" {
			buildType = bs.Configurations[dr.configuration]
		}
		if buildType == "" {
			return nil, nil, fmt.Errorf("build system %s has no configuration for buildy's '%s': map it in the dependency's build.configurations (%s)",
				bs.Name, dr.configuration, configurationExample(bs, dr.configuration))
		}
	} else if len(build.Configurations) > 0 {
		return nil, nil, fmt.Errorf("build system %s has no configurations, so build.configurations means nothing; pass configuration-dependent settings in build.args with ${config}", bs.Name)
	}

	override := ""
	if build.Override != "" {
		if bs.Name != "buildy" {
			return nil, nil, fmt.Errorf("build.override is a buildy config, for build system buildy, not %s", bs.Name)
		}
		override = filepath.Join(dr.workspaceRoot, "buildy_config", "dependencies", build.Override)
		if !fileExists(override) {
			return nil, nil, fmt.Errorf("build.override: %s does not exist", override)
		}
	}
	var args []string
	if err := dr.resolveAll(&args, build.Args, varEnv, name, "build.args"); err != nil {
		return nil, nil, err
	}
	execSpec := util.ExecSpec{}
	if section.Execution != nil {
		execSpec = *section.Execution
	}

	b := newDependencyBuild(name, bs, dr.buildSystemManager.definition(bs.Name), source, dest,
		dr.platform, dr.architecture, dr.configuration, buildType, args, override, execSpec, dr.dockerMounts())
	if bs.Name == "buildy" {
		exe, err := os.Executable()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to find buildy executable: %w", err)
		}
		b.values["buildy_exe"] = []string{exe}
	}
	varEnv.SetVariable("dep_build_dir", b.buildDir, "dependency")
	varEnv.SetVariable("dep_install_dir", b.installDir, "dependency")

	if err := b.identify(ctx, dr.tools); err != nil {
		return nil, nil, err
	}
	switch {
	case !dr.Force && b.upToDate():
		util.LogInfo("Dependency %s (%s) is up to date", name, dr.configuration)
	case dr.plan:
		util.LogProgress("Dry run: would build %s (%s) with %s", name, dr.configuration, bs.Name)
	default:
		util.LogProgress("=== Building dependency: %s (%s, using %s) ===", name, dr.configuration, bs.Name)
		start := time.Now()
		if err := b.run(ctx, dr.Timeout); err != nil {
			return nil, nil, err
		}
		util.LogProgress("=== Dependency %s built (%.1fs) ===", name, time.Since(start).Seconds())
	}

	return b.outputs()
}

// configurationExample shows a build.configurations entry for configuration.
func configurationExample(bs *BuildSystemConfig, configuration string) string {
	value := bs.Configurations["release"]
	if value == "" {
		for _, v := range bs.Configurations {
			value = v
			break
		}
	}
	return fmt.Sprintf("build: {configurations: {%s: %s}}", configuration, value)
}

// appendNew appends the values that dst doesn't have yet.
func appendNew(dst []string, values ...string) []string {
	for _, v := range values {
		if !slices.Contains(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
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
		return "", fmt.Errorf("dependency '%s': %s of %q (a dependency sees ${name}, ${version}, ${platform}, ${arch}, ${config}, ${toolchain}, ${root}, ${dep_dir}, ${dep_build_dir}, ${dep_install_dir}, ${workspace_root} and the workspace's variables)",
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
