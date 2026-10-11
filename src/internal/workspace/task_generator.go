package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"buildy/internal/config"
	"buildy/internal/resource"
	"buildy/pkg/util"
)

// sanitizeModuleNameForPath converts a module name to a filesystem-safe path that stays inside
// the directory it is joined to. This handles special prefixes like "@fetch:", which contain
// characters invalid on Windows, and modules outside the workspace root ("../lib"), whose ".."
// components become "_up" as in object paths.
func sanitizeModuleNameForPath(moduleName string) string {
	if moduleName == "" {
		return moduleName
	}
	result := strings.ReplaceAll(moduleName, "@", "_")
	result = strings.ReplaceAll(result, ":", "_")
	parts := strings.Split(filepath.ToSlash(result), "/")
	for i, part := range parts {
		if part == ".." {
			parts[i] = "_up"
		}
	}
	return strings.Join(parts, "/")
}

// TaskGenerator generates a module's build tasks from its typed config. It never changes the
// config: everything it derives (resolved lists, absolute paths, the template's view of a
// target) is a new value.
type TaskGenerator struct {
	Platform           string
	Architecture       string
	Configuration      string
	ToolchainManager   *resource.ToolchainManager
	TemplateEngine     *BuildTemplateEngine
	CurrentToolchain   *resource.ToolchainConfig
	ToolMatcher        *resource.ToolMatcher
	CommandBuilder     *resource.CommandBuilder
	DependencyResolver *resource.DependencyResolver
	VarEnv             *util.VariableEnvironment
	PathResolver       *PathResolver
	TaskIDGen          *TaskIDGenerator
	TargetRegistry     *TargetRegistry
	CurrentModule      string
	Workspace          *Workspace

	// Track generated targets for cross-module dependency resolution
	generatedTargets map[string]string // target name -> link task ID

	// The module's output directory, from GenerateTargetTasks
	outputDir string

	// Artifact tracking
	artifactOutputs    map[string][]string // artifact name -> output paths
	artifactTaskIDs    map[string][]string // artifact name -> task IDs
	outputToTaskID     map[string]string   // output file path -> task ID
	stagingTasksByName map[string][]string // staging name -> task IDs
	stagingDestByName  map[string]string   // staging name -> destination directory
}

// NewTaskGenerator creates a new TaskGenerator
func NewTaskGenerator(
	platform, architecture, configuration string,
	toolchainManager *resource.ToolchainManager,
	templateEngine *BuildTemplateEngine,
	dependencyResolver *resource.DependencyResolver,
	varEnv *util.VariableEnvironment,
	workspace *Workspace,
) *TaskGenerator {
	return &TaskGenerator{
		Platform:           platform,
		Architecture:       architecture,
		Configuration:      configuration,
		ToolchainManager:   toolchainManager,
		TemplateEngine:     templateEngine,
		DependencyResolver: dependencyResolver,
		VarEnv:             varEnv,
		Workspace:          workspace,
		generatedTargets:   make(map[string]string),
		artifactOutputs:    make(map[string][]string),
		artifactTaskIDs:    make(map[string][]string),
		outputToTaskID:     make(map[string]string),
		stagingTasksByName: make(map[string][]string),
		stagingDestByName:  make(map[string]string),
	}
}

// SetPathResolver sets the path resolver for this generator
func (tg *TaskGenerator) SetPathResolver(pr *PathResolver) {
	tg.PathResolver = pr
}

// SetToolchain sets up the toolchain for task generation
func (tg *TaskGenerator) SetToolchain(tc *resource.ToolchainConfig) error {
	tg.CurrentToolchain = tc

	toolMatcher, err := resource.NewToolMatcher(tc)
	if err != nil {
		return err
	}
	tg.ToolMatcher = toolMatcher
	tg.CommandBuilder = resource.NewCommandBuilder(tc, toolMatcher, tg.Platform, tg.Configuration)
	return nil
}

// isWindows reports whether the target platform is Windows
func (tg *TaskGenerator) isWindows() bool {
	return tg.Platform == config.Windows
}

// getBuildContext returns the context filtered lists are resolved against
func (tg *TaskGenerator) getBuildContext() BuildContext {
	toolchainName := ""
	if tg.CurrentToolchain != nil {
		toolchainName = tg.CurrentToolchain.Name
	}
	return BuildContext{
		Platform:      tg.Platform,
		Architecture:  tg.Architecture,
		Configuration: tg.Configuration,
		Toolchain:     toolchainName,
	}
}

// resolve resolves the variables of every string, failing on one it can't resolve.
func (tg *TaskGenerator) resolve(values []string, what string) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, v := range values {
		resolved, err := resolveStrict(tg.VarEnv, v, what)
		if err != nil {
			return nil, err
		}
		result = append(result, resolved)
	}
	return result, nil
}

// finalizeTask sets common task fields and calculates cache key
func (tg *TaskGenerator) finalizeTask(task *BuildTask, toolchain string, estimatedTime float64, resources ResourceRequirements) {
	task.Platform = tg.Platform
	task.Architecture = tg.Architecture
	task.Configuration = tg.Configuration
	task.Toolchain = toolchain
	task.EstimatedTime = estimatedTime
	task.ResourceRequirements = resources
}

// moduleSettings resolves the module's compile and link settings for the build, and their
// variables.
func (tg *TaskGenerator) moduleSettings(file *config.File) (config.Settings, error) {
	var mappings config.Ordered[config.Ordered[[]string]]
	if tg.CurrentToolchain != nil {
		mappings = tg.CurrentToolchain.FlagMappings
	}
	s, err := file.Settings(tg.getBuildContext(), mappings)
	if err != nil {
		return s, fmt.Errorf("module %s: %w", tg.moduleName(), err)
	}
	where := "the settings of module " + tg.moduleName()
	if s.CppStandard, err = resolveStrict(tg.VarEnv, s.CppStandard, where); err != nil {
		return s, err
	}
	if s.CStandard, err = resolveStrict(tg.VarEnv, s.CStandard, where); err != nil {
		return s, err
	}
	if s.OutputPrefix != nil {
		prefix, err := resolveStrict(tg.VarEnv, *s.OutputPrefix, where)
		if err != nil {
			return s, err
		}
		s.OutputPrefix = &prefix
	}
	for _, list := range []*[]string{&s.Defines, &s.Flags, &s.LinkFlags} {
		if *list, err = tg.resolve(*list, where); err != nil {
			return s, err
		}
	}
	return s, nil
}

// targetLists is a target's lists for the build: filters resolved, variables resolved.
type targetLists struct {
	sources     []config.Source
	includeDirs []string
	libDirs     []string
	libs        []string
	frameworks  []string
	deps        []string
	defines     []string
	flags       []string
}

// generateTargetTasks generates a target's tasks from the template for its language and type.
// settings are the module's; the target adds its own defines and flags to a copy of them.
func (tg *TaskGenerator) generateTargetTasks(
	target *config.Target,
	targetType string,
	settings config.Settings,
	outputDir string,
	existingTasks []*BuildTask,
) ([]*BuildTask, error) {
	targetName := target.Name
	where := fmt.Sprintf("target '%s' in module %s", targetName, tg.moduleName())

	template, err := tg.TemplateEngine.LookupTemplate(target.Language, targetType)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	metadata := &template.Metadata

	module := sanitizeModuleNameForPath(tg.CurrentModule)
	if module == "" {
		module = "workspace"
	}

	// The toolchain: the module's, or the platform's for the template's language
	toolMatcher := tg.ToolMatcher
	commandBuilder := tg.CommandBuilder
	targetToolchain := tg.CurrentToolchain
	if tg.CurrentToolchain == nil || tg.CurrentToolchain.Language != metadata.ToolchainLanguage {
		targetToolchain = tg.ToolchainManager.FindByLanguage(metadata.ToolchainLanguage, tg.Platform, tg.Architecture)
		if targetToolchain == nil {
			return nil, fmt.Errorf("%s: no %s toolchain found for platform %s-%s", where, metadata.ToolchainLanguage, tg.Platform, tg.Architecture)
		}
		if toolMatcher, err = resource.NewToolMatcher(targetToolchain); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		commandBuilder = resource.NewCommandBuilder(targetToolchain, toolMatcher, tg.Platform, tg.Configuration)
	}

	// The target's lists, filtered for the build (with the module's toolchain) and resolved
	ctx := tg.getBuildContext()
	lists := targetLists{}
	for _, l := range []struct {
		dst    *[]string
		values []string
		field  string
	}{
		{&lists.includeDirs, target.IncludeDirs.Resolve(ctx), "include_dirs"},
		{&lists.libDirs, target.LibDirs, "lib_dirs"},
		{&lists.libs, target.Libs.Resolve(ctx), "libs"},
		{&lists.frameworks, target.Frameworks, "frameworks"},
		{&lists.deps, target.Deps, "deps"},
		{&lists.defines, target.Defines.Resolve(ctx), "defines"},
		{&lists.flags, target.Flags.Resolve(ctx), "flags"},
	} {
		if *l.dst, err = tg.resolve(l.values, "the "+l.field+" of "+where); err != nil {
			return nil, err
		}
	}
	for _, source := range target.Sources.Resolve(ctx) {
		if source.Pattern, err = resolveStrict(tg.VarEnv, source.Pattern, "the sources of "+where); err != nil {
			return nil, err
		}
		lists.sources = append(lists.sources, source)
	}

	var depLibFiles []string
	if metadata.PreProcessing.ResolveDeps {
		if depLibFiles, err = tg.resolveDeps(&lists); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
	}

	// A target that depends on artifacts (generated sources and headers) sees their outputs as
	// virtual files for glob matching, and every one of its tasks, compiles included, runs after
	// the tasks that produce them.
	tg.PathResolver.ClearVirtualFiles()
	var artifactTaskIDs []string
	for _, artifactName := range target.DependsOn.Artifacts {
		taskIDs, exists := tg.artifactTaskIDs[artifactName]
		if !exists {
			return nil, fmt.Errorf("%s depends on artifact '%s', which is not a generate or transform artifact of the module",
				where, artifactName)
		}
		artifactTaskIDs = append(artifactTaskIDs, taskIDs...)
		outputs := tg.artifactOutputs[artifactName]
		tg.PathResolver.AddVirtualFiles(outputs)
		util.LogInfo("Target depends on artifact '%s': added %d virtual files for glob matching", artifactName, len(outputs))
	}

	// The template's view of the target: ${item.X}
	item := map[string]any{
		"name":         targetName,
		"language":     target.Language,
		"type":         targetType,
		"module":       module,
		"module_dir":   tg.PathResolver.ConfigFileDir, // object paths are relative to it
		"include_dirs": lists.includeDirs,
		"lib_dirs":     lists.libDirs,
		"libs":         lists.libs,
		"frameworks":   lists.frameworks,
		"deps":         lists.deps,
		"defines":      lists.defines,
		"flags":        lists.flags,
		"depends_on":   map[string]any{"targets": []string(target.DependsOn.Targets), "artifacts": []string(target.DependsOn.Artifacts)},
	}
	if metadata.PreProcessing.ResolveSources {
		sources, err := tg.PathResolver.ResolveSources(lists.sources)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		item["sources"] = sources
	}
	if metadata.PreProcessing.ResolveIncludeDirs {
		item["include_dirs"] = tg.PathResolver.ResolveIncludeDirs(lists.includeDirs)
	}
	path := target.Path
	if path != "" || metadata.PreProcessing.ResolvePath {
		if path, err = resolveStrict(tg.VarEnv, path, "the path of "+where); err != nil {
			return nil, err
		}
		if metadata.PreProcessing.ResolvePath {
			if path == "" {
				path = "."
			}
			path = tg.PathResolver.ResolveRelativePath(path)
		}
		item["path"] = path
	}
	if target.OutputPrefix != nil {
		prefix, err := resolveStrict(tg.VarEnv, *target.OutputPrefix, "the output_prefix of "+where)
		if err != nil {
			return nil, err
		}
		item["output_prefix"] = prefix
	}
	for _, l := range []struct {
		key    string
		values []string
	}{{"build_tags", target.BuildTags}, {"ldflags", target.Ldflags}, {"gcflags", target.Gcflags}, {"features", target.Features}} {
		if len(l.values) > 0 {
			if item[l.key], err = tg.resolve(l.values, "the "+l.key+" of "+where); err != nil {
				return nil, err
			}
		}
	}
	for _, v := range []struct{ key, value string }{{"buildmode", target.Buildmode}, {"bin", target.Bin}} {
		if v.value != "" {
			if item[v.key], err = resolveStrict(tg.VarEnv, v.value, "the "+v.key+" of "+where); err != nil {
				return nil, err
			}
		}
	}

	// The template's view of the settings, ${config.X}: the module's, with the target's
	scope, err := tg.targetSettings(target, settings, lists, where)
	if err != nil {
		return nil, err
	}

	// The toolchain's variables (e.g. where Cargo puts artifacts) are visible to the template
	templateVarEnv := tg.VarEnv.CreateChild()
	for _, key := range targetToolchain.Variables.Keys {
		templateVarEnv.SetVariable(key, targetToolchain.Variables.Values[key], "toolchain "+targetToolchain.Name)
	}

	tasks, err := tg.TemplateEngine.ExpandTemplate(
		template.Name,
		item,
		scope,
		outputDir,
		tg.TaskIDGen,
		toolMatcher,
		commandBuilder,
		tg.Platform,
		tg.Architecture,
		tg.Configuration,
		targetToolchain.Name,
		existingTasks,
		templateVarEnv,
	)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	// The tasks run where their toolchain says (natively or in Docker)
	execSpec, err := resource.ExecSpecFromToolchain(targetToolchain)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}

	for _, task := range tasks {
		task.Module = tg.CurrentModule
		task.Target = targetName
		task.Exec = execSpec
		if len(task.LinkLibs) > 0 {
			for _, file := range depLibFiles {
				task.Inputs = append(task.Inputs, NewTaskInput(file))
			}
		}
		for _, id := range artifactTaskIDs {
			if !hasString(task.Dependencies, id) {
				task.Dependencies = append(task.Dependencies, id)
			}
		}
	}

	if metadata.PostProcessing.RegisterTarget {
		tg.registerTarget(targetName, tasks)
	}

	return tasks, nil
}

// targetSettings returns the template's view of a target's settings (${config.X}): the module's
// settings with the target's defines and flags (and its dependencies' defines) appended, then its
// compile: and link: sections, then its removals.
func (tg *TaskGenerator) targetSettings(target *config.Target, s config.Settings, lists targetLists, where string) (map[string]any, error) {
	ctx := tg.getBuildContext()
	resolve := func(f config.Filtered[string], field string) ([]string, error) {
		return tg.resolve(f.Resolve(ctx), "the "+field+" of "+where)
	}
	defines := append(append([]string{}, s.Defines...), lists.defines...)
	flags := append(append([]string{}, s.Flags...), lists.flags...)
	linkFlags := append([]string{}, s.LinkFlags...)

	compileFlags, err := resolve(target.Compile.Flags, "compile.flags")
	if err != nil {
		return nil, err
	}
	compileRemove, err := resolve(target.Compile.RemoveFlags, "compile.remove_flags")
	if err != nil {
		return nil, err
	}
	compileDefines, err := resolve(target.Compile.Defines, "compile.defines")
	if err != nil {
		return nil, err
	}
	linkAdd, err := resolve(target.Link.Flags, "link.flags")
	if err != nil {
		return nil, err
	}
	linkRemove, err := resolve(target.Link.RemoveFlags, "link.remove_flags")
	if err != nil {
		return nil, err
	}
	removeFlags, err := tg.resolve(target.RemoveFlags, "the remove_flags of "+where)
	if err != nil {
		return nil, err
	}
	removeDefines, err := tg.resolve(target.RemoveDefines, "the remove_defines of "+where)
	if err != nil {
		return nil, err
	}

	flags = config.Remove(append(flags, compileFlags...), compileRemove)
	defines = append(defines, compileDefines...)
	linkFlags = config.Remove(append(linkFlags, linkAdd...), linkRemove)
	flags = config.Remove(flags, removeFlags)
	defines = config.Remove(defines, removeDefines)

	scope := map[string]any{
		"defines":    defines,
		"flags":      flags,
		"link_flags": linkFlags,
	}
	if s.CppStandard != "" {
		scope["cpp_standard"] = s.CppStandard
	}
	if s.CStandard != "" {
		scope["c_standard"] = s.CStandard
	}
	if s.OutputPrefix != nil {
		scope["output_prefix"] = *s.OutputPrefix
	}
	return scope, nil
}

// registerTarget registers a target's final task (its link or build task) and outputs, for
// libs, depends_on, copies and staging in any module.
func (tg *TaskGenerator) registerTarget(name string, tasks []*BuildTask) {
	var targetTask *BuildTask
	for _, task := range tasks {
		if task.TaskType == "link" || strings.Contains(task.TaskType, "build") {
			targetTask = task
			break
		}
	}
	if targetTask == nil && len(tasks) > 0 {
		targetTask = tasks[len(tasks)-1]
	}
	if targetTask == nil {
		return
	}
	tg.generatedTargets[name] = targetTask.TaskID
	outputPath := ""
	if len(targetTask.Outputs) > 0 {
		outputPath = targetTask.Outputs[0]
	}
	globalTaskRegistry.RegisterTargetWithOutput(name, targetTask.TaskID, tg.CurrentModule, outputPath)
	if len(targetTask.Outputs) > 0 {
		globalTaskRegistry.RegisterTargetOutputs(targetTask.TaskID, targetTask.Outputs)
	}
	util.LogInfo("Registered target '%s' -> task '%s' (output: %s)", name, targetTask.TaskID, outputPath)
}

// GenerateTargetTasks generates a module's generate and transform artifacts and its targets.
// Every module's targets are generated before any module's packaging tasks
// (GeneratePackagingTasks), which may name targets of other modules.
func (tg *TaskGenerator) GenerateTargetTasks(file *config.File, outputDir string) ([]*BuildTask, error) {
	tasks := []*BuildTask{}
	tg.outputDir = outputDir

	if tg.TaskIDGen == nil {
		moduleName := tg.CurrentModule
		if moduleName == "" {
			moduleName = "workspace"
		}
		tg.TaskIDGen = NewTaskIDGenerator(moduleName)
	}

	settings, err := tg.moduleSettings(file)
	if err != nil {
		return nil, err
	}

	// Artifacts first, so targets can depend on them (depends_on.artifacts)
	generateTasks, err := tg.generateGenerateTasks(file.Artifacts.Generate, outputDir)
	if err != nil {
		return nil, err
	}
	tasks = append(tasks, generateTasks...)
	transformTasks, err := tg.generateTransformTasks(file.Artifacts.Transform, outputDir)
	if err != nil {
		return nil, err
	}
	tasks = append(tasks, transformTasks...)

	var overrides *config.Targets
	if p := file.Platforms[tg.Platform]; p != nil {
		overrides = &p.Targets
	}
	for i, section := range file.Targets.Sections() {
		for _, target := range section.Targets {
			if overrides != nil {
				for _, o := range overrides.Sections()[i].Targets {
					if o.Name == target.Name {
						target = target.WithOverride(o)
						util.LogInfo("Applied platform '%s' overrides to target '%s'", tg.Platform, target.Name)
					}
				}
			}
			targetTasks, err := tg.generateTargetTasks(target, section.Type, settings, outputDir, tasks)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, targetTasks...)
		}
	}

	return tasks, nil
}

// GeneratePackagingTasks generates a module's copy artifacts, staging and install tasks, once
// every module's targets exist. existing are the module's tasks from GenerateTargetTasks.
func (tg *TaskGenerator) GeneratePackagingTasks(file *config.File, existing []*BuildTask) ([]*BuildTask, error) {
	tasks := []*BuildTask{}

	copyTasks, err := tg.generateArtifactCopyTasks(file.Artifacts.Copy, tg.outputDir)
	if err != nil {
		return nil, err
	}
	tasks = append(tasks, copyTasks...)

	if file.Staging != nil {
		stagingTasks, err := tg.generateStagingTasks(file.Staging, tg.outputDir)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, stagingTasks...)
	}

	if len(file.Install) > 0 {
		installTasks, err := tg.generateInstallTasks(file.Install, file.Project, tg.outputDir)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, installTasks...)
	}

	return tasks, nil
}

// resolveDeps adds the target's dependencies' settings to its lists, ahead of its own, and
// returns the dependencies' library files, which are inputs of the target's link: a rebuilt
// dependency relinks. Libraries not in a dependency's lib dirs are system libraries.
func (tg *TaskGenerator) resolveDeps(lists *targetLists) ([]string, error) {
	if tg.DependencyResolver == nil || len(lists.deps) == 0 {
		return nil, nil
	}
	dep, err := tg.DependencyResolver.ResolveDependencies(lists.deps)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve dependencies: %w", err)
	}
	prepend := func(dep, own []string) []string {
		return append(append([]string{}, dep...), own...)
	}
	lists.includeDirs = prepend(dep.IncludeDirs, lists.includeDirs)
	lists.libDirs = prepend(dep.LibDirs, lists.libDirs)
	lists.libs = prepend(dep.Libs, lists.libs)
	lists.frameworks = prepend(dep.Frameworks, lists.frameworks)
	lists.defines = prepend(dep.Defines, lists.defines)
	var sources []config.Source
	for _, s := range dep.Sources {
		sources = append(sources, config.Source{Pattern: s})
	}
	lists.sources = append(sources, lists.sources...)

	var libFiles []string
	for _, lib := range dep.Libs {
		if file := tg.findLibrary(lib, dep.LibDirs); file != "" {
			libFiles = append(libFiles, file)
		}
	}
	return libFiles, nil
}

// findLibrary returns the file the linker would use for -l<lib> from one of dirs, or "".
func (tg *TaskGenerator) findLibrary(lib string, dirs []string) string {
	for _, dir := range dirs {
		for _, name := range config.LibraryFileNames(tg.Platform, lib) {
			path := filepath.Join(dir, name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path
			}
		}
	}
	return ""
}

// moduleName names the generator's module for messages.
func (tg *TaskGenerator) moduleName() string {
	if tg.CurrentModule == "" {
		return "workspace"
	}
	return tg.CurrentModule
}

// moduleDir is the directory a module's relative paths are relative to.
func (tg *TaskGenerator) moduleDir() string {
	if tg.PathResolver != nil && tg.PathResolver.ConfigFileDir != "" {
		return tg.PathResolver.ConfigFileDir
	}
	if tg.Workspace != nil {
		return tg.Workspace.RootDir
	}
	return ""
}

// absolute makes a path relative to the module directory absolute.
func (tg *TaskGenerator) absolute(path string) string {
	if !filepath.IsAbs(path) && tg.moduleDir() != "" {
		path = filepath.Join(tg.moduleDir(), path)
	}
	return filepath.Clean(path)
}

// registerArtifact records a named artifact's outputs and the tasks that produce them, for the
// module's targets and staging, and for staging in other modules. Artifact names are unique
// across the workspace.
func (tg *TaskGenerator) registerArtifact(name string, outputs, taskIDs []string) error {
	if name == "" {
		return nil
	}
	tg.artifactOutputs[name] = outputs
	tg.artifactTaskIDs[name] = taskIDs
	return globalTaskRegistry.RegisterArtifact(name, tg.moduleName(), outputs, taskIDs)
}

// globInputs expands input patterns (relative to the module directory), in order.
func (tg *TaskGenerator) globInputs(patterns []string, what string) ([]string, error) {
	var files []string
	for _, pattern := range patterns {
		resolved, err := resolveStrict(tg.VarEnv, pattern, "the inputs of "+what)
		if err != nil {
			return nil, err
		}
		matches, err := filepath.Glob(tg.absolute(resolved))
		if err != nil {
			return nil, fmt.Errorf("%s in module %s: invalid glob pattern '%s': %w", what, tg.moduleName(), resolved, err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s in module %s: no files match its inputs %v, so it has no outputs", what, tg.moduleName(), patterns)
	}
	return files, nil
}

// toolchainFor returns the toolchain an artifact's tool names.
func (tg *TaskGenerator) toolchainFor(name, what string) (*resource.ToolchainConfig, util.ExecSpec, error) {
	toolchain := tg.ToolchainManager.GetToolchain(name)
	if toolchain == nil {
		return nil, util.ExecSpec{}, fmt.Errorf("%s in module %s: %w", what, tg.moduleName(), unknownToolchain(name, "tool"))
	}
	execSpec, err := resource.ExecSpecFromToolchain(toolchain)
	if err != nil {
		return nil, util.ExecSpec{}, fmt.Errorf("%s: %w", what, err)
	}
	return toolchain, execSpec, nil
}

// setToolchainVariables makes a toolchain's variables visible in env.
func setToolchainVariables(env *util.VariableEnvironment, toolchain *resource.ToolchainConfig) {
	for _, key := range toolchain.Variables.Keys {
		env.SetVariable(key, toolchain.Variables.Values[key], "toolchain")
	}
}

// generateTransformTasks generates a task per input file of each artifacts.transform
func (tg *TaskGenerator) generateTransformTasks(transforms []*config.Transform, outputDir string) ([]*BuildTask, error) {
	var tasks []*BuildTask
	for _, t := range transforms {
		what := fmt.Sprintf("artifacts.transform '%s'", t.Name)
		toolchain, execSpec, err := tg.toolchainFor(t.Tool, what)
		if err != nil {
			return nil, err
		}
		inputFiles, err := tg.globInputs(t.Inputs, what)
		if err != nil {
			return nil, err
		}

		var outputs, taskIDs []string
		for _, inputFile := range inputFiles {
			inputFile = filepath.Clean(inputFile)
			fileName := filepath.Base(inputFile)
			baseName := strings.TrimSuffix(fileName, filepath.Ext(inputFile))
			env := tg.VarEnv.CreateChild()
			env.SetVariable("filename", fileName, "transform")
			env.SetVariable("basename", baseName, "transform")
			env.SetVariable("input", inputFile, "transform")
			env.SetVariable("out_dir", outputDir, "transform")
			env.SetVariable("output_dir", outputDir, "transform")

			output, err := resolveStrict(env, t.Outputs, "the outputs of "+what)
			if err != nil {
				return nil, err
			}
			output = tg.absolute(output)

			// The tool for this file: the one that takes its extension
			tool, err := toolchain.ToolFor(inputFile, "transform", "convert", "compile")
			if err != nil {
				return nil, fmt.Errorf("%s in module %s: %w", what, tg.moduleName(), err)
			}
			env.SetVariable("output", output, "transform")
			setToolchainVariables(env, toolchain)
			flags := tool.Flags["common"]
			if configFlags, ok := tool.Flags[tg.Configuration]; ok {
				flags = configFlags
			}
			values := map[string][]string{"input": {inputFile}, "output": {output}, "defines": nil, "includes": nil}
			if values["flags"], err = resolveAll(env, flags, "the flags of tool "+tool.Name); err != nil {
				return nil, err
			}
			if values["args"], err = resolveAll(env, t.Args, "the args of "+what); err != nil {
				return nil, err
			}
			command, err := tool.Expand(tg.Platform, execSpec, envValues(values, env))
			if err != nil {
				return nil, fmt.Errorf("%s in module %s: %w", what, tg.moduleName(), err)
			}

			taskID := tg.TaskIDGen.Next("transform", baseName)
			task := NewBuildTask(taskID, "transform", []TaskInput{NewTaskInput(inputFile)}, []string{output}, []string{}, command)
			tg.finalizeTask(&task, t.Tool, 1.0, ResourceRequirements{CPUCores: 1, MemoryMB: 256, DiskMB: 50})
			task.Target = t.Name
			task.Exec = execSpec

			tasks = append(tasks, &task)
			outputs = append(outputs, output)
			taskIDs = append(taskIDs, taskID)
			tg.outputToTaskID[output] = taskID
			util.LogInfo("Generated transform task: %s -> %s", inputFile, output)
		}
		if err := tg.registerArtifact(t.Name, outputs, taskIDs); err != nil {
			return nil, err
		}
	}
	return tasks, nil
}

// generateGenerateTasks generates the tasks of artifacts.generate: a template file filled in
// with variables, or a tool run on each input file
func (tg *TaskGenerator) generateGenerateTasks(generates []*config.Generate, outputDir string) ([]*BuildTask, error) {
	var tasks []*BuildTask
	genDir := filepath.Join(outputDir, "gen")

	for _, g := range generates {
		what := fmt.Sprintf("artifacts.generate '%s'", g.Name)
		if g.Template != "" {
			task, err := tg.generateFromTemplate(g, outputDir, genDir, what)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, task)
			continue
		}

		toolchain, execSpec, err := tg.toolchainFor(g.Tool, what)
		if err != nil {
			return nil, err
		}
		inputFiles, err := tg.globInputs(g.Inputs, what)
		if err != nil {
			return nil, err
		}

		var outputs, taskIDs []string
		for _, inputFile := range inputFiles {
			inputFile = filepath.Clean(inputFile)
			fileName := filepath.Base(inputFile)
			baseName := strings.TrimSuffix(fileName, filepath.Ext(inputFile))
			env := tg.VarEnv.CreateChild()
			env.SetVariable("filename", fileName, "generate")
			env.SetVariable("basename", baseName, "generate")
			env.SetVariable("input", inputFile, "generate")
			env.SetVariable("gen_dir", genDir, "generate")
			env.SetVariable("out_dir", outputDir, "generate")
			env.SetVariable("output_dir", outputDir, "generate")
			setToolchainVariables(env, toolchain)

			tool, err := toolchain.ToolFor(inputFile, "generate", "compile")
			if err != nil {
				return nil, fmt.Errorf("%s in module %s: %w", what, tg.moduleName(), err)
			}
			var resolvedOutputs []string
			for _, pattern := range g.Outputs {
				resolved, err := resolveStrict(env, pattern, "the outputs of "+what)
				if err != nil {
					return nil, err
				}
				resolvedOutputs = append(resolvedOutputs, tg.absolute(resolved))
			}
			env.SetVariable("output", resolvedOutputs[0], "generate")
			env.SetVariable("output_dir", filepath.Dir(resolvedOutputs[0]), "generate")

			args, err := resolveAll(env, g.Args, "the args of "+what)
			if err != nil {
				return nil, err
			}
			values := map[string][]string{"input": {inputFile}, "output": {resolvedOutputs[0]}, "args": args, "flags": nil, "defines": nil, "includes": nil}
			command, err := tool.Expand(tg.Platform, execSpec, envValues(values, env))
			if err != nil {
				return nil, fmt.Errorf("%s in module %s: %w", what, tg.moduleName(), err)
			}
			// A command without ${args} gets them at the end
			if tmpl, _ := tool.Command.For(tg.Platform); !hasString(tmpl.Placeholders(), "args") {
				if command, err = appendArgs(command, args, execSpec); err != nil {
					return nil, fmt.Errorf("%s in module %s: %w", what, tg.moduleName(), err)
				}
			}

			taskID := tg.TaskIDGen.Next("generate", baseName)
			task := NewBuildTask(taskID, "generate", []TaskInput{NewTaskInput(inputFile)}, resolvedOutputs, []string{}, command)
			tg.finalizeTask(&task, g.Tool, 0.5, ResourceRequirements{CPUCores: 1, MemoryMB: 128, DiskMB: 10})
			task.Target = g.Name
			task.Exec = execSpec

			tasks = append(tasks, &task)
			outputs = append(outputs, resolvedOutputs...)
			taskIDs = append(taskIDs, taskID)
			for _, outputPath := range resolvedOutputs {
				tg.outputToTaskID[outputPath] = taskID
			}
			util.LogInfo("Generated codegen task: %s -> %v", inputFile, resolvedOutputs)
		}
		if err := tg.registerArtifact(g.Name, outputs, taskIDs); err != nil {
			return nil, err
		}
	}
	return tasks, nil
}

// appendArgs adds arguments at the end of a command (quoted, for a shell script).
func appendArgs(command util.Command, args []string, exec util.ExecSpec) (util.Command, error) {
	if command.Script == "" {
		command.Args = append(command.Args, args...)
		return command, nil
	}
	quote := util.ShellQuoter(exec)
	for _, arg := range args {
		q, err := quote(arg)
		if err != nil {
			return util.Command{}, err
		}
		command.Script += " " + q
	}
	return command, nil
}

// generateFromTemplate generates the task that fills a template file in: @NAME@ and ${NAME} are
// replaced by the variables, in the order they are written.
func (tg *TaskGenerator) generateFromTemplate(g *config.Generate, outputDir, genDir, what string) (*BuildTask, error) {
	templatePath := tg.absolute(g.Template)
	env := tg.VarEnv.CreateChild()
	env.SetVariable("gen_dir", genDir, "generate")
	env.SetVariable("out_dir", outputDir, "generate")
	env.SetVariable("output_dir", outputDir, "generate")
	env.SetVariable("template", templatePath, "generate")

	outputPath, err := resolveStrict(env, g.Output, "the output of "+what)
	if err != nil {
		return nil, err
	}
	outputPath = tg.absolute(outputPath)

	var substitutions [][2]string
	for _, key := range g.Variables.Keys {
		value, err := resolveStrict(env, string(g.Variables.Values[key]), "variable "+key+" of "+what)
		if err != nil {
			return nil, err
		}
		substitutions = append(substitutions, [2]string{key, value})
	}

	taskID := tg.TaskIDGen.Next("generate", g.Name)
	task := NewNativeTask(taskID, "generate", []TaskInput{NewTaskInput(templatePath)}, []string{outputPath}, []string{},
		NativeOp{Op: NativeTemplate, Substitutions: substitutions})
	tg.finalizeTask(&task, "", 0.1, ResourceRequirements{CPUCores: 1, MemoryMB: 64, DiskMB: 1})
	task.Target = g.Name

	if err := tg.registerArtifact(g.Name, []string{outputPath}, []string{taskID}); err != nil {
		return nil, err
	}
	tg.outputToTaskID[outputPath] = taskID
	util.LogInfo("Generated template task: %s -> %s", templatePath, outputPath)
	return &task, nil
}

// generateStagingTasks generates tasks to populate a staging area using hierarchical folder structure
func (tg *TaskGenerator) generateStagingTasks(staging *config.Staging, outputDir string) ([]*BuildTask, error) {
	stagingName := staging.Name
	if stagingName == "" {
		stagingName = "staging"
	}

	stagingVarEnv := tg.VarEnv.CreateChild()
	stagingVarEnv.SetVariable("output_dir", outputDir, "staging")
	stagingVarEnv.SetVariable("out_dir", outputDir, "staging")

	destination := filepath.Join(outputDir, "staging")
	if staging.Destination != "" {
		resolved, err := resolveStrict(stagingVarEnv, staging.Destination, "staging.destination")
		if err != nil {
			return nil, err
		}
		// Relative to the module directory, so module-level staging can use "../bin"
		destination = tg.absolute(resolved)
	}

	// Windows always copies (symlinks require admin or developer mode)
	defaultUseSymlinks := staging.UseSymlinks && !tg.isWindows()

	tasks, err := tg.processStagingContents(staging.Contents, destination, "", defaultUseSymlinks, stagingVarEnv)
	if err != nil {
		return nil, err
	}
	var stagingTaskIDs []string
	for _, t := range tasks {
		stagingTaskIDs = append(stagingTaskIDs, t.TaskID)
	}

	tg.stagingTasksByName[stagingName] = stagingTaskIDs
	tg.stagingDestByName[stagingName] = destination
	util.LogInfo("Generated %d staging tasks for '%s' at %s", len(tasks), stagingName, destination)
	return tasks, nil
}

// useSymlink is an item's choice of symlink or copy: its own, else the staging area's. Windows
// always copies.
func (tg *TaskGenerator) useSymlink(own *bool, def bool) bool {
	if tg.isWindows() {
		return false
	}
	if own != nil {
		return *own
	}
	return def
}

// processStagingContents recursively processes the hierarchical folder structure
func (tg *TaskGenerator) processStagingContents(
	contents []*config.StagingFolder,
	stagingRoot string,
	currentPath string,
	defaultUseSymlinks bool,
	varEnv *util.VariableEnvironment,
) ([]*BuildTask, error) {
	var tasks []*BuildTask

	for _, item := range contents {
		// Folder names are relative to the staging root ("." is the root); one that resolves to an
		// absolute path (${workspace}/...) is used as is
		folderName := "."
		if item.Folder != "" {
			resolved, err := resolveStrict(varEnv, item.Folder, "staging folder")
			if err != nil {
				return nil, err
			}
			folderName = filepath.Clean(resolved)
		}
		var folderPath string
		switch {
		case folderName == ".":
			folderPath = filepath.Join(stagingRoot, currentPath)
		case filepath.IsAbs(folderName):
			folderPath = folderName
		default:
			folderPath = filepath.Join(stagingRoot, currentPath, folderName)
		}

		// Targets (executables and libraries)
		for _, ref := range item.Targets {
			task, err := tg.createTargetStagingTask(ref.Name, folderPath, tg.useSymlink(ref.UseSymlink, defaultUseSymlinks))
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, task)
		}

		// Artifacts: the module's own, or another module's (workspace-level staging assembles
		// the artifacts of every module)
		for _, ref := range item.Artifacts {
			outputs, taskIDs := tg.artifactOutputs[ref.Name], tg.artifactTaskIDs[ref.Name]
			if len(outputs) == 0 {
				outputs, taskIDs, _ = globalTaskRegistry.GetArtifact(ref.Name)
			}
			if len(outputs) == 0 {
				return nil, fmt.Errorf("staging folder '%s' in module %s: no module defines artifact '%s' with outputs", folderName, tg.moduleName(), ref.Name)
			}
			useSymlink := tg.useSymlink(ref.UseSymlink, defaultUseSymlinks)
			for i, sourcePath := range outputs {
				var deps []string
				if i < len(taskIDs) {
					deps = []string{taskIDs[i]}
				}
				tasks = append(tasks, tg.createSymlinkOrCopyTask(sourcePath, filepath.Join(folderPath, filepath.Base(sourcePath)), useSymlink, deps))
			}
		}

		// Files and globs (a filtered list: files: [windows: ["mylib.dll"], linux: ["libmylib.so"]])
		for _, file := range item.Files.Resolve(tg.getBuildContext()) {
			useSymlink := tg.useSymlink(file.UseSymlink, defaultUseSymlinks)
			resolved, err := resolveStrict(varEnv, file.Source, "staging files of folder '"+folderName+"'")
			if err != nil {
				return nil, err
			}
			sourcePattern := tg.absolute(resolved)

			matches := []string{sourcePattern}
			if strings.Contains(sourcePattern, "*") {
				if matches, err = filepath.Glob(sourcePattern); err != nil {
					return nil, fmt.Errorf("staging folder '%s': invalid glob pattern '%s': %w", folderName, sourcePattern, err)
				}
			}
			for _, match := range matches {
				match = filepath.Clean(match)
				// A file a task produces depends on that task
				var deps []string
				if taskID := tg.outputToTaskID[match]; taskID != "" {
					deps = []string{taskID}
				} else if taskID, ok := globalTaskRegistry.GetTaskIDByOutputPath(match); ok {
					deps = []string{taskID}
				}
				tasks = append(tasks, tg.createSymlinkOrCopyTask(match, filepath.Join(folderPath, filepath.Base(match)), useSymlink, deps))
			}
		}

		// Nested folders
		if len(item.Contents) > 0 {
			nestedPath := currentPath
			if folderName != "." {
				nestedPath = filepath.Join(currentPath, folderName)
			}
			nestedTasks, err := tg.processStagingContents(item.Contents, stagingRoot, nestedPath, defaultUseSymlinks, varEnv)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, nestedTasks...)
		}
	}

	return tasks, nil
}

// createTargetStagingTask creates a staging task copying (or linking) a target's output
func (tg *TaskGenerator) createTargetStagingTask(targetName string, destFolder string, useSymlink bool) (*BuildTask, error) {
	taskID, found := tg.generatedTargets[targetName]
	if !found {
		// Workspace-level staging names the targets of every module
		taskID, found = globalTaskRegistry.GetLinkTaskID(targetName)
	}
	if !found {
		return nil, fmt.Errorf("staging folder %s in module %s: '%s' is not a target of the workspace", destFolder, tg.moduleName(), targetName)
	}
	sourcePath, ok := globalTaskRegistry.GetTargetOutputPath(targetName)
	if !ok || sourcePath == "" {
		return nil, fmt.Errorf("staging folder %s in module %s: target '%s' has no output", destFolder, tg.moduleName(), targetName)
	}
	return tg.createSymlinkOrCopyTask(sourcePath, filepath.Join(destFolder, filepath.Base(sourcePath)), useSymlink, []string{taskID}), nil
}

// createSymlinkOrCopyTask creates a task that either symlinks or copies a file
func (tg *TaskGenerator) createSymlinkOrCopyTask(source, dest string, useSymlink bool, dependencies []string) *BuildTask {
	source = filepath.Clean(source)
	dest = filepath.Clean(dest)

	taskID := tg.TaskIDGen.Next("stage", filepath.Base(dest))
	var task BuildTask
	if useSymlink {
		// A symlink's key is the path it points to, not the content behind it
		task = NewNativeTask(taskID, NativeSymlink, []TaskInput{{Path: source, Hash: "symlink:" + source}}, []string{dest}, dependencies, NativeOp{Op: NativeSymlink})
	} else {
		task = NewNativeTask(taskID, NativeCopy, []TaskInput{NewTaskInput(source)}, []string{dest}, dependencies, NativeOp{Op: NativeCopy})
	}
	tg.finalizeTask(&task, "", 0.1, ResourceRequirements{CPUCores: 1, MemoryMB: 64, DiskMB: 10})
	util.LogInfo("Generated %s task: %s -> %s", task.TaskType, source, dest)
	return &task
}

// generateInstallTasks generates the tasks that package staging areas
func (tg *TaskGenerator) generateInstallTasks(installs []*config.Install, project *config.Project, outputDir string) ([]*BuildTask, error) {
	var tasks []*BuildTask

	workspaceRoot := ""
	if tg.Workspace != nil {
		workspaceRoot = tg.Workspace.RootDir
	}
	projectName, projectVersion := "unknown", "0.0.0"
	if project != nil {
		if project.Name != "" {
			projectName = project.Name
		}
		if project.Version != "" {
			projectVersion = project.Version
		}
	}

	for _, inst := range installs {
		name := inst.Name
		env := tg.VarEnv.CreateChild()
		env.SetVariable("output_dir", outputDir, "install")
		env.SetVariable("out_dir", outputDir, "install")
		env.SetVariable("project_name", projectName, "install")
		env.SetVariable("project_version", projectVersion, "install")

		destination := filepath.Join(outputDir, "dist")
		if inst.Destination != "" {
			resolved, err := resolveStrict(env, inst.Destination, "the destination of install '"+name+"'")
			if err != nil {
				return nil, err
			}
			destination = resolved
		}
		if !filepath.IsAbs(destination) && workspaceRoot != "" {
			destination = filepath.Join(workspaceRoot, destination)
		}

		format := inst.Format
		if format == "" || format == "tgz" {
			format = "tar.gz"
		}
		followSymlinks := inst.FollowSymlinks == nil || *inst.FollowSymlinks

		filename := name
		if inst.Filename != "" {
			resolved, err := resolveStrict(env, inst.Filename, "the filename of install '"+name+"'")
			if err != nil {
				return nil, err
			}
			filename = resolved
		}

		// The install packages its staging area's destination (the module's only one if unnamed)
		stagingName := inst.Staging
		if stagingName == "" && len(tg.stagingDestByName) == 1 {
			for only := range tg.stagingDestByName {
				stagingName = only
			}
		}
		stagingDir, ok := tg.stagingDestByName[stagingName]
		if !ok {
			return nil, fmt.Errorf("install '%s' in module %s: staging '%s' is not a staging section of the module", name, tg.moduleName(), stagingName)
		}

		outputFile := filepath.Join(destination, filename+"."+format)

		taskID := tg.TaskIDGen.Next("install", name)
		task := NewNativeTask(taskID, "install", []TaskInput{{Path: stagingDir}}, []string{outputFile},
			tg.stagingTasksByName[stagingName], NativeOp{Op: NativeArchive, Format: format, FollowSymlinks: followSymlinks})
		tg.finalizeTask(&task, "", 5.0, ResourceRequirements{CPUCores: 1, MemoryMB: 512, DiskMB: 500})
		tasks = append(tasks, &task)
		util.LogInfo("Generated install task: %s -> %s (format: %s)", stagingDir, outputFile, format)
	}

	return tasks, nil
}

// generateArtifactCopyTasks generates copy tasks from the artifacts.copy section
func (tg *TaskGenerator) generateArtifactCopyTasks(copies []*config.Copy, outputDir string) ([]*BuildTask, error) {
	var tasks []*BuildTask

	for _, c := range copies {
		env := tg.VarEnv.CreateChild()
		env.SetVariable("output_dir", outputDir, "copy")
		env.SetVariable("out_dir", outputDir, "copy")
		dest := c.Destination
		if dest == "" {
			dest = c.Dest
		}

		// Files matching globs, into a directory
		if len(c.Sources) > 0 {
			what := fmt.Sprintf("artifacts.copy '%s'", c.Name)
			resolved, err := resolveStrict(env, dest, "the destination of "+what)
			if err != nil {
				return nil, err
			}
			destDir := tg.absolute(resolved)

			var outputs, taskIDs []string
			for _, pattern := range c.Sources {
				pattern, err := resolveStrict(env, pattern, "the sources of "+what)
				if err != nil {
					return nil, err
				}
				files, err := filepath.Glob(tg.absolute(pattern))
				if err != nil {
					return nil, fmt.Errorf("%s in module %s: invalid glob pattern '%s': %w", what, tg.moduleName(), pattern, err)
				}
				for _, srcFile := range files {
					srcFile = filepath.Clean(srcFile)
					destFile := filepath.Join(destDir, filepath.Base(srcFile))
					taskID := tg.TaskIDGen.Next("copy", filepath.Base(srcFile))
					task := NewNativeTask(taskID, NativeCopy, []TaskInput{NewTaskInput(srcFile)}, []string{destFile}, nil, NativeOp{Op: NativeCopy})
					tg.finalizeTask(&task, "", 0.1, ResourceRequirements{CPUCores: 1, MemoryMB: 64, DiskMB: 10})
					tasks = append(tasks, &task)
					outputs = append(outputs, destFile)
					taskIDs = append(taskIDs, taskID)
					util.LogInfo("Generated batch copy task: %s -> %s", srcFile, destFile)
				}
			}
			if len(outputs) > 0 {
				if err := tg.registerArtifact(c.Name, outputs, taskIDs); err != nil {
					return nil, err
				}
			}
			continue
		}

		// One file: a target's output, or a source path
		var dependencies []string
		source := c.Source
		if c.Target != "" {
			outputPath, found := globalTaskRegistry.GetTargetOutputPath(c.Target)
			if !found || outputPath == "" {
				return nil, fmt.Errorf("artifacts.copy in module %s: target '%s' is not a target of the workspace", tg.moduleName(), c.Target)
			}
			source = outputPath
			if taskID, found := globalTaskRegistry.GetLinkTaskID(c.Target); found {
				dependencies = append(dependencies, taskID)
			}
		}
		var err error
		if source, err = resolveStrict(env, source, "the source of artifacts.copy"); err != nil {
			return nil, err
		}
		if dest, err = resolveStrict(env, dest, "the destination of artifacts.copy"); err != nil {
			return nil, err
		}
		endsInSeparator := strings.HasSuffix(dest, "/") || strings.HasSuffix(dest, string(filepath.Separator))
		source, dest = tg.absolute(source), tg.absolute(dest)

		for _, dep := range c.DependsOn {
			if taskID, found := tg.generatedTargets[dep]; found {
				dependencies = append(dependencies, taskID)
			} else if taskID, found := globalTaskRegistry.GetLinkTaskID(dep); found {
				dependencies = append(dependencies, taskID)
			} else {
				return nil, fmt.Errorf("artifacts.copy in module %s depends on '%s', which is not a target of the workspace", tg.moduleName(), dep)
			}
		}

		// A destination ending in a separator, or without an extension when the source has one, is
		// a directory
		if endsInSeparator || (filepath.Ext(dest) == "" && filepath.Ext(source) != "") {
			dest = filepath.Join(dest, filepath.Base(source))
		}

		taskID := tg.TaskIDGen.Next("copy", filepath.Base(dest))
		task := NewNativeTask(taskID, NativeCopy, []TaskInput{NewTaskInput(source)}, []string{dest}, dependencies, NativeOp{Op: NativeCopy})
		tg.finalizeTask(&task, "", 0.1, ResourceRequirements{CPUCores: 1, MemoryMB: 64, DiskMB: 10})
		tasks = append(tasks, &task)
		util.LogInfo("Generated copy task: %s -> %s", source, dest)
	}

	return tasks, nil
}
