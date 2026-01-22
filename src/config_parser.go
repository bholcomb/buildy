package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigParser parses build configuration files with hierarchical variable support
type ConfigParser struct {
	Platform         string
	Architecture     string
	Configuration    string
	GlobalConfig     map[string]any
	PlatformConfig   map[string]any
	ArchConfig       map[string]any
	ConfigConfig     map[string]any
	ConfigFileDir    string
	VarEnv           *VariableEnvironment
	CLIDefines       map[string]string
	ToolchainManager *ToolchainManager
	DefaultToolchain string
	TemplateEngine   *BuildTemplateEngine
	CurrentToolchain *ToolchainConfig
	ToolMatcher      *ToolMatcher
	CommandBuilder   *CommandBuilder
	ExecEnv          *ExecutionEnvironment
	Workspace        *Workspace
	TargetRegistry   *TargetRegistry
	CurrentModule    string
	PackageManager   *PackageManager
	TaskIDGen        *TaskIDGenerator
}

// NewConfigParser creates a new ConfigParser
func NewConfigParser(
	platform, architecture, configuration string,
	cliDefines map[string]string,
	toolchainManager *ToolchainManager,
	defaultToolchain string,
	templateEngine *BuildTemplateEngine,
	workspace *Workspace,
	packageManager *PackageManager,
	parentVarEnv *VariableEnvironment,
) *ConfigParser {
	varEnv := NewVariableEnvironment(parentVarEnv)

	return &ConfigParser{
		Platform:         platform,
		Architecture:     architecture,
		Configuration:    configuration,
		GlobalConfig:     make(map[string]any),
		PlatformConfig:   make(map[string]any),
		ArchConfig:       make(map[string]any),
		ConfigConfig:     make(map[string]any),
		VarEnv:           varEnv,
		CLIDefines:       cliDefines,
		ToolchainManager: toolchainManager,
		DefaultToolchain: defaultToolchain,
		TemplateEngine:   templateEngine,
		Workspace:        workspace,
		PackageManager:   packageManager,
		TaskIDGen:        NewTaskIDGenerator("workspace"),
	}
}

// ParseConfigFile parses YAML build configuration file
func (cp *ConfigParser) ParseConfigFile(configFile string) (map[string]any, error) {
	// Store the directory of the config file for relative path resolution
	absPath, err := filepath.Abs(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config file path: %w", err)
	}
	cp.ConfigFileDir = filepath.Dir(absPath)
	log.Printf("Config file directory: %s", cp.ConfigFileDir)

	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("error reading config file %s: %w", configFile, err)
	}

	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("error parsing config file %s: %w", configFile, err)
	}

	// Validate configuration
	validationErrors := cp.validateConfig(config)
	if len(validationErrors) > 0 {
		for _, errMsg := range validationErrors {
			log.Printf("ERROR: Config validation error: %s", errMsg)
		}
		return make(map[string]any), nil
	}

	return config, nil
}

// validateConfig validates configuration and returns list of errors
func (cp *ConfigParser) validateConfig(config map[string]any) []string {
	errors := []string{}

	// Validate project section
	project, hasProject := config["project"].(map[string]any)
	if !hasProject {
		errors = append(errors, "Missing 'project' section")
	} else {
		if name, ok := project["name"].(string); !ok || strings.TrimSpace(name) == "" {
			errors = append(errors, "Missing or invalid 'project.name'")
		}
	}

	// Helper to validate target list
	validateTargetList := func(targets []any, targetType string) {
		for i, targetRaw := range targets {
			target, ok := targetRaw.(map[string]any)
			if !ok {
				errors = append(errors, fmt.Sprintf("%s %d must be a dictionary", targetType, i))
				continue
			}
			if _, ok := target["name"].(string); !ok {
				errors = append(errors, fmt.Sprintf("%s %d missing required 'name' field", targetType, i))
			}
			if _, ok := target["sources"]; !ok {
				targetName := "unknown"
				if name, ok := target["name"].(string); ok {
					targetName = name
				}
				errors = append(errors, fmt.Sprintf("%s '%s' missing required 'sources' field", targetType, targetName))
			}
		}
	}

	// Validate new format: targets.libraries and targets.executables
	if targets, ok := config["targets"].(map[string]any); ok {
		if libs, ok := targets["libraries"].([]any); ok {
			validateTargetList(libs, "Library")
		}
		if exes, ok := targets["executables"].([]any); ok {
			validateTargetList(exes, "Executable")
		}
	}

	// Validate legacy library format (can be a single dict or list of dicts)
	if library, ok := config["library"]; ok {
		var libraries []any
		switch v := library.(type) {
		case map[string]any:
			libraries = []any{v}
		case []any:
			libraries = v
		default:
			errors = append(errors, "'library' must be a dictionary or list of dictionaries")
		}
		validateTargetList(libraries, "Library")
	}

	// Validate legacy executable format (can be a single dict or list of dicts)
	if executable, ok := config["executable"]; ok {
		var executables []any
		switch v := executable.(type) {
		case map[string]any:
			executables = []any{v}
		case []any:
			executables = v
		default:
			errors = append(errors, "'executable' must be a dictionary or list of dictionaries")
		}
		validateTargetList(executables, "Executable")
	}

	// Validate output paths don't escape project
	if outputConfig, ok := config["output"].(map[string]any); ok {
		if baseDir, ok := outputConfig["base_dir"].(string); ok {
			if strings.Contains(baseDir, "..") || filepath.IsAbs(baseDir) {
				errors = append(errors, fmt.Sprintf("Invalid output base_dir '%s' - must be relative and not contain '..'", baseDir))
			}
		}
	}

	return errors
}

// selectToolchain selects toolchain based on hierarchical configuration
func (cp *ConfigParser) selectToolchain(config map[string]any) (*ToolchainConfig, error) {
	if cp.ToolchainManager == nil {
		return nil, fmt.Errorf("ToolchainManager not initialized")
	}

	// Check CLI override first
	if cp.DefaultToolchain != "" {
		tc := cp.ToolchainManager.GetToolchain(cp.DefaultToolchain)
		if tc != nil {
			log.Printf("Using CLI-specified toolchain: %s", tc.Name)
			return tc, nil
		}
		log.Printf("WARNING: CLI toolchain '%s' not found, falling back", cp.DefaultToolchain)
	}

	// Check project-level toolchain
	if project, ok := config["project"].(map[string]any); ok {
		if projectToolchain, ok := project["toolchain"].(string); ok {
			tc := cp.ToolchainManager.GetToolchain(projectToolchain)
			if tc != nil {
				log.Printf("Using project-specified toolchain: %s", tc.Name)
				return tc, nil
			}
			log.Printf("WARNING: Project toolchain '%s' not found, falling back", projectToolchain)
		}
	}

	// Check workspace-level toolchain
	if workspaceToolchain, ok := config["toolchain"].(string); ok {
		tc := cp.ToolchainManager.GetToolchain(workspaceToolchain)
		if tc != nil {
			log.Printf("Using workspace-specified toolchain: %s", tc.Name)
			return tc, nil
		}
		log.Printf("WARNING: Workspace toolchain '%s' not found, falling back", workspaceToolchain)
	}

	// Auto-detect based on platform/architecture
	tc := cp.ToolchainManager.AutoDetect(cp.Platform, cp.Architecture)
	if tc != nil {
		return tc, nil
	}

	// No toolchain found
	availableToolchains := cp.ToolchainManager.ListToolchains()
	names := []string{}
	for _, tc := range availableToolchains {
		names = append(names, tc.Name)
	}
	return nil, fmt.Errorf(
		"no suitable toolchain found for %s-%s. Available toolchains: %v",
		cp.Platform, cp.Architecture, names,
	)
}

// GenerateWorkspaceTasks generates tasks for entire workspace or specific targets
func (cp *ConfigParser) GenerateWorkspaceTasks(targetFilter []string) ([]*BuildTask, error) {
	if cp.Workspace == nil {
		return nil, fmt.Errorf("no workspace configured. Use GenerateTasks() for single-file builds")
	}

	// Initialize target registry
	cp.TargetRegistry = NewTargetRegistry(cp.Workspace)
	if err := cp.TargetRegistry.Initialize(); err != nil {
		return nil, err
	}

	allTasks := []*BuildTask{}

	// Create workspace-level variable environment
	workspaceVarEnv := NewVariableEnvironment(nil)
	workspaceVarEnv.PushScope("workspace")
	if variables, ok := cp.Workspace.Config.RawConfig["variables"].(map[string]any); ok {
		workspaceVarEnv.ExtractVariablesFromSection(variables, "workspace")
	}

	// Process each module
	for modulePath, moduleInfo := range cp.Workspace.Modules {
		// Skip if target filter specified and this module has no matching targets
		if len(targetFilter) > 0 {
			hasMatchingTarget := false
			for _, target := range moduleInfo.Targets {
				for _, filter := range targetFilter {
					if target == filter {
						hasMatchingTarget = true
						break
					}
				}
				if hasMatchingTarget {
					break
				}
			}
			if !hasMatchingTarget {
				continue
			}
		}

		log.Printf("Processing module: %s", modulePath)

		// Create module-specific parser with chained variable environment
		moduleParser := NewConfigParser(
			cp.Platform,
			cp.Architecture,
			cp.Configuration,
			cp.CLIDefines,
			cp.ToolchainManager,
			cp.DefaultToolchain,
			cp.TemplateEngine,
			cp.Workspace,
			cp.PackageManager,
			workspaceVarEnv, // Chain to workspace environment
		)

		// Set current module for dependency resolution
		moduleParser.CurrentModule = modulePath
		moduleParser.TargetRegistry = cp.TargetRegistry
		moduleParser.TaskIDGen = NewTaskIDGenerator(modulePath)

		// Set config file directory for base_dir resolution
		moduleParser.ConfigFileDir = filepath.Dir(moduleInfo.Path)

		// Merge workspace root config into module config
		moduleConfig := make(map[string]any)
		for k, v := range moduleInfo.Config {
			moduleConfig[k] = v
		}

		// Inherit environment section from workspace if module doesn't have one
		if workspaceEnv, ok := cp.Workspace.Config.RawConfig["environment"].(map[string]any); ok {
			if _, hasEnv := moduleConfig["environment"]; !hasEnv {
				moduleConfig["environment"] = workspaceEnv
			} else if moduleEnv, ok := moduleConfig["environment"].(map[string]any); ok {
				// Merge: workspace environment as base, module environment overrides
				mergedEnv := make(map[string]any)
				for k, v := range workspaceEnv {
					mergedEnv[k] = v
				}
				for k, v := range moduleEnv {
					mergedEnv[k] = v
				}
				moduleConfig["environment"] = mergedEnv
			}
		}

		if workspaceConfig, ok := cp.Workspace.Config.RawConfig["config"].(map[string]any); ok {
			// Workspace config is the base, module config overrides
			mergedModuleConfig := make(map[string]any)
			for k, v := range workspaceConfig {
				mergedModuleConfig[k] = v
			}

			if moduleConfigSection, ok := moduleConfig["config"].(map[string]any); ok {
				// Merge lists (like defines, compiler_flags)
				for key, value := range moduleConfigSection {
					if valueList, ok := value.([]any); ok {
						if existingList, ok := mergedModuleConfig[key].([]any); ok {
							mergedModuleConfig[key] = append(existingList, valueList...)
						} else {
							mergedModuleConfig[key] = value
						}
					} else {
						mergedModuleConfig[key] = value
					}
				}
			}
			moduleConfig["config"] = mergedModuleConfig
		}

		// Generate tasks for this module
		moduleTasks, err := moduleParser.GenerateTasks(moduleConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to generate tasks for module %s: %w", modulePath, err)
		}

		allTasks = append(allTasks, moduleTasks...)
	}

	// Second pass: resolve cross-module dependencies
	allTasks = cp.resolveCrossModuleDependencies(allTasks)

	log.Printf("Generated %d tasks from %d modules", len(allTasks), len(cp.Workspace.Modules))
	return allTasks, nil
}

// resolveCrossModuleDependencies resolves cross-module dependencies
func (cp *ConfigParser) resolveCrossModuleDependencies(allTasks []*BuildTask) []*BuildTask {
	// Build a mapping of target names to their link task IDs
	targetToLinkTask := make(map[string]string)
	for _, task := range allTasks {
		// Link tasks have task_type='link' and task_id like "link_<target_name>_<counter>"
		if task.TaskType == "link" {
			baseID := stripModuleSuffix(task.TaskID)
			parts := strings.Split(baseID, "_")
			if len(parts) >= 3 && parts[0] == "link" {
				// Reconstruct target name (everything between 'link_' and the final '_<number>')
				targetName := strings.Join(parts[1:len(parts)-1], "_")
				targetToLinkTask[targetName] = task.TaskID
				log.Printf("Mapped target '%s' to link task '%s'", targetName, task.TaskID)
			}
		}
	}

	// Now resolve dependencies in all tasks
	for _, task := range allTasks {
		resolvedDeps := []string{}
		for _, dep := range task.Dependencies {
			// If it's already a task ID (starts with a task type), keep it
			baseDep := stripModuleSuffix(dep)
			if strings.HasPrefix(baseDep, "setup_") || strings.HasPrefix(baseDep, "compile_") || strings.HasPrefix(baseDep, "link_") {
				resolvedDeps = append(resolvedDeps, dep)
				continue
			}

			// Try to resolve as a target name
			if linkTaskID, ok := targetToLinkTask[dep]; ok {
				resolvedDeps = append(resolvedDeps, linkTaskID)
				log.Printf("Resolved dependency '%s' to link task '%s' in task '%s'", dep, linkTaskID, task.TaskID)
			} else {
				// Keep original if we can't resolve it
				log.Printf("WARNING: Could not resolve dependency '%s' in task '%s'", dep, task.TaskID)
				resolvedDeps = append(resolvedDeps, dep)
			}
		}

		task.Dependencies = resolvedDeps
	}

	return allTasks
}

func stripModuleSuffix(taskID string) string {
	if idx := strings.Index(taskID, "__"); idx != -1 {
		return taskID[:idx]
	}
	return taskID
}

// GenerateTasks generates tasks from configuration with variable resolution
func (cp *ConfigParser) GenerateTasks(config map[string]any) ([]*BuildTask, error) {
	tasks := []*BuildTask{}

	if cp.TaskIDGen == nil {
		moduleName := cp.CurrentModule
		if moduleName == "" {
			moduleName = "workspace"
		}
		cp.TaskIDGen = NewTaskIDGenerator(moduleName)
	}

	// Select and initialize toolchain
	tc, err := cp.selectToolchain(config)
	if err != nil {
		return nil, err
	}
	cp.CurrentToolchain = tc

	toolMatcher, err := NewToolMatcher(tc)
	if err != nil {
		return nil, err
	}
	cp.ToolMatcher = toolMatcher

	cp.CommandBuilder = NewCommandBuilder(tc, toolMatcher, cp.Configuration)
	cp.ExecEnv = CreateExecutionEnvironmentFromToolchain(tc)

	log.Printf("Using toolchain: %s (%s)", tc.Name, tc.Description)
	log.Printf("Execution environment: %s", tc.ExecutionType)

	// Build variable environment hierarchy
	// Priority (lowest to highest): workspace -> project -> platform -> arch -> config -> built-ins -> CLI

	// 1. Workspace-level variables
	cp.VarEnv.PushScope("workspace")
	
	// Process import_env_vars first (if present in variables section)
	if variablesSection, ok := config["variables"].(map[string]any); ok {
		if err := cp.VarEnv.ImportEnvVars(variablesSection, "buildy.yaml"); err != nil {
			return nil, err
		}
		// Extract platform-specific variables
		cp.VarEnv.ExtractPlatformVariables(variablesSection, cp.Platform, "variables")
	}
	
	cp.VarEnv.ExtractVariablesFromSection(config, "workspace")

	// Extract configuration sections
	project := make(map[string]any)
	if p, ok := config["project"].(map[string]any); ok {
		project = p
	}

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

	// 2. Project-level variables
	cp.VarEnv.PushScope("project")
	cp.VarEnv.ExtractVariablesFromSection(project, "project")

	// 3. Platform-specific variables
	platformConfig := make(map[string]any)
	if pc, ok := platforms[cp.Platform].(map[string]any); ok {
		platformConfig = pc
	}
	cp.VarEnv.PushScope(fmt.Sprintf("platform[%s]", cp.Platform))
	cp.VarEnv.ExtractVariablesFromSection(platformConfig, fmt.Sprintf("platform[%s]", cp.Platform))

	// 4. Architecture-specific variables
	archConfig := make(map[string]any)
	if ac, ok := architectures[cp.Architecture].(map[string]any); ok {
		archConfig = ac
	}
	cp.VarEnv.PushScope(fmt.Sprintf("architecture[%s]", cp.Architecture))
	cp.VarEnv.ExtractVariablesFromSection(archConfig, fmt.Sprintf("architecture[%s]", cp.Architecture))

	// 5. Configuration-specific variables
	configConfig := make(map[string]any)
	if cc, ok := configurations[cp.Configuration].(map[string]any); ok {
		configConfig = cc
	}
	cp.VarEnv.PushScope(fmt.Sprintf("configuration[%s]", cp.Configuration))
	cp.VarEnv.ExtractVariablesFromSection(configConfig, fmt.Sprintf("configuration[%s]", cp.Configuration))

	// 6. Built-in variables (highest priority except CLI)
	cp.VarEnv.PushScope("built-in")
	cp.VarEnv.SetVariable("platform", cp.Platform, "built-in")
	cp.VarEnv.SetVariable("arch", cp.Architecture, "built-in")
	cp.VarEnv.SetVariable("config", cp.Configuration, "built-in")

	projectName := "unknown"
	if name, ok := project["name"].(string); ok {
		projectName = name
	}
	cp.VarEnv.SetVariable("project_name", projectName, "built-in")

	projectVersion := "0.0.0"
	if version, ok := project["version"]; ok {
		projectVersion = fmt.Sprintf("%v", version)
	}
	cp.VarEnv.SetVariable("project_version", projectVersion, "built-in")

	// Also set base_dir early from output config (defaults to "build")
	baseDir := "build"
	if outputConfig, ok := config["output"].(map[string]any); ok {
		if bd, ok := outputConfig["base_dir"].(string); ok {
			baseDir = bd
		}
	}
	cp.VarEnv.SetVariable("base_dir", baseDir, "output.base_dir")

	// 7. CLI-provided overrides (highest priority)
	if len(cp.CLIDefines) > 0 {
		cp.VarEnv.PushScope("cli")
		for name, value := range cp.CLIDefines {
			cp.VarEnv.SetVariable(name, value, "cli")
		}
	}

	// Now resolve all variables in the config before proceeding
	errors := []string{}
	resolvedConfigRaw := cp.VarEnv.ResolveRecursive(config, &errors)

	// Check for unresolved variables and fail if any found
	if len(errors) > 0 {
		log.Printf("ERROR: Unresolved variables found in configuration:")
		for _, errMsg := range errors {
			log.Printf("  - %s", errMsg)
		}
		return nil, fmt.Errorf("configuration contains %d unresolved variable(s)", len(errors))
	}

	// Type assert resolved config
	resolvedConfig, ok := resolvedConfigRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("resolved config is not a map")
	}

	// Re-extract sections from resolved config
	if p, ok := resolvedConfig["project"].(map[string]any); ok {
		project = p
	}
	if gc, ok := resolvedConfig["config"].(map[string]any); ok {
		globalConfig = gc
	}
	if p, ok := resolvedConfig["platforms"].(map[string]any); ok {
		platforms = p
	}
	if a, ok := resolvedConfig["architectures"].(map[string]any); ok {
		architectures = a
	}
	if c, ok := resolvedConfig["configurations"].(map[string]any); ok {
		configurations = c
	}

	// NEW DSL: Also extract from environment section
	if env, ok := resolvedConfig["environment"].(map[string]any); ok {
		// environment.compile -> merge into globalConfig
		if compile, ok := env["compile"].(map[string]any); ok {
			for k, v := range compile {
				globalConfig[k] = v
			}
		}
		// environment.configurations -> merge into configurations
		if envConfigs, ok := env["configurations"].(map[string]any); ok {
			for configName, configData := range envConfigs {
				if configMap, ok := configData.(map[string]any); ok {
					if _, exists := configurations[configName]; !exists {
						configurations[configName] = configMap
					} else if existingMap, ok := configurations[configName].(map[string]any); ok {
						// Merge: environment.configurations overrides top-level configurations
						for k, v := range configMap {
							existingMap[k] = v
						}
					}
				}
			}
		}
	}

	// Merge configuration hierarchy
	mergedConfig := cp.mergeConfigs(
		globalConfig,
		platforms[cp.Platform],
		architectures[cp.Architecture],
		configurations[cp.Configuration],
	)

	// Generate setup task
	outputDir, err := cp.getOutputDir(resolvedConfig)
	if err != nil {
		return nil, err
	}

	setupTask := cp.createSetupTask(outputDir)
	tasks = append(tasks, &setupTask)

	// Generate library tasks - support both old and new formats
	libraries := []any{}
	
	// New format: targets.libraries
	if targets, ok := resolvedConfig["targets"].(map[string]any); ok {
		if libs, ok := targets["libraries"].([]any); ok {
			libraries = append(libraries, libs...)
		}
	}
	
	// Legacy format: library (single or list)
	if library, ok := resolvedConfig["library"]; ok {
		switch v := library.(type) {
		case map[string]any:
			libraries = append(libraries, v)
		case []any:
			libraries = append(libraries, v...)
		}
	}

	for _, libRaw := range libraries {
		if lib, ok := libRaw.(map[string]any); ok {
			libTasks, err := cp.generateLibraryTasks(lib, mergedConfig, outputDir, setupTask.TaskID)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, libTasks...)
		}
	}

	// Generate executable tasks - support both old and new formats
	executables := []any{}
	
	// New format: targets.executables
	if targets, ok := resolvedConfig["targets"].(map[string]any); ok {
		if exes, ok := targets["executables"].([]any); ok {
			executables = append(executables, exes...)
		}
	}
	
	// Legacy format: executable (single or list)
	if executable, ok := resolvedConfig["executable"]; ok {
		switch v := executable.(type) {
		case map[string]any:
			executables = append(executables, v)
		case []any:
			executables = append(executables, v...)
		}
	}

	for _, exeRaw := range executables {
		if exe, ok := exeRaw.(map[string]any); ok {
			exeTasks, err := cp.generateExecutableTasks(exe, mergedConfig, outputDir, setupTask.TaskID, tasks)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, exeTasks...)
		}
	}

	// Generate Go module tasks
	if targets, ok := resolvedConfig["targets"].(map[string]any); ok {
		if goModules, ok := targets["go_modules"].([]any); ok {
			for _, goModRaw := range goModules {
				if goMod, ok := goModRaw.(map[string]any); ok {
					goTasks, err := cp.generateGoModuleTasks(goMod, mergedConfig, outputDir, setupTask.TaskID)
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

// mergeConfigs merges configuration hierarchy
func (cp *ConfigParser) mergeConfigs(configs ...any) map[string]any {
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

// getOutputDir gets output directory pattern and resolves template variables
func (cp *ConfigParser) getOutputDir(config map[string]any) (string, error) {
	outputConfig := make(map[string]any)
	if oc, ok := config["output"].(map[string]any); ok {
		outputConfig = oc
	}

	pattern := fmt.Sprintf("${base_dir}/%s-%s-%s", cp.Platform, cp.Architecture, cp.Configuration)
	if p, ok := outputConfig["pattern"].(string); ok {
		pattern = p
	}

	// Resolve all variables in the pattern
	errors := []string{}
	resolvedPattern := cp.VarEnv.ResolveString(pattern, &errors, 10)

	if len(errors) > 0 {
		return "", fmt.Errorf("output pattern contains unresolved variables: %v", errors)
	}

	// In workspace mode, make paths absolute relative to workspace root
	if cp.Workspace != nil && !filepath.IsAbs(resolvedPattern) {
		resolvedPattern = filepath.Join(cp.Workspace.RootDir, resolvedPattern)
	}

	log.Printf("Output directory pattern '%s' resolved to '%s'", pattern, resolvedPattern)
	return resolvedPattern, nil
}

// createSetupTask creates directory setup task
func (cp *ConfigParser) createSetupTask(outputDir string) BuildTask {
	// Build platform-appropriate mkdir command
	libDir := filepath.Join(outputDir, "lib")
	binDir := filepath.Join(outputDir, "bin")
	objDir := filepath.Join(outputDir, "obj")

	var command string
	if strings.Contains(strings.ToLower(cp.Platform), "windows") {
		// Windows: use cmd /c mkdir (creates parent dirs automatically)
		command = fmt.Sprintf("cmd /c mkdir %s 2>nul & mkdir %s 2>nul & mkdir %s 2>nul",
			libDir, binDir, objDir)
	} else {
		// Unix/Mac: use mkdir -p
		command = fmt.Sprintf("mkdir -p %s %s %s", libDir, binDir, objDir)
	}

	taskID := cp.TaskIDGen.Next("setup", "dirs")
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
	task.Platform = cp.Platform
	task.Architecture = cp.Architecture
	task.Configuration = cp.Configuration
	task.EstimatedTime = DefaultSetupTimeSeconds
	task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 10, DiskMB: 1}
	task.CacheKey = task.CalculateCacheKey()
	return task
}

// resolvePackages resolves package dependencies and merges their settings into the target config
func (cp *ConfigParser) resolvePackages(targetConfig map[string]any) error {
	if cp.PackageManager == nil {
		return nil // No package manager, skip
	}

	// Get packages list from target config
	// Preferred format: packages: [...] at target level
	// Legacy format: depends_on.packages: [...] (deprecated)
	var packageNames []string
	
	// Preferred format: packages at target level
	if packages, ok := targetConfig["packages"]; ok {
		switch v := packages.(type) {
		case string:
			packageNames = append(packageNames, v)
		case []any:
			for _, item := range v {
				if str, ok := item.(string); ok {
					packageNames = append(packageNames, str)
				}
			}
		}
	}
	
	// Legacy format: depends_on.packages (still supported for backwards compatibility)
	if dependsOn, ok := targetConfig["depends_on"].(map[string]any); ok {
		if packages, ok := dependsOn["packages"]; ok {
			switch v := packages.(type) {
			case string:
				packageNames = append(packageNames, v)
			case []any:
				for _, item := range v {
					if str, ok := item.(string); ok {
						packageNames = append(packageNames, str)
					}
				}
			}
		}
	}

	if len(packageNames) == 0 {
		return nil // No packages to resolve
	}

	// Get workspace package configurations
	var workspacePackages map[string]map[string]string
	if cp.Workspace != nil && cp.Workspace.Config != nil {
		workspacePackages = cp.Workspace.Config.Packages
	}

	// Resolve packages
	mergedPackage, err := cp.PackageManager.ResolvePackages(
		packageNames,
		cp.Platform,
		cp.Architecture,
		workspacePackages,
	)
	if err != nil {
		return fmt.Errorf("failed to resolve packages: %w", err)
	}

	// Merge package settings into target config
	if len(mergedPackage.IncludeDirs) > 0 {
		existingIncludes := []string{}
		if inc, ok := targetConfig["include_dirs"]; ok {
			switch v := inc.(type) {
			case string:
				existingIncludes = []string{v}
			case []any:
				for _, item := range v {
					if str, ok := item.(string); ok {
						existingIncludes = append(existingIncludes, str)
					}
				}
			case []string:
				existingIncludes = v
			case map[string]any:
				// Handle new format with public/private subsections
				if publicDirs, ok := v["public"].([]any); ok {
					for _, item := range publicDirs {
						if str, ok := item.(string); ok {
							existingIncludes = append(existingIncludes, str)
						}
					}
				}
				if privateDirs, ok := v["private"].([]any); ok {
					for _, item := range privateDirs {
						if str, ok := item.(string); ok {
							existingIncludes = append(existingIncludes, str)
						}
					}
				}
			}
		}
		// Prepend package includes, then add target includes
		targetConfig["include_dirs"] = append(mergedPackage.IncludeDirs, existingIncludes...)
	}

	if len(mergedPackage.LibDirs) > 0 {
		existingLibDirs := []string{}
		if ld, ok := targetConfig["lib_dirs"]; ok {
			switch v := ld.(type) {
			case string:
				existingLibDirs = []string{v}
			case []any:
				for _, item := range v {
					if str, ok := item.(string); ok {
						existingLibDirs = append(existingLibDirs, str)
					}
				}
			case []string:
				existingLibDirs = v
			}
		}
		targetConfig["lib_dirs"] = append(mergedPackage.LibDirs, existingLibDirs...)
	}

	if len(mergedPackage.Libs) > 0 {
		existingLibs := []string{}
		if libs, ok := targetConfig["libs"]; ok {
			switch v := libs.(type) {
			case string:
				existingLibs = []string{v}
			case []any:
				for _, item := range v {
					if str, ok := item.(string); ok {
						existingLibs = append(existingLibs, str)
					}
				}
			case []string:
				existingLibs = v
			}
		}
		targetConfig["libs"] = append(mergedPackage.Libs, existingLibs...)
	}

	if len(mergedPackage.Frameworks) > 0 {
		existingFrameworks := []string{}
		if frameworks, ok := targetConfig["frameworks"]; ok {
			switch v := frameworks.(type) {
			case string:
				existingFrameworks = []string{v}
			case []any:
				for _, item := range v {
					if str, ok := item.(string); ok {
						existingFrameworks = append(existingFrameworks, str)
					}
				}
			case []string:
				existingFrameworks = v
			}
		}
		targetConfig["frameworks"] = append(mergedPackage.Frameworks, existingFrameworks...)
	}

	if len(mergedPackage.Defines) > 0 {
		existingDefines := []string{}
		if defines, ok := targetConfig["defines"]; ok {
			switch v := defines.(type) {
			case string:
				existingDefines = []string{v}
			case []any:
				for _, item := range v {
					if str, ok := item.(string); ok {
						existingDefines = append(existingDefines, str)
					}
				}
			case []string:
				existingDefines = v
			}
		}
		targetConfig["defines"] = append(mergedPackage.Defines, existingDefines...)
	}

	if len(mergedPackage.Sources) > 0 {
		existingSources := []string{}
		if sources, ok := targetConfig["sources"]; ok {
			switch v := sources.(type) {
			case string:
				existingSources = []string{v}
			case []any:
				for _, item := range v {
					if str, ok := item.(string); ok {
						existingSources = append(existingSources, str)
					}
				}
			case []string:
				existingSources = v
			}
		}
		targetConfig["sources"] = append(mergedPackage.Sources, existingSources...)
	}

	return nil
}

// generateLibraryTasks generates tasks for a library using template engine
func (cp *ConfigParser) generateLibraryTasks(
	libConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
) ([]*BuildTask, error) {
	// Determine library type (default to shared_library)
	libType := "shared_library"
	if lt, ok := libConfig["type"].(string); ok {
		libType = lt
	}

	// Resolve packages first
	if err := cp.resolvePackages(libConfig); err != nil {
		return nil, err
	}

	// Expand glob patterns in sources and resolve paths
	sources, err := cp.resolveSources(libConfig)
	if err != nil {
		return nil, err
	}
	libConfig["sources"] = sources

	// Process include directories
	includeDirs, err := cp.resolveIncludeDirs(libConfig)
	if err != nil {
		return nil, err
	}
	libConfig["include_dirs"] = includeDirs

	// Use template engine to generate tasks
	tasks, err := cp.TemplateEngine.ExpandTemplate(
		libType,
		libConfig,
		mergedConfig,
		outputDir,
		setupTaskID,
		cp.TaskIDGen,
		cp.ToolMatcher,
		cp.CommandBuilder,
		cp.Platform,
		cp.Architecture,
		cp.Configuration,
		[]*BuildTask{},
	)
	if err != nil {
		return nil, err
	}

	return tasks, nil
}

// generateExecutableTasks generates tasks for an executable using template engine
func (cp *ConfigParser) generateExecutableTasks(
	exeConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
	existingTasks []*BuildTask,
) ([]*BuildTask, error) {
	// Determine executable type (default to executable)
	exeType := "executable"
	if et, ok := exeConfig["type"].(string); ok {
		exeType = et
	}

	// Resolve packages first
	if err := cp.resolvePackages(exeConfig); err != nil {
		return nil, err
	}

	// Expand glob patterns in sources and resolve paths
	sources, err := cp.resolveSources(exeConfig)
	if err != nil {
		return nil, err
	}
	exeConfig["sources"] = sources

	// Process include directories
	includeDirs, err := cp.resolveIncludeDirs(exeConfig)
	if err != nil {
		return nil, err
	}
	exeConfig["include_dirs"] = includeDirs

	// Use template engine to generate tasks
	tasks, err := cp.TemplateEngine.ExpandTemplate(
		exeType,
		exeConfig,
		mergedConfig,
		outputDir,
		setupTaskID,
		cp.TaskIDGen,
		cp.ToolMatcher,
		cp.CommandBuilder,
		cp.Platform,
		cp.Architecture,
		cp.Configuration,
		existingTasks,
	)
	if err != nil {
		return nil, err
	}

	return tasks, nil
}

// resolveSources resolves source file paths relative to config file directory
func (cp *ConfigParser) resolveSources(itemConfig map[string]any) ([]string, error) {
	sourcesRaw, ok := itemConfig["sources"]
	if !ok {
		return []string{}, nil
	}

	var sources []string

	switch v := sourcesRaw.(type) {
	case string:
		// Expand glob pattern
		sources = cp.expandGlob(v)
	case []any:
		// Resolve individual source paths
		for _, srcRaw := range v {
			if src, ok := srcRaw.(string); ok {
				if cp.ConfigFileDir != "" {
					absSrcPath := filepath.Join(cp.ConfigFileDir, src)
					cwd, _ := os.Getwd()
					relSrcPath, err := filepath.Rel(cwd, absSrcPath)
					if err != nil {
						sources = append(sources, absSrcPath)
					} else {
						sources = append(sources, relSrcPath)
					}
				} else {
					sources = append(sources, src)
				}
			}
		}
	case []string:
		for _, src := range v {
			if cp.ConfigFileDir != "" {
				absSrcPath := filepath.Join(cp.ConfigFileDir, src)
				cwd, _ := os.Getwd()
				relSrcPath, err := filepath.Rel(cwd, absSrcPath)
				if err != nil {
					sources = append(sources, absSrcPath)
				} else {
					sources = append(sources, relSrcPath)
				}
			} else {
				sources = append(sources, src)
			}
		}
	}

	return sources, nil
}

// resolveIncludeDirs resolves include directory paths relative to config file directory
func (cp *ConfigParser) resolveIncludeDirs(itemConfig map[string]any) ([]string, error) {
	includeDirsRaw, ok := itemConfig["include_dirs"]
	if !ok {
		return []string{}, nil
	}

	var includeDirs []string
	
	// Helper to resolve a single path
	resolvePath := func(inc string) string {
		// Skip absolute paths (start with / or have a drive letter on Windows)
		if filepath.IsAbs(inc) {
			return inc
		}
		// Only resolve relative paths with the config file directory
		if cp.ConfigFileDir != "" && cp.ConfigFileDir != "." {
			absIncPath := filepath.Join(cp.ConfigFileDir, inc)
			cwd, _ := os.Getwd()
			relIncPath, err := filepath.Rel(cwd, absIncPath)
			if err != nil {
				return absIncPath
			}
			return relIncPath
		}
		return inc
	}
	
	// Helper to extract paths from a value
	extractPaths := func(value any) []string {
		result := []string{}
		switch v := value.(type) {
		case string:
			result = append(result, resolvePath(v))
		case []any:
			for _, item := range v {
				if str, ok := item.(string); ok {
					result = append(result, resolvePath(str))
				}
			}
		case []string:
			for _, str := range v {
				result = append(result, resolvePath(str))
			}
		}
		return result
	}

	switch v := includeDirsRaw.(type) {
	case map[string]any:
		// New format with public/private subsections
		if publicDirs, ok := v["public"]; ok {
			includeDirs = append(includeDirs, extractPaths(publicDirs)...)
		}
		if privateDirs, ok := v["private"]; ok {
			includeDirs = append(includeDirs, extractPaths(privateDirs)...)
		}
	case []any:
		// Legacy format: simple list
		includeDirs = extractPaths(v)
	case []string:
		// Legacy format: simple list
		includeDirs = extractPaths(v)
	}

	return includeDirs, nil
}

// generateGoModuleTasks generates tasks for a Go module
func (cp *ConfigParser) generateGoModuleTasks(
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

	// Get the module path (directory containing go.mod)
	modulePath := "."
	if p, ok := goModConfig["path"].(string); ok {
		modulePath = p
	}

	// Resolve path relative to config file directory
	if cp.ConfigFileDir != "" {
		modulePath = filepath.Join(cp.ConfigFileDir, modulePath)
	}

	// Get output path
	output := filepath.Join(outputDir, "bin", name)
	if o, ok := goModConfig["output"].(string); ok {
		output = o
		if !filepath.IsAbs(output) {
			output = filepath.Join(outputDir, output)
		}
	}

	// Add .exe extension on Windows
	if cp.Platform == "windows" && !strings.HasSuffix(output, ".exe") {
		output += ".exe"
	}

	// Build the go build command
	cmdParts := []string{"go", "build", "-o", output}

	// Add build tags if specified
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

	// Add ldflags based on configuration
	ldflags := []string{}
	if cp.Configuration == "release" {
		ldflags = append(ldflags, "-s", "-w") // Strip debug info
	}

	// Add custom ldflags
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

	// Add gcflags for debug builds
	if cp.Configuration == "debug" {
		cmdParts = append(cmdParts, "-gcflags", "all=-N -l")
	}

	// Add trimpath for release builds
	if cp.Configuration == "release" {
		cmdParts = append(cmdParts, "-trimpath")
	}

	// Add the package path (default to current directory)
	cmdParts = append(cmdParts, "./...")

	command := strings.Join(cmdParts, " ")

	// Find go.mod file as input
	goModFile := filepath.Join(modulePath, "go.mod")
	inputs := []TaskInput{}
	if _, err := os.Stat(goModFile); err == nil {
		inputs = append(inputs, NewTaskInput(goModFile))
	}

	// Create the build task
	taskID := cp.TaskIDGen.Next("go_build", name)
	task := NewBuildTask(
		taskID,
		"go_build",
		inputs,
		[]string{output},
		[]string{setupTaskID},
		command,
	)
	task.Platform = cp.Platform
	task.Architecture = cp.Architecture
	task.Configuration = cp.Configuration
	task.EstimatedTime = 10.0 // Go builds can take a while
	task.ResourceRequirements = ResourceRequirements{CPUCores: 2, MemoryMB: 500, DiskMB: 100}
	task.CacheKey = task.CalculateCacheKey()

	tasks = append(tasks, &task)

	log.Printf("Generated Go build task: %s -> %s", name, output)
	return tasks, nil
}

// expandGlob expands glob pattern to sorted file list
func (cp *ConfigParser) expandGlob(pattern string) []string {
	var searchPattern string
	if cp.ConfigFileDir == "" {
		log.Printf("WARNING: Config file directory not set, using current directory for pattern '%s'", pattern)
		searchPattern = pattern
	} else {
		// Make pattern relative to config file directory
		searchPattern = filepath.Join(cp.ConfigFileDir, pattern)
		log.Printf("Expanding pattern '%s' as '%s'", pattern, searchPattern)
	}

	results, err := filepath.Glob(searchPattern)
	if err != nil {
		log.Printf("ERROR: Error expanding glob '%s': %v", pattern, err)
		return []string{}
	}

	if len(results) == 0 {
		log.Printf("WARNING: Glob pattern '%s' (resolved to '%s') matched no files", pattern, searchPattern)
		return []string{}
	}

	// Convert absolute paths back to relative paths from current directory
	cwd, _ := os.Getwd()
	relativeResults := []string{}
	for _, result := range results {
		relPath, err := filepath.Rel(cwd, result)
		if err != nil {
			// If it's not relative to cwd, use absolute path
			absPath, _ := filepath.Abs(result)
			relativeResults = append(relativeResults, absPath)
		} else {
			relativeResults = append(relativeResults, relPath)
		}
	}

	sort.Strings(relativeResults)
	log.Printf("Pattern '%s' matched %d files: %v", pattern, len(relativeResults), relativeResults)
	return relativeResults
}
