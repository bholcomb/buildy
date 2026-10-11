package workspace

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"buildy/internal/config"
	"buildy/internal/resource"
	"buildy/pkg/util"
)

// TemplateMetadata is a template's metadata.
type TemplateMetadata = config.TemplateMetadata

// BuildTemplateEngine expands build templates into concrete tasks
type BuildTemplateEngine struct {
	templates map[string]*config.Template // by name
	byTarget  map[string]*config.Template // by "language:target type"
}

// NewBuildTemplateEngine creates a new BuildTemplateEngine from a single directory
func NewBuildTemplateEngine(templatesDir string) (*BuildTemplateEngine, error) {
	return NewBuildTemplateEngineMulti([]string{templatesDir})
}

// NewBuildTemplateEngineMulti loads the built-in templates, then the template files of each
// directory in order; a template with the name of an earlier one replaces it. Every file must
// load, and each language and target type has one template.
func NewBuildTemplateEngineMulti(templatesDirs []string) (*BuildTemplateEngine, error) {
	engine := &BuildTemplateEngine{templates: map[string]*config.Template{}}

	files, err := resource.ListEmbeddedFiles("templates")
	if err != nil {
		return nil, fmt.Errorf("listing the built-in templates: %w", err)
	}
	for _, filePath := range files {
		if !strings.HasSuffix(filePath, ".yaml") && !strings.HasSuffix(filePath, ".yml") {
			continue
		}
		data, err := resource.GetEmbeddedFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("reading built-in template %s: %w", filePath, err)
		}
		if err := engine.add(data, "built-in "+filePath); err != nil {
			return nil, err
		}
	}

	for _, dir := range templatesDirs {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			util.LogVerbose("Additional templates directory not found, using built-in: %s", dir)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading templates directory %s: %w", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || (!strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml")) {
				continue
			}
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("reading template file %s: %w", path, err)
			}
			if err := engine.add(data, path); err != nil {
				return nil, err
			}
		}
	}

	if err := engine.index(); err != nil {
		return nil, err
	}
	util.LogInfo("Total templates loaded: %d", len(engine.templates))
	return engine, nil
}

// add loads a template file's templates.
func (bte *BuildTemplateEngine) add(data []byte, source string) error {
	templates, err := config.LoadTemplates(data, source)
	if err != nil {
		return err
	}
	for _, t := range templates {
		if previous, exists := bte.templates[t.Name]; exists {
			if !strings.HasPrefix(previous.Source, "built-in ") && previous.Source == source {
				return fmt.Errorf("template '%s' is defined twice in %s", t.Name, source)
			}
			util.LogInfo("Template '%s' from %s replaces the one from %s", t.Name, source, previous.Source)
		}
		bte.templates[t.Name] = t
	}
	return nil
}

// index maps each language and target type to its template, and fails if two templates build
// the same.
func (bte *BuildTemplateEngine) index() error {
	bte.byTarget = map[string]*config.Template{}
	names := make([]string, 0, len(bte.templates))
	for name := range bte.templates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t := bte.templates[name]
		for _, targetType := range t.Metadata.TargetTypes {
			key := t.Metadata.Language + ":" + targetType
			if other, ok := bte.byTarget[key]; ok {
				return fmt.Errorf("templates '%s' (%s) and '%s' (%s) both build a %s %s; give one the other's name to replace it",
					other.Name, other.Source, t.Name, t.Source, t.Metadata.Language, targetType)
			}
			bte.byTarget[key] = t
		}
	}
	return nil
}

// GetTemplate gets a template by name
func (bte *BuildTemplateEngine) GetTemplate(templateName string) *config.Template {
	return bte.templates[templateName]
}

// ListTemplates lists the templates, by name
func (bte *BuildTemplateEngine) ListTemplates() []*config.Template {
	names := make([]string, 0, len(bte.templates))
	for name := range bte.templates {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]*config.Template, len(names))
	for i, name := range names {
		result[i] = bte.templates[name]
	}
	return result
}

// LookupTemplate finds the template for a language and target type
func (bte *BuildTemplateEngine) LookupTemplate(language, targetType string) (*config.Template, error) {
	t, ok := bte.byTarget[language+":"+targetType]
	if !ok {
		return nil, fmt.Errorf("no template found for language '%s' and target type '%s'", language, targetType)
	}
	util.LogInfo("Found template '%s' for language=%s, type=%s", t.Name, language, targetType)
	return t, nil
}

// GetTemplateMetadata returns the metadata for a template by name
func (bte *BuildTemplateEngine) GetTemplateMetadata(templateName string) *TemplateMetadata {
	if t := bte.templates[templateName]; t != nil {
		return &t.Metadata
	}
	return nil
}

// registerMetadataVariables uses reflection to register all string fields from
// TemplateMetadata as template variables with the "template." prefix.
// This makes the system extensible - any new field added to TemplateMetadata
// automatically becomes available as ${template.<field_name>} in templates.
func (bte *BuildTemplateEngine) registerMetadataVariables(env *util.VariableEnvironment, metadata *TemplateMetadata) {
	v := reflect.ValueOf(metadata).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)

		// Convert field name from PascalCase to snake_case
		varName := toSnakeCase(field.Name)

		switch value.Kind() {
		case reflect.String:
			if str := value.String(); str != "" {
				env.SetVariable("template."+varName, str, "template-metadata")
			}
		case reflect.Slice:
			// For string slices, join with commas
			if value.Type().Elem().Kind() == reflect.String {
				strs := make([]string, value.Len())
				for j := 0; j < value.Len(); j++ {
					strs[j] = value.Index(j).String()
				}
				if len(strs) > 0 {
					env.SetVariable("template."+varName, strings.Join(strs, ","), "template-metadata")
				}
			}
		}
	}
}

// toSnakeCase converts PascalCase to snake_case
func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteByte('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}

// GetLibraryOutputDir returns the directory under the output directory where a template puts a
// language's libraries of a target type ("bin" if no template builds them).
func (bte *BuildTemplateEngine) GetLibraryOutputDir(language, targetType string) string {
	if t, ok := bte.byTarget[language+":"+targetType]; ok {
		return t.Metadata.LibraryOutputDir
	}
	return "bin"
}

// ExpandTemplate expands a template into concrete build tasks
func (bte *BuildTemplateEngine) ExpandTemplate(
	templateName string,
	itemConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	idGen *TaskIDGenerator,
	toolMatcher *resource.ToolMatcher,
	commandBuilder *resource.CommandBuilder,
	platform, architecture, configuration, toolchain string,
	configHash string,
	existingTasks []*BuildTask,
	varEnv *util.VariableEnvironment,
) ([]*BuildTask, error) {
	if existingTasks == nil {
		existingTasks = []*BuildTask{}
	}

	if idGen == nil {
		idGen = NewTaskIDGenerator("workspace")
	}

	template := bte.GetTemplate(templateName)
	if template == nil {
		return nil, fmt.Errorf("template '%s' not found", templateName)
	}

	tasks := []*BuildTask{}
	stepResults := make(map[string]map[string]any)

	// Create a child VarEnv for this template expansion with item/config context
	templateEnv := varEnv.CreateChild()
	templateEnv.PushScope("template")
	
	// Add item config variables (accessible as ${item.name}, ${item.sources}, etc.)
	for key, val := range itemConfig {
		if strVal, ok := val.(string); ok {
			templateEnv.SetVariable("item."+key, strVal, "item-config")
		}
	}
	
	// Add merged config variables (accessible as ${config.defines}, ${config.cpp_standard}, etc.)
	for key, val := range mergedConfig {
		if strVal, ok := val.(string); ok {
			templateEnv.SetVariable("config."+key, strVal, "merged-config")
		}
	}
	
	// Module name for unique object paths
	module := "workspace"
	if m, ok := itemConfig["module"].(string); ok {
		module = m
	}
	templateEnv.SetVariable("module", module, "template")
	if name, ok := itemConfig["name"].(string); ok {
		templateEnv.SetVariable("target", name, "template")
	}
	
	// Add template metadata variables (accessible as ${template.<field_name>}, etc.)
	// Uses reflection to auto-register all string fields from TemplateMetadata
	if metadata := bte.GetTemplateMetadata(templateName); metadata != nil {
		bte.registerMetadataVariables(templateEnv, metadata)
	}
	
	// Keep legacy context map for step reference resolution (compile.outputs, etc.)
	// This is structural, not variable resolution
	moduleDir, _ := itemConfig["module_dir"].(string)
	context := map[string]any{
		"template_name": templateName,
		"module_dir":    moduleDir,
		"item":          itemConfig,
		"config":        mergedConfig,
		"output_dir":    outputDir,
		"platform":      platform,
		"architecture":  architecture,
		"configuration": configuration,
		"module":        module,
	}

	for _, step := range template.Steps {
		stepName, action := step.Name, step.Action

		var stepTask *BuildTask
		var stepTasks []*BuildTask
		var err error

		if step.ForEach != "" {
			// Generate multiple tasks (one per source file)
			stepTasks, err = bte.expandForEachStep(
				step, context, itemConfig, mergedConfig, outputDir,
				idGen, toolMatcher, commandBuilder,
				platform, architecture, configuration, toolchain,
				templateEnv,
			)
		} else if action == "build" {
			// Generate single build task (single-step compile+link like Go)
			stepTask, err = bte.expandBuildStep(
				step, context, itemConfig, mergedConfig, outputDir,
				idGen, toolMatcher, platform, architecture, configuration, toolchain,
				templateEnv,
			)
		} else {
			// Generate single link task
			stepTask, err = bte.expandSingleStep(
				step, context, stepResults, itemConfig, mergedConfig,
				outputDir, idGen, toolMatcher, commandBuilder,
				platform, architecture, configuration, toolchain, existingTasks,
				templateEnv,
			)
		}

		if err != nil {
			return nil, err
		}

		// Store results for later steps to reference
		if len(stepTasks) > 0 {
			tasks = append(tasks, stepTasks...)

			taskIDs := []string{}
			outputs := []string{}
			for _, t := range stepTasks {
				taskIDs = append(taskIDs, t.TaskID)
				outputs = append(outputs, t.Outputs...)
			}

			stepResults[stepName] = map[string]any{
				"tasks":    stepTasks,
				"task_ids": taskIDs,
				"outputs":  outputs,
			}
		} else if stepTask != nil {
			tasks = append(tasks, stepTask)

			stepResults[stepName] = map[string]any{
				"tasks":    []*BuildTask{stepTask},
				"task_ids": []string{stepTask.TaskID},
				"outputs":  stepTask.Outputs,
			}
		}
	}

	// Stamp config hash on all generated tasks
	for _, task := range tasks {
		task.ConfigHash = configHash
	}

	return tasks, nil
}

// expandForEachStep expands a for_each step into multiple tasks
func (bte *BuildTemplateEngine) expandForEachStep(
	step *config.Step,
	context map[string]any,
	itemConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	idGen *TaskIDGenerator,
	toolMatcher *resource.ToolMatcher,
	commandBuilder *resource.CommandBuilder,
	platform, architecture, configuration, toolchain string,
	varEnv *util.VariableEnvironment,
) ([]*BuildTask, error) {
	tasks := []*BuildTask{}

	// Get sources
	sourcesRaw, ok := itemConfig["sources"]
	if !ok {
		return tasks, nil
	}

	var sources []string
	switch v := sourcesRaw.(type) {
	case string:
		// Expand glob pattern
		matches, err := filepath.Glob(v)
		if err != nil {
			return nil, fmt.Errorf("failed to expand glob %s: %w", v, err)
		}
		sources = matches
		sort.Strings(sources)
	case []any:
		for _, s := range v {
			if str, ok := s.(string); ok {
				sources = append(sources, str)
			}
		}
	case []string:
		sources = v
	}

	action := step.Action

	// Object paths are built from the source's path relative to the module directory
	moduleDir, _ := context["module_dir"].(string)

	for _, source := range sources {
		// Find appropriate tool for this source file
		tool := toolMatcher.FindTool(action, source)
		if tool == nil {
			return nil, fmt.Errorf("no %s tool found for source file '%s'. "+
				"Check that your toolchain supports files with extension '%s'",
				action, source, filepath.Ext(source))
		}

		// source_rel is the source's path relative to the module, without its extension:
		// "src/widgets/window.cpp" in module dir M gives "src/widgets/window", whatever the
		// directory buildy was started from.
		sourceRel, err := computeSourceRel(source, moduleDir)
		if err != nil {
			return nil, err
		}

		sc := newStepContext(context, varEnv, "for_each", struct {
			OutputExt     string
			OutputPattern string
		}{tool.OutputExtension, tool.OutputPattern})
		
		// Add source-specific variables
		sc.VarEnv.SetVariable("source", source, "for_each")
		sc.VarEnv.SetVariable("source_rel", sourceRel, "for_each")
		sc.Context["source"] = source
		sc.Context["source_rel"] = sourceRel

		// Resolve output path using VarEnv
		output, err := resolveStrict(sc.VarEnv, step.Output, "the output of step "+step.Name)
		if err != nil {
			return nil, err
		}
		output = filepath.Clean(output)

		// Get tool parameters
		resolvedParams, err := bte.resolveToolParams(step.ToolParams, sc.Context, sc.VarEnv)
		if err != nil {
			return nil, fmt.Errorf("compiling %s: %w", source, err)
		}

		// Convert params to expected format
		defines, includeDirs, extraFlags, kwargs := convertResolvedParams(resolvedParams)

		// Build command
		command, depFile, depFormat, err := commandBuilder.BuildCommand(
			tool, source, output,
			defines, includeDirs, extraFlags, kwargs,
		)
		if err != nil {
			return nil, err
		}

		// Build outputs list
		outputs := []string{output}
		if depFile != "" {
			outputs = append(outputs, depFile)
		}

		// Create task
		taskName := "unnamed"
		if name, ok := itemConfig["name"].(string); ok {
			taskName = name
		}

		taskID := idGen.Next(action, fmt.Sprintf("%s_%s%s", taskName, sourceRel, filepath.Ext(source)))
		task := NewBuildTask(
			taskID,
			action,
			[]TaskInput{NewTaskInput(source)},
			outputs,
			nil,
			command,
		)
		task.Platform = platform
		task.Architecture = architecture
		task.Configuration = configuration
		task.Toolchain = toolchain
		if depFile != "" {
			task.DepFile = &DepFile{Path: depFile, Format: depFormat}
		}
		task.EstimatedTime = util.DefaultCompileTimeSeconds
		task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 200, DiskMB: 15}

		tasks = append(tasks, &task)
	}

	return tasks, nil
}

// expandSingleStep expands a single (non-foreach) step into a task
func (bte *BuildTemplateEngine) expandSingleStep(
	step *config.Step,
	context map[string]any,
	stepResults map[string]map[string]any,
	itemConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	idGen *TaskIDGenerator,
	toolMatcher *resource.ToolMatcher,
	commandBuilder *resource.CommandBuilder,
	platform, architecture, configuration, toolchain string,
	existingTasks []*BuildTask,
	varEnv *util.VariableEnvironment,
) (*BuildTask, error) {
	action, outputType := step.Action, step.OutputType

	// Find appropriate tool for link action
	var tool *resource.Tool
	if outputType != "" {
		tool = toolMatcher.FindLinkTool(outputType)
	}

	if tool == nil {
		return nil, fmt.Errorf("no tool found for action='%s' output_type='%s'. "+
			"Check that your toolchain defines a link tool for this output type", action, outputType)
	}

	// Get item name for output pattern
	itemName := "output"
	if name, ok := itemConfig["name"].(string); ok {
		itemName = name
	}
	// Resolve output pattern using VarEnv (set name and output_prefix for resolution)
	patternVarEnv := varEnv.CreateChild()
	patternVarEnv.SetVariable("name", itemName, "output-pattern")
	patternVarEnv.SetVariable("output_prefix", resolveOutputPrefix(itemConfig, mergedConfig, toolMatcher), "output-pattern")
	resolvedOutputPattern, err := resolveStrict(patternVarEnv, tool.OutputPattern, "the output pattern of tool "+tool.Name)
	if err != nil {
		return nil, err
	}

	// Create step context with tool info
	sc := newStepContext(context, varEnv, "link_step", struct {
		OutputExt     string
		OutputPattern string
	}{tool.OutputExtension, resolvedOutputPattern})
	
	// Add step results to context for reference resolution
	for k, v := range stepResults {
		sc.Context[k] = v
	}

	// Resolve output path using VarEnv
	output, err := resolveStrict(sc.VarEnv, step.Output, "the output of step "+step.Name)
	if err != nil {
		return nil, err
	}
	output = filepath.Clean(output)

	// Collect inputs from previous steps
	var inputStrs []string
	for _, ref := range step.Inputs {
		inputStrs = append(inputStrs, toStringSlice(bte.resolveReference(ref, stepResults))...)
	}

	// Filter inputs to only include files matching tool's input extensions
	filteredInputs := []string{}
	for _, inp := range inputStrs {
		for _, ext := range tool.InputExtensions {
			if strings.HasSuffix(inp, ext) {
				filteredInputs = append(filteredInputs, inp)
				break
			}
		}
	}

	// Resolve dependencies
	dependencies := []string{}
	for _, depRef := range step.DependsOn {
		resolvedDeps := bte.resolveReference(depRef, stepResults)
		dependencies = append(dependencies, toStringSlice(resolvedDeps)...)
	}

	// Handle target dependencies (depends_on.targets) for all target types
	// This ONLY adds build order dependencies (no linking) - use for code generators, etc.
	dependsOnTargets := []string{}
	if depsMap, ok := itemConfig["depends_on"].(map[string]any); ok {
		if targets, ok := depsMap["targets"].([]any); ok {
			for _, d := range targets {
				if str, ok := d.(string); ok {
					dependsOnTargets = append(dependsOnTargets, str)
				}
			}
		}
	} else if deps, ok := itemConfig["depends_on"].([]any); ok {
		// Handle flat format: depends_on: [...]
		for _, d := range deps {
			if str, ok := d.(string); ok {
				dependsOnTargets = append(dependsOnTargets, str)
			}
		}
	}

	// Add build order dependencies for depends_on.targets (no linking)
	for _, dep := range dependsOnTargets {
		dependencies = append(dependencies, dep)
	}

	// Extract libs from itemConfig - these establish both linking AND build order
	libsFromConfig := []string{}
	if libs, ok := itemConfig["libs"].([]any); ok {
		for _, lib := range libs {
			if libStr, ok := lib.(string); ok {
				libsFromConfig = append(libsFromConfig, libStr)
			}
		}
	} else if libs, ok := itemConfig["libs"].([]string); ok {
		libsFromConfig = libs
	}

	// Handle library linking for executables and shared libraries
	// libs: establishes both build order AND linking
	// (static libraries don't link against other libs at archive time)
	libDirs := []string{}
	libNames := []string{}
	if (outputType == "executable" || outputType == "shared_library") && len(libsFromConfig) > 0 {
		// Get the library output directory from template metadata
		// This makes it template-driven rather than hardcoded
		libOutputDir := bte.GetLibraryOutputDir("cpp", "shared_library")
		libDirs = append(libDirs, filepath.Join(outputDir, libOutputDir))

		// For link ordering within the libs list, we don't need complex
		// topological sorting here - the linker handles static lib order,
		// and the dependency resolution will ensure build order is correct
		libNames = libsFromConfig
		
		util.LogDebug("Resolved libs for %s: %v", itemConfig["name"], libsFromConfig)
	}

	// Get tool parameters
	resolvedParams, err := bte.resolveToolParams(step.ToolParams, sc.Context, sc.VarEnv)
	if err != nil {
		return nil, err
	}

	// Merge library linking info (prepend dependency libs, append item libs)
	if len(libDirs) > 0 {
		// Merge lib_dirs from dependencies with existing lib_dirs
		existingLibDirs := bte.flattenStringList(resolvedParams["lib_dirs"])
		mergedLibDirs := append(libDirs, existingLibDirs...)
		resolvedParams["lib_dirs"] = mergedLibDirs
	}
	if len(libNames) > 0 {
		// Get any additional libs from external packages (not from item.libs)
		existingLibs := bte.flattenStringList(resolvedParams["libs"])
		// Filter out libs that are already in libNames (from item.libs)
		// to avoid duplicates - existingLibs may contain item.libs resolved from template
		libNamesSet := make(map[string]bool)
		for _, lib := range libNames {
			libNamesSet[lib] = true
		}
		additionalLibs := []string{}
		for _, lib := range existingLibs {
			if !libNamesSet[lib] {
				additionalLibs = append(additionalLibs, lib)
			}
		}
		// Sorted item.libs first, then any additional external libs
		resolvedParams["libs"] = append(libNames, additionalLibs...)
	}

	// Convert lib_dirs, libs, and frameworks to string slices
	libDirsParam := []string{}
	if ld, ok := resolvedParams["lib_dirs"].([]string); ok {
		libDirsParam = ld
	}

	libsParam := []string{}
	if l, ok := resolvedParams["libs"].([]string); ok {
		libsParam = l
	}

	frameworksParam := []string{}
	if f, ok := resolvedParams["frameworks"].([]string); ok {
		frameworksParam = f
	}

	extraFlagsParam := []string{}
	if e, ok := resolvedParams["extra_flags"].([]string); ok {
		extraFlagsParam = e
	}

	// Build command
	command, implib, err := commandBuilder.BuildLinkCommand(
		tool,
		filteredInputs,
		output,
		libDirsParam,
		libsParam,
		frameworksParam,
		extraFlagsParam,
	)
	if err != nil {
		return nil, err
	}

	// Build outputs list - primary output plus any secondary outputs (like import libraries)
	outputs := []string{output}
	if implib != "" {
		outputs = append(outputs, implib)
		util.LogVerbose("Shared library will also produce import library: %s", implib)
	}

	// Create task
	taskName := "unnamed"
	if name, ok := itemConfig["name"].(string); ok {
		taskName = name
	}

	taskInputs := []TaskInput{}
	for _, inp := range filteredInputs {
		taskInputs = append(taskInputs, NewTaskInput(inp))
	}

	taskID := idGen.Next(action, taskName)
	task := NewBuildTask(
		taskID,
		action,
		taskInputs,
		outputs,
		dependencies,
		command,
	)
	// Libraries that are workspace targets are linked to their link tasks once every module is
	// generated (resolveCrossModuleDependencies); the rest are system libraries
	task.LinkLibs = libNames
	task.Platform = platform
	task.Architecture = architecture
	task.Configuration = configuration
	task.Toolchain = toolchain
	task.EstimatedTime = util.DefaultLinkTimeSeconds
	task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 150, DiskMB: 25}

	return &task, nil
}

// expandBuildStep expands a build step (single-step compile+link like Go modules)
func (bte *BuildTemplateEngine) expandBuildStep(
	step *config.Step,
	context map[string]any,
	itemConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	idGen *TaskIDGenerator,
	toolMatcher *resource.ToolMatcher,
	platform, architecture, configuration, toolchain string,
	varEnv *util.VariableEnvironment,
) (*BuildTask, error) {
	// Find build tool
	tool := toolMatcher.FindBuildTool()
	if tool == nil {
		return nil, fmt.Errorf("no build tool found in toolchain. " +
			"Check that your toolchain defines a tool with action='build'")
	}

	// Get item name
	itemName := "module"
	if name, ok := itemConfig["name"].(string); ok {
		itemName = name
	}
	// Resolve output pattern using VarEnv (set name and output_prefix for resolution)
	patternVarEnv := varEnv.CreateChild()
	patternVarEnv.SetVariable("name", itemName, "output-pattern")
	patternVarEnv.SetVariable("output_prefix", resolveOutputPrefix(itemConfig, mergedConfig, toolMatcher), "output-pattern")
	resolvedOutputPattern, err := resolveStrict(patternVarEnv, tool.OutputPattern, "the output pattern of tool "+tool.Name)
	if err != nil {
		return nil, err
	}

	// Create step context with tool info
	sc := newStepContext(context, varEnv, "build_step", struct {
		OutputExt     string
		OutputPattern string
	}{tool.OutputExtension, resolvedOutputPattern})


	// Get working directory (module path)
	workingDir := ""
	if wd := step.WorkingDir; wd != "" {
		if workingDir, err = resolveStrict(sc.VarEnv, wd, "the working_dir of step "+step.Name); err != nil {
			return nil, err
		}
		workingDir = filepath.Clean(workingDir)
	}
	if workingDir == "" {
		if path, ok := itemConfig["path"].(string); ok {
			workingDir = path
		}
	}

	// Get tool parameters from template step
	toolParams, err := bte.resolveToolParams(step.ToolParams, sc.Context, sc.VarEnv)
	if err != nil {
		return nil, err
	}

	// Command parameters are variables of the output path too (Cargo's ${profile}, ${bin_name})
	for paramName, paramValue := range resource.ResolveCommandParams(tool, toolParams, itemConfig, configuration) {
		sc.VarEnv.SetVariable(paramName, paramValue, "command-param")
	}

	// Resolve output path from template using VarEnv
	output, err := resolveStrict(sc.VarEnv, step.Output, "the output of step "+step.Name)
	if err != nil {
		return nil, err
	}
	output = filepath.Clean(output)

	// Create a child VarEnv for command resolution with all needed variables
	cmdVarEnv := sc.VarEnv.CreateChild()

	// Set command-specific variables
	cmdVarEnv.SetVariable("output", output, "build-command")
	cmdVarEnv.SetVariable("input", ".", "build-command")
	if outDir, ok := mergedConfig["output_dir"].(string); ok {
		cmdVarEnv.SetVariable("output_dir", outDir, "build-command")
	}

	// Set flags from tool's configuration-specific flags
	var flagsList []string
	if commonFlags, ok := tool.Flags["common"]; ok {
		flagsList = append(flagsList, commonFlags...)
	}
	if configFlags, ok := tool.Flags[configuration]; ok {
		flagsList = append(flagsList, configFlags...)
	}
	cmdVarEnv.SetVariable("flags", strings.Join(flagsList, " "), "build-command")

	// Build and resolve the command using VarEnv
	toolCommand, err := tool.Command.For(platform)
	if err != nil {
		return nil, fmt.Errorf("tool %s %w", tool.Name, err)
	}
	command, err := resolveStrict(cmdVarEnv, toolCommand, "the command of tool "+tool.Name)
	if err != nil {
		return nil, err
	}

	// Clean up extra spaces from empty optional parameters
	command = strings.Join(strings.Fields(command), " ")

	// Prepend cd if working directory is specified
	if workingDir != "" && workingDir != "." {
		command = fmt.Sprintf("cd %s && %s", workingDir, command)
	}

	// The tool tracks its own inputs (imports outside the target directory, go.mod, Cargo.toml,
	// path dependencies, build scripts, embedded files): a tool-cached task always runs and lets
	// the tool's cache decide, so it declares no inputs to hash.
	taskID := idGen.Next("build", itemName)
	task := NewBuildTask(
		taskID,
		tool.Name,
		nil,
		[]string{output},
		[]string{}, // No dependencies - build tools handle internally
		command,
	)
	if metadata := bte.GetTemplateMetadata(fmt.Sprint(context["template_name"])); metadata != nil {
		task.ToolCached = metadata.ToolCached
	}
	task.Platform = platform
	task.Architecture = architecture
	task.Configuration = configuration
	task.Toolchain = toolchain
	task.EstimatedTime = 10.0 // Build tasks are typically longer
	task.ResourceRequirements = ResourceRequirements{CPUCores: 2, MemoryMB: 512, DiskMB: 100}

	return &task, nil
}

// computeSourceRel returns a source's path relative to the module directory, with forward
// slashes and without its extension. Each ".." becomes "_up", so a source outside the module
// still lands under the object directory. Both paths must be absolute: the result never depends
// on the directory buildy was started from.
//
//	"/ws/core/src/window.cpp", "/ws/core" -> "src/window"
//	"/ws/shared/util.cpp",     "/ws/core" -> "_up/shared/util"
func computeSourceRel(source, moduleDir string) (string, error) {
	if !filepath.IsAbs(source) || !filepath.IsAbs(moduleDir) {
		return "", fmt.Errorf("internal error: object path for source %q needs absolute paths (module dir %q)", source, moduleDir)
	}
	rel, err := filepath.Rel(moduleDir, source)
	if err != nil {
		return "", fmt.Errorf("source %s: no path from module directory %s: %w", source, moduleDir, err)
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, part := range parts {
		if part == ".." {
			parts[i] = "_up"
		}
	}
	rel = strings.Join(parts, "/")
	return strings.TrimSuffix(rel, path.Ext(rel)), nil
}

func resolveOutputPrefix(itemConfig, mergedConfig map[string]any, toolMatcher *resource.ToolMatcher) string {
	if prefix, ok := itemConfig["output_prefix"].(string); ok {
		return prefix
	}
	if prefix, ok := mergedConfig["output_prefix"].(string); ok {
		return prefix
	}
	return toolMatcher.GetOutputPrefix()
}
