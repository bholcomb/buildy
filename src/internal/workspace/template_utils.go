package workspace

import (
	"fmt"
	"log"
	"sort"
	"strings"
)

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

// convertResolvedParams converts resolved params to the format expected by BuildCommand
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
