package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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
	CurrentModule      string
	Workspace          *Workspace
	Outputs            *DeclaredOutputs // the outputs of every task generated so far, for file patterns
	names              *generatedNames  // the generation's targets and artifacts, by name

	// The module's output directory, from GenerateTargetTasks
	outputDir string

	stagedByName      map[string][]string // staging name -> the files its tasks produce
	stagingDestByName map[string]string   // staging name -> destination directory
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
		Outputs:            NewDeclaredOutputs(),
		stagedByName:       make(map[string][]string),
		stagingDestByName:  make(map[string]string),
	}
}

// SetPathResolver sets the path resolver for this generator
func (tg *TaskGenerator) SetPathResolver(pr *PathResolver) {
	tg.PathResolver = pr
}

// SetToolchain sets the module's toolchain
func (tg *TaskGenerator) SetToolchain(tc *resource.ToolchainConfig) {
	tg.CurrentToolchain = tc
	tg.ToolMatcher = resource.NewToolMatcher(tc)
	tg.CommandBuilder = resource.NewCommandBuilder(tc, tg.Platform, tg.Configuration)
}

// hostCanSymlink reports whether staging can make symlinks: not on a Windows host, where a
// symlink needs administrator rights or developer mode. The host decides, not the target: a
// Windows package staged on Linux may use symlinks (its install stores what they point to).
var hostCanSymlink = runtime.GOOS != "windows"

// getBuildContext returns the context the module's filtered lists are resolved against: the build
// and the module's toolchain.
func (tg *TaskGenerator) getBuildContext() BuildContext {
	return tg.contextFor(tg.CurrentToolchain)
}

// contextFor returns the context of a target or tool that uses toolchain tc (nil: none).
func (tg *TaskGenerator) contextFor(tc *resource.ToolchainConfig) BuildContext {
	ctx := BuildContext{Platform: tg.Platform, Architecture: tg.Architecture, Configuration: tg.Configuration}
	if tc != nil {
		ctx.Toolchain = tc.Name
	}
	return ctx
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

// finalizeTask sets the build context and toolchain of a task
func (tg *TaskGenerator) finalizeTask(task *BuildTask, toolchain string) {
	task.Platform = tg.Platform
	task.Architecture = tg.Architecture
	task.Configuration = tg.Configuration
	task.Toolchain = toolchain
}

// moduleSettings resolves the module's compile and link settings for the build and a target
// toolchain (its toolchain-name filter keys and its flag_mappings), and their variables.
func (tg *TaskGenerator) moduleSettings(file *config.File, tc *resource.ToolchainConfig) (config.Settings, error) {
	s, err := file.Settings(tg.contextFor(tc), tc.FlagMappings)
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
// The module's settings, for the target's toolchain (moduleSettings), get the target's own defines
// and flags added to a copy of them.
func (tg *TaskGenerator) generateTargetTasks(
	file *config.File,
	target *config.Target,
	targetType string,
	outputDir string,
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
		if targetToolchain, err = tg.ToolchainManager.FindByLanguage(metadata.ToolchainLanguage, tg.Platform, tg.Architecture); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		if targetToolchain == nil {
			return nil, fmt.Errorf("%s: no %s toolchain found for platform %s-%s", where, metadata.ToolchainLanguage, tg.Platform, tg.Architecture)
		}
		toolMatcher = resource.NewToolMatcher(targetToolchain)
		commandBuilder = resource.NewCommandBuilder(targetToolchain, tg.Platform, tg.Configuration)
	}

	// The module's settings and the target's lists, filtered for the build with the target's
	// toolchain, and resolved
	settings, err := tg.moduleSettings(file, targetToolchain)
	if err != nil {
		return nil, err
	}
	ctx := tg.contextFor(targetToolchain)
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

	// A target that depends on artifacts (generated sources and headers) runs every one of its
	// tasks, compiles included, after the tasks that produce them: a header is no declared input.
	// (Its source patterns match generated sources whether or not it names their artifact.)
	var artifactTaskIDs []string
	for _, artifactName := range target.DependsOn.Artifacts {
		a, exists := tg.names.artifacts[artifactName]
		if !exists || a.module != tg.moduleName() {
			return nil, fmt.Errorf("%s depends on artifact '%s', which is not a generate or transform artifact of the module",
				where, artifactName)
		}
		artifactTaskIDs = append(artifactTaskIDs, a.taskIDs...)
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
	}{{"build_tags", target.BuildTags.Resolve(ctx)}, {"ldflags", target.Ldflags.Resolve(ctx)}, {"gcflags", target.Gcflags.Resolve(ctx)}, {"features", target.Features.Resolve(ctx)}} {
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
	scope, err := tg.targetSettings(target, ctx, settings, lists, where)
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
		tg.finalizeTask(task, targetToolchain.Name)
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
func (tg *TaskGenerator) targetSettings(target *config.Target, ctx BuildContext, s config.Settings, lists targetLists, where string) (map[string]any, error) {
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
	defines = config.UniqueDefines(config.Remove(defines, removeDefines))

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
	tg.names.targets[name] = targetTask
	util.LogInfo("Registered target '%s' -> task '%s' (outputs: %v)", name, targetTask.TaskID, targetTask.Outputs)
}

// GenerateTargetTasks generates a module's generate and transform artifacts and its targets.
// Every module's targets are generated before any module's packaging tasks
// (GeneratePackagingTasks), which may name targets of other modules.
func (tg *TaskGenerator) GenerateTargetTasks(file *config.File, outputDir string) ([]*BuildTask, error) {
	tasks := []*BuildTask{}
	tg.outputDir = outputDir

	// The module's settings for its own toolchain: a problem in them is the module's, whatever
	// its targets (each target resolves them for its own toolchain)
	if _, err := tg.moduleSettings(file, tg.CurrentToolchain); err != nil {
		return nil, err
	}

	// Artifacts first, so targets can depend on them (depends_on.artifacts)
	generateTasks, err := tg.generateGenerateTasks(file, outputDir)
	if err != nil {
		return nil, err
	}
	tasks = append(tasks, generateTasks...)
	tg.Outputs.Add(generateTasks...)
	transformTasks, err := tg.generateTransformTasks(file, outputDir)
	if err != nil {
		return nil, err
	}
	tasks = append(tasks, transformTasks...)
	tg.Outputs.Add(transformTasks...)

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
			targetTasks, err := tg.generateTargetTasks(file, target, section.Type, outputDir)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, targetTasks...)
			tg.Outputs.Add(targetTasks...)
		}
	}

	return tasks, nil
}

// GenerateCopyTasks generates a module's copy artifacts, once every module's targets exist. Every
// module's copies are generated before any staging, whose patterns may name what they copy.
func (tg *TaskGenerator) GenerateCopyTasks(file *config.File) ([]*BuildTask, error) {
	tasks, err := tg.generateArtifactCopyTasks(file.Artifacts.Copy, tg.outputDir)
	if err != nil {
		return nil, err
	}
	tg.Outputs.Add(tasks...)
	return tasks, nil
}

// GenerateStagingTasks generates a module's staging and install tasks, once every module's
// targets and copies exist.
func (tg *TaskGenerator) GenerateStagingTasks(file *config.File) ([]*BuildTask, error) {
	return tg.generateStagingAndInstall(file.Staging, file.Install, file.Project, tg.outputDir)
}

// generateStagingAndInstall generates a staging area's tasks, then the installs that package it.
func (tg *TaskGenerator) generateStagingAndInstall(staging *config.Staging, installs []*config.Install, project *config.Project, outputDir string) ([]*BuildTask, error) {
	var tasks []*BuildTask
	if staging != nil {
		stagingTasks, err := tg.generateStagingTasks(staging, outputDir)
		if err != nil {
			return nil, err
		}
		tg.Outputs.Add(stagingTasks...)
		tasks = append(tasks, stagingTasks...)
	}
	if len(installs) > 0 {
		installTasks, err := tg.generateInstallTasks(installs, project, outputDir)
		if err != nil {
			return nil, err
		}
		tg.Outputs.Add(installTasks...)
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
	lists.flags = prepend(dep.CompileFlags, lists.flags)
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
	return tg.names.addArtifact(name, tg.moduleName(), outputs, taskIDs)
}

// files expands a file pattern, relative to the module directory: the files on disk outside the
// build's output directories, and the outputs of the tasks generated so far (DeclaredOutputs).
// A pattern into an output directory that names no output is an error: it would name nothing on
// a clean build.
func (tg *TaskGenerator) files(pattern string) (util.Glob, []string, error) {
	glob, err := util.ParseGlob(tg.moduleDir(), pattern)
	if err != nil {
		return glob, nil, err
	}
	files, outputDir, err := tg.Outputs.Files(glob)
	if err != nil {
		return glob, nil, err
	}
	if len(files) == 0 && outputDir != "" {
		return glob, nil, noOutputMatches(glob, outputDir)
	}
	return glob, files, nil
}

// globInputs expands input patterns (relative to the module directory), in order.
func (tg *TaskGenerator) globInputs(patterns []string, what string) ([]string, error) {
	var files []string
	for _, pattern := range patterns {
		resolved, err := resolveStrict(tg.VarEnv, pattern, "the inputs of "+what)
		if err != nil {
			return nil, err
		}
		_, matches, err := tg.files(resolved)
		if err != nil {
			return nil, fmt.Errorf("%s in module %s: %w", what, tg.moduleName(), err)
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

// toolFlags are the flags a transform or generate tool gets, as a compile tool does: the module's
// abstract keywords as the tool's toolchain maps them, then the tool's common flags, then those
// of the configuration.
func (tg *TaskGenerator) toolFlags(file *config.File, toolchain *resource.ToolchainConfig, tool *resource.Tool, env *util.VariableEnvironment, what string) ([]string, error) {
	flags, err := file.KeywordFlags(tg.contextFor(toolchain), toolchain.FlagMappings)
	if err != nil {
		return nil, fmt.Errorf("%s with toolchain %s: %w", what, toolchain.Name, err)
	}
	flags = append(flags, tool.Flags["common"]...)
	flags = append(flags, tool.Flags[tg.Configuration]...)
	return resolveAll(env, flags, "the flags of tool "+tool.Name)
}

// generateTransformTasks generates a task per input file of each artifacts.transform
func (tg *TaskGenerator) generateTransformTasks(file *config.File, outputDir string) ([]*BuildTask, error) {
	var tasks []*BuildTask
	for _, t := range file.Artifacts.Transform {
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
			values := map[string][]string{"input": {inputFile}, "output": {output}, "defines": nil, "includes": nil}
			if values["flags"], err = tg.toolFlags(file, toolchain, tool, env, what); err != nil {
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
			tg.finalizeTask(&task, t.Tool)
			task.Target = t.Name
			task.Exec = execSpec

			tasks = append(tasks, &task)
			outputs = append(outputs, output)
			taskIDs = append(taskIDs, taskID)
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
func (tg *TaskGenerator) generateGenerateTasks(file *config.File, outputDir string) ([]*BuildTask, error) {
	var tasks []*BuildTask
	genDir := filepath.Join(outputDir, "gen")

	for _, g := range file.Artifacts.Generate {
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
			flags, err := tg.toolFlags(file, toolchain, tool, env, what)
			if err != nil {
				return nil, err
			}
			values := map[string][]string{"input": {inputFile}, "output": {resolvedOutputs[0]}, "args": args, "flags": flags, "defines": nil, "includes": nil}
			command, err := tool.Expand(tg.Platform, execSpec, envValues(values, env))
			if err != nil {
				return nil, fmt.Errorf("%s in module %s: %w", what, tg.moduleName(), err)
			}
			// A command without ${args} gets them at the end
			tmpl, err := tool.Command.For(tg.Platform)
			if err != nil {
				return nil, fmt.Errorf("%s in module %s: tool %s %w", what, tg.moduleName(), tool.Name, err)
			}
			if !hasString(tmpl.Placeholders(), "args") {
				if command, err = appendArgs(command, args, execSpec); err != nil {
					return nil, fmt.Errorf("%s in module %s: %w", what, tg.moduleName(), err)
				}
			}

			taskID := tg.TaskIDGen.Next("generate", baseName)
			task := NewBuildTask(taskID, "generate", []TaskInput{NewTaskInput(inputFile)}, resolvedOutputs, []string{}, command)
			tg.finalizeTask(&task, g.Tool)
			task.Target = g.Name
			task.Exec = execSpec

			tasks = append(tasks, &task)
			outputs = append(outputs, resolvedOutputs...)
			taskIDs = append(taskIDs, taskID)
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
	tg.finalizeTask(&task, "")
	task.Target = g.Name

	if err := tg.registerArtifact(g.Name, []string{outputPath}, []string{taskID}); err != nil {
		return nil, err
	}
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

	defaultUseSymlinks := staging.UseSymlinks

	tasks, err := tg.processStagingContents(staging.Contents, destination, "", defaultUseSymlinks, stagingVarEnv)
	if err != nil {
		return nil, err
	}
	var staged []string
	for _, t := range tasks {
		staged = append(staged, t.Outputs...)
	}

	tg.stagedByName[stagingName] = staged
	tg.stagingDestByName[stagingName] = destination
	util.LogInfo("Generated %d staging tasks for '%s' at %s", len(tasks), stagingName, destination)
	return tasks, nil
}

// useSymlink is an item's choice of symlink or copy: its own, else the staging area's. A Windows
// host always copies.
func (tg *TaskGenerator) useSymlink(own *bool, def bool) bool {
	if !hostCanSymlink {
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
			outputs := tg.names.artifacts[ref.Name].outputs
			if len(outputs) == 0 {
				return nil, fmt.Errorf("staging folder '%s' in module %s: no module defines artifact '%s' with outputs", folderName, tg.moduleName(), ref.Name)
			}
			useSymlink := tg.useSymlink(ref.UseSymlink, defaultUseSymlinks)
			for _, sourcePath := range outputs {
				tasks = append(tasks, tg.createSymlinkOrCopyTask(sourcePath, filepath.Join(folderPath, filepath.Base(sourcePath)), useSymlink))
			}
		}

		// Files and globs (a filtered list: files: [windows: ["mylib.dll"], linux: ["libmylib.so"]])
		for _, file := range item.Files.Resolve(tg.getBuildContext()) {
			useSymlink := tg.useSymlink(file.UseSymlink, defaultUseSymlinks)
			resolved, err := resolveStrict(varEnv, file.Source, "staging files of folder '"+folderName+"'")
			if err != nil {
				return nil, err
			}
			// A plain path outside the output directories is staged as named (a missing file fails
			// the staging task); one inside must be an output of the build.
			glob, matches, err := tg.files(resolved)
			if err != nil {
				return nil, fmt.Errorf("staging folder '%s' in module %s: %w", folderName, tg.moduleName(), err)
			}
			if glob.Literal() {
				matches = []string{glob.Base}
			}
			for _, match := range matches {
				tasks = append(tasks, tg.createSymlinkOrCopyTask(match, filepath.Join(folderPath, filepath.Base(match)), useSymlink))
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
	// Staging, at module or workspace level, names the targets of every module
	target, found := tg.names.targets[targetName]
	if !found {
		return nil, fmt.Errorf("staging folder %s in module %s: '%s' is not a target of the workspace", destFolder, tg.moduleName(), targetName)
	}
	if len(target.Outputs) == 0 {
		return nil, fmt.Errorf("staging folder %s in module %s: target '%s' has no output", destFolder, tg.moduleName(), targetName)
	}
	sourcePath := target.Outputs[0]
	return tg.createSymlinkOrCopyTask(sourcePath, filepath.Join(destFolder, filepath.Base(sourcePath)), useSymlink), nil
}

// createSymlinkOrCopyTask creates a task that either symlinks or copies a file. It runs after the
// task that produces the file, if any: graph validation adds the edge from an input to its producer.
func (tg *TaskGenerator) createSymlinkOrCopyTask(source, dest string, useSymlink bool) *BuildTask {
	source = filepath.Clean(source)
	dest = filepath.Clean(dest)

	taskID := tg.TaskIDGen.Next("stage", filepath.Base(dest))
	var task BuildTask
	if useSymlink {
		// A symlink's key is the path it points to, not the content behind it
		task = NewNativeTask(taskID, NativeSymlink, []TaskInput{{Path: source, Hash: "symlink:" + source}}, []string{dest}, nil, NativeOp{Op: NativeSymlink})
	} else {
		task = NewNativeTask(taskID, NativeCopy, []TaskInput{NewTaskInput(source)}, []string{dest}, nil, NativeOp{Op: NativeCopy})
	}
	tg.finalizeTask(&task, "")
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
	projectName, projectVersion := projectNameVersion(project)

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
		if !followSymlinks && tg.Platform == config.Windows {
			return nil, fmt.Errorf("install '%s' in module %s: follow_symlinks: false would put symlinks in a package for windows, "+
				"which doesn't use them; leave follow_symlinks out (the archive stores what a link points to)", name, tg.moduleName())
		}

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

		// The archive holds exactly the files the staging tasks produce under the staging
		// directory, as inputs: whatever else is in the directory (what an earlier build staged,
		// the user's own files) is not shipped, and a change to the staged set changes the key.
		// Files a staging folder puts outside its directory are not in it.
		var inputs []TaskInput
		for _, file := range tg.stagedByName[stagingName] {
			if util.PathWithin(file, stagingDir) {
				inputs = append(inputs, NewTaskInput(file))
			} else {
				util.LogInfo("install '%s': %s is outside the staging directory %s, so not in the archive", name, file, stagingDir)
			}
		}
		sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })

		taskID := tg.TaskIDGen.Next("install", name)
		task := NewNativeTask(taskID, "install", inputs, []string{outputFile}, nil,
			NativeOp{Op: NativeArchive, Root: stagingDir, Format: format, FollowSymlinks: followSymlinks})
		tg.finalizeTask(&task, "")
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
				_, files, err := tg.files(pattern)
				if err != nil {
					return nil, fmt.Errorf("%s in module %s: %w", what, tg.moduleName(), err)
				}
				for _, srcFile := range files {
					srcFile = filepath.Clean(srcFile)
					destFile := filepath.Join(destDir, filepath.Base(srcFile))
					taskID := tg.TaskIDGen.Next("copy", filepath.Base(srcFile))
					task := NewNativeTask(taskID, NativeCopy, []TaskInput{NewTaskInput(srcFile)}, []string{destFile}, nil, NativeOp{Op: NativeCopy})
					tg.finalizeTask(&task, "")
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

		// One file: a target's output (the copy runs after the target's final task: a producer
		// edge), or a source path
		source := c.Source
		if c.Target != "" {
			target, found := tg.names.targets[c.Target]
			if !found || len(target.Outputs) == 0 {
				return nil, fmt.Errorf("artifacts.copy in module %s: target '%s' is not a target of the workspace", tg.moduleName(), c.Target)
			}
			source = target.Outputs[0]
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

		// depends_on orders the copy after targets it has no file relation with
		var dependencies []string
		for _, dep := range c.DependsOn {
			if target, found := tg.names.targets[dep]; found {
				dependencies = append(dependencies, target.TaskID)
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
		tg.finalizeTask(&task, "")
		tasks = append(tasks, &task)
		util.LogInfo("Generated copy task: %s -> %s", source, dest)
	}

	return tasks, nil
}
