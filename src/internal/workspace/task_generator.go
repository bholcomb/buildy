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

	// Determine target type (default to executable for executables list, shared_library for libraries)
	targetType := "executable"
	if tt, ok := targetConfig["type"].(string); ok {
		targetType = tt
	} else if _, isLibrary := targetConfig["_is_library"]; isLibrary {
		targetType = "shared_library"
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

	// Generate artifact copy tasks
	if artifacts, ok := config["artifacts"].(map[string]any); ok {
		copyTasks, err := tg.generateArtifactCopyTasks(artifacts, mergedConfig, outputDir, tasks)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, copyTasks...)
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

// generateLibraryTasks generates tasks for a library using unified task generation
func (tg *TaskGenerator) generateLibraryTasks(
	libConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
) ([]*BuildTask, error) {
	// Mark this as a library target (used for default type detection)
	libConfig["_is_library"] = true

	// Set default type if not specified
	if _, hasType := libConfig["type"]; !hasType {
		libConfig["type"] = "shared_library"
	}

	return tg.generateTargetTasks(libConfig, mergedConfig, outputDir, setupTaskID, []*BuildTask{})
}



// generateExecutableTasks generates tasks for an executable using unified task generation
func (tg *TaskGenerator) generateExecutableTasks(
	exeConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
	existingTasks []*BuildTask,
) ([]*BuildTask, error) {
	// Set default type if not specified
	if _, hasType := exeConfig["type"]; !hasType {
		exeConfig["type"] = "executable"
	}

	return tg.generateTargetTasks(exeConfig, mergedConfig, outputDir, setupTaskID, existingTasks)
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
