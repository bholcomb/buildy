package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

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
func (yp *YAMLParser) ValidateConfig(config map[string]any) []string {
	errors := []string{}

	// Validate project section
	project, hasProject := config["project"].(map[string]any)
	if !hasProject {
		errors = append(errors, "Missing 'project' section")
	} else {
		if name, ok := project["name"].(string); !ok || strings.TrimSpace(name) == "" {
			errors = append(errors, "Missing or invalid 'project.name'")
		}
	}

	// Helper to validate target list
	validateTargetList := func(targets []any, targetType string) {
		for i, targetRaw := range targets {
			target, ok := targetRaw.(map[string]any)
			if !ok {
				errors = append(errors, fmt.Sprintf("%s %d must be a dictionary", targetType, i))
				continue
			}
			if _, ok := target["name"].(string); !ok {
				errors = append(errors, fmt.Sprintf("%s %d missing required 'name' field", targetType, i))
			}
			if _, ok := target["sources"]; !ok {
				targetName := "unknown"
				if name, ok := target["name"].(string); ok {
					targetName = name
				}
				errors = append(errors, fmt.Sprintf("%s '%s' missing required 'sources' field", targetType, targetName))
			}
		}
	}

	// Validate new format: targets.libraries and targets.executables
	if targets, ok := config["targets"].(map[string]any); ok {
		if libs, ok := targets["libraries"].([]any); ok {
			validateTargetList(libs, "Library")
		}
		if exes, ok := targets["executables"].([]any); ok {
			validateTargetList(exes, "Executable")
		}
	}

	// Validate legacy library format (can be a single dict or list of dicts)
	if library, ok := config["library"]; ok {
		var libraries []any
		switch v := library.(type) {
		case map[string]any:
			libraries = []any{v}
		case []any:
			libraries = v
		default:
			errors = append(errors, "'library' must be a dictionary or list of dictionaries")
		}
		validateTargetList(libraries, "Library")
	}

	// Validate legacy executable format (can be a single dict or list of dicts)
	if executable, ok := config["executable"]; ok {
		var executables []any
		switch v := executable.(type) {
		case map[string]any:
			executables = []any{v}
		case []any:
			executables = v
		default:
			errors = append(errors, "'executable' must be a dictionary or list of dictionaries")
		}
		validateTargetList(executables, "Executable")
	}

	// Validate output paths don't escape project
	if outputConfig, ok := config["output"].(map[string]any); ok {
		if baseDir, ok := outputConfig["base_dir"].(string); ok {
			if strings.Contains(baseDir, "..") || filepath.IsAbs(baseDir) {
				errors = append(errors, fmt.Sprintf("Invalid output base_dir '%s' - must be relative and not contain '..'", baseDir))
			}
		}
	}

	return errors
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
