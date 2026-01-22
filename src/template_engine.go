package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// BuildTemplateEngine expands universal build templates into concrete tasks
type BuildTemplateEngine struct {
	templatesDirs []string
	templates     map[string]any
}

// NewBuildTemplateEngine creates a new BuildTemplateEngine from a single directory
func NewBuildTemplateEngine(templatesDir string) (*BuildTemplateEngine, error) {
	return NewBuildTemplateEngineMulti([]string{templatesDir})
}

// NewBuildTemplateEngineMulti creates a new BuildTemplateEngine from multiple directories
func NewBuildTemplateEngineMulti(templatesDirs []string) (*BuildTemplateEngine, error) {
	engine := &BuildTemplateEngine{
		templatesDirs: templatesDirs,
		templates:     make(map[string]any),
	}

	// First load embedded templates (built-in)
	if err := engine.loadEmbeddedTemplates(); err != nil {
		log.Printf("WARNING: Failed to load embedded templates: %v", err)
	}

	// Then load from filesystem directories (can override built-in)
	if err := engine.loadTemplates(); err != nil {
		return nil, err
	}

	return engine, nil
}

// loadEmbeddedTemplates loads all templates from embedded data
func (bte *BuildTemplateEngine) loadEmbeddedTemplates() error {
	// List all embedded template files
	files, err := ListEmbeddedFiles("templates")
	if err != nil {
		return fmt.Errorf("failed to list embedded templates: %w", err)
	}

	for _, filePath := range files {
		if !strings.HasSuffix(filePath, ".yaml") && !strings.HasSuffix(filePath, ".yml") {
			continue
		}

		// Read the embedded file
		data, err := GetEmbeddedFile(filePath)
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

// ExpandTemplate expands a template into concrete build tasks
func (bte *BuildTemplateEngine) ExpandTemplate(
	templateName string,
	itemConfig map[string]any,
	mergedConfig map[string]any,
	outputDir string,
	setupTaskID string,
	idGen *TaskIDGenerator,
	toolMatcher *ToolMatcher,
	commandBuilder *CommandBuilder,
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

		// Check if this is a for_each step
		if _, hasForEach := step["for_each"]; hasForEach {
			// Generate multiple tasks (one per source file)
			stepTasks, err := bte.expandForEachStep(
				step, context, itemConfig, mergedConfig, outputDir,
				setupTaskID, idGen, toolMatcher, commandBuilder,
				platform, architecture, configuration, toolchain,
			)
			if err != nil {
				return nil, err
			}

			tasks = append(tasks, stepTasks...)

			// Store results for later steps to reference
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
		} else {
			// Generate single task
			stepTask, err := bte.expandSingleStep(
				step, context, stepResults, itemConfig, mergedConfig,
				outputDir, idGen, toolMatcher, commandBuilder,
				platform, architecture, configuration, toolchain, existingTasks,
			)
			if err != nil {
				return nil, err
			}

			if stepTask != nil {
				tasks = append(tasks, stepTask)

				stepResults[stepName] = map[string]any{
					"tasks":    []*BuildTask{stepTask},
					"task_ids": []string{stepTask.TaskID},
					"outputs":  stepTask.Outputs,
				}
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
	toolMatcher *ToolMatcher,
	commandBuilder *CommandBuilder,
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
		task.EstimatedTime = DefaultCompileTimeSeconds
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
	toolMatcher *ToolMatcher,
	commandBuilder *CommandBuilder,
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

	// Find appropriate tool
	var tool *Tool
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
	task.EstimatedTime = DefaultLinkTimeSeconds
	task.ResourceRequirements = ResourceRequirements{CPUCores: 1, MemoryMB: 150, DiskMB: 25}
	task.CacheKey = task.CalculateCacheKey()

	return &task, nil
}

// topologicalSortLibs topologically sorts libraries in reverse dependency order
func (bte *BuildTemplateEngine) topologicalSortLibs(libDeps map[string][]string) []string {
	// Build in-degree map (how many libraries depend on each library)
	inDegree := make(map[string]int)
	for lib := range libDeps {
		inDegree[lib] = 0
	}

	for _, deps := range libDeps {
		for _, dep := range deps {
			if _, exists := inDegree[dep]; exists {
				inDegree[dep]++
			}
		}
	}

	// Start with libraries that have no dependents (highest in dependency tree)
	queue := []string{}
	for lib, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, lib)
		}
	}

	result := []string{}

	for len(queue) > 0 {
		// Sort for deterministic output
		sort.Strings(queue)
		lib := queue[0]
		queue = queue[1:]
		result = append(result, lib)

		// Remove this library from the graph
		for _, dep := range libDeps[lib] {
			if _, exists := inDegree[dep]; exists {
				inDegree[dep]--
				if inDegree[dep] == 0 {
					queue = append(queue, dep)
				}
			}
		}
	}

	// Check for cycles
	if len(result) != len(libDeps) {
		log.Printf("WARNING: Circular dependency detected in libraries, using original order")
		result = []string{}
		for lib := range libDeps {
			result = append(result, lib)
		}
	}

	return result
}

// resolveTemplateString resolves template variables in a string
func (bte *BuildTemplateEngine) resolveTemplateString(template string, context map[string]any) string {
	if template == "" {
		return ""
	}

	// Check if the entire template is just a single reference
	if strings.HasPrefix(template, "{") && strings.HasSuffix(template, "}") && strings.Count(template, "{") == 1 {
		ref := strings.Trim(template, "{}")
		parts := strings.Split(ref, ".")
		obj := any(context)
		for _, part := range parts {
			if m, ok := obj.(map[string]any); ok {
				obj = m[part]
			} else {
				break
			}
		}
		if obj != nil {
			if str, ok := obj.(string); ok {
				return str
			}
			return fmt.Sprintf("%v", obj)
		}
	}

	// Otherwise do string replacement
	result := template
	for key, value := range context {
		if valueMap, ok := value.(map[string]any); ok {
			for subkey, subvalue := range valueMap {
				switch v := subvalue.(type) {
				case string:
					result = strings.ReplaceAll(result, fmt.Sprintf("{%s.%s}", key, subkey), v)
				case int, int64, float64, bool:
					result = strings.ReplaceAll(result, fmt.Sprintf("{%s.%s}", key, subkey), fmt.Sprintf("%v", v))
				}
			}
		} else {
			switch v := value.(type) {
			case string:
				result = strings.ReplaceAll(result, fmt.Sprintf("{%s}", key), v)
			case int, int64, float64, bool:
				result = strings.ReplaceAll(result, fmt.Sprintf("{%s}", key), fmt.Sprintf("%v", v))
			}
		}
	}

	return result
}

// resolveReference resolves a reference to previous step results
func (bte *BuildTemplateEngine) resolveReference(ref string, stepResults map[string]map[string]any) any {
	if ref == "" {
		return ref
	}

	// Remove curly braces if present
	ref = strings.Trim(ref, "{}")

	// Parse reference like "compile.outputs" or "compile.task_ids"
	parts := strings.Split(ref, ".")
	if len(parts) < 2 {
		return ref
	}

	stepName := parts[0]
	attrName := parts[1]

	if stepResult, ok := stepResults[stepName]; ok {
		if attr, ok := stepResult[attrName]; ok {
			return attr
		}
	}

	return []string{}
}

// resolveToolParams resolves tool parameters from template
func (bte *BuildTemplateEngine) resolveToolParams(params map[string]any, context map[string]any) map[string]any {
	resolved := map[string]any{
		"defines":      []string{},
		"include_dirs": []string{},
		"extra_flags":  []string{},
		"lib_dirs":     []string{},
		"libs":         []string{},
		"kwargs":       make(map[string]any),
	}

	for key, value := range params {
		switch v := value.(type) {
		case string:
			resolvedValue := bte.resolveTemplateString(v, context)
			// Try to parse as list reference
			if strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}") {
				ref := strings.Trim(v, "{}")
				parts := strings.Split(ref, ".")
				if len(parts) == 2 {
					obj := any(context)
					for _, part := range parts {
						if m, ok := obj.(map[string]any); ok {
							obj = m[part]
						} else {
							break
						}
					}
					if list, ok := obj.([]any); ok {
						strList := []string{}
						for _, item := range list {
							if str, ok := item.(string); ok {
								strList = append(strList, str)
							}
						}
						resolved[key] = strList
						continue
					} else if strList, ok := obj.([]string); ok {
						resolved[key] = strList
						continue
					}
				}
			}
			resolved[key] = resolvedValue
		case []any:
			strList := []string{}
			for _, item := range v {
				if str, ok := item.(string); ok {
					resolvedStr := bte.resolveTemplateString(str, context)
					// Check if the resolved string is actually a reference to an array
					if strings.HasPrefix(str, "{") && strings.HasSuffix(str, "}") {
						ref := strings.Trim(str, "{}")
						parts := strings.Split(ref, ".")
						obj := any(context)
						for _, part := range parts {
							if m, ok := obj.(map[string]any); ok {
								obj = m[part]
							} else {
								break
							}
						}
						// If it's an array, flatten it
						if nestedList := bte.flattenStringList(obj); len(nestedList) > 0 {
							strList = append(strList, nestedList...)
							continue
						}
					}
					strList = append(strList, resolvedStr)
				}
			}
			resolved[key] = strList
		default:
			resolved[key] = v
		}
	}

	return resolved
}

// flattenStringList flattens a value into a string slice, handling nested arrays
func (bte *BuildTemplateEngine) flattenStringList(value any) []string {
	result := []string{}
	switch v := value.(type) {
	case []string:
		result = v
	case []any:
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			} else if nested := bte.flattenStringList(item); len(nested) > 0 {
				result = append(result, nested...)
			}
		}
	case string:
		if v != "" {
			result = append(result, v)
		}
	}
	return result
}

// Helper function to convert resolved params to the format expected by BuildCommand
func convertResolvedParams(resolved map[string]any) ([]string, []string, []string, map[string]any) {
	defines := []string{}
	if d, ok := resolved["defines"].([]string); ok {
		defines = d
	}

	includeDirs := []string{}
	if i, ok := resolved["include_dirs"].([]string); ok {
		includeDirs = i
	} else if i, ok := resolved["includes"].([]string); ok {
		includeDirs = i
	}

	extraFlags := []string{}
	if e, ok := resolved["extra_flags"].([]string); ok {
		extraFlags = e
	}

	kwargs := make(map[string]any)
	if k, ok := resolved["kwargs"].(map[string]any); ok {
		kwargs = k
	}

	// Add any other params to kwargs
	for key, value := range resolved {
		if key != "defines" && key != "include_dirs" && key != "includes" && key != "extra_flags" && key != "lib_dirs" && key != "libs" && key != "kwargs" {
			kwargs[key] = value
		}
	}

	return defines, includeDirs, extraFlags, kwargs
}
