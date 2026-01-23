package workspace

import (
	"fmt"
	"log"
	"path/filepath"

	"buildy/internal/resource"
	"buildy/pkg/util"
)

// ConfigParser orchestrates configuration parsing and task generation
// It delegates to specialized components for parsing, path resolution, and task generation
type ConfigParser struct {
	Platform         string
	Architecture     string
	Configuration    string
	CLIDefines       map[string]string
	VarEnv           *util.VariableEnvironment
	YAMLParser       *YAMLParser
	PathResolver     *PathResolver
	TaskGenerator    *TaskGenerator
	ToolchainManager *resource.ToolchainManager
	DefaultToolchain string
	TemplateEngine   *BuildTemplateEngine
	CurrentToolchain *resource.ToolchainConfig
	ToolchainHash    string
	ToolMatcher      *resource.ToolMatcher
	CommandBuilder   *resource.CommandBuilder
	ExecEnv          *util.ExecutionEnvironment
	Workspace        *Workspace
	TargetRegistry   *TargetRegistry
	CurrentModule    string
	PackageManager   *resource.PackageManager
	TaskIDGen        *TaskIDGenerator
	ConfigFileDir    string
}

// NewConfigParser creates a new ConfigParser
func NewConfigParser(
	platform, architecture, configuration string,
	cliDefines map[string]string,
	toolchainManager *resource.ToolchainManager,
	defaultToolchain string,
	templateEngine *BuildTemplateEngine,
	workspace *Workspace,
	packageManager *resource.PackageManager,
	parentVarEnv *util.VariableEnvironment,
) *ConfigParser {
	varEnv := util.NewVariableEnvironment(parentVarEnv)

	return &ConfigParser{
		Platform:         platform,
		Architecture:     architecture,
		Configuration:    configuration,
		CLIDefines:       cliDefines,
		VarEnv:           varEnv,
		YAMLParser:       NewYAMLParser(),
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
	config, err := cp.YAMLParser.ParseFile(configFile)
	if err != nil {
		return nil, err
	}

	cp.ConfigFileDir = cp.YAMLParser.ConfigFileDir

	// Validate configuration
	validationErrors := cp.YAMLParser.ValidateConfig(config)
	if len(validationErrors) > 0 {
		for _, errMsg := range validationErrors {
			log.Printf("ERROR: Config validation error: %s", errMsg)
		}
		return nil, fmt.Errorf("configuration failed validation with %d error(s)", len(validationErrors))
	}

	return config, nil
}

// selectToolchain selects toolchain based on hierarchical configuration
func (cp *ConfigParser) SelectToolchain(config map[string]any) (*resource.ToolchainConfig, error) {
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
		util.BuildWarning("config", "CLI toolchain '%s' not found, falling back", cp.DefaultToolchain)
	}

	// Check project-level toolchain
	if project, ok := config["project"].(map[string]any); ok {
		if projectToolchain, ok := project["toolchain"].(string); ok {
			tc := cp.ToolchainManager.GetToolchain(projectToolchain)
			if tc != nil {
				log.Printf("Using project-specified toolchain: %s", tc.Name)
				return tc, nil
			}
			util.BuildWarning("config", "Project toolchain '%s' not found, falling back", projectToolchain)
		}
	}

	// Check workspace-level toolchain
	if workspaceToolchain, ok := config["toolchain"].(string); ok {
		tc := cp.ToolchainManager.GetToolchain(workspaceToolchain)
		if tc != nil {
			log.Printf("Using workspace-specified toolchain: %s", tc.Name)
			return tc, nil
		}
		util.BuildWarning("config", "Workspace toolchain '%s' not found, falling back", workspaceToolchain)
	}

	// Check environment.toolchains structure (hierarchical: platform-specific, then default)
	if env, ok := config["environment"].(map[string]any); ok {
		if toolchains, ok := env["toolchains"].(map[string]any); ok {
			// Check platform-specific first
			if platformToolchain, ok := toolchains[cp.Platform].(string); ok {
				tc := cp.ToolchainManager.GetToolchain(platformToolchain)
				if tc != nil {
					log.Printf("Using platform-specified toolchain: %s", tc.Name)
					return tc, nil
				}
				util.BuildWarning("config", "Platform toolchain '%s' not found, falling back", platformToolchain)
			}
			// Check default
			if defaultToolchain, ok := toolchains["default"].(string); ok {
				tc := cp.ToolchainManager.GetToolchain(defaultToolchain)
				if tc != nil {
					log.Printf("Using default toolchain: %s", tc.Name)
					return tc, nil
				}
				util.BuildWarning("config", "Default toolchain '%s' not found, falling back", defaultToolchain)
			}
		}
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
	workspaceVarEnv := util.NewVariableEnvironment(nil)
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
			workspaceVarEnv,
		)

		moduleParser.CurrentModule = modulePath
		moduleParser.TargetRegistry = cp.TargetRegistry
		moduleParser.TaskIDGen = NewTaskIDGenerator(modulePath)
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
			mergedModuleConfig := make(map[string]any)
			for k, v := range workspaceConfig {
				mergedModuleConfig[k] = v
			}

			if moduleConfigSection, ok := moduleConfig["config"].(map[string]any); ok {
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

// resolveCrossModuleDependencies resolves cross-module dependencies using the global task registry
func (cp *ConfigParser) resolveCrossModuleDependencies(allTasks []*BuildTask) []*BuildTask {
	// First pass: ensure all link tasks are registered in the global registry
	// (they should already be registered during generation, but this is a safety net)
	for _, task := range allTasks {
		if task.TaskType == "link" {
			// Check if already registered
			if targetName, ok := globalTaskRegistry.GetTargetName(task.TaskID); ok {
				log.Printf("Target '%s' already registered -> link task '%s'", targetName, task.TaskID)
			}
		}
	}

	// Now resolve dependencies in all tasks using the registry
	for _, task := range allTasks {
		resolvedDeps := []string{}
		for _, dep := range task.Dependencies {
			// Try to resolve using the registry
			if resolvedID, ok := globalTaskRegistry.ResolveDependency(dep); ok {
				if resolvedID != dep {
					log.Printf("Resolved dependency '%s' to link task '%s' in task '%s'", dep, resolvedID, task.TaskID)
				}
				resolvedDeps = append(resolvedDeps, resolvedID)
			} else {
			util.BuildWarning("dependency", "Could not resolve dependency '%s' in task '%s'", dep, task.TaskID)
			resolvedDeps = append(resolvedDeps, dep)
			}
		}

		task.Dependencies = resolvedDeps
	}

	return allTasks
}

// GenerateTasks generates tasks from configuration with variable resolution
func (cp *ConfigParser) GenerateTasks(config map[string]any) ([]*BuildTask, error) {
	if cp.TaskIDGen == nil {
		moduleName := cp.CurrentModule
		if moduleName == "" {
			moduleName = "workspace"
		}
		cp.TaskIDGen = NewTaskIDGenerator(moduleName)
	}

	// Select and initialize toolchain
	tc, err := cp.SelectToolchain(config)
	if err != nil {
		return nil, err
	}
	cp.CurrentToolchain = tc
	cp.ToolchainHash = resource.HashToolchainConfig(tc)

	toolMatcher, err := resource.NewToolMatcher(tc)
	if err != nil {
		return nil, err
	}
	cp.ToolMatcher = toolMatcher
	cp.CommandBuilder = resource.NewCommandBuilder(tc, toolMatcher, cp.Configuration)
	cp.ExecEnv = resource.CreateExecutionEnvironmentFromToolchain(tc)

	log.Printf("Using toolchain: %s (%s)", tc.Name, tc.Description)
	log.Printf("Execution environment: %s", tc.ExecutionType)

	// Build variable environment
	if err := cp.setupVariableEnvironment(config); err != nil {
		return nil, err
	}

	// Resolve all variables in the config
	errors := []string{}
	resolvedConfigRaw := cp.VarEnv.ResolveRecursive(config, &errors)
	if len(errors) > 0 {
		log.Printf("ERROR: Unresolved variables found in configuration:")
		for _, errMsg := range errors {
			log.Printf("  - %s", errMsg)
		}
		return nil, fmt.Errorf("configuration contains %d unresolved variable(s)", len(errors))
	}

	resolvedConfig, ok := resolvedConfigRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("resolved config is not a map")
	}

	// Get output directory
	outputDir, err := cp.getOutputDir(resolvedConfig)
	if err != nil {
		return nil, err
	}

	// Create path resolver
	cp.PathResolver = NewPathResolver(cp.ConfigFileDir, "")
	if cp.Workspace != nil {
		cp.PathResolver.WorkspaceRoot = cp.Workspace.RootDir
	}

	// Create task generator
	taskGen := NewTaskGenerator(
		cp.Platform,
		cp.Architecture,
		cp.Configuration,
		cp.ToolchainManager,
		cp.TemplateEngine,
		cp.PackageManager,
		cp.VarEnv,
		cp.Workspace,
	)
	taskGen.SetPathResolver(cp.PathResolver)
	taskGen.SetToolchain(tc)
	taskGen.TaskIDGen = cp.TaskIDGen
	taskGen.CurrentModule = cp.CurrentModule
	taskGen.TargetRegistry = cp.TargetRegistry

	// Generate tasks
	return taskGen.GenerateTasks(resolvedConfig, outputDir)
}

// setupVariableEnvironment builds the variable environment hierarchy
func (cp *ConfigParser) setupVariableEnvironment(config map[string]any) error {
	// Extract sections
	project := cp.YAMLParser.ExtractSection(config, "project")
	platforms := cp.YAMLParser.ExtractSection(config, "platforms")
	architectures := cp.YAMLParser.ExtractSection(config, "architectures")
	configurations := cp.YAMLParser.ExtractSection(config, "configurations")

	// 1. Workspace-level variables
	cp.VarEnv.PushScope("workspace")

	if variablesSection, ok := config["variables"].(map[string]any); ok {
		if err := cp.VarEnv.ImportEnvVars(variablesSection, "buildy.yaml"); err != nil {
			return err
		}
		cp.VarEnv.ExtractPlatformVariables(variablesSection, cp.Platform, "variables")
	}

	cp.VarEnv.ExtractVariablesFromSection(config, "workspace")

	// 2. Project-level variables
	cp.VarEnv.PushScope("project")
	cp.VarEnv.ExtractVariablesFromSection(project, "project")

	// 3. Platform-specific variables
	platformConfig := cp.YAMLParser.ExtractSection(platforms, cp.Platform)
	cp.VarEnv.PushScope(fmt.Sprintf("platform[%s]", cp.Platform))
	cp.VarEnv.ExtractVariablesFromSection(platformConfig, fmt.Sprintf("platform[%s]", cp.Platform))

	// 4. Architecture-specific variables
	archConfig := cp.YAMLParser.ExtractSection(architectures, cp.Architecture)
	cp.VarEnv.PushScope(fmt.Sprintf("architecture[%s]", cp.Architecture))
	cp.VarEnv.ExtractVariablesFromSection(archConfig, fmt.Sprintf("architecture[%s]", cp.Architecture))

	// 5. Configuration-specific variables
	configConfig := cp.YAMLParser.ExtractSection(configurations, cp.Configuration)
	cp.VarEnv.PushScope(fmt.Sprintf("configuration[%s]", cp.Configuration))
	cp.VarEnv.ExtractVariablesFromSection(configConfig, fmt.Sprintf("configuration[%s]", cp.Configuration))

	// 6. Built-in variables
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

	baseDir := "build"
	if outputConfig, ok := config["output"].(map[string]any); ok {
		if bd, ok := outputConfig["base_dir"].(string); ok {
			baseDir = bd
		}
	}
	cp.VarEnv.SetVariable("base_dir", baseDir, "output.base_dir")

	// 7. CLI-provided overrides
	if len(cp.CLIDefines) > 0 {
		cp.VarEnv.PushScope("cli")
		for name, value := range cp.CLIDefines {
			cp.VarEnv.SetVariable(name, value, "cli")
		}
	}

	return nil
}

// getOutputDir gets output directory pattern and resolves template variables
func (cp *ConfigParser) getOutputDir(config map[string]any) (string, error) {
	outputConfig := cp.YAMLParser.ExtractSection(config, "output")

	pattern := fmt.Sprintf("${base_dir}/%s-%s-%s", cp.Platform, cp.Architecture, cp.Configuration)
	if p, ok := outputConfig["pattern"].(string); ok {
		pattern = p
	}

	errors := []string{}
	resolvedPattern := cp.VarEnv.ResolveString(pattern, &errors, 10)

	if len(errors) > 0 {
		return "", fmt.Errorf("output pattern contains unresolved variables: %v", errors)
	}

	if cp.Workspace != nil && !filepath.IsAbs(resolvedPattern) {
		resolvedPattern = filepath.Join(cp.Workspace.RootDir, resolvedPattern)
	}

	log.Printf("Output directory pattern '%s' resolved to '%s'", pattern, resolvedPattern)
	return resolvedPattern, nil
}
