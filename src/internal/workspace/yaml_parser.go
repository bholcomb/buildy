package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Known top-level keys in buildy.yaml
var knownTopLevelKeys = map[string]bool{
	"project":      true,
	"variables":    true,
	"workspace":    true,
	"dependencies": true,
	"environment":  true,
	"targets":      true,
	"artifacts":    true,
	"staging":      true,
	"install":      true,
	// Legacy keys
	"library":     true,
	"executable":  true,
	"config":      true,
	"platforms":   true,
	"tasks":       true,
	"output":      true,
}

// Known keys for various sections
var knownProjectKeys = map[string]bool{
	"name": true, "version": true, "description": true,
}

var knownVariablesKeys = map[string]bool{
	"import_env_vars": true, "platforms": true,
	// Allow any other key as user-defined variable
}

var knownWorkspaceKeys = map[string]bool{
	"modules": true, "discover": true, "exclude": true, "package_paths": true, "packages": true,
}

var knownEnvironmentKeys = map[string]bool{
	"toolchains": true, "compile": true, "configurations": true, "dependency_builds": true,
}

var knownTargetKeys = map[string]bool{
	"static_libraries": true, "shared_libraries": true, "executables": true,
}

var knownLibraryKeys = map[string]bool{
	"name": true, "type": true, "language": true, "sources": true, "public_headers": true,
	"include_dirs": true, "packages": true, "libs": true, "depends_on": true,
	"compile": true, "toolchain": true, "defines": true, "flags": true,
}

var knownExecutableKeys = map[string]bool{
	"name": true, "language": true, "sources": true, "include_dirs": true,
	"packages": true, "libs": true, "depends_on": true, "runtime_deps": true,
	"compile": true, "toolchain": true, "defines": true, "flags": true,
	// Go-specific keys (when language: go)
	"path": true, "output": true, "build_tags": true, "ldflags": true,
	// Rust-specific keys (when language: rust)
	"features": true, "bin": true,
}

var knownArtifactsKeys = map[string]bool{
	"copy": true, "transform": true, "generate": true, "install": true,
}

var knownDependenciesKeys = map[string]bool{
	"file": true, "system": true, "paths": true, "fetch": true, "packages": true, "package_paths": true,
}

// YAMLParser handles raw YAML parsing and validation
type YAMLParser struct {
	ConfigFileDir string
}

// NewYAMLParser creates a new YAMLParser
func NewYAMLParser() *YAMLParser {
	return &YAMLParser{}
}

// ParseFile parses a YAML build configuration file
func (yp *YAMLParser) ParseFile(configFile string) (map[string]any, error) {
	// Store the directory of the config file for relative path resolution
	absPath, err := filepath.Abs(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config file path: %w", err)
	}
	yp.ConfigFileDir = filepath.Dir(absPath)

	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("error reading config file %s: %w", configFile, err)
	}

	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("error parsing config file %s: %w", configFile, err)
	}

	return config, nil
}

// ParseData parses YAML data from bytes
func (yp *YAMLParser) ParseData(data []byte) (map[string]any, error) {
	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("error parsing YAML data: %w", err)
	}
	return config, nil
}

// ValidateConfig validates configuration and returns list of errors
// This performs strict validation - unknown keys are treated as errors
func (yp *YAMLParser) ValidateConfig(config map[string]any) []string {
	errors := []string{}

	// Check for unknown top-level keys
	for key := range config {
		if !knownTopLevelKeys[key] {
			errors = append(errors, fmt.Sprintf("Unknown top-level key '%s'. Did you mean one of: %s?",
				key, yp.suggestKey(key, knownTopLevelKeys)))
		}
	}

	// Validate project section (required)
	project, hasProject := config["project"].(map[string]any)
	if !hasProject {
		errors = append(errors, "Missing required 'project' section")
	} else {
		errors = append(errors, yp.validateProject(project)...)
	}

	// Validate workspace section
	if workspace, ok := config["workspace"].(map[string]any); ok {
		errors = append(errors, yp.validateWorkspace(workspace)...)
	}

	// Validate environment section
	if environment, ok := config["environment"].(map[string]any); ok {
		errors = append(errors, yp.validateEnvironment(environment)...)
	}

	// Validate targets section
	if targets, ok := config["targets"].(map[string]any); ok {
		errors = append(errors, yp.validateTargets(targets)...)
	}

	// Validate artifacts section
	if artifacts, ok := config["artifacts"].(map[string]any); ok {
		errors = append(errors, yp.validateArtifacts(artifacts)...)
	}

	// Validate staging section
	if staging, ok := config["staging"].(map[string]any); ok {
		errors = append(errors, yp.validateStaging(staging)...)
	}

	// Validate install section
	if install, ok := config["install"].([]any); ok {
		errors = append(errors, yp.validateInstall(install)...)
	}

	// Validate dependencies section
	if deps, ok := config["dependencies"].(map[string]any); ok {
		errors = append(errors, yp.validateDependencies(deps)...)
	}

	// Validate legacy library format
	if library, ok := config["library"]; ok {
		errors = append(errors, yp.validateLegacyTargetSection(library, "library", knownLibraryKeys)...)
	}

	// Validate legacy executable format
	if executable, ok := config["executable"]; ok {
		errors = append(errors, yp.validateLegacyTargetSection(executable, "executable", knownExecutableKeys)...)
	}

	// Validate output paths don't escape project
	if outputConfig, ok := config["output"].(map[string]any); ok {
		if baseDir, ok := outputConfig["base_dir"].(string); ok {
			if strings.Contains(baseDir, "..") || filepath.IsAbs(baseDir) {
				errors = append(errors, fmt.Sprintf("Invalid output.base_dir '%s' - must be relative and not contain '..'", baseDir))
			}
		}
	}

	return errors
}

// validateProject validates the project section
func (yp *YAMLParser) validateProject(project map[string]any) []string {
	errors := []string{}

	// Check for unknown keys
	for key := range project {
		if !knownProjectKeys[key] {
			errors = append(errors, fmt.Sprintf("Unknown key 'project.%s'. Valid keys are: name, version, description", key))
		}
	}

	// Validate required fields
	if name, ok := project["name"].(string); !ok || strings.TrimSpace(name) == "" {
		errors = append(errors, "Missing or empty 'project.name' (required)")
	}

	// Validate optional field types
	if version, exists := project["version"]; exists {
		if _, ok := version.(string); !ok {
			errors = append(errors, "'project.version' must be a string")
		}
	}

	if desc, exists := project["description"]; exists {
		if _, ok := desc.(string); !ok {
			errors = append(errors, "'project.description' must be a string")
		}
	}

	return errors
}

// validateWorkspace validates the workspace section
func (yp *YAMLParser) validateWorkspace(workspace map[string]any) []string {
	errors := []string{}

	for key := range workspace {
		if !knownWorkspaceKeys[key] {
			errors = append(errors, fmt.Sprintf("Unknown key 'workspace.%s'. Did you mean one of: %s?",
				key, yp.suggestKey(key, knownWorkspaceKeys)))
		}
	}

	// Validate modules structure
	if modules, ok := workspace["modules"].(map[string]any); ok {
		validPlatforms := map[string]bool{"common": true, "linux": true, "windows": true, "macos": true}
		for platform := range modules {
			if !validPlatforms[platform] {
				errors = append(errors, fmt.Sprintf("Unknown platform 'workspace.modules.%s'. Valid platforms: common, linux, windows, macos", platform))
			}
		}
	}

	return errors
}

// validateEnvironment validates the environment section
func (yp *YAMLParser) validateEnvironment(environment map[string]any) []string {
	errors := []string{}

	for key := range environment {
		if !knownEnvironmentKeys[key] {
			errors = append(errors, fmt.Sprintf("Unknown key 'environment.%s'. Did you mean one of: %s?",
				key, yp.suggestKey(key, knownEnvironmentKeys)))
		}
	}

	// Validate toolchains is a map
	if toolchains, exists := environment["toolchains"]; exists {
		if _, ok := toolchains.(map[string]any); !ok {
			errors = append(errors, "'environment.toolchains' must be a map (e.g., default: gcc-linux)")
		}
	}

	// Validate configurations structure
	if configs, ok := environment["configurations"].(map[string]any); ok {
		for configName, configData := range configs {
			if _, ok := configData.(map[string]any); !ok {
				errors = append(errors, fmt.Sprintf("'environment.configurations.%s' must be a map", configName))
			}
		}
	}

	return errors
}

// validateTargets validates the targets section
func (yp *YAMLParser) validateTargets(targets map[string]any) []string {
	errors := []string{}

	for key := range targets {
		if !knownTargetKeys[key] {
			errors = append(errors, fmt.Sprintf("Unknown key 'targets.%s'. Valid keys: static_libraries, shared_libraries, executables", key))
		}
	}

	// Validate static_libraries
	if libs, ok := targets["static_libraries"].([]any); ok {
		for i, libRaw := range libs {
			if lib, ok := libRaw.(map[string]any); ok {
				errors = append(errors, yp.validateLibrary(lib, i, "static_libraries")...)
			} else {
				errors = append(errors, fmt.Sprintf("targets.static_libraries[%d] must be a map", i))
			}
		}
	}

	// Validate shared_libraries
	if libs, ok := targets["shared_libraries"].([]any); ok {
		for i, libRaw := range libs {
			if lib, ok := libRaw.(map[string]any); ok {
				errors = append(errors, yp.validateLibrary(lib, i, "shared_libraries")...)
			} else {
				errors = append(errors, fmt.Sprintf("targets.shared_libraries[%d] must be a map", i))
			}
		}
	}

	// Validate executables
	if exes, ok := targets["executables"].([]any); ok {
		for i, exeRaw := range exes {
			if exe, ok := exeRaw.(map[string]any); ok {
				errors = append(errors, yp.validateExecutable(exe, i)...)
			} else {
				errors = append(errors, fmt.Sprintf("targets.executables[%d] must be a map", i))
			}
		}
	}

	return errors
}

// validateLibrary validates a library target
func (yp *YAMLParser) validateLibrary(lib map[string]any, index int, section string) []string {
	errors := []string{}
	targetName := fmt.Sprintf("targets.%s[%d]", section, index)

	if name, ok := lib["name"].(string); ok {
		targetName = fmt.Sprintf("library '%s'", name)
	} else {
		errors = append(errors, fmt.Sprintf("targets.%s[%d] missing required 'name' field", section, index))
	}

	// Check for unknown keys
	for key := range lib {
		if !knownLibraryKeys[key] {
			errors = append(errors, fmt.Sprintf("Unknown key in %s: '%s'. Did you mean one of: %s?",
				targetName, key, yp.suggestKey(key, knownLibraryKeys)))
		}
	}

	// Validate required fields
	if _, ok := lib["sources"]; !ok {
		errors = append(errors, fmt.Sprintf("%s missing required 'sources' field", targetName))
	}

	// Validate type if present
	if libType, exists := lib["type"]; exists {
		if typeStr, ok := libType.(string); ok {
			validTypes := map[string]bool{"static_library": true, "shared_library": true}
			if !validTypes[typeStr] {
				errors = append(errors, fmt.Sprintf("%s has invalid 'type': '%s'. Must be 'static_library' or 'shared_library'", targetName, typeStr))
			}
		} else {
			errors = append(errors, fmt.Sprintf("%s 'type' must be a string", targetName))
		}
	}

	// Validate language if present
	if lang, exists := lib["language"]; exists {
		if langStr, ok := lang.(string); ok {
			validLangs := map[string]bool{"c": true, "c++": true, "cpp": true}
			if !validLangs[langStr] {
				errors = append(errors, fmt.Sprintf("%s has invalid 'language': '%s'. Must be 'c' or 'c++'", targetName, langStr))
			}
		}
	}

	return errors
}

// validateExecutable validates an executable target
func (yp *YAMLParser) validateExecutable(exe map[string]any, index int) []string {
	errors := []string{}
	targetName := fmt.Sprintf("targets.executables[%d]", index)

	if name, ok := exe["name"].(string); ok {
		targetName = fmt.Sprintf("executable '%s'", name)
	} else {
		errors = append(errors, fmt.Sprintf("targets.executables[%d] missing required 'name' field", index))
	}

	// Check for unknown keys
	for key := range exe {
		if !knownExecutableKeys[key] {
			errors = append(errors, fmt.Sprintf("Unknown key in %s: '%s'. Did you mean one of: %s?",
				targetName, key, yp.suggestKey(key, knownExecutableKeys)))
		}
	}

	// Validate required fields
	if _, ok := exe["sources"]; !ok {
		errors = append(errors, fmt.Sprintf("%s missing required 'sources' field", targetName))
	}

	return errors
}

// validateArtifacts validates the artifacts section
func (yp *YAMLParser) validateArtifacts(artifacts map[string]any) []string {
	errors := []string{}

	for key := range artifacts {
		if !knownArtifactsKeys[key] {
			errors = append(errors, fmt.Sprintf("Unknown key 'artifacts.%s'. Valid keys: copy, transform, generate, install", key))
		}
	}

	return errors
}

// Known staging keys
var knownStagingKeys = map[string]bool{
	"name": true, "destination": true, "use_symlinks": true, "contents": true,
}

// Known staging folder entry keys
var knownStagingFolderKeys = map[string]bool{
	"folder": true, "targets": true, "artifacts": true, "files": true, "contents": true,
}

// validateStaging validates the staging section
func (yp *YAMLParser) validateStaging(staging map[string]any) []string {
	errors := []string{}

	for key := range staging {
		if !knownStagingKeys[key] {
			errors = append(errors, fmt.Sprintf("Unknown key 'staging.%s'. Valid keys: name, destination, use_symlinks, contents", key))
		}
	}

	// Validate contents section if present (array of folder entries)
	if contents, ok := staging["contents"].([]any); ok {
		for i, itemRaw := range contents {
			if item, ok := itemRaw.(map[string]any); ok {
				for key := range item {
					if !knownStagingFolderKeys[key] {
						errors = append(errors, fmt.Sprintf("Unknown key 'staging.contents[%d].%s'. Valid keys: folder, targets, artifacts, files, contents", i, key))
					}
				}
			}
		}
	}

	return errors
}

// Known install keys
var knownInstallKeys = map[string]bool{
	"name": true, "staging": true, "destination": true, "format": true,
	"follow_symlinks": true, "filename": true,
}

// validateInstall validates the install section
func (yp *YAMLParser) validateInstall(install []any) []string {
	errors := []string{}

	for i, itemRaw := range install {
		item, ok := itemRaw.(map[string]any)
		if !ok {
			errors = append(errors, fmt.Sprintf("install[%d] must be a map", i))
			continue
		}

		for key := range item {
			if !knownInstallKeys[key] {
				errors = append(errors, fmt.Sprintf("Unknown key 'install[%d].%s'. Valid keys: name, staging, destination, format, follow_symlinks, filename", i, key))
			}
		}

		// Validate required fields
		if _, ok := item["name"]; !ok {
			errors = append(errors, fmt.Sprintf("install[%d] missing required 'name' field", i))
		}
	}

	return errors
}

// validateDependencies validates the dependencies section
func (yp *YAMLParser) validateDependencies(deps map[string]any) []string {
	errors := []string{}

	for key := range deps {
		if !knownDependenciesKeys[key] {
			errors = append(errors, fmt.Sprintf("Unknown key 'dependencies.%s'. Did you mean one of: %s?",
				key, yp.suggestKey(key, knownDependenciesKeys)))
		}
	}

	return errors
}

// validateLegacyTargetSection validates legacy library/executable sections
func (yp *YAMLParser) validateLegacyTargetSection(section any, sectionName string, knownKeys map[string]bool) []string {
	errors := []string{}

	var targets []any
	switch v := section.(type) {
	case map[string]any:
		targets = []any{v}
	case []any:
		targets = v
	default:
		errors = append(errors, fmt.Sprintf("'%s' must be a dictionary or list of dictionaries", sectionName))
		return errors
	}

	for i, targetRaw := range targets {
		target, ok := targetRaw.(map[string]any)
		if !ok {
			errors = append(errors, fmt.Sprintf("%s[%d] must be a dictionary", sectionName, i))
			continue
		}

		targetName := fmt.Sprintf("%s[%d]", sectionName, i)
		if name, ok := target["name"].(string); ok {
			targetName = fmt.Sprintf("%s '%s'", sectionName, name)
		}

		// Check for unknown keys
		for key := range target {
			if !knownKeys[key] {
				errors = append(errors, fmt.Sprintf("Unknown key in %s: '%s'", targetName, key))
			}
		}

		// Validate required fields
		if _, ok := target["name"].(string); !ok {
			errors = append(errors, fmt.Sprintf("%s missing required 'name' field", targetName))
		}
		if _, ok := target["sources"]; !ok {
			errors = append(errors, fmt.Sprintf("%s missing required 'sources' field", targetName))
		}
	}

	return errors
}

// suggestKey returns a comma-separated list of valid keys for error messages
func (yp *YAMLParser) suggestKey(unknownKey string, knownKeys map[string]bool) string {
	keys := make([]string, 0, len(knownKeys))
	for k := range knownKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	
	// Find closest match using simple prefix matching
	for _, k := range keys {
		if strings.HasPrefix(k, unknownKey[:min(3, len(unknownKey))]) {
			return k + " (or see documentation for all options)"
		}
	}
	
	if len(keys) <= 5 {
		return strings.Join(keys, ", ")
	}
	return strings.Join(keys[:5], ", ") + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ExtractSection extracts a typed section from config
func (yp *YAMLParser) ExtractSection(config map[string]any, key string) map[string]any {
	if section, ok := config[key].(map[string]any); ok {
		return section
	}
	return make(map[string]any)
}

// ExtractList extracts a list section from config
func (yp *YAMLParser) ExtractList(config map[string]any, key string) []any {
	if list, ok := config[key].([]any); ok {
		return list
	}
	return []any{}
}

// MergeConfigs merges configuration sections (later configs override earlier ones)
// Lists are concatenated, maps are merged recursively
func (yp *YAMLParser) MergeConfigs(configs ...any) map[string]any {
	merged := make(map[string]any)
	for _, configRaw := range configs {
		config, ok := configRaw.(map[string]any)
		if !ok {
			continue
		}
		for key, value := range config {
			if valueList, ok := value.([]any); ok {
				if existingList, ok := merged[key].([]any); ok {
					merged[key] = append(existingList, valueList...)
				} else {
					merged[key] = value
				}
			} else {
				merged[key] = value
			}
		}
	}
	return merged
}
