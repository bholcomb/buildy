package workspace

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"buildy/internal/resource"
	"buildy/pkg/util"
)

// TaskGenerator generates build tasks from configuration
type TaskGenerator struct {
	Platform         string
	Architecture     string
	Configuration    string
	ToolchainManager *resource.ToolchainManager
	TemplateEngine   *BuildTemplateEngine
	CurrentToolchain *resource.ToolchainConfig
	ToolchainHash    string
	ToolMatcher      *resource.ToolMatcher
	CommandBuilder   *resource.CommandBuilder
	ExecEnv          *util.ExecutionEnvironment
	PackageManager   *resource.PackageManager
	VarEnv           *util.VariableEnvironment
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
	toolchainManager *resource.ToolchainManager,
	templateEngine *BuildTemplateEngine,
	packageManager *resource.PackageManager,
	varEnv *util.VariableEnvironment,
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
func (tg *TaskGenerator) SetToolchain(tc *resource.ToolchainConfig) error {
	tg.CurrentToolchain = tc
	tg.ToolchainHash = resource.HashToolchainConfig(tc)

	toolMatcher, err := resource.NewToolMatcher(tc)
	if err != nil {
		return err
	}
	tg.ToolMatcher = toolMatcher
	tg.CommandBuilder = resource.NewCommandBuilder(tc, toolMatcher, tg.Configuration)
	tg.ExecEnv = resource.CreateExecutionEnvironmentFromToolchain(tc)

	return nil
}

// generateTargetTasks is the unified task generation function that uses template metadata
// to drive all pre-processing, template expansion, and post-processing
func (tg *TaskGenerator) generateTargetTasks(
	targetConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
	existingTasks []*BuildTask,
) ([]*BuildTask, error) {
	// Require explicit language field
	language, ok := targetConfig["language"].(string)
	if !ok || language == "" {
		targetName := "unknown"
		if name, ok := targetConfig["name"].(string); ok {
			targetName = name
		}
		return nil, fmt.Errorf("target '%s' is missing required 'language' field", targetName)
	}

	// Get target type (must be set by caller or explicitly in config)
	targetType := "executable"
	if tt, ok := targetConfig["type"].(string); ok {
		targetType = tt
	}

	// Look up template by language and target type
	templateName, _, metadata, err := tg.TemplateEngine.LookupTemplate(language, targetType)
	if err != nil {
		return nil, fmt.Errorf("failed to find template for language '%s' and type '%s': %w", language, targetType, err)
	}

	// Set module name for unique paths
	targetConfig["module"] = tg.CurrentModule
	if targetConfig["module"] == "" {
		targetConfig["module"] = "workspace"
	}

	// Select toolchain based on metadata
	toolMatcher := tg.ToolMatcher
	toolchainName := ""
	toolchainLanguage := metadata.ToolchainLanguage
	if toolchainLanguage == "" {
		toolchainLanguage = language
	}

	// Check if current toolchain matches the required language
	if tg.CurrentToolchain != nil && tg.CurrentToolchain.Language == toolchainLanguage {
		toolchainName = tg.CurrentToolchain.Name
	} else if tg.ToolchainManager != nil {
		// Find toolchain by language
		langToolchain := tg.ToolchainManager.FindByLanguage(toolchainLanguage, tg.Platform, tg.Architecture)
		if langToolchain != nil {
			toolchainName = langToolchain.Name
			var matcherErr error
			toolMatcher, matcherErr = resource.NewToolMatcher(langToolchain)
			if matcherErr != nil {
				return nil, fmt.Errorf("failed to create tool matcher for %s: %w", toolchainLanguage, matcherErr)
			}
			// Update command builder for the new toolchain
			tg.CommandBuilder = resource.NewCommandBuilder(langToolchain, toolMatcher, tg.Configuration)
		} else {
			return nil, fmt.Errorf("no %s toolchain found for platform %s-%s", toolchainLanguage, tg.Platform, tg.Architecture)
		}
	}

	// Apply pre-processing based on metadata
	if metadata.PreProcessing.ResolvePackages {
		if err := tg.resolvePackages(targetConfig); err != nil {
			return nil, err
		}
	}

	if metadata.PreProcessing.ResolveSources {
		sources, err := tg.PathResolver.ResolveSources(targetConfig)
		if err != nil {
			return nil, err
		}
		targetConfig["sources"] = sources
	}

	if metadata.PreProcessing.ResolveIncludeDirs {
		includeDirs, err := tg.PathResolver.ResolveIncludeDirs(targetConfig)
		if err != nil {
			return nil, err
		}
		targetConfig["include_dirs"] = includeDirs
	}

	if metadata.PreProcessing.ResolvePath {
		pathField := "."
		if p, ok := targetConfig["path"].(string); ok {
			pathField = p
		}
		if tg.PathResolver != nil {
			pathField = tg.PathResolver.ResolveRelativePath(pathField)
		}
		targetConfig["path"] = pathField
	}

	// Expand template
	tasks, err := tg.TemplateEngine.ExpandTemplate(
		templateName,
		targetConfig,
		mergedConfig,
		outputDir,
		setupTaskID,
		tg.TaskIDGen,
		toolMatcher,
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

	// Apply post-processing based on metadata
	if metadata.PostProcessing.AddSetupDependency && setupTaskID != "" {
		for _, task := range tasks {
			// Check if setup task is already in dependencies
			hasSetup := false
			for _, dep := range task.Dependencies {
				if dep == setupTaskID {
					hasSetup = true
					break
				}
			}
			if !hasSetup {
				task.Dependencies = append([]string{setupTaskID}, task.Dependencies...)
			}
		}
	}

	if metadata.PostProcessing.ScanSourcesPattern != "" {
		// Get the path to scan (use resolved path if available)
		scanPath := "."
		if p, ok := targetConfig["path"].(string); ok {
			scanPath = p
		}

		// Scan for source files using the pattern from metadata
		sourceFiles, scanErr := scanSourceFiles(scanPath, metadata.PostProcessing.ScanSourcesPattern)
		if scanErr != nil {
			log.Printf("WARNING: Failed to scan source files in %s: %v", scanPath, scanErr)
		} else {
			for _, task := range tasks {
				for _, srcFile := range sourceFiles {
					task.Inputs = append(task.Inputs, NewTaskInput(srcFile))
				}
				// Recalculate cache key with updated inputs
				task.CacheKey = task.CalculateCacheKey()
			}
			log.Printf("Found %d source files matching '%s' in %s", len(sourceFiles), metadata.PostProcessing.ScanSourcesPattern, scanPath)
		}
	}

	if metadata.PostProcessing.RegisterTarget {
		if name, ok := targetConfig["name"].(string); ok {
			// Find the final output task (link or build task) to register
			// For C/C++: look for "link" task type
			// For Go/Rust: look for "build" task type or tool name containing "build"
			var targetTask *BuildTask
			for _, task := range tasks {
				if task.TaskType == "link" || task.TaskType == "build" ||
					strings.Contains(task.TaskType, "build") {
					targetTask = task
					break
				}
			}
			// Fallback to last task if no link/build task found
			if targetTask == nil && len(tasks) > 0 {
				targetTask = tasks[len(tasks)-1]
			}
			if targetTask != nil {
				tg.generatedTargets[name] = targetTask.TaskID
				globalTaskRegistry.RegisterTarget(name, targetTask.TaskID, tg.CurrentModule)
				log.Printf("Registered target '%s' -> task '%s'", name, targetTask.TaskID)
			}
		}
	}

	return tasks, nil
}

// scanSourceFiles recursively scans a directory for files matching a glob pattern
func scanSourceFiles(rootDir string, pattern string) ([]string, error) {
	var files []string

	// Common directories to skip
	skipDirs := map[string]bool{
		"vendor":        true,
		".git":          true,
		"testdata":      true,
		".buildy_cache": true,
		"target":        true, // Rust/Cargo output
		"node_modules":  true,
		"build":         true,
	}

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip common non-source directories
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Check if file matches the pattern
		matched, matchErr := filepath.Match(pattern, info.Name())
		if matchErr != nil {
			return matchErr
		}
		if matched {
			files = append(files, path)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// For Go, also include go.mod and go.sum if they exist
	if pattern == "*.go" {
		goMod := filepath.Join(rootDir, "go.mod")
		if _, err := os.Stat(goMod); err == nil {
			files = append(files, goMod)
		}
		goSum := filepath.Join(rootDir, "go.sum")
		if _, err := os.Stat(goSum); err == nil {
			files = append(files, goSum)
		}
	}

	// For Rust, also include Cargo.toml and Cargo.lock if they exist
	if pattern == "*.rs" {
		cargoToml := filepath.Join(rootDir, "Cargo.toml")
		if _, err := os.Stat(cargoToml); err == nil {
			files = append(files, cargoToml)
		}
		cargoLock := filepath.Join(rootDir, "Cargo.lock")
		if _, err := os.Stat(cargoLock); err == nil {
			files = append(files, cargoLock)
		}
	}

	return files, nil
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

	mergedConfig := tg.getMergedConfig(config)

	// Define target sections with their default types
	// Each section maps to a specific target type for clarity
	targetSections := []struct {
		section     string
		defaultType string
	}{
		{"static_libraries", "static_library"},
		{"shared_libraries", "shared_library"},
		{"executables", "executable"},
	}

	// Process each target section
	for _, ts := range targetSections {
		targets := []any{}

		// Extract targets from config
		if targetsMap, ok := config["targets"].(map[string]any); ok {
			if sectionTargets, ok := targetsMap[ts.section].([]any); ok {
				targets = append(targets, sectionTargets...)
			}
		}

		// Apply platform-specific target overrides
		targets = tg.applyPlatformTargetOverrides(config, targets, ts.section)

		// Generate tasks for each target
		for _, targetRaw := range targets {
			if target, ok := targetRaw.(map[string]any); ok {
				// Set the type based on which section this target came from
				target["type"] = ts.defaultType
				targetTasks, err := tg.generateTargetTasks(target, mergedConfig, outputDir, setupTask.TaskID, tasks)
				if err != nil {
					return nil, err
				}
				tasks = append(tasks, targetTasks...)
			}
		}
	}

	// Generate artifact tasks
	if artifacts, ok := config["artifacts"].(map[string]any); ok {
		// Generate transform tasks
		transformTasks, err := tg.generateTransformTasks(artifacts, mergedConfig, outputDir, tasks)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, transformTasks...)

		// Generate generate tasks
		generateTasks, err := tg.generateGenerateTasks(artifacts, mergedConfig, outputDir, tasks)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, generateTasks...)

		// Generate copy tasks
		copyTasks, err := tg.generateArtifactCopyTasks(artifacts, mergedConfig, outputDir, tasks)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, copyTasks...)
	}

	// Generate staging tasks
	if staging, ok := config["staging"].(map[string]any); ok {
		stagingTasks, err := tg.generateStagingTasks(staging, mergedConfig, outputDir, tasks)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, stagingTasks...)
	}

	// Generate install/packaging tasks
	if install, ok := config["install"].([]any); ok {
		installTasks, err := tg.generateInstallTasks(install, mergedConfig, outputDir, tasks)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, installTasks...)
	}

	return tasks, nil
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
	task.EstimatedTime = util.DefaultSetupTimeSeconds
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

// artifactOutputs tracks artifact name -> output paths for staging
var artifactOutputs = make(map[string][]string)

// GetArtifactOutputs returns the outputs for a named artifact
func GetArtifactOutputs(name string) []string {
	return artifactOutputs[name]
}

// generateTransformTasks generates transform tasks from artifacts.transform section
func (tg *TaskGenerator) generateTransformTasks(
	artifacts map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	existingTasks []*BuildTask,
) ([]*BuildTask, error) {
	var tasks []*BuildTask

	transformItems, ok := artifacts["transform"].([]any)
	if !ok {
		return tasks, nil
	}

	// Use module directory (PathResolver.ConfigFileDir) for relative paths, not workspace root
	moduleDir := ""
	if tg.PathResolver != nil && tg.PathResolver.ConfigFileDir != "" {
		moduleDir = tg.PathResolver.ConfigFileDir
	} else if tg.Workspace != nil {
		moduleDir = tg.Workspace.RootDir
	}

	for _, transformRaw := range transformItems {
		transformItem, ok := transformRaw.(map[string]any)
		if !ok {
			continue
		}

		// Extract transform configuration
		name := ""
		if n, ok := transformItem["name"].(string); ok {
			name = n
		}

		toolName := ""
		if t, ok := transformItem["tool"].(string); ok {
			toolName = t
		}

		inputPattern := ""
		if i, ok := transformItem["inputs"].(string); ok {
			inputPattern = i
		}

		outputPattern := ""
		if o, ok := transformItem["outputs"].(string); ok {
			outputPattern = o
		}

		var args []string
		if a, ok := transformItem["args"].([]any); ok {
			for _, arg := range a {
				if s, ok := arg.(string); ok {
					args = append(args, s)
				}
			}
		}

		if toolName == "" || inputPattern == "" || outputPattern == "" {
			log.Printf("WARNING: artifacts.transform item '%s' missing required fields (tool, inputs, outputs)", name)
			continue
		}

		// Look up the toolchain by name
		toolchain := tg.ToolchainManager.GetToolchain(toolName)
		if toolchain == nil {
			log.Printf("WARNING: artifacts.transform '%s': toolchain '%s' not found", name, toolName)
			continue
		}

		// Find the transform/convert/compile tool in the toolchain
		var tool *resource.Tool
		for _, t := range toolchain.Tools {
			if t.Action == "transform" || t.Action == "convert" || t.Action == "compile" {
				tool = t
				break
			}
		}
		if tool == nil {
			log.Printf("WARNING: artifacts.transform '%s': no transform/convert/compile tool found in toolchain '%s'", name, toolName)
			continue
		}

		// Resolve input pattern - make it absolute if relative (use module directory)
		if !filepath.IsAbs(inputPattern) && moduleDir != "" {
			inputPattern = filepath.Join(moduleDir, inputPattern)
		}

		// Expand glob pattern to get input files
		inputFiles, err := filepath.Glob(inputPattern)
		if err != nil {
			log.Printf("WARNING: artifacts.transform '%s': invalid glob pattern '%s': %v", name, inputPattern, err)
			continue
		}

		if len(inputFiles) == 0 {
			log.Printf("WARNING: artifacts.transform '%s': no files matched pattern '%s'", name, inputPattern)
			continue
		}

		var artifactOutputPaths []string

		// Create a task for each input file
		for _, inputFile := range inputFiles {
			// Resolve output path with ${basename} and ${out_dir} substitution
			baseName := strings.TrimSuffix(filepath.Base(inputFile), filepath.Ext(inputFile))
			resolvedOutput := outputPattern
			resolvedOutput = strings.ReplaceAll(resolvedOutput, "${basename}", baseName)
			resolvedOutput = strings.ReplaceAll(resolvedOutput, "${out_dir}", outputDir)
			resolvedOutput = strings.ReplaceAll(resolvedOutput, "${output_dir}", outputDir)
			resolvedOutput = strings.ReplaceAll(resolvedOutput, "${config}", tg.Configuration)
			resolvedOutput = strings.ReplaceAll(resolvedOutput, "${platform}", tg.Platform)
			resolvedOutput = strings.ReplaceAll(resolvedOutput, "${arch}", tg.Architecture)

			// Make output path absolute if relative (use module directory)
			if !filepath.IsAbs(resolvedOutput) && moduleDir != "" {
				resolvedOutput = filepath.Join(moduleDir, resolvedOutput)
			}

			// Build the command
			outputDir := filepath.Dir(resolvedOutput)
			command := tool.Command

			// Substitute command placeholders
			command = strings.ReplaceAll(command, "{input}", inputFile)
			command = strings.ReplaceAll(command, "{output}", resolvedOutput)
			command = strings.ReplaceAll(command, "{output_dir}", outputDir)

			// Add flags from tool configuration
			flagsStr := ""
			if flags, ok := tool.Flags[tg.Configuration]; ok {
				flagsStr = strings.Join(flags, " ")
			} else if flags, ok := tool.Flags["common"]; ok {
				flagsStr = strings.Join(flags, " ")
			}
			command = strings.ReplaceAll(command, "{flags}", flagsStr)

			// Add user-provided args
			if len(args) > 0 {
				argsStr := strings.Join(args, " ")
				// If command has {args} placeholder, substitute it; otherwise append
				if strings.Contains(command, "{args}") {
					command = strings.ReplaceAll(command, "{args}", argsStr)
				} else {
					command = command + " " + argsStr
				}
			} else {
				command = strings.ReplaceAll(command, "{args}", "")
			}

			// Clean up any remaining empty placeholders
			command = strings.ReplaceAll(command, "{defines}", "")
			command = strings.ReplaceAll(command, "{includes}", "")

			// Create mkdir prefix for output directory
			var fullCommand string
			if strings.Contains(strings.ToLower(tg.Platform), "windows") {
				fullCommand = fmt.Sprintf("if not exist \"%s\" mkdir \"%s\" && %s", outputDir, outputDir, command)
			} else {
				fullCommand = fmt.Sprintf("mkdir -p %s && %s", outputDir, command)
			}

			// Create the task
			taskID := tg.TaskIDGen.Next("transform", baseName)
			task := NewBuildTask(
				taskID,
				"transform",
				[]TaskInput{NewTaskInput(inputFile)},
				[]string{resolvedOutput},
				[]string{}, // No dependencies by default
				fullCommand,
			)
			task.Platform = tg.Platform
			task.Architecture = tg.Architecture
			task.Configuration = tg.Configuration
			task.Toolchain = toolName
			task.EstimatedTime = 1.0
			task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 256, DiskMB: 50}
			task.CacheKey = task.CalculateCacheKey()

			tasks = append(tasks, &task)
			artifactOutputPaths = append(artifactOutputPaths, resolvedOutput)
			log.Printf("Generated transform task: %s -> %s", inputFile, resolvedOutput)
		}

		// Register artifact outputs for staging
		if name != "" {
			artifactOutputs[name] = artifactOutputPaths
			log.Printf("Registered artifact '%s' with %d outputs", name, len(artifactOutputPaths))
		}
	}

	return tasks, nil
}

// generateGenerateTasks generates tasks from artifacts.generate section
func (tg *TaskGenerator) generateGenerateTasks(
	artifacts map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	existingTasks []*BuildTask,
) ([]*BuildTask, error) {
	var tasks []*BuildTask

	generateItems, ok := artifacts["generate"].([]any)
	if !ok {
		return tasks, nil
	}

	// Use module directory (PathResolver.ConfigFileDir) for relative paths, not workspace root
	moduleDir := ""
	if tg.PathResolver != nil && tg.PathResolver.ConfigFileDir != "" {
		moduleDir = tg.PathResolver.ConfigFileDir
	} else if tg.Workspace != nil {
		moduleDir = tg.Workspace.RootDir
	}

	// Get gen_dir (generated files directory)
	genDir := filepath.Join(outputDir, "gen")

	for _, generateRaw := range generateItems {
		generateItem, ok := generateRaw.(map[string]any)
		if !ok {
			continue
		}

		name := ""
		if n, ok := generateItem["name"].(string); ok {
			name = n
		}

		// Check if this is a template-based generation or tool-based generation
		if templatePath, ok := generateItem["template"].(string); ok {
			// Template-based generation
			outputPath := ""
			if o, ok := generateItem["output"].(string); ok {
				outputPath = o
			}

			if outputPath == "" {
				log.Printf("WARNING: artifacts.generate '%s' missing 'output' field", name)
				continue
			}

			// Resolve template path (use module directory)
			if !filepath.IsAbs(templatePath) && moduleDir != "" {
				templatePath = filepath.Join(moduleDir, templatePath)
			}

			// Resolve output path
			outputPath = strings.ReplaceAll(outputPath, "${gen_dir}", genDir)
			outputPath = strings.ReplaceAll(outputPath, "${out_dir}", outputDir)
			outputPath = strings.ReplaceAll(outputPath, "${output_dir}", outputDir)
			outputPath = strings.ReplaceAll(outputPath, "${config}", tg.Configuration)
			outputPath = strings.ReplaceAll(outputPath, "${platform}", tg.Platform)
			outputPath = strings.ReplaceAll(outputPath, "${arch}", tg.Architecture)

			if !filepath.IsAbs(outputPath) && moduleDir != "" {
				outputPath = filepath.Join(moduleDir, outputPath)
			}

			// Get variables for substitution
			vars := make(map[string]string)
			if v, ok := generateItem["variables"].(map[string]any); ok {
				for key, val := range v {
					if strVal, ok := val.(string); ok {
						// Resolve built-in variables
						strVal = strings.ReplaceAll(strVal, "${project_version}", tg.getProjectVersion(mergedConfig))
						strVal = strings.ReplaceAll(strVal, "${project_name}", tg.getProjectName(mergedConfig))
						strVal = strings.ReplaceAll(strVal, "${config}", tg.Configuration)
						strVal = strings.ReplaceAll(strVal, "${platform}", tg.Platform)
						strVal = strings.ReplaceAll(strVal, "${arch}", tg.Architecture)
						vars[key] = strVal
					}
				}
			}

			// Build sed command for template substitution (or use Go template processing)
			// For simplicity, we'll use a shell-based approach with sed
			outputFileDir := filepath.Dir(outputPath)
			
			// Build sed substitution commands
			var sedCmds []string
			for key, val := range vars {
				// Escape special characters in value for sed
				escapedVal := strings.ReplaceAll(val, "/", "\\/")
				escapedVal = strings.ReplaceAll(escapedVal, "&", "\\&")
				sedCmds = append(sedCmds, fmt.Sprintf("s/@%s@/%s/g", key, escapedVal))
				sedCmds = append(sedCmds, fmt.Sprintf("s/${%s}/%s/g", key, escapedVal))
			}

			var command string
			if strings.Contains(strings.ToLower(tg.Platform), "windows") {
				// Windows: use PowerShell for template substitution
				psScript := fmt.Sprintf("$content = Get-Content '%s' -Raw; ", templatePath)
				for key, val := range vars {
					psScript += fmt.Sprintf("$content = $content -replace '@%s@', '%s'; ", key, val)
					psScript += fmt.Sprintf("$content = $content -replace '\\${%s}', '%s'; ", key, val)
				}
				psScript += fmt.Sprintf("$content | Set-Content '%s'", outputPath)
				command = fmt.Sprintf("if not exist \"%s\" mkdir \"%s\" && powershell -Command \"%s\"",
					outputFileDir, outputFileDir, psScript)
			} else {
				// Unix: use sed
				sedExpr := strings.Join(sedCmds, "; ")
				if sedExpr == "" {
					// No substitutions, just copy
					command = fmt.Sprintf("mkdir -p %s && cp %s %s", outputFileDir, templatePath, outputPath)
				} else {
					command = fmt.Sprintf("mkdir -p %s && sed '%s' %s > %s", outputFileDir, sedExpr, templatePath, outputPath)
				}
			}

			taskID := tg.TaskIDGen.Next("generate", name)
			task := NewBuildTask(
				taskID,
				"generate",
				[]TaskInput{NewTaskInput(templatePath)},
				[]string{outputPath},
				[]string{}, // Generate tasks should run early
				command,
			)
			task.Platform = tg.Platform
			task.Architecture = tg.Architecture
			task.Configuration = tg.Configuration
			task.EstimatedTime = 0.1
			task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 64, DiskMB: 1}
			task.CacheKey = task.CalculateCacheKey()

			tasks = append(tasks, &task)

			// Register artifact outputs
			if name != "" {
				artifactOutputs[name] = []string{outputPath}
			}

			log.Printf("Generated template task: %s -> %s", templatePath, outputPath)

		} else if toolName, ok := generateItem["tool"].(string); ok {
			// Tool-based generation (e.g., protoc)
			inputPattern := ""
			if i, ok := generateItem["inputs"].(string); ok {
				inputPattern = i
			}

			outputPatterns := []string{}
			if o, ok := generateItem["outputs"].(string); ok {
				outputPatterns = []string{o}
			} else if o, ok := generateItem["outputs"].([]any); ok {
				for _, p := range o {
					if s, ok := p.(string); ok {
						outputPatterns = append(outputPatterns, s)
					}
				}
			}

			var args []string
			if a, ok := generateItem["args"].([]any); ok {
				for _, arg := range a {
					if s, ok := arg.(string); ok {
						args = append(args, s)
					}
				}
			}

			if inputPattern == "" || len(outputPatterns) == 0 {
				log.Printf("WARNING: artifacts.generate '%s' with tool '%s' missing inputs or outputs", name, toolName)
				continue
			}

			// Look up the toolchain
			toolchain := tg.ToolchainManager.GetToolchain(toolName)
			if toolchain == nil {
				log.Printf("WARNING: artifacts.generate '%s': toolchain '%s' not found", name, toolName)
				continue
			}

			// Find the generate/compile tool
			var tool *resource.Tool
			for _, t := range toolchain.Tools {
				if t.Action == "generate" || t.Action == "compile" {
					tool = t
					break
				}
			}
			if tool == nil {
				log.Printf("WARNING: artifacts.generate '%s': no generate/compile tool found in toolchain '%s'", name, toolName)
				continue
			}

			// Resolve input pattern (use module directory)
			if !filepath.IsAbs(inputPattern) && moduleDir != "" {
				inputPattern = filepath.Join(moduleDir, inputPattern)
			}

			// Expand glob pattern
			inputFiles, err := filepath.Glob(inputPattern)
			if err != nil {
				log.Printf("WARNING: artifacts.generate '%s': invalid glob pattern '%s': %v", name, inputPattern, err)
				continue
			}

			if len(inputFiles) == 0 {
				log.Printf("WARNING: artifacts.generate '%s': no files matched pattern '%s'", name, inputPattern)
				continue
			}

			var artifactOutputPaths []string

			// Create a task for each input file
			for _, inputFile := range inputFiles {
				baseName := strings.TrimSuffix(filepath.Base(inputFile), filepath.Ext(inputFile))

				// Resolve output paths
				var resolvedOutputs []string
				for _, pattern := range outputPatterns {
					resolved := pattern
					resolved = strings.ReplaceAll(resolved, "${basename}", baseName)
					resolved = strings.ReplaceAll(resolved, "${gen_dir}", genDir)
					resolved = strings.ReplaceAll(resolved, "${out_dir}", outputDir)
					resolved = strings.ReplaceAll(resolved, "${output_dir}", outputDir)
					if !filepath.IsAbs(resolved) && moduleDir != "" {
						resolved = filepath.Join(moduleDir, resolved)
					}
					resolvedOutputs = append(resolvedOutputs, resolved)
				}

				// Build command
				command := tool.Command
				command = strings.ReplaceAll(command, "{input}", inputFile)
				if len(resolvedOutputs) > 0 {
					command = strings.ReplaceAll(command, "{output}", resolvedOutputs[0])
					command = strings.ReplaceAll(command, "{output_dir}", filepath.Dir(resolvedOutputs[0]))
				}
				command = strings.ReplaceAll(command, "{gen_dir}", genDir)

				// Add args
				argsStr := strings.Join(args, " ")
				argsStr = strings.ReplaceAll(argsStr, "${gen_dir}", genDir)
				if strings.Contains(command, "{args}") {
					command = strings.ReplaceAll(command, "{args}", argsStr)
				} else if argsStr != "" {
					command = command + " " + argsStr
				}

				// Clean up placeholders
				command = strings.ReplaceAll(command, "{flags}", "")
				command = strings.ReplaceAll(command, "{defines}", "")
				command = strings.ReplaceAll(command, "{includes}", "")

				// Create mkdir prefix
				var fullCommand string
				if len(resolvedOutputs) > 0 {
					outputFileDir := filepath.Dir(resolvedOutputs[0])
					if strings.Contains(strings.ToLower(tg.Platform), "windows") {
						fullCommand = fmt.Sprintf("if not exist \"%s\" mkdir \"%s\" && %s", outputFileDir, outputFileDir, command)
					} else {
						fullCommand = fmt.Sprintf("mkdir -p %s && %s", outputFileDir, command)
					}
				} else {
					fullCommand = command
				}

				taskID := tg.TaskIDGen.Next("generate", baseName)
				task := NewBuildTask(
					taskID,
					"generate",
					[]TaskInput{NewTaskInput(inputFile)},
					resolvedOutputs,
					[]string{},
					fullCommand,
				)
				task.Platform = tg.Platform
				task.Architecture = tg.Architecture
				task.Configuration = tg.Configuration
				task.Toolchain = toolName
				task.EstimatedTime = 0.5
				task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 128, DiskMB: 10}
				task.CacheKey = task.CalculateCacheKey()

				tasks = append(tasks, &task)
				artifactOutputPaths = append(artifactOutputPaths, resolvedOutputs...)
				log.Printf("Generated codegen task: %s -> %v", inputFile, resolvedOutputs)
			}

			// Register artifact outputs
			if name != "" {
				artifactOutputs[name] = artifactOutputPaths
			}
		}
	}

	return tasks, nil
}

// getProjectVersion extracts project version from config
func (tg *TaskGenerator) getProjectVersion(config map[string]any) string {
	if project, ok := config["project"].(map[string]any); ok {
		if version, ok := project["version"].(string); ok {
			return version
		}
	}
	return "0.0.0"
}

// getProjectName extracts project name from config
func (tg *TaskGenerator) getProjectName(config map[string]any) string {
	if project, ok := config["project"].(map[string]any); ok {
		if name, ok := project["name"].(string); ok {
			return name
		}
	}
	return "unknown"
}

// stagingTaskInfo tracks staging task IDs for install dependencies
var stagingTasksByName = make(map[string][]string)

// generateStagingTasks generates tasks to populate a staging area using hierarchical folder structure
func (tg *TaskGenerator) generateStagingTasks(
	staging map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	existingTasks []*BuildTask,
) ([]*BuildTask, error) {
	var tasks []*BuildTask

	// Get staging configuration
	stagingName := "staging"
	if n, ok := staging["name"].(string); ok {
		stagingName = n
	}

	destination := filepath.Join(outputDir, "staging")
	if d, ok := staging["destination"].(string); ok {
		destination = d
		destination = strings.ReplaceAll(destination, "${out_dir}", outputDir)
		destination = strings.ReplaceAll(destination, "${output_dir}", outputDir)
		destination = strings.ReplaceAll(destination, "${config}", tg.Configuration)
		destination = strings.ReplaceAll(destination, "${platform}", tg.Platform)
	}

	workspaceRoot := ""
	if tg.Workspace != nil {
		workspaceRoot = tg.Workspace.RootDir
	}
	if !filepath.IsAbs(destination) && workspaceRoot != "" {
		destination = filepath.Join(workspaceRoot, destination)
	}

	// Get top-level use_symlinks setting (default: false = copy)
	defaultUseSymlinks := false
	if us, ok := staging["use_symlinks"].(bool); ok {
		defaultUseSymlinks = us
	}

	// Windows always uses copy (symlinks require admin/developer mode)
	isWindows := strings.Contains(strings.ToLower(tg.Platform), "windows")
	if isWindows {
		defaultUseSymlinks = false
	}

	// Track staging task IDs for this staging area
	var stagingTaskIDs []string

	// Process contents section - new hierarchical folder-based format
	if contents, ok := staging["contents"].([]any); ok {
		folderTasks := tg.processStagingContents(contents, destination, "", defaultUseSymlinks, isWindows, outputDir, workspaceRoot)
		tasks = append(tasks, folderTasks...)
		for _, t := range folderTasks {
			stagingTaskIDs = append(stagingTaskIDs, t.TaskID)
		}
	}

	// Register staging tasks for install dependencies
	stagingTasksByName[stagingName] = stagingTaskIDs
	log.Printf("Generated %d staging tasks for '%s' at %s", len(tasks), stagingName, destination)

	return tasks, nil
}

// processStagingContents recursively processes the hierarchical folder structure
func (tg *TaskGenerator) processStagingContents(
	contents []any,
	stagingRoot string,
	currentPath string,
	defaultUseSymlinks bool,
	isWindows bool,
	outputDir string,
	workspaceRoot string,
) []*BuildTask {
	var tasks []*BuildTask

	for _, itemRaw := range contents {
		item, ok := itemRaw.(map[string]any)
		if !ok {
			continue
		}

		// Get folder name (use "." for root level)
		folderName := "."
		if f, ok := item["folder"].(string); ok {
			folderName = f
		}

		// Calculate the destination path for this folder
		var folderPath string
		if folderName == "." {
			folderPath = filepath.Join(stagingRoot, currentPath)
		} else if currentPath == "" {
			folderPath = filepath.Join(stagingRoot, folderName)
		} else {
			folderPath = filepath.Join(stagingRoot, currentPath, folderName)
		}

		// Process targets (executables and libraries - auto-detected)
		if targets, ok := item["targets"].([]any); ok {
			for _, targetRaw := range targets {
				targetName := ""
				useSymlink := defaultUseSymlinks

				switch v := targetRaw.(type) {
				case string:
					targetName = v
				case map[string]any:
					if n, ok := v["name"].(string); ok {
						targetName = n
					}
					if us, ok := v["use_symlink"].(bool); ok {
						useSymlink = us
					}
				}

				if targetName == "" {
					continue
				}

				// Windows override
				if isWindows {
					useSymlink = false
				}

				// Look up the target - try to determine if it's an executable or library
				task := tg.createTargetStagingTask(targetName, folderPath, useSymlink, isWindows, outputDir)
				if task != nil {
					tasks = append(tasks, task)
				}
			}
		}

		// Process artifacts
		if artifacts, ok := item["artifacts"].([]any); ok {
			for _, artifactRaw := range artifacts {
				artifactName := ""
				useSymlink := defaultUseSymlinks

				switch v := artifactRaw.(type) {
				case string:
					artifactName = v
				case map[string]any:
					if n, ok := v["name"].(string); ok {
						artifactName = n
					}
					if us, ok := v["use_symlink"].(bool); ok {
						useSymlink = us
					}
				}

				if artifactName == "" {
					continue
				}

				if isWindows {
					useSymlink = false
				}

				// Get artifact outputs
				outputs := artifactOutputs[artifactName]
				if len(outputs) == 0 {
					log.Printf("WARNING: artifact '%s' has no outputs for staging", artifactName)
					continue
				}

				for _, sourcePath := range outputs {
					destPath := filepath.Join(folderPath, filepath.Base(sourcePath))
					task := tg.createSymlinkOrCopyTask(sourcePath, destPath, useSymlink, isWindows, []string{})
					if task != nil {
						tasks = append(tasks, task)
					}
				}
			}
		}

		// Process files
		if files, ok := item["files"].([]any); ok {
			for _, fileRaw := range files {
				useSymlink := defaultUseSymlinks
				var sourcePattern string

				switch v := fileRaw.(type) {
				case string:
					sourcePattern = v
				case map[string]any:
					if s, ok := v["source"].(string); ok {
						sourcePattern = s
					}
					if us, ok := v["use_symlink"].(bool); ok {
						useSymlink = us
					}
				}

				if sourcePattern == "" {
					continue
				}

				if isWindows {
					useSymlink = false
				}

				// Resolve source pattern
				if !filepath.IsAbs(sourcePattern) && workspaceRoot != "" {
					sourcePattern = filepath.Join(workspaceRoot, sourcePattern)
				}

				// Check if source is a glob pattern
				if strings.Contains(sourcePattern, "*") {
					matches, err := filepath.Glob(sourcePattern)
					if err != nil {
						log.Printf("WARNING: invalid glob pattern '%s': %v", sourcePattern, err)
						continue
					}

					for _, match := range matches {
						destPath := filepath.Join(folderPath, filepath.Base(match))
						task := tg.createSymlinkOrCopyTask(match, destPath, useSymlink, isWindows, []string{})
						if task != nil {
							tasks = append(tasks, task)
						}
					}
				} else {
					// Single file
					destPath := filepath.Join(folderPath, filepath.Base(sourcePattern))
					task := tg.createSymlinkOrCopyTask(sourcePattern, destPath, useSymlink, isWindows, []string{})
					if task != nil {
						tasks = append(tasks, task)
					}
				}
			}
		}

		// Process nested contents (subfolders)
		if nestedContents, ok := item["contents"].([]any); ok {
			var nestedPath string
			if folderName == "." {
				nestedPath = currentPath
			} else if currentPath == "" {
				nestedPath = folderName
			} else {
				nestedPath = filepath.Join(currentPath, folderName)
			}
			nestedTasks := tg.processStagingContents(nestedContents, stagingRoot, nestedPath, defaultUseSymlinks, isWindows, outputDir, workspaceRoot)
			tasks = append(tasks, nestedTasks...)
		}
	}

	return tasks
}

// createTargetStagingTask creates a staging task for a target (auto-detects executable vs library)
func (tg *TaskGenerator) createTargetStagingTask(
	targetName string,
	destFolder string,
	useSymlink bool,
	isWindows bool,
	outputDir string,
) *BuildTask {
	// Look up the target's task ID - first try local generatedTargets, then global registry
	taskID, found := tg.generatedTargets[targetName]
	if !found {
		// Try global registry (for workspace-level staging that references module targets)
		taskID, found = globalTaskRegistry.GetLinkTaskID(targetName)
	}
	if !found {
		log.Printf("WARNING: staging target '%s' not found in generated targets or global registry", targetName)
		return nil
	}

	// Try to find the output file - check multiple locations
	possiblePaths := []string{
		// Executable paths
		filepath.Join(outputDir, "bin", targetName),
		filepath.Join(outputDir, "bin", targetName+".exe"),
		// Library paths
		filepath.Join(outputDir, "lib", "lib"+targetName+".so"),
		filepath.Join(outputDir, "lib", "lib"+targetName+".a"),
		filepath.Join(outputDir, "lib", "lib"+targetName+".dylib"),
		filepath.Join(outputDir, "lib", targetName+".dll"),
		filepath.Join(outputDir, "lib", targetName+".lib"),
	}

	var sourcePath string
	for _, p := range possiblePaths {
		// For now, construct based on platform conventions
		// The actual file may not exist yet at task generation time
		if isWindows {
			if strings.HasSuffix(p, ".exe") || strings.HasSuffix(p, ".dll") || strings.HasSuffix(p, ".lib") {
				sourcePath = p
				break
			}
		} else if strings.Contains(tg.Platform, "darwin") || strings.Contains(tg.Platform, "macos") {
			if !strings.HasSuffix(p, ".exe") && !strings.HasSuffix(p, ".dll") && !strings.HasSuffix(p, ".lib") {
				if strings.HasSuffix(p, ".dylib") || !strings.Contains(p, ".") || strings.HasSuffix(p, ".a") {
					sourcePath = p
					break
				}
			}
		} else {
			// Linux
			if !strings.HasSuffix(p, ".exe") && !strings.HasSuffix(p, ".dll") && !strings.HasSuffix(p, ".lib") && !strings.HasSuffix(p, ".dylib") {
				sourcePath = p
				break
			}
		}
	}

	if sourcePath == "" {
		// Default fallback - assume executable
		if isWindows {
			sourcePath = filepath.Join(outputDir, "bin", targetName+".exe")
		} else {
			sourcePath = filepath.Join(outputDir, "bin", targetName)
		}
	}

	destPath := filepath.Join(destFolder, filepath.Base(sourcePath))
	return tg.createSymlinkOrCopyTask(sourcePath, destPath, useSymlink, isWindows, []string{taskID})
}

// createSymlinkOrCopyTask creates a task that either symlinks or copies a file
func (tg *TaskGenerator) createSymlinkOrCopyTask(
	source string,
	dest string,
	useSymlink bool,
	isWindows bool,
	dependencies []string,
) *BuildTask {
	destDir := filepath.Dir(dest)

	var command string
	taskType := "copy"

	if useSymlink && !isWindows {
		taskType = "symlink"
		// ln -sf: -s for symbolic link, -f to force overwrite
		command = fmt.Sprintf("mkdir -p %s && ln -sf %s %s", destDir, source, dest)
	} else {
		// Copy command
		if isWindows {
			command = fmt.Sprintf("if not exist \"%s\" mkdir \"%s\" && copy /Y \"%s\" \"%s\"",
				destDir, destDir, source, dest)
		} else {
			command = fmt.Sprintf("mkdir -p %s && cp %s %s", destDir, source, dest)
		}
	}

	taskID := tg.TaskIDGen.Next("stage", filepath.Base(dest))

	// For symlinks, cache key should include source path but not content hash
	// For copies, include content hash
	var inputs []TaskInput
	if useSymlink && !isWindows {
		// For symlinks, we still track the source as input but the symlink just points to path
		inputs = []TaskInput{{Path: source, Hash: "symlink:" + source}}
	} else {
		inputs = []TaskInput{NewTaskInput(source)}
	}

	task := NewBuildTask(
		taskID,
		taskType,
		inputs,
		[]string{dest},
		dependencies,
		command,
	)
	task.Platform = tg.Platform
	task.Architecture = tg.Architecture
	task.Configuration = tg.Configuration
	task.EstimatedTime = 0.1
	task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 64, DiskMB: 10}
	task.CacheKey = task.CalculateCacheKey()

	log.Printf("Generated %s task: %s -> %s", taskType, source, dest)
	return &task
}

// generateInstallTasks generates packaging/install tasks
func (tg *TaskGenerator) generateInstallTasks(
	installItems []any,
	mergedConfig map[string]any,
	outputDir string,
	existingTasks []*BuildTask,
) ([]*BuildTask, error) {
	var tasks []*BuildTask

	workspaceRoot := ""
	if tg.Workspace != nil {
		workspaceRoot = tg.Workspace.RootDir
	}

	for _, installRaw := range installItems {
		installItem, ok := installRaw.(map[string]any)
		if !ok {
			continue
		}

		name := ""
		if n, ok := installItem["name"].(string); ok {
			name = n
		}

		stagingName := ""
		if s, ok := installItem["staging"].(string); ok {
			stagingName = s
		}

		destination := filepath.Join(outputDir, "dist")
		if d, ok := installItem["destination"].(string); ok {
			destination = d
			destination = strings.ReplaceAll(destination, "${out_dir}", outputDir)
			destination = strings.ReplaceAll(destination, "${output_dir}", outputDir)
		}
		if !filepath.IsAbs(destination) && workspaceRoot != "" {
			destination = filepath.Join(workspaceRoot, destination)
		}

		format := "tar.gz"
		if f, ok := installItem["format"].(string); ok {
			format = f
		}

		// followSymlinks defaults to true
		followSymlinks := true
		if fs, ok := installItem["follow_symlinks"].(bool); ok {
			followSymlinks = fs
		}

		// Build filename
		filename := name
		if fn, ok := installItem["filename"].(string); ok {
			filename = fn
			filename = strings.ReplaceAll(filename, "${project_name}", tg.getProjectName(mergedConfig))
			filename = strings.ReplaceAll(filename, "${project_version}", tg.getProjectVersion(mergedConfig))
			filename = strings.ReplaceAll(filename, "${platform}", tg.Platform)
			filename = strings.ReplaceAll(filename, "${arch}", tg.Architecture)
			filename = strings.ReplaceAll(filename, "${config}", tg.Configuration)
		}

		// Get staging directory path
		stagingDir := filepath.Join(outputDir, "staging")
		if stagingName != "" {
			// Look up staging destination from config (we'd need to track this, for now use convention)
			stagingDir = filepath.Join(outputDir, "staging")
		}

		// Determine output file extension
		ext := ".tar.gz"
		switch format {
		case "tar.gz", "tgz":
			ext = ".tar.gz"
		case "zip":
			ext = ".zip"
		case "tar":
			ext = ".tar"
		}

		outputFile := filepath.Join(destination, filename+ext)

		// Build archive command
		var command string
		isWindows := strings.Contains(strings.ToLower(tg.Platform), "windows")

		if isWindows {
			// Use PowerShell for compression on Windows
			switch format {
			case "zip":
				command = fmt.Sprintf("if not exist \"%s\" mkdir \"%s\" && powershell -Command \"Compress-Archive -Path '%s\\*' -DestinationPath '%s' -Force\"",
					destination, destination, stagingDir, outputFile)
			default:
				// tar.gz using tar if available
				command = fmt.Sprintf("if not exist \"%s\" mkdir \"%s\" && tar -czvf \"%s\" -C \"%s\" .",
					destination, destination, outputFile, stagingDir)
			}
		} else {
			mkdirCmd := fmt.Sprintf("mkdir -p %s", destination)
			switch format {
			case "zip":
				// zip follows symlinks by default
				command = fmt.Sprintf("%s && cd %s && zip -r %s .", mkdirCmd, stagingDir, outputFile)
			case "tar":
				if followSymlinks {
					command = fmt.Sprintf("%s && tar -chf %s -C %s .", mkdirCmd, outputFile, stagingDir)
				} else {
					command = fmt.Sprintf("%s && tar -cf %s -C %s .", mkdirCmd, outputFile, stagingDir)
				}
			default: // tar.gz
				if followSymlinks {
					command = fmt.Sprintf("%s && tar -czhf %s -C %s .", mkdirCmd, outputFile, stagingDir)
				} else {
					command = fmt.Sprintf("%s && tar -czf %s -C %s .", mkdirCmd, outputFile, stagingDir)
				}
			}
		}

		// Dependencies: all staging tasks for the referenced staging area
		var dependencies []string
		if stagingTaskIDs, ok := stagingTasksByName[stagingName]; ok {
			dependencies = stagingTaskIDs
		}

		taskID := tg.TaskIDGen.Next("install", name)
		task := NewBuildTask(
			taskID,
			"install",
			[]TaskInput{{Path: stagingDir, Hash: "directory"}},
			[]string{outputFile},
			dependencies,
			command,
		)
		task.Platform = tg.Platform
		task.Architecture = tg.Architecture
		task.Configuration = tg.Configuration
		task.EstimatedTime = 5.0
		task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 512, DiskMB: 500}
		task.CacheKey = task.CalculateCacheKey()

		tasks = append(tasks, &task)
		log.Printf("Generated install task: %s -> %s (format: %s)", stagingDir, outputFile, format)
	}

	return tasks, nil
}

// GetGeneratedTargets returns the mapping of target names to their link task IDs
func (tg *TaskGenerator) GetGeneratedTargets() map[string]string {
	return tg.generatedTargets
}

// generateArtifactCopyTasks generates copy tasks from the artifacts.copy section
func (tg *TaskGenerator) generateArtifactCopyTasks(
	artifacts map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	existingTasks []*BuildTask,
) ([]*BuildTask, error) {
	var tasks []*BuildTask

	copyItems, ok := artifacts["copy"].([]any)
	if !ok {
		return tasks, nil
	}

	// Log available targets for debugging
	log.Printf("Available targets for artifact dependencies: %v", tg.generatedTargets)

	// Get workspace root for resolving relative paths
	workspaceRoot := ""
	if tg.Workspace != nil {
		workspaceRoot = tg.Workspace.RootDir
	}

	for _, copyRaw := range copyItems {
		copyItem, ok := copyRaw.(map[string]any)
		if !ok {
			continue
		}

		// Get source and destination
		source := ""
		if s, ok := copyItem["source"].(string); ok {
			source = s
		}
		dest := ""
		if d, ok := copyItem["dest"].(string); ok {
			dest = d
		}

		if source == "" || dest == "" {
			log.Printf("WARNING: artifacts.copy item missing source or dest")
			continue
		}

		// Resolve variables in source and dest
		source = strings.ReplaceAll(source, "${output_dir}", outputDir)
		source = strings.ReplaceAll(source, "${config}", tg.Configuration)
		source = strings.ReplaceAll(source, "${platform}", tg.Platform)
		source = strings.ReplaceAll(source, "${arch}", tg.Architecture)

		dest = strings.ReplaceAll(dest, "${output_dir}", outputDir)
		dest = strings.ReplaceAll(dest, "${config}", tg.Configuration)
		dest = strings.ReplaceAll(dest, "${platform}", tg.Platform)
		dest = strings.ReplaceAll(dest, "${arch}", tg.Architecture)

		// Make relative paths absolute
		if !filepath.IsAbs(source) && workspaceRoot != "" {
			source = filepath.Join(workspaceRoot, source)
		}
		if !filepath.IsAbs(dest) && workspaceRoot != "" {
			dest = filepath.Join(workspaceRoot, dest)
		}

		// Resolve dependencies
		var dependencies []string
		if deps, ok := copyItem["depends_on"].([]any); ok {
			for _, dep := range deps {
				if depStr, ok := dep.(string); ok {
					// Look up the task ID for this target name
					if taskID, found := tg.generatedTargets[depStr]; found {
						dependencies = append(dependencies, taskID)
						log.Printf("Resolved dependency '%s' -> task '%s'", depStr, taskID)
					} else {
						log.Printf("WARNING: artifacts.copy dependency '%s' not found in generated targets", depStr)
					}
				}
			}
		}

		// Create the copy command
		var command string
		destDir := filepath.Dir(dest)
		if strings.Contains(strings.ToLower(tg.Platform), "windows") {
			command = fmt.Sprintf("if not exist \"%s\" mkdir \"%s\" && copy /Y \"%s\" \"%s\"",
				destDir, destDir, source, dest)
		} else {
			command = fmt.Sprintf("mkdir -p %s && cp %s %s", destDir, source, dest)
		}

		// Create the copy task
		taskID := tg.TaskIDGen.Next("copy", filepath.Base(dest))
		task := NewBuildTask(
			taskID,
			"copy",
			[]TaskInput{NewTaskInput(source)},
			[]string{dest},
			dependencies,
			command,
		)
		task.Platform = tg.Platform
		task.Architecture = tg.Architecture
		task.Configuration = tg.Configuration
		task.Toolchain = ""
		task.EstimatedTime = 0.1
		task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 64, DiskMB: 10}
		task.CacheKey = task.CalculateCacheKey()

		tasks = append(tasks, &task)
		log.Printf("Generated copy task: %s -> %s", source, dest)
	}

	return tasks, nil
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
