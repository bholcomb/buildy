package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"buildy/internal/resource"
	"buildy/pkg/util"

	"gopkg.in/yaml.v3"
)

// TemplateMetadata defines the metadata for a build template
type TemplateMetadata struct {
	Language      string   // Language this template is for (e.g., "cpp", "c", "go", "rust")
	TargetTypes   []string // Target types this template can build (e.g., ["executable", "shared_library"])
	PreProcessing struct {
		ResolveDeps        bool // Call resolveDeps() for external dependencies
		ResolveSources     bool // Call PathResolver.ResolveSources() for source file globbing
		ResolveIncludeDirs bool // Call PathResolver.ResolveIncludeDirs() for include paths
		ResolvePath        bool // Call PathResolver.ResolveRelativePath() on "path" field
	}
	PostProcessing struct {
		ScanSourcesPattern string // Glob pattern for source file scanning (e.g., "*.go", "*.rs")
		RegisterTarget     bool   // Register the target in the global registry
		AddSetupDependency bool   // Add setup task as dependency
	}
	ToolchainLanguage string // Language to use for toolchain selection (if different from Language)
	LibraryOutputDir  string // Subdirectory under output_dir where libraries are placed (e.g., "bin", "lib")
}

// BuildTemplateEngine expands universal build templates into concrete tasks
type BuildTemplateEngine struct {
	templatesDirs    []string
	templates        map[string]any
	templateMetadata map[string]*TemplateMetadata // Parsed metadata for each template
}

// NewBuildTemplateEngine creates a new BuildTemplateEngine from a single directory
func NewBuildTemplateEngine(templatesDir string) (*BuildTemplateEngine, error) {
	return NewBuildTemplateEngineMulti([]string{templatesDir})
}

// NewBuildTemplateEngineMulti creates a new BuildTemplateEngine from multiple directories
func NewBuildTemplateEngineMulti(templatesDirs []string) (*BuildTemplateEngine, error) {
	engine := &BuildTemplateEngine{
		templatesDirs:    templatesDirs,
		templates:        make(map[string]any),
		templateMetadata: make(map[string]*TemplateMetadata),
	}

	// First load embedded templates (built-in)
	if err := engine.loadEmbeddedTemplates(); err != nil {
		util.LogWarning("Failed to load embedded templates: %v", err)
	}

	// Then load from filesystem directories (can override built-in)
	if err := engine.loadTemplates(); err != nil {
		return nil, err
	}

	// Parse metadata for all loaded templates
	engine.parseAllTemplateMetadata()

	return engine, nil
}

// loadEmbeddedTemplates loads all templates from embedded data
func (bte *BuildTemplateEngine) loadEmbeddedTemplates() error {
	// List all embedded template files
	files, err := resource.ListEmbeddedFiles("templates")
	if err != nil {
		return fmt.Errorf("failed to list embedded templates: %w", err)
	}

	for _, filePath := range files {
		if !strings.HasSuffix(filePath, ".yaml") && !strings.HasSuffix(filePath, ".yml") {
			continue
		}

		// Read the embedded file
		data, err := resource.GetEmbeddedFile(filePath)
		if err != nil {
			util.LogWarning("Failed to read embedded template %s: %v", filePath, err)
			continue
		}

		// Parse and merge templates
		if err := bte.loadTemplateData(data, filePath); err != nil {
			util.LogWarning("Failed to parse embedded template %s: %v", filePath, err)
			continue
		}
	}

	util.LogInfo("Loaded %d embedded template(s)", len(bte.templates))
	return nil
}

// loadTemplateData loads templates from YAML data
func (bte *BuildTemplateEngine) loadTemplateData(data []byte, sourceName string) error {
	var rawData map[string]any
	if err := yaml.Unmarshal(data, &rawData); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	if templates, ok := rawData["templates"].(map[string]any); ok {
		// Merge templates into the engine's template map
		for name, tmpl := range templates {
			if _, exists := bte.templates[name]; exists {
				util.LogInfo("Template '%s' in %s overrides existing template", name, sourceName)
			}
			bte.templates[name] = tmpl
		}
	}

	return nil
}

// loadTemplates loads all build templates from YAML files in filesystem directories
func (bte *BuildTemplateEngine) loadTemplates() error {
	// Load templates from each directory in order
	for _, templatesDir := range bte.templatesDirs {
		// Find all .yaml files in templates directory
		entries, err := os.ReadDir(templatesDir)
		if err != nil {
			util.LogVerbose("Additional templates directory not found, using built-in: %s", templatesDir)
			continue // Not fatal, try next directory
		}

		dirTemplateCount := len(bte.templates)

		// Load each template file
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			if !strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml") {
				continue
			}

			templateFile := filepath.Join(templatesDir, entry.Name())
			if err := bte.loadTemplateFile(templateFile); err != nil {
				return fmt.Errorf("failed to load template file %s: %w", entry.Name(), err)
			}
		}

		newTemplates := len(bte.templates) - dirTemplateCount
		if newTemplates > 0 {
			util.LogInfo("Loaded %d template(s) from %s", newTemplates, templatesDir)
		}
	}

	util.LogInfo("Total templates loaded: %d", len(bte.templates))
	return nil
}

// loadTemplateFile loads templates from a single YAML file
func (bte *BuildTemplateEngine) loadTemplateFile(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}
	return bte.loadTemplateData(data, filePath)
}

// GetTemplate gets a template by name
func (bte *BuildTemplateEngine) GetTemplate(templateName string) map[string]any {
	if tmpl, ok := bte.templates[templateName].(map[string]any); ok {
		return tmpl
	}
	return nil
}

// ListTemplates lists available templates with descriptions
func (bte *BuildTemplateEngine) ListTemplates() []struct {
	Name        string
	Description string
} {
	result := []struct {
		Name        string
		Description string
	}{}

	for name, tmplRaw := range bte.templates {
		tmpl, ok := tmplRaw.(map[string]any)
		if !ok {
			continue
		}
		desc := ""
		if d, ok := tmpl["description"].(string); ok {
			desc = d
		}
		result = append(result, struct {
			Name        string
			Description string
		}{Name: name, Description: desc})
	}

	return result
}

// parseAllTemplateMetadata parses metadata for all loaded templates
func (bte *BuildTemplateEngine) parseAllTemplateMetadata() {
	for name, tmplRaw := range bte.templates {
		tmpl, ok := tmplRaw.(map[string]any)
		if !ok {
			continue
		}
		metadata := bte.parseTemplateMetadata(tmpl)
		if metadata != nil {
			bte.templateMetadata[name] = metadata
			util.LogVerbose("Parsed metadata for template '%s': language=%s, types=%v",
				name, metadata.Language, metadata.TargetTypes)
		}
	}
}

// parseTemplateMetadata extracts metadata from a template definition
func (bte *BuildTemplateEngine) parseTemplateMetadata(tmpl map[string]any) *TemplateMetadata {
	metadataRaw, ok := tmpl["metadata"].(map[string]any)
	if !ok {
		return nil
	}

	metadata := &TemplateMetadata{}

	// Parse language
	if lang, ok := metadataRaw["language"].(string); ok {
		metadata.Language = lang
	}

	// Parse target_types
	if types, ok := metadataRaw["target_types"].([]any); ok {
		for _, t := range types {
			if str, ok := t.(string); ok {
				metadata.TargetTypes = append(metadata.TargetTypes, str)
			}
		}
	}

	// Parse toolchain_language (defaults to language if not specified)
	if tcLang, ok := metadataRaw["toolchain_language"].(string); ok {
		metadata.ToolchainLanguage = tcLang
	} else {
		metadata.ToolchainLanguage = metadata.Language
	}

	// Parse pre_processing
	if preProc, ok := metadataRaw["pre_processing"].(map[string]any); ok {
		if v, ok := preProc["resolve_deps"].(bool); ok {
			metadata.PreProcessing.ResolveDeps = v
		}
		if v, ok := preProc["resolve_sources"].(bool); ok {
			metadata.PreProcessing.ResolveSources = v
		}
		if v, ok := preProc["resolve_include_dirs"].(bool); ok {
			metadata.PreProcessing.ResolveIncludeDirs = v
		}
		if v, ok := preProc["resolve_path"].(bool); ok {
			metadata.PreProcessing.ResolvePath = v
		}
	}

	// Parse post_processing
	if postProc, ok := metadataRaw["post_processing"].(map[string]any); ok {
		if v, ok := postProc["scan_sources_pattern"].(string); ok {
			metadata.PostProcessing.ScanSourcesPattern = v
		}
		if v, ok := postProc["register_target"].(bool); ok {
			metadata.PostProcessing.RegisterTarget = v
		}
		if v, ok := postProc["add_setup_dependency"].(bool); ok {
			metadata.PostProcessing.AddSetupDependency = v
		}
	}

	// Parse library_output_dir (defaults to "bin" if not specified)
	if libDir, ok := metadataRaw["library_output_dir"].(string); ok {
		metadata.LibraryOutputDir = libDir
	} else {
		metadata.LibraryOutputDir = "bin"
	}

	return metadata
}

// LookupTemplate finds a template by language and target type
// Returns the template name, template data, and metadata
func (bte *BuildTemplateEngine) LookupTemplate(language, targetType string) (string, map[string]any, *TemplateMetadata, error) {
	// Search for a template that matches the language and target type
	for name, metadata := range bte.templateMetadata {
		if metadata.Language != language {
			continue
		}

		// Check if this template supports the target type
		for _, tt := range metadata.TargetTypes {
			if tt == targetType {
				tmpl := bte.GetTemplate(name)
				if tmpl != nil {
					util.LogInfo("Found template '%s' for language=%s, type=%s", name, language, targetType)
					return name, tmpl, metadata, nil
				}
			}
		}
	}

	return "", nil, nil, fmt.Errorf("no template found for language '%s' and target type '%s'", language, targetType)
}

// GetTemplateMetadata returns the metadata for a template by name
func (bte *BuildTemplateEngine) GetTemplateMetadata(templateName string) *TemplateMetadata {
	return bte.templateMetadata[templateName]
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

// GetLibraryOutputDir returns the library output directory for a given target type.
// This allows dependent targets to find where libraries are placed without hardcoding paths.
func (bte *BuildTemplateEngine) GetLibraryOutputDir(language, targetType string) string {
	// Find the template for this language/type and return its library_output_dir
	for _, metadata := range bte.templateMetadata {
		if metadata.Language == language {
			for _, tt := range metadata.TargetTypes {
				if tt == targetType {
					if metadata.LibraryOutputDir != "" {
						return metadata.LibraryOutputDir
					}
					return "bin" // Default fallback
				}
			}
		}
	}
	return "bin" // Default if no matching template found
}

// ExpandTemplate expands a template into concrete build tasks
func (bte *BuildTemplateEngine) ExpandTemplate(
	templateName string,
	itemConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
	idGen *TaskIDGenerator,
	toolMatcher *resource.ToolMatcher,
	commandBuilder *resource.CommandBuilder,
	platform, architecture, configuration, toolchain string,
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
	
	// Add template metadata variables (accessible as ${template.<field_name>}, etc.)
	// Uses reflection to auto-register all string fields from TemplateMetadata
	if metadata := bte.GetTemplateMetadata(templateName); metadata != nil {
		bte.registerMetadataVariables(templateEnv, metadata)
	}
	
	// Keep legacy context map for step reference resolution (compile.outputs, etc.)
	// This is structural, not variable resolution
	context := map[string]any{
		"item":          itemConfig,
		"config":        mergedConfig,
		"output_dir":    outputDir,
		"platform":      platform,
		"architecture":  architecture,
		"configuration": configuration,
		"module":        module,
	}

	// Get steps
	stepsRaw, ok := template["steps"]
	if !ok {
		return tasks, nil
	}

	steps, ok := stepsRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("template steps must be a list")
	}

	for _, stepRaw := range steps {
		step, ok := stepRaw.(map[string]any)
		if !ok {
			continue
		}

		stepName := "unnamed"
		if name, ok := step["name"].(string); ok {
			stepName = name
		}

		// Determine step type and route to appropriate handler
		action := ""
		if a, ok := step["action"].(string); ok {
			action = a
		}

		var stepTask *BuildTask
		var stepTasks []*BuildTask
		var err error

		if _, hasForEach := step["for_each"]; hasForEach {
			// Generate multiple tasks (one per source file)
			stepTasks, err = bte.expandForEachStep(
				step, context, itemConfig, mergedConfig, outputDir,
				setupTaskID, idGen, toolMatcher, commandBuilder,
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

	return tasks, nil
}

// expandForEachStep expands a for_each step into multiple tasks
func (bte *BuildTemplateEngine) expandForEachStep(
	step map[string]any,
	context map[string]any,
	itemConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
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

	// Get action
	action := "compile"
	if a, ok := step["action"].(string); ok {
		action = a
	}

	for _, source := range sources {
		// Find appropriate tool for this source file
		tool := toolMatcher.FindTool(action, source)
		if tool == nil {
			return nil, fmt.Errorf("no %s tool found for source file '%s'. "+
				"Check that your toolchain supports files with extension '%s'",
				action, source, filepath.Ext(source))
		}

		// Create step context with tool info
		sourceStem := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
		sc := newStepContext(context, varEnv, "for_each", struct {
			OutputExt     string
			OutputPattern string
		}{tool.OutputExtension, tool.OutputPattern})
		
		// Add source-specific variables
		sc.VarEnv.SetVariable("source", source, "for_each")
		sc.VarEnv.SetVariable("source_stem", sourceStem, "for_each")
		sc.Context["source"] = source
		sc.Context["source_stem"] = sourceStem

		// Resolve output path using VarEnv
		outputTemplate := ""
		if out, ok := step["output"].(string); ok {
			outputTemplate = out
		}
		output := sc.VarEnv.ResolveString(outputTemplate, nil, 10)

		// Get tool parameters
		toolParams := map[string]any{}
		if tp, ok := step["tool_params"].(map[string]any); ok {
			toolParams = tp
		}
		resolvedParams := bte.resolveToolParams(toolParams, sc.Context, sc.VarEnv)

		// Convert params to expected format
		defines, includeDirs, extraFlags, kwargs := convertResolvedParams(resolvedParams)

		// Build command
		command, depFile, err := commandBuilder.BuildCommand(
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

		taskID := idGen.Next(action, fmt.Sprintf("%s_%s", taskName, source))
		task := NewBuildTask(
			taskID,
			action,
			[]TaskInput{NewTaskInput(source)},
			outputs,
			[]string{setupTaskID},
			command,
		)
		task.Platform = platform
		task.Architecture = architecture
		task.Configuration = configuration
		task.Toolchain = toolchain
		task.EstimatedTime = util.DefaultCompileTimeSeconds
		task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 200, DiskMB: 15}
		task.CacheKey = task.CalculateCacheKey()

		tasks = append(tasks, &task)
	}

	return tasks, nil
}

// expandSingleStep expands a single (non-foreach) step into a task
func (bte *BuildTemplateEngine) expandSingleStep(
	step map[string]any,
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
	action := "link"
	if a, ok := step["action"].(string); ok {
		action = a
	}

	outputType := ""
	if ot, ok := step["output_type"].(string); ok {
		outputType = ot
	}

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
	// Resolve output pattern using VarEnv (set name temporarily for resolution)
	patternVarEnv := varEnv.CreateChild()
	patternVarEnv.SetVariable("name", itemName, "output-pattern")
	resolvedOutputPattern := patternVarEnv.ResolveString(tool.OutputPattern, nil, 10)

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
	outputTemplate := ""
	if out, ok := step["output"].(string); ok {
		outputTemplate = out
	}
	output := sc.VarEnv.ResolveString(outputTemplate, nil, 10)

	// Collect inputs from previous step
	inputsRef := ""
	if inp, ok := step["inputs"].(string); ok {
		inputsRef = inp
	}
	inputs := bte.resolveReference(inputsRef, stepResults)

	// Convert inputs to string slice
	inputStrs := toStringSlice(inputs)

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
	dependsOnRef := []string{}
	if dep, ok := step["depends_on"].(string); ok {
		dependsOnRef = []string{dep}
	} else if deps, ok := step["depends_on"].([]any); ok {
		for _, d := range deps {
			if str, ok := d.(string); ok {
				dependsOnRef = append(dependsOnRef, str)
			}
		}
	}

	dependencies := []string{}
	for _, depRef := range dependsOnRef {
		resolvedDeps := bte.resolveReference(depRef, stepResults)
		dependencies = append(dependencies, toStringSlice(resolvedDeps)...)
	}

	// Handle artifact dependencies (depends_on.artifacts)
	// Artifacts are processed before targets, so their task IDs are available
	if depsMap, ok := itemConfig["depends_on"].(map[string]any); ok {
		if artifacts, ok := depsMap["artifacts"].([]any); ok {
			for _, a := range artifacts {
				if artifactName, ok := a.(string); ok {
					// Look up artifact task IDs from existing tasks
					for _, task := range existingTasks {
						// Artifact tasks are named like "generate_<name>_..." or "transform_<name>_..."
						if strings.Contains(task.TaskID, artifactName) &&
							(strings.HasPrefix(task.TaskID, "generate_") || strings.HasPrefix(task.TaskID, "transform_")) {
							dependencies = append(dependencies, task.TaskID)
							util.LogInfo("Added artifact dependency: %s -> %s", artifactName, task.TaskID)
						}
					}
				}
			}
		}
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

		// Add all libs as potential dependencies - resolution happens later
		// in resolveCrossModuleDependencies where we know all targets
		for _, lib := range libsFromConfig {
			dependencies = append(dependencies, lib)
		}

		// For link ordering within the libs list, we don't need complex
		// topological sorting here - the linker handles static lib order,
		// and the dependency resolution will ensure build order is correct
		libNames = libsFromConfig
		
		util.LogDebug("Resolved libs for %s: %v", itemConfig["name"], libsFromConfig)
	}

	// Get tool parameters
	toolParams := map[string]any{}
	if tp, ok := step["tool_params"].(map[string]any); ok {
		toolParams = tp
	}
	resolvedParams := bte.resolveToolParams(toolParams, sc.Context, sc.VarEnv)

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

	// Build command
	command, implib, err := commandBuilder.BuildLinkCommand(
		tool,
		filteredInputs,
		output,
		libDirsParam,
		libsParam,
		frameworksParam,
	)
	if err != nil {
		return nil, err
	}

	// Apply target-specific link flags from itemConfig["link"]["flags"]
	if linkConfig, ok := itemConfig["link"].(map[string]any); ok {
		if flagsConfig, ok := linkConfig["flags"].(map[string]any); ok {
			// Check for platform-specific flags
			platformKey := platform
			if flags, ok := flagsConfig[platformKey].([]any); ok {
				for _, f := range flags {
					if flagStr, ok := f.(string); ok {
						command = command + " " + flagStr
					}
				}
			}
			// Also check for "common" flags
			if flags, ok := flagsConfig["common"].([]any); ok {
				for _, f := range flags {
					if flagStr, ok := f.(string); ok {
						command = command + " " + flagStr
					}
				}
			}
		} else if flags, ok := linkConfig["flags"].([]any); ok {
			// Flat array of flags (not platform-specific)
			for _, f := range flags {
				if flagStr, ok := f.(string); ok {
					command = command + " " + flagStr
				}
			}
		}
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
	task.Platform = platform
	task.Architecture = architecture
	task.Configuration = configuration
	task.Toolchain = toolchain
	task.EstimatedTime = util.DefaultLinkTimeSeconds
	task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 150, DiskMB: 25}
	task.CacheKey = task.CalculateCacheKey()

	return &task, nil
}

// expandBuildStep expands a build step (single-step compile+link like Go modules)
func (bte *BuildTemplateEngine) expandBuildStep(
	step map[string]any,
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
	// Resolve output pattern using VarEnv (set name temporarily for resolution)
	patternVarEnv := varEnv.CreateChild()
	patternVarEnv.SetVariable("name", itemName, "output-pattern")
	resolvedOutputPattern := patternVarEnv.ResolveString(tool.OutputPattern, nil, 10)

	// Create step context with tool info
	sc := newStepContext(context, varEnv, "build_step", struct {
		OutputExt     string
		OutputPattern string
	}{tool.OutputExtension, resolvedOutputPattern})

	// Resolve output path from template using VarEnv
	outputTemplate := ""
	if out, ok := step["output"].(string); ok {
		outputTemplate = out
	}
	output := sc.VarEnv.ResolveString(outputTemplate, nil, 10)

	// Get working directory (module path)
	workingDir := ""
	if wd, ok := step["working_dir"].(string); ok {
		workingDir = sc.VarEnv.ResolveString(wd, nil, 10)
	}
	if workingDir == "" {
		if path, ok := itemConfig["path"].(string); ok {
			workingDir = path
		}
	}

	// Get tool parameters from template step
	toolParams := map[string]any{}
	if tp, ok := step["tool_params"].(map[string]any); ok {
		toolParams = bte.resolveToolParams(tp, sc.Context, sc.VarEnv)
	}

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

	// Resolve command parameters using data-driven approach and set them in VarEnv
	if len(tool.CommandParams) > 0 {
		resolvedParams := tool.ResolveCommandParams(toolParams, itemConfig, configuration)
		for paramName, paramValue := range resolvedParams {
			cmdVarEnv.SetVariable(paramName, paramValue, "command-param")
		}
	}

	// Build and resolve the command using VarEnv
	command := cmdVarEnv.ResolveString(tool.Command, nil, 10)

	// Clean up extra spaces from empty optional parameters
	command = strings.Join(strings.Fields(command), " ")

	// Prepend cd if working directory is specified
	if workingDir != "" && workingDir != "." {
		command = fmt.Sprintf("cd %s && %s", workingDir, command)
	}

	// Collect inputs for cache invalidation from manifest_files
	taskInputs := []TaskInput{}
	if workingDir != "" && len(tool.ManifestFiles) > 0 {
		for _, manifestFile := range tool.ManifestFiles {
			manifestPath := filepath.Join(workingDir, manifestFile)
			taskInputs = append(taskInputs, NewTaskInput(manifestPath))
		}
	}

	// Create task with tool-specific type
	taskID := idGen.Next("build", itemName)
	task := NewBuildTask(
		taskID,
		tool.Name,
		taskInputs,
		[]string{output},
		[]string{}, // No dependencies - build tools handle internally
		command,
	)
	task.Platform = platform
	task.Architecture = architecture
	task.Configuration = configuration
	task.Toolchain = toolchain
	task.EstimatedTime = 10.0 // Build tasks are typically longer
	task.ResourceRequirements = ResourceRequirements{CPUCores: 2, MemoryMB: 512, DiskMB: 100}
	task.CacheKey = task.CalculateCacheKey()

	return &task, nil
}
