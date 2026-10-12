package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"buildy/internal/config"
	"buildy/internal/resource"
	"buildy/pkg/util"
)

// ConfigParser generates the tasks of a workspace from its typed configuration
type ConfigParser struct {
	Platform           string
	Architecture       string
	Configuration      string
	CLIDefines         map[string]string
	VarEnv             *util.VariableEnvironment // the workspace root's variables
	PathResolver       *PathResolver
	ToolchainManager   *resource.ToolchainManager
	DefaultToolchain   string
	TemplateEngine     *BuildTemplateEngine
	CurrentToolchain   *resource.ToolchainConfig
	Workspace          *Workspace
	CurrentModule      string
	DependencyResolver *resource.DependencyResolver
	TaskIDGen          *TaskIDGenerator
	ConfigFileDir      string
	CacheDir           string           // ${cache_dir}
	Outputs            *DeclaredOutputs // every output generated so far, for file patterns
	names              *generatedNames  // the generation's targets and artifacts, by name
}

// Generated is what generating a workspace's tasks produced: the tasks, in the order they were
// generated, and each target's final task (its link or build), by target name, for --target.
type Generated struct {
	Tasks   []*BuildTask
	Targets map[string]string
}

// NewConfigParser creates a new ConfigParser
func NewConfigParser(
	ctx BuildContext,
	cliDefines map[string]string,
	toolchainManager *resource.ToolchainManager,
	defaultToolchain string,
	templateEngine *BuildTemplateEngine,
	workspace *Workspace,
	dependencyResolver *resource.DependencyResolver,
) *ConfigParser {
	return &ConfigParser{
		Platform:           ctx.Platform,
		Architecture:       ctx.Architecture,
		Configuration:      ctx.Configuration,
		CLIDefines:         cliDefines,
		ToolchainManager:   toolchainManager,
		DefaultToolchain:   defaultToolchain,
		TemplateEngine:     templateEngine,
		Workspace:          workspace,
		DependencyResolver: dependencyResolver,
		TaskIDGen:          NewTaskIDGenerator("workspace"),
		Outputs:            NewDeclaredOutputs(),
	}
}

// buildContext is the platform, architecture and configuration being built.
func (cp *ConfigParser) buildContext() BuildContext {
	return BuildContext{Platform: cp.Platform, Architecture: cp.Architecture, Configuration: cp.Configuration}
}

// unknownToolchain is the error for a toolchain name that names no toolchain.
func unknownToolchain(name, where string) error {
	return fmt.Errorf("toolchain '%s' (from %s) is not a known toolchain; buildy --list-toolchains lists them", name, where)
}

// SelectToolchain selects a config's toolchain: --toolchain, project.toolchain, toolchain,
// environment.toolchains.<platform>, environment.toolchains.default, or the platform's default.
func (cp *ConfigParser) SelectToolchain(file *config.File) (*resource.ToolchainConfig, error) {
	if cp.ToolchainManager == nil {
		return nil, fmt.Errorf("internal error: no toolchain manager")
	}
	type choice struct{ name, where string }
	choices := []choice{{cp.DefaultToolchain, "--toolchain"}}
	if file.Project != nil {
		choices = append(choices, choice{file.Project.Toolchain, "project.toolchain"})
	}
	choices = append(choices,
		choice{file.Toolchain, "toolchain"},
		choice{file.Environment.Toolchains[cp.Platform], "environment.toolchains." + cp.Platform},
		choice{file.Environment.Toolchains["default"], "environment.toolchains.default"},
	)
	for _, c := range choices {
		if c.name == "" {
			continue
		}
		tc := cp.ToolchainManager.GetToolchain(c.name)
		if tc == nil {
			return nil, unknownToolchain(c.name, c.where)
		}
		util.LogInfo("Using toolchain %s (from %s)", tc.Name, c.where)
		return tc, nil
	}

	tc, err := cp.ToolchainManager.AutoDetect(cp.Platform, cp.Architecture)
	if err != nil || tc != nil {
		return tc, err
	}
	var names []string
	for _, tc := range cp.ToolchainManager.ListToolchains() {
		names = append(names, tc.Name)
	}
	return nil, fmt.Errorf("no suitable toolchain found for %s-%s. Available toolchains: %v", cp.Platform, cp.Architecture, names)
}

// GenerateWorkspaceTasks generates the tasks of every module. A build of selected targets runs
// part of them (TaskGraph.Select): generation is cheap, and every target is needed to resolve
// names anyway.
func (cp *ConfigParser) GenerateWorkspaceTasks() (*Generated, error) {
	if cp.Workspace == nil {
		return nil, fmt.Errorf("no workspace configured")
	}
	if err := cp.checkNames(); err != nil {
		return nil, err
	}
	if err := checkTargetNames(cp.Workspace); err != nil {
		return nil, err
	}

	// What this generation produces and names
	cp.Outputs = NewDeclaredOutputs()
	cp.names = newGeneratedNames()

	// The workspace root's variables (workspace-level staging and install use them)
	rootVars, err := Variables(cp.Workspace.Config.File, cp.buildContext(), cp.Workspace.RootDir, cp.CLIDefines)
	if err != nil {
		return nil, err
	}
	cp.VarEnv = rootVars

	// Generate every module's targets, in a fixed order, then every module's copies, then every
	// module's staging and install: each may name what the earlier ones produce, in any module
	modulePaths := make([]string, 0, len(cp.Workspace.Modules))
	for modulePath := range cp.Workspace.Modules {
		modulePaths = append(modulePaths, modulePath)
	}
	sort.Strings(modulePaths)
	type generated struct {
		gen   *TaskGenerator
		file  *config.File
		tasks []*BuildTask
	}
	allTasks := []*BuildTask{}
	modules := make([]generated, 0, len(modulePaths))
	for _, modulePath := range modulePaths {
		moduleTasks, gen, file, err := cp.processModule(modulePath, cp.Workspace.Modules[modulePath])
		if err != nil {
			return nil, err
		}
		modules = append(modules, generated{gen, file, moduleTasks})
		allTasks = append(allTasks, moduleTasks...)
	}
	for _, step := range []func(*TaskGenerator, *config.File) ([]*BuildTask, error){
		(*TaskGenerator).GenerateCopyTasks,
		(*TaskGenerator).GenerateStagingTasks,
	} {
		for i, modulePath := range modulePaths {
			packaging, err := step(modules[i].gen, modules[i].file)
			if err != nil {
				return nil, fmt.Errorf("failed to generate tasks for module %s: %w", modulePath, err)
			}
			for _, task := range packaging {
				task.Module = modulePath
			}
			allTasks = append(allTasks, packaging...)
		}
	}

	// Second pass: resolve cross-module dependencies (target names -> task IDs)
	if err := cp.resolveCrossModuleDependencies(allTasks); err != nil {
		return nil, err
	}

	// Third pass: generate workspace-level staging and install tasks from root config
	workspaceTasks, err := cp.generateWorkspaceLevelTasks()
	if err != nil {
		return nil, fmt.Errorf("failed to generate workspace-level tasks: %w", err)
	}
	allTasks = append(allTasks, workspaceTasks...)

	util.LogProgress("Generated %d tasks from %d modules", len(allTasks), len(cp.Workspace.Modules))
	targets := make(map[string]string, len(cp.names.targets))
	for name, task := range cp.names.targets {
		targets[name] = task.TaskID
	}
	return &Generated{Tasks: allTasks, Targets: targets}, nil
}

// checkTargetNames checks that no two targets share a name: libraries are linked by name, and
// executables and libraries share bin/ and lib/ in the output directory. Load the modules first.
func checkTargetNames(ws *Workspace) error {
	if !ws.discovered {
		return fmt.Errorf("internal error: the workspace's modules were not loaded")
	}
	modulePaths := make([]string, 0, len(ws.Modules))
	for modulePath := range ws.Modules {
		modulePaths = append(modulePaths, modulePath)
	}
	sort.Strings(modulePaths)
	defined := map[string]string{} // target name -> module
	for _, modulePath := range modulePaths {
		module := ws.Modules[modulePath]
		for _, name := range module.Targets {
			if other, ok := defined[name]; ok {
				if other == modulePath {
					return fmt.Errorf("target '%s' is defined twice in module %s (%s); target names must be unique across the workspace",
						name, modulePath, module.Path)
				}
				return fmt.Errorf("target '%s' is defined in module %s (%s) and in module %s (%s); target names must be unique across the workspace",
					name, other, ws.Modules[other].Path, modulePath, module.Path)
			}
			defined[name] = modulePath
		}
	}
	return nil
}

// checkNames checks the names the workspace's configs use that only the loaded toolchains can
// confirm: every filter key names a platform, an architecture, a configuration (debug, release,
// or one a config declares) or a toolchain, and every abstract keyword is one some toolchain's
// flag_mappings maps.
func (cp *ConfigParser) checkNames() error {
	files := map[string]*config.File{}
	if root := cp.Workspace.Config.File; root != nil {
		files[filepath.Clean(root.Path)] = root
	}
	for _, m := range cp.Workspace.Modules {
		files[filepath.Clean(m.File.Path)] = m.File
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	names := config.Names{Configurations: []string{"debug", "release"}}
	for _, path := range paths {
		for _, c := range files[path].ConfigurationNames() {
			if !hasString(names.Configurations, c) {
				names.Configurations = append(names.Configurations, c)
			}
		}
	}
	sort.Strings(names.Configurations)
	for _, tc := range cp.ToolchainManager.ListToolchains() {
		names.Toolchains = append(names.Toolchains, tc.Name)
		for _, k := range tc.Keywords() {
			if !hasString(names.Keywords, k) {
				names.Keywords = append(names.Keywords, k)
			}
		}
	}
	sort.Strings(names.Keywords)

	var problems []string
	for _, path := range paths {
		problems = append(problems, config.CheckFilterKeys(files[path].FilterKeys, names)...)
		problems = append(problems, config.CheckKeywords(files[path].Keywords, names.Keywords)...)
	}
	if len(problems) > 0 {
		return fmt.Errorf("the workspace's configs name what doesn't exist:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// processModule generates the target tasks of a module, from its config with the workspace
// root's settings merged in
func (cp *ConfigParser) processModule(modulePath string, moduleInfo *ModuleInfo) ([]*BuildTask, *TaskGenerator, *config.File, error) {
	util.LogInfo("Processing module: %s", modulePath)

	moduleParser := NewConfigParser(cp.buildContext(), cp.CLIDefines, cp.ToolchainManager, cp.DefaultToolchain,
		cp.TemplateEngine, cp.Workspace, cp.DependencyResolver)
	moduleParser.CacheDir = cp.CacheDir
	moduleParser.Outputs = cp.Outputs
	moduleParser.CurrentModule = modulePath
	moduleParser.names = cp.names
	moduleParser.TaskIDGen = NewTaskIDGenerator(modulePath)

	// Paths in a module are relative to its directory; in a standalone config (a dependency's
	// override config) to the workspace root, which is the fetched source
	if cp.Workspace.ConfigPath != "" {
		moduleParser.ConfigFileDir = cp.Workspace.RootDir
	} else {
		moduleParser.ConfigFileDir = filepath.Dir(moduleInfo.Path)
	}

	file := config.Inherit(cp.Workspace.Config.File, moduleInfo.File)
	moduleTasks, gen, err := moduleParser.GenerateTasks(file)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate tasks for module %s: %w", modulePath, err)
	}
	for _, task := range moduleTasks {
		task.Module = modulePath
	}
	return moduleTasks, gen, file, nil
}

// generateWorkspaceLevelTasks generates staging and install tasks from the workspace root config
func (cp *ConfigParser) generateWorkspaceLevelTasks() ([]*BuildTask, error) {
	root := cp.Workspace.Config.File
	if root == nil {
		return nil, nil
	}

	// A root config that is also a module (".", as in discovery mode) has had its staging and
	// install generated with the module; generating them again would produce every output twice
	for _, module := range cp.Workspace.Modules {
		if filepath.Clean(module.Path) == filepath.Clean(root.Path) {
			return nil, nil
		}
	}

	// The root's output directory: the modules' too, unless one sets its own output
	outputDir, err := cp.getOutputDir(root)
	if err != nil {
		return nil, err
	}

	tg := NewTaskGenerator(cp.Platform, cp.Architecture, cp.Configuration, cp.ToolchainManager, cp.TemplateEngine,
		cp.DependencyResolver, cp.VarEnv, cp.Workspace)
	tg.TaskIDGen = NewTaskIDGenerator("workspace")
	tg.names = cp.names
	cp.Outputs.AddDir(outputDir)
	tg.Outputs = cp.Outputs
	tg.PathResolver = NewPathResolver(cp.Workspace.RootDir, cp.Workspace.RootDir, cp.Outputs)
	if cp.CurrentToolchain != nil {
		if err := tg.SetToolchain(cp.CurrentToolchain); err != nil {
			return nil, err
		}
	}
	return tg.generateStagingAndInstall(root.Staging, root.Install, root.Project, outputDir)
}

// resolveCrossModuleDependencies resolves dependencies by target name, now that every module's
// targets are registered. A dependency is a task ID or a target name (depends_on.targets); a name
// that is neither is an error. A link task's libraries that are workspace targets become inputs:
// the library files their final task produces, so a changed library relinks (and graph
// validation orders the link after it). The others are system libraries.
func (cp *ConfigParser) resolveCrossModuleDependencies(allTasks []*BuildTask) error {
	tasksByID := make(map[string]*BuildTask, len(allTasks))
	for _, task := range allTasks {
		tasksByID[task.TaskID] = task
	}

	for _, task := range allTasks {
		resolvedDeps := []string{}
		add := func(id string) {
			if !hasString(resolvedDeps, id) {
				resolvedDeps = append(resolvedDeps, id)
			}
		}
		for _, dep := range task.Dependencies {
			if tasksByID[dep] != nil {
				add(dep)
			} else if target, ok := cp.names.targets[dep]; ok {
				util.LogInfo("Resolved dependency '%s' to task '%s' in task '%s'", dep, target.TaskID, task.TaskID)
				add(target.TaskID)
			} else {
				return fmt.Errorf("%s depends on '%s', which is not a target in the workspace", task.Describe(), dep)
			}
		}
		for _, lib := range task.LinkLibs {
			target, ok := cp.names.targets[lib]
			if !ok {
				util.LogVerbose("Library '%s' of task '%s' is not a workspace target: a system library", lib, task.TaskID)
				continue
			}
			for _, output := range target.Outputs {
				if !hasInput(task, output) {
					task.Inputs = append(task.Inputs, NewTaskInput(output))
				}
			}
		}
		task.Dependencies = resolvedDeps
	}

	return nil
}

func hasInput(task *BuildTask, path string) bool {
	for _, input := range task.Inputs {
		if input.Path == path {
			return true
		}
	}
	return false
}

// GenerateTasks generates a module's target tasks from its (inherited) config, and returns the
// task generator that generates its packaging tasks later
func (cp *ConfigParser) GenerateTasks(file *config.File) ([]*BuildTask, *TaskGenerator, error) {
	if cp.TaskIDGen == nil {
		moduleName := cp.CurrentModule
		if moduleName == "" {
			moduleName = "workspace"
		}
		cp.TaskIDGen = NewTaskIDGenerator(moduleName)
	}

	tc, err := cp.SelectToolchain(file)
	if err != nil {
		return nil, nil, err
	}
	cp.CurrentToolchain = tc
	util.LogInfo("Using toolchain: %s (%s)", tc.Name, tc.Description)

	root := ""
	if cp.Workspace != nil {
		root = cp.Workspace.RootDir
	}
	if cp.VarEnv, err = Variables(file, cp.buildContext(), root, cp.CLIDefines); err != nil {
		return nil, nil, err
	}

	outputDir, err := cp.getOutputDir(file)
	if err != nil {
		return nil, nil, err
	}
	cp.VarEnv.SetVariable("output_dir", outputDir, "built-in")
	cp.VarEnv.SetVariable("out_dir", outputDir, "built-in")
	cp.VarEnv.SetVariable("gen_dir", filepath.Join(outputDir, "gen"), "built-in")
	if cp.CacheDir != "" {
		cp.VarEnv.SetVariable("cache_dir", cp.CacheDir, "built-in")
	}

	cp.Outputs.AddDir(outputDir)
	cp.PathResolver = NewPathResolver(cp.ConfigFileDir, root, cp.Outputs)

	taskGen := NewTaskGenerator(cp.Platform, cp.Architecture, cp.Configuration, cp.ToolchainManager, cp.TemplateEngine,
		cp.DependencyResolver, cp.VarEnv, cp.Workspace)
	taskGen.SetPathResolver(cp.PathResolver)
	if err := taskGen.SetToolchain(tc); err != nil {
		return nil, nil, err
	}
	taskGen.TaskIDGen = cp.TaskIDGen
	taskGen.CurrentModule = cp.CurrentModule
	taskGen.names = cp.names
	taskGen.Outputs = cp.Outputs

	tasks, err := taskGen.GenerateTargetTasks(file, outputDir)
	return tasks, taskGen, err
}

// Variables builds a config's variables for a build, in this order of priority, highest first:
//
//  1. --define VAR=value
//  2. imported environment variables (variables.import_env_vars)
//  3. the configuration's variables (configurations.<config>.variables)
//  4. the architecture's (architectures.<arch>.variables)
//  5. the platform's (variables.platforms.<platform>, platforms.<platform>.variables)
//  6. the module's variables, then the workspace root's (variables)
//  7. the built-in variables (platform, arch, config, workspace, project_name, ...)
//
// file is a module's config with the root's merged in (config.Inherit), or the root's own.
func Variables(file *config.File, ctx BuildContext, workspaceRoot string, cliDefines map[string]string) (*util.VariableEnvironment, error) {
	builtins := util.NewVariableEnvironment(nil)
	builtins.PushScope("built-in")
	builtins.SetVariable("platform", ctx.Platform, "built-in")
	builtins.SetVariable("arch", ctx.Architecture, "built-in")
	builtins.SetVariable("config", ctx.Configuration, "built-in")
	if workspaceRoot != "" {
		builtins.SetVariable("workspace", workspaceRoot, "built-in")
	}
	projectName, projectVersion := "unknown", "0.0.0"
	if file.Project != nil {
		if file.Project.Name != "" {
			projectName = file.Project.Name
		}
		if file.Project.Version != "" {
			projectVersion = file.Project.Version
		}
	}
	builtins.SetVariable("project_name", projectName, "built-in")
	builtins.SetVariable("project_version", projectVersion, "built-in")
	baseDir := "build"
	if file.Output.BaseDir != "" {
		baseDir = file.Output.BaseDir
	}
	builtins.SetVariable("base_dir", baseDir, "output.base_dir")

	layer := func(parent *util.VariableEnvironment, source string, vars ...map[string]config.Scalar) *util.VariableEnvironment {
		env := parent.CreateChild()
		env.PushScope(source)
		for _, m := range vars {
			for name, value := range m {
				env.SetVariable(name, string(value), source)
			}
		}
		return env
	}
	env := layer(builtins, "variables", file.Variables.Values)
	platformVars := []map[string]config.Scalar{file.Variables.Platforms[ctx.Platform]}
	if p := file.Platforms[ctx.Platform]; p != nil {
		platformVars = append(platformVars, p.Variables)
	}
	env = layer(env, "platform["+ctx.Platform+"]", platformVars...)
	if a := file.Architectures[ctx.Architecture]; a != nil {
		env = layer(env, "architecture["+ctx.Architecture+"]", a.Variables)
	}
	var configVars []map[string]config.Scalar
	for _, levels := range []map[string]*config.Level{file.Configurations, file.Environment.Configurations} {
		if c := levels[ctx.Configuration]; c != nil {
			configVars = append(configVars, c.Variables)
		}
	}
	env = layer(env, "configuration["+ctx.Configuration+"]", configVars...)

	imports := env.CreateChild()
	imports.PushScope("import_env_vars")
	for _, imp := range file.Variables.ImportEnvVars {
		if value, ok := os.LookupEnv(imp.Name); ok {
			imports.SetVariable(imp.Name, value, "environment")
		} else if !imp.Default.Null {
			imports.SetVariable(imp.Name, imp.Default.Value, "default")
		} else {
			return nil, fmt.Errorf("required environment variable '%s' is not set (variables.import_env_vars at %s)\n"+
				"       To fix: export %s=/path/to/value", imp.Name, imp.Pos, imp.Name)
		}
	}

	cli := imports.CreateChild()
	cli.PushScope("cli")
	for name, value := range cliDefines {
		cli.SetVariable(name, value, "cli")
	}
	return cli, nil
}

// getOutputDir resolves the module's output directory: output.pattern, by default
// ${base_dir}/<platform>-<arch>-<config>
func (cp *ConfigParser) getOutputDir(file *config.File) (string, error) {
	root := ""
	if cp.Workspace != nil {
		root = cp.Workspace.RootDir
	}
	return outputDir(file, cp.buildContext(), cp.VarEnv, root)
}

// outputDir resolves a config's output directory for a build with its variables: output.pattern,
// by default ${base_dir}/<platform>-<arch>-<config>, relative to the workspace root.
func outputDir(file *config.File, ctx BuildContext, env *util.VariableEnvironment, root string) (string, error) {
	pattern := fmt.Sprintf("${base_dir}/%s-%s-%s", ctx.Platform, ctx.Architecture, ctx.Configuration)
	if file.Output.Pattern != "" {
		pattern = file.Output.Pattern
	}
	resolved, err := resolveStrict(env, pattern, "output.pattern")
	if err != nil {
		return "", err
	}
	if root != "" && !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, resolved)
	}
	util.LogInfo("Output directory pattern '%s' resolved to '%s'", pattern, resolved)
	return filepath.Clean(resolved), nil
}

// OutputRoots returns the directories that hold the workspace's build outputs, for --clean: for the
// root config and each module (whose output is the root's unless it sets its own), the output base
// directory (output.base_dir, build/) when its output directory lies in it, as in the default
// layout, so every configuration's; otherwise its output directory for ctx. Load the modules first.
func (ws *Workspace) OutputRoots(ctx BuildContext, cliDefines map[string]string) ([]string, error) {
	files := []*config.File{ws.Config.File}
	paths := make([]string, 0, len(ws.Modules))
	for path := range ws.Modules {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		files = append(files, config.Inherit(ws.Config.File, ws.Modules[path].File))
	}
	var roots []string
	for _, file := range files {
		env, err := Variables(file, ctx, ws.RootDir, cliDefines)
		if err != nil {
			return nil, err
		}
		dir, err := outputDir(file, ctx, env, ws.RootDir)
		if err != nil {
			return nil, err
		}
		base := "build"
		if file.Output.BaseDir != "" {
			base = file.Output.BaseDir
		}
		if base = filepath.Join(ws.RootDir, base); dir == base || strings.HasPrefix(dir, base+string(filepath.Separator)) {
			dir = base
		}
		if !hasString(roots, dir) {
			roots = append(roots, dir)
		}
	}
	return roots, nil
}
