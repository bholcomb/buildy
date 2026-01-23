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

	// Generate Go module tasks (legacy support)
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

// generateLibraryTasks generates tasks for a library
func (tg *TaskGenerator) generateLibraryTasks(
	libConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
) ([]*BuildTask, error) {
	// Check for language field to determine routing
	language := ""
	if lang, ok := libConfig["language"].(string); ok {
		language = lang
	}

	// Route to language-specific handler
	switch language {
	case "go":
		return tg.generateGoLibraryTasks(libConfig, mergedConfig, outputDir, setupTaskID)
	case "rust":
		return tg.generateRustLibraryTasks(libConfig, mergedConfig, outputDir, setupTaskID)
	default:
		// C++ or unspecified language
		return tg.generateCppLibraryTasks(libConfig, mergedConfig, outputDir, setupTaskID)
	}
}

// generateCppLibraryTasks generates tasks for a C++ library
func (tg *TaskGenerator) generateCppLibraryTasks(
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

// generateGoLibraryTasks generates tasks for a Go library (plugin)
func (tg *TaskGenerator) generateGoLibraryTasks(
	libConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
) ([]*BuildTask, error) {
	// Go libraries are relatively rare - typically plugins
	// For now, use the same logic as Go executables but with go_library template
	libConfig["module"] = tg.CurrentModule
	if libConfig["module"] == "" {
		libConfig["module"] = "workspace"
	}

	// Select Go toolchain based on platform
	toolMatcher := tg.ToolMatcher
	toolchainName := ""
	if tg.CurrentToolchain != nil && tg.CurrentToolchain.Language == "go" {
		toolchainName = tg.CurrentToolchain.Name
	} else if tg.ToolchainManager != nil {
		goToolchain := tg.ToolchainManager.FindByLanguage("go", tg.Platform, tg.Architecture)
		if goToolchain != nil {
			toolchainName = goToolchain.Name
			var err error
			toolMatcher, err = resource.NewToolMatcher(goToolchain)
			if err != nil {
				return nil, fmt.Errorf("failed to create tool matcher for Go: %w", err)
			}
		} else {
			return nil, fmt.Errorf("no Go toolchain found for platform %s-%s", tg.Platform, tg.Architecture)
		}
	}

	tasks, err := tg.TemplateEngine.ExpandTemplate(
		"go_library",
		libConfig,
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
		[]*BuildTask{},
	)
	if err != nil {
		return nil, err
	}

	return tasks, nil
}

// generateRustLibraryTasks generates tasks for a Rust library
func (tg *TaskGenerator) generateRustLibraryTasks(
	libConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
) ([]*BuildTask, error) {
	libConfig["module"] = tg.CurrentModule
	if libConfig["module"] == "" {
		libConfig["module"] = "workspace"
	}

	// Select Rust toolchain based on platform
	toolMatcher := tg.ToolMatcher
	toolchainName := ""
	if tg.CurrentToolchain != nil && tg.CurrentToolchain.Language == "rust" {
		toolchainName = tg.CurrentToolchain.Name
	} else if tg.ToolchainManager != nil {
		rustToolchain := tg.ToolchainManager.FindByLanguage("rust", tg.Platform, tg.Architecture)
		if rustToolchain != nil {
			toolchainName = rustToolchain.Name
			var err error
			toolMatcher, err = resource.NewToolMatcher(rustToolchain)
			if err != nil {
				return nil, fmt.Errorf("failed to create tool matcher for Rust: %w", err)
			}
		} else {
			return nil, fmt.Errorf("no Rust toolchain found for platform %s-%s", tg.Platform, tg.Architecture)
		}
	}

	tasks, err := tg.TemplateEngine.ExpandTemplate(
		"rust_library",
		libConfig,
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
		[]*BuildTask{},
	)
	if err != nil {
		return nil, err
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
	// Check for language field to determine routing
	language := ""
	if lang, ok := exeConfig["language"].(string); ok {
		language = lang
	}

	// Route to language-specific handler
	switch language {
	case "go":
		return tg.generateGoModuleTasks(exeConfig, mergedConfig, outputDir, setupTaskID)
	case "rust":
		return tg.generateRustCrateTasks(exeConfig, mergedConfig, outputDir, setupTaskID)
	default:
		// C++ or unspecified language - use traditional compile+link
		return tg.generateCppExecutableTasks(exeConfig, mergedConfig, outputDir, setupTaskID, existingTasks)
	}
}

// generateCppExecutableTasks generates tasks for a C++ executable
func (tg *TaskGenerator) generateCppExecutableTasks(
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

// generateGoModuleTasks generates tasks for a Go module using template engine
func (tg *TaskGenerator) generateGoModuleTasks(
	goModConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
) ([]*BuildTask, error) {
	// Resolve module path
	modulePath := "."
	if p, ok := goModConfig["path"].(string); ok {
		modulePath = p
	}
	if tg.PathResolver != nil {
		modulePath = tg.PathResolver.ResolveRelativePath(modulePath)
	}
	goModConfig["path"] = modulePath

	// Add module name for unique task IDs
	goModConfig["module"] = tg.CurrentModule
	if goModConfig["module"] == "" {
		goModConfig["module"] = "workspace"
	}

	// Select Go toolchain based on platform
	toolMatcher := tg.ToolMatcher
	toolchainName := ""
	if tg.CurrentToolchain != nil && tg.CurrentToolchain.Language == "go" {
		toolchainName = tg.CurrentToolchain.Name
	} else if tg.ToolchainManager != nil {
		goToolchain := tg.ToolchainManager.FindByLanguage("go", tg.Platform, tg.Architecture)
		if goToolchain != nil {
			toolchainName = goToolchain.Name
			var err error
			toolMatcher, err = resource.NewToolMatcher(goToolchain)
			if err != nil {
				return nil, fmt.Errorf("failed to create tool matcher for Go: %w", err)
			}
		} else {
			return nil, fmt.Errorf("no Go toolchain found for platform %s-%s", tg.Platform, tg.Architecture)
		}
	}

	// Use template engine to expand go_executable template
	tasks, err := tg.TemplateEngine.ExpandTemplate(
		"go_executable",
		goModConfig,
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
		[]*BuildTask{},
	)
	if err != nil {
		return nil, err
	}

	// Add setup task as dependency and enhance with Go source files for cache invalidation
	for _, task := range tasks {
		// Add setup task dependency
		if setupTaskID != "" {
			task.Dependencies = append([]string{setupTaskID}, task.Dependencies...)
		}

		// Scan for all .go files in the module directory for cache invalidation
		goFiles, scanErr := findGoSourceFiles(modulePath)
		if scanErr != nil {
			log.Printf("WARNING: Failed to scan Go source files in %s: %v", modulePath, scanErr)
		} else {
			for _, goFile := range goFiles {
				task.Inputs = append(task.Inputs, NewTaskInput(goFile))
			}
			log.Printf("Found %d Go source files in %s", len(goFiles), modulePath)
		}

		// Recalculate cache key with updated inputs
		task.CacheKey = task.CalculateCacheKey()
	}

	// Register target
	if name, ok := goModConfig["name"].(string); ok {
		for _, task := range tasks {
			tg.generatedTargets[name] = task.TaskID
			globalTaskRegistry.RegisterTarget(name, task.TaskID, tg.CurrentModule)
			log.Printf("Generated Go build task: %s -> %v", name, task.Outputs)
			break
		}
	}

	return tasks, nil
}

// generateRustCrateTasks generates tasks for a Rust crate using template engine
func (tg *TaskGenerator) generateRustCrateTasks(
	crateConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
) ([]*BuildTask, error) {
	// Resolve crate path
	cratePath := "."
	if p, ok := crateConfig["path"].(string); ok {
		cratePath = p
	}
	if tg.PathResolver != nil {
		cratePath = tg.PathResolver.ResolveRelativePath(cratePath)
	}
	crateConfig["path"] = cratePath

	// Add module name for unique task IDs
	crateConfig["module"] = tg.CurrentModule
	if crateConfig["module"] == "" {
		crateConfig["module"] = "workspace"
	}

	// Select Rust toolchain based on platform
	toolMatcher := tg.ToolMatcher
	toolchainName := ""
	if tg.CurrentToolchain != nil && tg.CurrentToolchain.Language == "rust" {
		toolchainName = tg.CurrentToolchain.Name
	} else if tg.ToolchainManager != nil {
		rustToolchain := tg.ToolchainManager.FindByLanguage("rust", tg.Platform, tg.Architecture)
		if rustToolchain != nil {
			toolchainName = rustToolchain.Name
			var err error
			toolMatcher, err = resource.NewToolMatcher(rustToolchain)
			if err != nil {
				return nil, fmt.Errorf("failed to create tool matcher for Rust: %w", err)
			}
		} else {
			return nil, fmt.Errorf("no Rust toolchain found for platform %s-%s", tg.Platform, tg.Architecture)
		}
	}

	// Use template engine to expand rust_executable template
	tasks, err := tg.TemplateEngine.ExpandTemplate(
		"rust_executable",
		crateConfig,
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
		[]*BuildTask{},
	)
	if err != nil {
		return nil, err
	}

	// Add setup task as dependency and enhance with Rust source files for cache invalidation
	for _, task := range tasks {
		// Add setup task dependency
		if setupTaskID != "" {
			task.Dependencies = append([]string{setupTaskID}, task.Dependencies...)
		}

		// Scan for Rust source files for cache invalidation
		rsFiles, scanErr := findRustSourceFiles(cratePath)
		if scanErr != nil {
			log.Printf("WARNING: Failed to scan Rust source files in %s: %v", cratePath, scanErr)
		} else {
			for _, rsFile := range rsFiles {
				task.Inputs = append(task.Inputs, NewTaskInput(rsFile))
			}
			log.Printf("Found %d Rust source files in %s", len(rsFiles), cratePath)
		}

		// Recalculate cache key with updated inputs
		task.CacheKey = task.CalculateCacheKey()
	}

	// Register target
	if name, ok := crateConfig["name"].(string); ok {
		for _, task := range tasks {
			tg.generatedTargets[name] = task.TaskID
			globalTaskRegistry.RegisterTarget(name, task.TaskID, tg.CurrentModule)
			log.Printf("Generated Rust build task: %s -> %v", name, task.Outputs)
			break
		}
	}

	return tasks, nil
}

// findRustSourceFiles recursively finds all .rs files in a directory
func findRustSourceFiles(rootDir string) ([]string, error) {
	var rsFiles []string

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip target directory (Cargo build output)
		if info.IsDir() && info.Name() == "target" {
			return filepath.SkipDir
		}

		// Collect .rs files
		if !info.IsDir() && strings.HasSuffix(path, ".rs") {
			rsFiles = append(rsFiles, path)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Also add Cargo.toml and Cargo.lock if they exist
	cargoToml := filepath.Join(rootDir, "Cargo.toml")
	if _, err := os.Stat(cargoToml); err == nil {
		rsFiles = append(rsFiles, cargoToml)
	}
	cargoLock := filepath.Join(rootDir, "Cargo.lock")
	if _, err := os.Stat(cargoLock); err == nil {
		rsFiles = append(rsFiles, cargoLock)
	}

	return rsFiles, nil
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
