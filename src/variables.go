package main

import (
	"fmt"
	"log"
	"os"
	"regexp"
)

// VariableValue stores a variable's value and its source for provenance tracking
type VariableValue struct {
	Value  string
	Source string
}

// ImportedEnvVar represents an environment variable import with default
type ImportedEnvVar struct {
	Name     string
	Default  *string // nil means required (no default)
	Value    string  // Resolved value
	Source   string  // "environment" or "default"
}

// VariableEnvironment provides hierarchical variable environment with provenance tracking and chaining
type VariableEnvironment struct {
	variables       map[string]VariableValue // name -> (value, source)
	scopes          []string                 // Stack of scope names for tracking
	parent          *VariableEnvironment     // Parent environment for chaining
	importedEnvVars map[string]ImportedEnvVar // Explicitly imported env vars
}

// NewVariableEnvironment creates a new variable environment
func NewVariableEnvironment(parent *VariableEnvironment) *VariableEnvironment {
	return &VariableEnvironment{
		variables:       make(map[string]VariableValue),
		scopes:          make([]string, 0),
		parent:          parent,
		importedEnvVars: make(map[string]ImportedEnvVar),
	}
}

// CreateChild creates a child variable environment
func (ve *VariableEnvironment) CreateChild() *VariableEnvironment {
	return NewVariableEnvironment(ve)
}

// PushScope pushes a new scope onto the stack
func (ve *VariableEnvironment) PushScope(scopeName string) {
	ve.scopes = append(ve.scopes, scopeName)
	log.Printf("Entered scope: %s", scopeName)
}

// PopScope pops the current scope
func (ve *VariableEnvironment) PopScope() {
	if len(ve.scopes) > 0 {
		scope := ve.scopes[len(ve.scopes)-1]
		ve.scopes = ve.scopes[:len(ve.scopes)-1]
		log.Printf("Exited scope: %s", scope)
	}
}

// SetVariable sets a variable with its source location
func (ve *VariableEnvironment) SetVariable(name, value, source string) {
	if source == "" {
		if len(ve.scopes) > 0 {
			source = ve.scopes[len(ve.scopes)-1]
		} else {
			source = "unknown"
		}
	}
	
	// Override existing variable (higher priority)
	if oldVal, exists := ve.variables[name]; exists {
		log.Printf("Variable '%s' overridden: '%s' (%s) -> '%s' (%s)", 
			name, oldVal.Value, oldVal.Source, value, source)
	} else {
		log.Printf("Variable set: %s = %s (%s)", name, value, source)
	}
	
	ve.variables[name] = VariableValue{Value: value, Source: source}
}

// GetVariable gets a variable value, searching parent chain if not found locally
// Only allows access to explicitly imported environment variables
func (ve *VariableEnvironment) GetVariable(name string) (string, bool) {
	if val, exists := ve.variables[name]; exists {
		return val.Value, true
	}
	
	// Check imported env vars
	if imported, exists := ve.importedEnvVars[name]; exists {
		return imported.Value, true
	}
	
	// Search parent environment if not found locally
	if ve.parent != nil {
		return ve.parent.GetVariable(name)
	}
	
	return "", false
}

// GetProvenance gets the source/provenance of a variable, searching parent chain
func (ve *VariableEnvironment) GetProvenance(name string) (string, bool) {
	if val, exists := ve.variables[name]; exists {
		return val.Source, true
	}
	
	// Search parent environment if not found locally
	if ve.parent != nil {
		return ve.parent.GetProvenance(name)
	}
	
	return "", false
}

// GetVariableWithSource gets a variable and its source, searching parent chain
func (ve *VariableEnvironment) GetVariableWithSource(name string) (VariableValue, bool) {
	if val, exists := ve.variables[name]; exists {
		return val, true
	}
	
	// Search parent environment if not found locally
	if ve.parent != nil {
		return ve.parent.GetVariableWithSource(name)
	}
	
	return VariableValue{}, false
}

// ResolveString resolves all ${variable} references in a string
func (ve *VariableEnvironment) ResolveString(text string, collectedErrors *[]string, maxIterations int) string {
	if maxIterations == 0 {
		maxIterations = 10
	}
	
	// Find all ${variable} patterns
	pattern := regexp.MustCompile(`\$\{([^}]+)\}`)
	
	// Resolve iteratively to handle nested variables
	for iteration := 0; iteration < maxIterations; iteration++ {
		unresolved := make([]string, 0)
		
		newText := pattern.ReplaceAllStringFunc(text, func(match string) string {
			// Extract variable name from ${varname}
			varName := match[2 : len(match)-1]
			
			if value, found := ve.GetVariable(varName); found {
				return value
			}
			
			unresolved = append(unresolved, varName)
			return match // Keep original if not found
		})
		
		// If nothing changed, we're done
		if newText == text {
			break
		}
		
		text = newText
		
		// If we still have unresolved variables and nothing changed, stop
		if len(unresolved) > 0 {
			break
		}
	}
	
	// Collect errors if requested
	if collectedErrors != nil {
		// Check for remaining unresolved variables
		matches := pattern.FindAllStringSubmatch(text, -1)
		if len(matches) > 0 {
			for _, match := range matches {
				varName := match[1]
				*collectedErrors = append(*collectedErrors, 
					fmt.Sprintf("Unresolved variable '${%s}' in: %s", varName, text))
			}
		}
	}
	
	return text
}

// ResolveRecursive recursively resolves variables in nested data structures
func (ve *VariableEnvironment) ResolveRecursive(value interface{}, collectedErrors *[]string) interface{} {
	switch v := value.(type) {
	case string:
		return ve.ResolveString(v, collectedErrors, 10)
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = ve.ResolveRecursive(item, collectedErrors)
		}
		return result
	case map[string]interface{}:
		result := make(map[string]interface{})
		for k, val := range v {
			result[k] = ve.ResolveRecursive(val, collectedErrors)
		}
		return result
	default:
		return value
	}
}

// GetAllVariables gets all variables with their values and sources for export
func (ve *VariableEnvironment) GetAllVariables(includeParent bool) map[string]map[string]string {
	result := make(map[string]map[string]string)
	
	// Get parent variables first (so local overrides them)
	if includeParent && ve.parent != nil {
		result = ve.parent.GetAllVariables(true)
	}
	
	// Add local variables (overriding parent)
	for name, val := range ve.variables {
		result[name] = map[string]string{
			"value":  val.Value,
			"source": val.Source,
		}
	}
	
	return result
}

// ExtractVariablesFromSection extracts variables from a 'variables' section in the config
func (ve *VariableEnvironment) ExtractVariablesFromSection(section map[string]interface{}, sourceName string) {
	variables, ok := section["variables"]
	if !ok {
		return
	}
	
	varsMap, ok := variables.(map[string]interface{})
	if !ok {
		log.Printf("WARNING: Variables section in '%s' is not a dictionary", sourceName)
		return
	}
	
	for name, value := range varsMap {
		// Skip import_env_vars and platforms - handled separately
		if name == "import_env_vars" || name == "platforms" {
			continue
		}
		
		var strValue string
		switch v := value.(type) {
		case string:
			strValue = v
		default:
			strValue = fmt.Sprintf("%v", v)
		}
		ve.SetVariable(name, strValue, sourceName)
	}
}

// ImportEnvVars processes the import_env_vars section and imports environment variables
// Returns an error if any required env var is not set
func (ve *VariableEnvironment) ImportEnvVars(variablesSection map[string]interface{}, sourceName string) error {
	importSection, ok := variablesSection["import_env_vars"]
	if !ok {
		return nil
	}
	
	importList, ok := importSection.([]interface{})
	if !ok {
		return fmt.Errorf("import_env_vars must be a list")
	}
	
	for i, item := range importList {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("import_env_vars[%d]: each entry must be a map with 'name' and 'default'", i)
		}
		
		name, ok := itemMap["name"].(string)
		if !ok || name == "" {
			return fmt.Errorf("import_env_vars[%d]: 'name' is required and must be a string", i)
		}
		
		// Get default value (can be string or null)
		var defaultVal *string
		if def, exists := itemMap["default"]; exists {
			if def == nil {
				// null/~ means required
				defaultVal = nil
			} else if defStr, ok := def.(string); ok {
				defaultVal = &defStr
			} else {
				defStr := fmt.Sprintf("%v", def)
				defaultVal = &defStr
			}
		} else {
			return fmt.Errorf("import_env_vars[%d] (%s): 'default' is required (use ~ for required vars)", i, name)
		}
		
		// Resolve the value
		var value string
		var source string
		
		if envVal, exists := os.LookupEnv(name); exists {
			value = envVal
			source = "environment"
			log.Printf("Imported env var: %s = %s (from environment)", name, value)
		} else if defaultVal != nil {
			value = *defaultVal
			source = "default"
			log.Printf("Imported env var: %s = %s (using default)", name, value)
		} else {
			// Required but not set
			return fmt.Errorf(
				"ERROR: Required environment variable '%s' is not set.\n"+
				"       Defined in: variables.import_env_vars (%s)\n"+
				"       \n"+
				"       To fix: export %s=/path/to/value",
				name, sourceName, name,
			)
		}
		
		ve.importedEnvVars[name] = ImportedEnvVar{
			Name:    name,
			Default: defaultVal,
			Value:   value,
			Source:  source,
		}
	}
	
	return nil
}

// ExtractPlatformVariables extracts platform-specific variables
func (ve *VariableEnvironment) ExtractPlatformVariables(variablesSection map[string]interface{}, platform, sourceName string) {
	platforms, ok := variablesSection["platforms"]
	if !ok {
		return
	}
	
	platformsMap, ok := platforms.(map[string]interface{})
	if !ok {
		log.Printf("WARNING: variables.platforms in '%s' is not a dictionary", sourceName)
		return
	}
	
	platformVars, ok := platformsMap[platform].(map[string]interface{})
	if !ok {
		return // No variables for this platform
	}
	
	for name, value := range platformVars {
		var strValue string
		switch v := value.(type) {
		case string:
			strValue = v
		default:
			strValue = fmt.Sprintf("%v", v)
		}
		ve.SetVariable(name, strValue, fmt.Sprintf("%s.platforms.%s", sourceName, platform))
	}
}

