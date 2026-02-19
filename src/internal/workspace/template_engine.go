package workspace

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
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
		ResolvePackages    bool // Call resolvePackages() for package dependencies
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
		log.Printf("WARNING: Failed to load embedded templates: %v", err)
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
			log.Printf("WARNING: Failed to read embedded template %s: %v", filePath, err)
			continue
		}

		// Parse and merge templates
		if err := bte.loadTemplateData(data, filePath); err != nil {
			log.Printf("WARNING: Failed to parse embedded template %s: %v", filePath, err)
			continue
		}
	}

	log.Printf("Loaded %d embedded template(s)", len(bte.templates))
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
				log.Printf("Template '%s' in %s overrides existing template", name, sourceName)
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
			log.Printf("WARNING: Templates directory not found: %s", templatesDir)
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
			log.Printf("Loaded %d template(s) from %s", newTemplates, templatesDir)
		}
	}

	log.Printf("Total templates loaded: %d", len(bte.templates))
	return nil
}

// loadTemplateFile loads templates from a single YAML file
func (bte *BuildTemplateEngine) loadTemplateFile(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	var rawData map[string]any
	if err := yaml.Unmarshal(data, &rawData); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	if templates, ok := rawData["templates"].(map[string]any); ok {
		// Merge templates into the engine's template map
		for name, tmpl := range templates {
			if _, exists := bte.templates[name]; exists {
				log.Printf("WARNING: Template '%s' in %s overrides existing template", name, filepath.Base(filePath))
			}
			bte.templates[name] = tmpl
		}
	}

	return nil
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
			log.Printf("Parsed metadata for template '%s': language=%s, types=%v",
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
		if v, ok := preProc["resolve_packages"].(bool); ok {
			metadata.PreProcessing.ResolvePackages = v
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
					log.Printf("Found template '%s' for language=%s, type=%s", name, language, targetType)
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

	// Build context for template variable resolution
	context := map[string]any{
		"item":          itemConfig,
		"config":        mergedConfig,
		"output_dir":    outputDir,
		"platform":      platform,
		"architecture":  architecture,
		"configuration": configuration,
	}

	// Add module name for unique object paths (prevents collisions in multi-module builds)
	if module, ok := itemConfig["module"].(string); ok {
		context["module"] = module
	} else {
		context["module"] = "workspace"
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
			)
		} else if action == "build" {
			// Generate single build task (single-step compile+link like Go)
			stepTask, err = bte.expandBuildStep(
				step, context, itemConfig, mergedConfig, outputDir,
				idGen, toolMatcher, platform, architecture, configuration, toolchain,
			)
		} else {
			// Generate single link task
			stepTask, err = bte.expandSingleStep(
				step, context, stepResults, itemConfig, mergedConfig,
				outputDir, idGen, toolMatcher, commandBuilder,
				platform, architecture, configuration, toolchain, existingTasks,
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

		// Build context for this iteration
		sourceStem := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
		iterContext := map[string]any{}
		for k, v := range context {
			iterContext[k] = v
		}
		iterContext["source"] = source
		iterContext["source_stem"] = sourceStem
		iterContext["tool"] = map[string]any{
			"output_ext":     tool.OutputExtension,
			"output_pattern": tool.OutputPattern,
		}

		// Resolve output path
		outputTemplate := ""
		if out, ok := step["output"].(string); ok {
			outputTemplate = out
		}
		output := bte.resolveTemplateString(outputTemplate, iterContext)

		// Get tool parameters
		toolParams := map[string]any{}
		if tp, ok := step["tool_params"].(map[string]any); ok {
			toolParams = tp
		}
		resolvedParams := bte.resolveToolParams(toolParams, iterContext)

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

	// Update context with tool info
	stepContext := map[string]any{}
	for k, v := range context {
		stepContext[k] = v
	}
	for k, v := range stepResults {
		stepContext[k] = v
	}

	itemName := "output"
	if name, ok := itemConfig["name"].(string); ok {
		itemName = name
	}

	stepContext["tool"] = map[string]any{
		"output_ext":     tool.OutputExtension,
		"output_pattern": strings.ReplaceAll(tool.OutputPattern, "{name}", itemName),
	}

	// Resolve output path
	outputTemplate := ""
	if out, ok := step["output"].(string); ok {
		outputTemplate = out
	}
	output := bte.resolveTemplateString(outputTemplate, stepContext)

	// Collect inputs from previous step
	inputsRef := ""
	if inp, ok := step["inputs"].(string); ok {
		inputsRef = inp
	}
	inputs := bte.resolveReference(inputsRef, stepResults)

	// Convert inputs to string slice
	inputStrs := []string{}
	switch v := inputs.(type) {
	case []string:
		inputStrs = v
	case []any:
		for _, item := range v {
			if str, ok := item.(string); ok {
				inputStrs = append(inputStrs, str)
			}
		}
	case string:
		if v != "" {
			inputStrs = []string{v}
		}
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
		switch v := resolvedDeps.(type) {
		case []string:
			dependencies = append(dependencies, v...)
		case []any:
			for _, item := range v {
				if str, ok := item.(string); ok {
					dependencies = append(dependencies, str)
				}
			}
		case string:
			if v != "" {
				dependencies = append(dependencies, v)
			}
		}
	}

	// Handle library dependencies for executables
	libDirs := []string{}
	libNames := []string{}
	if outputType == "executable" {
		dependsOnLibs := []string{}
		// Handle nested format: depends_on.targets: [...]
		if depsMap, ok := itemConfig["depends_on"].(map[string]any); ok {
			if targets, ok := depsMap["targets"].([]any); ok {
				for _, d := range targets {
					if str, ok := d.(string); ok {
						dependsOnLibs = append(dependsOnLibs, str)
					}
				}
			}
		} else if deps, ok := itemConfig["depends_on"].([]any); ok {
			// Handle flat format: depends_on: [...]
			for _, d := range deps {
				if str, ok := d.(string); ok {
					dependsOnLibs = append(dependsOnLibs, str)
				}
			}
		}

		if len(dependsOnLibs) > 0 {
			libDirs = append(libDirs, filepath.Join(outputDir, "lib"))

			// Build dependency graph for libraries to determine correct link order
			libDeps := make(map[string][]string)

			for _, dep := range dependsOnLibs {
				// Extract the target name (handle scoped references)
				libName := dep
				if strings.Contains(dep, ":") {
					parts := strings.Split(dep, ":")
					libName = parts[len(parts)-1]
				}

				// Find this library's dependencies from existing_tasks
				libDependencies := []string{}
				for _, task := range existingTasks {
					if task.TaskType == "link" && strings.Contains(task.TaskID, libName) {
						// This is the library's link task, check its dependencies
						for _, taskDep := range task.Dependencies {
							// If dependency is another library link task, extract its name
							if strings.HasPrefix(taskDep, "link_") && taskDep != task.TaskID {
								// Extract library name from task_id like "link_engine_core_005"
								parts := strings.Split(taskDep, "_")
								if len(parts) >= 3 {
									depLibName := strings.Join(parts[1:len(parts)-1], "_")
									// Check if this is one of our depends_on libs
									for _, checkDep := range dependsOnLibs {
										checkName := checkDep
										if strings.Contains(checkDep, ":") {
											checkParts := strings.Split(checkDep, ":")
											checkName = checkParts[len(checkParts)-1]
										}
										if depLibName == checkName {
											libDependencies = append(libDependencies, depLibName)
											break
										}
									}
								}
							}
						}
						break
					}
				}

				libDeps[libName] = libDependencies
				dependencies = append(dependencies, dep)
			}

			// Topological sort to get correct link order
			libNames = bte.topologicalSortLibs(libDeps)

			log.Printf("Library link order: %v", libNames)
		}
	}

	// Get tool parameters
	toolParams := map[string]any{}
	if tp, ok := step["tool_params"].(map[string]any); ok {
		toolParams = tp
	}
	resolvedParams := bte.resolveToolParams(toolParams, stepContext)

	// Merge library linking info (prepend dependency libs, append item libs)
	if len(libDirs) > 0 {
		// Merge lib_dirs from dependencies with existing lib_dirs
		existingLibDirs := bte.flattenStringList(resolvedParams["lib_dirs"])
		mergedLibDirs := append(libDirs, existingLibDirs...)
		resolvedParams["lib_dirs"] = mergedLibDirs
	}
	if len(libNames) > 0 {
		// Merge libs from dependencies with existing libs from packages
		existingLibs := bte.flattenStringList(resolvedParams["libs"])
		// Dependency libs first, then package/item libs
		mergedLibs := append(libNames, existingLibs...)
		resolvedParams["libs"] = mergedLibs
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
	command, err := commandBuilder.BuildLinkCommand(
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
		[]string{output},
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

	// Build step context
	stepContext := map[string]any{}
	for k, v := range context {
		stepContext[k] = v
	}
	stepContext["tool"] = map[string]any{
		"output_ext":     tool.OutputExtension,
		"output_pattern": strings.ReplaceAll(tool.OutputPattern, "{name}", itemName),
	}

	// Resolve output path from template
	outputTemplate := ""
	if out, ok := step["output"].(string); ok {
		outputTemplate = out
	}
	output := bte.resolveTemplateString(outputTemplate, stepContext)

	// Get working directory (module path)
	workingDir := ""
	if wd, ok := step["working_dir"].(string); ok {
		workingDir = bte.resolveTemplateString(wd, stepContext)
	}
	if workingDir == "" {
		if path, ok := itemConfig["path"].(string); ok {
			workingDir = path
		}
	}

	// Get tool parameters from template step
	toolParams := map[string]any{}
	if tp, ok := step["tool_params"].(map[string]any); ok {
		toolParams = bte.resolveToolParams(tp, stepContext)
	}

	// Build flags from tool configuration and item config
	flags := []string{}

	// Add configuration-specific flags from tool
	if configFlags, ok := tool.Flags[configuration]; ok {
		flags = append(flags, configFlags...)
	}
	if commonFlags, ok := tool.Flags["common"]; ok {
		flags = append(flags, commonFlags...)
	}

	// Build the command using tool's command template
	command := tool.Command

	// Replace common template variables
	command = strings.ReplaceAll(command, "{output}", output)
	if outDir, ok := mergedConfig["output_dir"].(string); ok {
		command = strings.ReplaceAll(command, "{output_dir}", outDir)
	}
	command = strings.ReplaceAll(command, "{input}", ".")

	// Handle release_flag (for Rust/Cargo) - use configuration flags
	releaseFlag := ""
	if configuration == "release" {
		for _, flag := range flags {
			if flag == "--release" {
				releaseFlag = "--release"
				break
			}
		}
	}
	command = strings.ReplaceAll(command, "{release_flag}", releaseFlag)

	// Handle features (for Rust)
	features := ""
	if f, ok := toolParams["features"].(string); ok && f != "" && !strings.HasPrefix(f, "{") {
		features = "--features " + f
	} else if f, ok := itemConfig["features"].(string); ok && f != "" {
		features = "--features " + f
	} else if fList, ok := itemConfig["features"].([]any); ok && len(fList) > 0 {
		featureStrs := []string{}
		for _, feat := range fList {
			if str, ok := feat.(string); ok {
				featureStrs = append(featureStrs, str)
			}
		}
		if len(featureStrs) > 0 {
			features = "--features " + strings.Join(featureStrs, ",")
		}
	}
	command = strings.ReplaceAll(command, "{features}", features)

	// Handle bin_flag (for Rust - specific binary in workspace)
	binFlag := ""
	if b, ok := toolParams["bin"].(string); ok && b != "" && !strings.HasPrefix(b, "{") {
		binFlag = "--bin " + b
	} else if b, ok := itemConfig["bin"].(string); ok && b != "" {
		binFlag = "--bin " + b
	}
	command = strings.ReplaceAll(command, "{bin_flag}", binFlag)

	// Handle build_tags (for Go)
	buildTags := ""
	if tags, ok := toolParams["build_tags"].([]string); ok && len(tags) > 0 {
		buildTags = "-tags " + strings.Join(tags, ",")
	} else if tags, ok := itemConfig["build_tags"].([]any); ok && len(tags) > 0 {
		tagStrs := []string{}
		for _, t := range tags {
			if str, ok := t.(string); ok {
				tagStrs = append(tagStrs, str)
			}
		}
		if len(tagStrs) > 0 {
			buildTags = "-tags " + strings.Join(tagStrs, ",")
		}
	}
	command = strings.ReplaceAll(command, "{build_tags}", buildTags)

	// Handle ldflags (for Go)
	// ldflags can contain shell command substitutions like $(date ...) and variables like ${config}
	ldflags := ""
	var lfStrs []string
	if lf, ok := toolParams["ldflags"].([]string); ok && len(lf) > 0 {
		lfStrs = lf
	} else if lf, ok := itemConfig["ldflags"].([]any); ok && len(lf) > 0 {
		for _, f := range lf {
			if str, ok := f.(string); ok {
				lfStrs = append(lfStrs, str)
			}
		}
	}
	if len(lfStrs) > 0 {
		// Replace ${config} with actual configuration value
		for i, lf := range lfStrs {
			lfStrs[i] = strings.ReplaceAll(lf, "${config}", configuration)
		}
		// Use double quotes to allow shell command substitution ($(date ...), $(git ...))
		ldflags = "-ldflags \"" + strings.Join(lfStrs, " ") + "\""
	}
	command = strings.ReplaceAll(command, "{ldflags}", ldflags)

	// Handle gcflags (for Go - use tool's configuration flags if not specified)
	gcflags := ""
	if gf, ok := toolParams["gcflags"].(string); ok && gf != "" {
		gcflags = gf
	} else {
		// Use configuration-specific flags from tool
		for _, flag := range flags {
			if strings.HasPrefix(flag, "-gcflags") {
				// Quote the value if it contains spaces
				// Convert -gcflags=all=-N -l to -gcflags='all=-N -l'
				if strings.Contains(flag, " ") && strings.Contains(flag, "=") {
					parts := strings.SplitN(flag, "=", 2)
					if len(parts) == 2 {
						gcflags = parts[0] + "='" + parts[1] + "'"
					} else {
						gcflags = flag
					}
				} else {
					gcflags = flag
				}
				break
			}
		}
	}
	command = strings.ReplaceAll(command, "{gcflags}", gcflags)

	// Clean up extra spaces
	command = strings.Join(strings.Fields(command), " ")

	// Prepend cd if working directory is specified
	if workingDir != "" && workingDir != "." {
		command = fmt.Sprintf("cd %s && %s", workingDir, command)
	}

	// Collect inputs for cache invalidation based on tool type
	taskInputs := []TaskInput{}
	if workingDir != "" {
		// Go: go.mod and go.sum
		if strings.Contains(tool.Name, "go") {
			goModPath := filepath.Join(workingDir, "go.mod")
			goSumPath := filepath.Join(workingDir, "go.sum")
			taskInputs = append(taskInputs, NewTaskInput(goModPath))
			taskInputs = append(taskInputs, NewTaskInput(goSumPath))
		}
		// Rust: Cargo.toml and Cargo.lock
		if strings.Contains(tool.Name, "cargo") {
			cargoTomlPath := filepath.Join(workingDir, "Cargo.toml")
			cargoLockPath := filepath.Join(workingDir, "Cargo.lock")
			taskInputs = append(taskInputs, NewTaskInput(cargoTomlPath))
			taskInputs = append(taskInputs, NewTaskInput(cargoLockPath))
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
