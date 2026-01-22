package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// TaskGenerator generates build tasks from configuration
type TaskGenerator struct {
	Platform         string
	Architecture     string
	Configuration    string
	ToolchainManager *ToolchainManager
	TemplateEngine   *BuildTemplateEngine
	CurrentToolchain *ToolchainConfig
	ToolchainHash    string
	ToolMatcher      *ToolMatcher
	CommandBuilder   *CommandBuilder
	ExecEnv          *ExecutionEnvironment
	PackageManager   *PackageManager
	VarEnv           *VariableEnvironment
	PathResolver     *PathResolver
	TaskIDGen        *TaskIDGenerator
	TargetRegistry   *TargetRegistry
	CurrentModule    string
	Workspace        *Workspace

	// Track generated targets for cross-module dependency resolution
	generatedTargets map[string]string // target name -> link task ID
}

// NewTaskGenerator creates a new TaskGenerator
func NewTaskGenerator(
	platform, architecture, configuration string,
	toolchainManager *ToolchainManager,
	templateEngine *BuildTemplateEngine,
	packageManager *PackageManager,
	varEnv *VariableEnvironment,
	workspace *Workspace,
) *TaskGenerator {
	return &TaskGenerator{
		Platform:         platform,
		Architecture:     architecture,
		Configuration:    configuration,
		ToolchainManager: toolchainManager,
		TemplateEngine:   templateEngine,
		PackageManager:   packageManager,
		VarEnv:           varEnv,
		Workspace:        workspace,
		generatedTargets: make(map[string]string),
	}
}

// SetPathResolver sets the path resolver for this generator
func (tg *TaskGenerator) SetPathResolver(pr *PathResolver) {
	tg.PathResolver = pr
}

// SetToolchain sets up the toolchain for task generation
func (tg *TaskGenerator) SetToolchain(tc *ToolchainConfig) error {
	tg.CurrentToolchain = tc
	tg.ToolchainHash = HashToolchainConfig(tc)

	toolMatcher, err := NewToolMatcher(tc)
	if err != nil {
		return err
	}
	tg.ToolMatcher = toolMatcher
	tg.CommandBuilder = NewCommandBuilder(tc, toolMatcher, tg.Configuration)
	tg.ExecEnv = CreateExecutionEnvironmentFromToolchain(tc)

	return nil
}

// GenerateTasks generates all tasks for a configuration
func (tg *TaskGenerator) GenerateTasks(config map[string]any, outputDir string) ([]*BuildTask, error) {
	tasks := []*BuildTask{}

	if tg.TaskIDGen == nil {
		moduleName := tg.CurrentModule
		if moduleName == "" {
			moduleName = "workspace"
		}
		tg.TaskIDGen = NewTaskIDGenerator(moduleName)
	}

	// Generate setup task
	setupTask := tg.createSetupTask(outputDir)
	tasks = append(tasks, &setupTask)

	// Generate library tasks - support both old and new formats
	libraries := []any{}

	// New format: targets.libraries
	if targets, ok := config["targets"].(map[string]any); ok {
		if libs, ok := targets["libraries"].([]any); ok {
			libraries = append(libraries, libs...)
		}
	}

	// Legacy format: library (single or list)
	if library, ok := config["library"]; ok {
		switch v := library.(type) {
		case map[string]any:
			libraries = append(libraries, v)
		case []any:
			libraries = append(libraries, v...)
		}
	}

	// Apply platform-specific target overrides
	libraries = tg.applyPlatformTargetOverrides(config, libraries, "libraries")

	mergedConfig := tg.getMergedConfig(config)

	for _, libRaw := range libraries {
		if lib, ok := libRaw.(map[string]any); ok {
			libTasks, err := tg.generateLibraryTasks(lib, mergedConfig, outputDir, setupTask.TaskID)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, libTasks...)
		}
	}

	// Generate executable tasks - support both old and new formats
	executables := []any{}

	// New format: targets.executables
	if targets, ok := config["targets"].(map[string]any); ok {
		if exes, ok := targets["executables"].([]any); ok {
			executables = append(executables, exes...)
		}
	}

	// Legacy format: executable (single or list)
	if executable, ok := config["executable"]; ok {
		switch v := executable.(type) {
		case map[string]any:
			executables = append(executables, v)
		case []any:
			executables = append(executables, v...)
		}
	}

	// Apply platform-specific target overrides
	executables = tg.applyPlatformTargetOverrides(config, executables, "executables")

	for _, exeRaw := range executables {
		if exe, ok := exeRaw.(map[string]any); ok {
			exeTasks, err := tg.generateExecutableTasks(exe, mergedConfig, outputDir, setupTask.TaskID, tasks)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, exeTasks...)
		}
	}

	// Generate Go module tasks
	if targets, ok := config["targets"].(map[string]any); ok {
		if goModules, ok := targets["go_modules"].([]any); ok {
			for _, goModRaw := range goModules {
				if goMod, ok := goModRaw.(map[string]any); ok {
					goTasks, err := tg.generateGoModuleTasks(goMod, mergedConfig, outputDir, setupTask.TaskID)
					if err != nil {
						return nil, err
					}
					tasks = append(tasks, goTasks...)
				}
			}
		}
	}

	return tasks, nil
}

// getMergedConfig extracts and merges configuration hierarchy
func (tg *TaskGenerator) getMergedConfig(config map[string]any) map[string]any {
	globalConfig := make(map[string]any)
	if gc, ok := config["config"].(map[string]any); ok {
		globalConfig = gc
	}

	platforms := make(map[string]any)
	if p, ok := config["platforms"].(map[string]any); ok {
		platforms = p
	}

	architectures := make(map[string]any)
	if a, ok := config["architectures"].(map[string]any); ok {
		architectures = a
	}

	configurations := make(map[string]any)
	if c, ok := config["configurations"].(map[string]any); ok {
		configurations = c
	}

	// Also extract from environment section (new DSL format)
	if env, ok := config["environment"].(map[string]any); ok {
		if compile, ok := env["compile"].(map[string]any); ok {
			for k, v := range compile {
				globalConfig[k] = v
			}
		}
		if envConfigs, ok := env["configurations"].(map[string]any); ok {
			for configName, configData := range envConfigs {
				if configMap, ok := configData.(map[string]any); ok {
					if _, exists := configurations[configName]; !exists {
						configurations[configName] = configMap
					} else if existingMap, ok := configurations[configName].(map[string]any); ok {
						for k, v := range configMap {
							existingMap[k] = v
						}
					}
				}
			}
		}
	}

	return tg.mergeConfigs(
		globalConfig,
		platforms[tg.Platform],
		architectures[tg.Architecture],
		configurations[tg.Configuration],
	)
}

// mergeConfigs merges configuration hierarchy
func (tg *TaskGenerator) mergeConfigs(configs ...any) map[string]any {
	merged := make(map[string]any)
	for _, configRaw := range configs {
		config, ok := configRaw.(map[string]any)
		if !ok {
			continue
		}
		for key, value := range config {
			if valueList, ok := value.([]any); ok {
				if existingList, ok := merged[key].([]any); ok {
					merged[key] = append(existingList, valueList...)
				} else {
					merged[key] = value
				}
			} else {
				merged[key] = value
			}
		}
	}
	return merged
}

// applyPlatformTargetOverrides merges platform-specific target configurations
// with the base targets. For example, if platforms.linux.targets.libraries defines
// overrides for a library, those are merged with the base library config.
func (tg *TaskGenerator) applyPlatformTargetOverrides(config map[string]any, baseTargets []any, targetType string) []any {
	// Get platform-specific targets
	platforms, ok := config["platforms"].(map[string]any)
	if !ok {
		return baseTargets
	}

	platformConfig, ok := platforms[tg.Platform].(map[string]any)
	if !ok {
		return baseTargets
	}

	platformTargets, ok := platformConfig["targets"].(map[string]any)
	if !ok {
		return baseTargets
	}

	platformTargetList, ok := platformTargets[targetType].([]any)
	if !ok || len(platformTargetList) == 0 {
		return baseTargets
	}

	// Build a map of platform overrides by target name
	overridesByName := make(map[string]map[string]any)
	for _, overrideRaw := range platformTargetList {
		override, ok := overrideRaw.(map[string]any)
		if !ok {
			continue
		}
		name, ok := override["name"].(string)
		if !ok {
			continue
		}
		overridesByName[name] = override
	}

	// Merge overrides into base targets
	result := make([]any, 0, len(baseTargets))
	for _, baseRaw := range baseTargets {
		base, ok := baseRaw.(map[string]any)
		if !ok {
			result = append(result, baseRaw)
			continue
		}

		name, ok := base["name"].(string)
		if !ok {
			result = append(result, baseRaw)
			continue
		}

		override, hasOverride := overridesByName[name]
		if !hasOverride {
			result = append(result, base)
			continue
		}

		// Merge override into base (deep merge for special keys)
		merged := tg.mergeTargetConfig(base, override)
		result = append(result, merged)

		log.Printf("Applied platform '%s' overrides to target '%s'", tg.Platform, name)
	}

	return result
}

// mergeTargetConfig merges a platform-specific target override into the base target config
func (tg *TaskGenerator) mergeTargetConfig(base, override map[string]any) map[string]any {
	result := make(map[string]any)

	// Copy base values
	for k, v := range base {
		result[k] = v
	}

	// Apply overrides
	for k, v := range override {
		if k == "name" {
			continue // Don't override name
		}

		switch k {
		case "sources":
			// Sources: replace entirely with platform-specific sources
			result[k] = v

		case "compile":
			// Compile settings: deep merge
			if baseCompile, ok := base["compile"].(map[string]any); ok {
				if overrideCompile, ok := v.(map[string]any); ok {
					mergedCompile := make(map[string]any)
					for ck, cv := range baseCompile {
						mergedCompile[ck] = cv
					}
					for ck, cv := range overrideCompile {
						// For defines, append rather than replace
						if ck == "defines" {
							if baseDefines, ok := mergedCompile["defines"].([]any); ok {
								if overrideDefines, ok := cv.([]any); ok {
									mergedCompile["defines"] = append(baseDefines, overrideDefines...)
								}
							} else {
								mergedCompile[ck] = cv
							}
						} else {
							mergedCompile[ck] = cv
						}
					}
					result[k] = mergedCompile
				} else {
					result[k] = v
				}
			} else {
				result[k] = v
			}

		case "include_dirs", "libs", "packages", "depends_on":
			// These could be merged or replaced - for now, replace if present
			result[k] = v

		default:
			// For other keys, override takes precedence
			result[k] = v
		}
	}

	return result
}

// createSetupTask creates directory setup task
func (tg *TaskGenerator) createSetupTask(outputDir string) BuildTask {
	libDir := filepath.Join(outputDir, "lib")
	binDir := filepath.Join(outputDir, "bin")
	objDir := filepath.Join(outputDir, "obj")

	var command string
	if strings.Contains(strings.ToLower(tg.Platform), "windows") {
		command = fmt.Sprintf("cmd /c mkdir %s 2>nul & mkdir %s 2>nul & mkdir %s 2>nul",
			libDir, binDir, objDir)
	} else {
		command = fmt.Sprintf("mkdir -p %s %s %s", libDir, binDir, objDir)
	}

	taskID := tg.TaskIDGen.Next("setup", "dirs")
	task := NewBuildTask(
		taskID,
		"setup",
		[]TaskInput{},
		[]string{
			libDir + string(filepath.Separator),
			binDir + string(filepath.Separator),
			objDir + string(filepath.Separator),
		},
		[]string{},
		command,
	)
	task.Platform = tg.Platform
	task.Architecture = tg.Architecture
	task.Configuration = tg.Configuration
	if tg.CurrentToolchain != nil {
		task.Toolchain = tg.CurrentToolchain.Name
	}
	task.EstimatedTime = DefaultSetupTimeSeconds
	task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 10, DiskMB: 1}
	task.CacheKey = task.CalculateCacheKey()
	return task
}

// resolvePackages resolves package dependencies and merges their settings into the target config
func (tg *TaskGenerator) resolvePackages(targetConfig map[string]any) error {
	if tg.PackageManager == nil {
		return nil
	}

	var packageNames []string

	// Preferred format: packages at target level
	if packages, ok := targetConfig["packages"]; ok {
		packageNames = append(packageNames, ExtractStringList(packages)...)
	}

	// Legacy format: depends_on.packages
	if dependsOn, ok := targetConfig["depends_on"].(map[string]any); ok {
		if packages, ok := dependsOn["packages"]; ok {
			packageNames = append(packageNames, ExtractStringList(packages)...)
		}
	}

	if len(packageNames) == 0 {
		return nil
	}

	var workspacePackages map[string]map[string]string
	if tg.Workspace != nil && tg.Workspace.Config != nil {
		workspacePackages = tg.Workspace.Config.Packages
	}

	mergedPackage, err := tg.PackageManager.ResolvePackages(
		packageNames,
		tg.Platform,
		tg.Architecture,
		workspacePackages,
	)
	if err != nil {
		return fmt.Errorf("failed to resolve packages: %w", err)
	}

	// Merge package settings into target config
	if len(mergedPackage.IncludeDirs) > 0 {
		existing := extractIncludeDirs(targetConfig["include_dirs"])
		targetConfig["include_dirs"] = append(mergedPackage.IncludeDirs, existing...)
	}

	if len(mergedPackage.LibDirs) > 0 {
		existing := ExtractStringList(targetConfig["lib_dirs"])
		targetConfig["lib_dirs"] = append(mergedPackage.LibDirs, existing...)
	}

	if len(mergedPackage.Libs) > 0 {
		existing := ExtractStringList(targetConfig["libs"])
		targetConfig["libs"] = append(mergedPackage.Libs, existing...)
	}

	if len(mergedPackage.Frameworks) > 0 {
		existing := ExtractStringList(targetConfig["frameworks"])
		targetConfig["frameworks"] = append(mergedPackage.Frameworks, existing...)
	}

	if len(mergedPackage.Defines) > 0 {
		existing := ExtractStringList(targetConfig["defines"])
		targetConfig["defines"] = append(mergedPackage.Defines, existing...)
	}

	if len(mergedPackage.Sources) > 0 {
		existing := ExtractStringList(targetConfig["sources"])
		targetConfig["sources"] = append(mergedPackage.Sources, existing...)
	}

	return nil
}

// generateLibraryTasks generates tasks for a library
func (tg *TaskGenerator) generateLibraryTasks(
	libConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
) ([]*BuildTask, error) {
	libType := "shared_library"
	if lt, ok := libConfig["type"].(string); ok {
		libType = lt
	}

	if err := tg.resolvePackages(libConfig); err != nil {
		return nil, err
	}

	sources, err := tg.PathResolver.ResolveSources(libConfig)
	if err != nil {
		return nil, err
	}
	libConfig["sources"] = sources

	includeDirs, err := tg.PathResolver.ResolveIncludeDirs(libConfig)
	if err != nil {
		return nil, err
	}
	libConfig["include_dirs"] = includeDirs

	// Add module name for unique object file paths
	libConfig["module"] = tg.CurrentModule
	if libConfig["module"] == "" {
		libConfig["module"] = "workspace"
	}

	toolchainName := ""
	if tg.CurrentToolchain != nil {
		toolchainName = tg.CurrentToolchain.Name
	}

	tasks, err := tg.TemplateEngine.ExpandTemplate(
		libType,
		libConfig,
		mergedConfig,
		outputDir,
		setupTaskID,
		tg.TaskIDGen,
		tg.ToolMatcher,
		tg.CommandBuilder,
		tg.Platform,
		tg.Architecture,
		tg.Configuration,
		toolchainName,
		[]*BuildTask{},
	)
	if err != nil {
		return nil, err
	}

	// Register the target's link task for cross-module dependency resolution
	if name, ok := libConfig["name"].(string); ok {
		for _, task := range tasks {
			if task.TaskType == "link" {
				tg.generatedTargets[name] = task.TaskID
				// Also register in global registry
				globalTaskRegistry.RegisterTarget(name, task.TaskID, tg.CurrentModule)
				break
			}
		}
	}

	return tasks, nil
}

// generateExecutableTasks generates tasks for an executable
func (tg *TaskGenerator) generateExecutableTasks(
	exeConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
	existingTasks []*BuildTask,
) ([]*BuildTask, error) {
	exeType := "executable"
	if et, ok := exeConfig["type"].(string); ok {
		exeType = et
	}

	if err := tg.resolvePackages(exeConfig); err != nil {
		return nil, err
	}

	sources, err := tg.PathResolver.ResolveSources(exeConfig)
	if err != nil {
		return nil, err
	}
	exeConfig["sources"] = sources

	includeDirs, err := tg.PathResolver.ResolveIncludeDirs(exeConfig)
	if err != nil {
		return nil, err
	}
	exeConfig["include_dirs"] = includeDirs

	// Add module name for unique object file paths
	exeConfig["module"] = tg.CurrentModule
	if exeConfig["module"] == "" {
		exeConfig["module"] = "workspace"
	}

	toolchainName := ""
	if tg.CurrentToolchain != nil {
		toolchainName = tg.CurrentToolchain.Name
	}

	tasks, err := tg.TemplateEngine.ExpandTemplate(
		exeType,
		exeConfig,
		mergedConfig,
		outputDir,
		setupTaskID,
		tg.TaskIDGen,
		tg.ToolMatcher,
		tg.CommandBuilder,
		tg.Platform,
		tg.Architecture,
		tg.Configuration,
		toolchainName,
		existingTasks,
	)
	if err != nil {
		return nil, err
	}

	return tasks, nil
}

// generateGoModuleTasks generates tasks for a Go module
func (tg *TaskGenerator) generateGoModuleTasks(
	goModConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
) ([]*BuildTask, error) {
	tasks := []*BuildTask{}

	name := "go_module"
	if n, ok := goModConfig["name"].(string); ok {
		name = n
	}

	modulePath := "."
	if p, ok := goModConfig["path"].(string); ok {
		modulePath = p
	}

	if tg.PathResolver != nil {
		modulePath = tg.PathResolver.ResolveRelativePath(modulePath)
	}

	output := filepath.Join(outputDir, "bin", name)
	if o, ok := goModConfig["output"].(string); ok {
		output = o
		if !filepath.IsAbs(output) {
			output = filepath.Join(outputDir, output)
		}
	}

	if tg.Platform == "windows" && !strings.HasSuffix(output, ".exe") {
		output += ".exe"
	}

	cmdParts := []string{"go", "build", "-o", output}

	if tags, ok := goModConfig["build_tags"].([]any); ok && len(tags) > 0 {
		tagStrs := []string{}
		for _, t := range tags {
			if str, ok := t.(string); ok {
				tagStrs = append(tagStrs, str)
			}
		}
		if len(tagStrs) > 0 {
			cmdParts = append(cmdParts, "-tags", strings.Join(tagStrs, ","))
		}
	}

	ldflags := []string{}
	if tg.Configuration == "release" {
		ldflags = append(ldflags, "-s", "-w")
	}

	if flags, ok := goModConfig["ldflags"].([]any); ok {
		for _, f := range flags {
			if str, ok := f.(string); ok {
				ldflags = append(ldflags, str)
			}
		}
	}

	if len(ldflags) > 0 {
		cmdParts = append(cmdParts, "-ldflags", strings.Join(ldflags, " "))
	}

	if tg.Configuration == "debug" {
		cmdParts = append(cmdParts, "-gcflags", "all=-N -l")
	}

	if tg.Configuration == "release" {
		cmdParts = append(cmdParts, "-trimpath")
	}

	cmdParts = append(cmdParts, "./...")

	command := strings.Join(cmdParts, " ")

	// Collect all Go source files as inputs for proper cache invalidation
	inputs := []TaskInput{}
	
	// Add go.mod and go.sum
	goModFile := filepath.Join(modulePath, "go.mod")
	if _, err := os.Stat(goModFile); err == nil {
		inputs = append(inputs, NewTaskInput(goModFile))
	}
	goSumFile := filepath.Join(modulePath, "go.sum")
	if _, err := os.Stat(goSumFile); err == nil {
		inputs = append(inputs, NewTaskInput(goSumFile))
	}

	// Scan for all .go files in the module directory
	goFiles, err := findGoSourceFiles(modulePath)
	if err != nil {
		log.Printf("WARNING: Failed to scan Go source files in %s: %v", modulePath, err)
	} else {
		for _, goFile := range goFiles {
			inputs = append(inputs, NewTaskInput(goFile))
		}
		log.Printf("Found %d Go source files in %s", len(goFiles), modulePath)
	}

	taskID := tg.TaskIDGen.Next("go_build", name)
	task := NewBuildTask(
		taskID,
		"go_build",
		inputs,
		[]string{output},
		[]string{setupTaskID},
		command,
	)
	task.Platform = tg.Platform
	task.Architecture = tg.Architecture
	task.Configuration = tg.Configuration
	if tg.CurrentToolchain != nil {
		task.Toolchain = tg.CurrentToolchain.Name
	} else {
		task.Toolchain = "go"
	}
	task.EstimatedTime = 10.0
	task.ResourceRequirements = ResourceRequirements{CPUCores: 2, MemoryMB: 500, DiskMB: 100}
	task.CacheKey = task.CalculateCacheKey()

	tasks = append(tasks, &task)

	log.Printf("Generated Go build task: %s -> %s", name, output)
	return tasks, nil
}

// GetGeneratedTargets returns the mapping of target names to their link task IDs
func (tg *TaskGenerator) GetGeneratedTargets() map[string]string {
	return tg.generatedTargets
}

// extractIncludeDirs extracts include directories from various formats
// Handles both flat lists and nested public/private structures
func extractIncludeDirs(value any) []string {
	result := []string{}
	switch v := value.(type) {
	case string:
		result = append(result, v)
	case []any:
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
	case []string:
		result = v
	case map[string]any:
		// Handle nested structure with public/private keys
		if publicDirs, ok := v["public"]; ok {
			result = append(result, ExtractStringList(publicDirs)...)
		}
		if privateDirs, ok := v["private"]; ok {
			result = append(result, ExtractStringList(privateDirs)...)
		}
	}
	return result
}

// RegisterTarget registers a target name to task ID mapping
func (tg *TaskGenerator) RegisterTarget(name, taskID string) {
	tg.generatedTargets[name] = taskID
}

// findGoSourceFiles recursively finds all .go files in a directory
// Excludes vendor directories and test files if needed
func findGoSourceFiles(rootDir string) ([]string, error) {
	var goFiles []string

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories we don't want to scan
		if info.IsDir() {
			name := info.Name()
			// Skip vendor, .git, and other common directories
			if name == "vendor" || name == ".git" || name == "testdata" || name == ".buildy_cache" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only include .go files
		if filepath.Ext(path) == ".go" {
			goFiles = append(goFiles, path)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return goFiles, nil
}
