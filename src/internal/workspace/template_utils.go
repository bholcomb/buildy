package workspace

import (
	"fmt"
	"strings"

	"buildy/internal/config"
	"buildy/pkg/util"
)

// resolveReference resolves a reference to previous step results
func (bte *BuildTemplateEngine) resolveReference(ref string, stepResults map[string]map[string]any) any {
	if ref == "" {
		return ref
	}

	// Remove ${...} wrapper if present
	if strings.HasPrefix(ref, "${") && strings.HasSuffix(ref, "}") {
		ref = strings.TrimPrefix(ref, "${")
		ref = strings.TrimSuffix(ref, "}")
	}

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

// lookupContextPath traverses a nested map using a dotted path (e.g., "item.includes")
// Returns the value and whether it was found
func lookupContextPath(context map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return nil, false
	}
	
	obj := any(context)
	for _, part := range parts {
		if m, ok := obj.(map[string]any); ok {
			if val, exists := m[part]; exists {
				obj = val
			} else {
				return nil, false
			}
		} else {
			return nil, false
		}
	}
	return obj, true
}

// resolveStrict resolves every ${...} in text, or fails naming what was being resolved.
func resolveStrict(env *util.VariableEnvironment, text, what string) (string, error) {
	var errs []string
	resolved := env.ResolveString(text, &errs, 10)
	if len(errs) > 0 {
		seen := map[string]bool{}
		var names []string
		for _, m := range unresolvedVariable.FindAllStringSubmatch(resolved, -1) {
			if !seen[m[2]] {
				seen[m[2]] = true
				names = append(names, m[2])
			}
		}
		return "", fmt.Errorf("unresolved variable %s in %s %q", strings.Join(names, ", "), what, text)
	}
	return resolved, nil
}

// resolveToolParams resolves tool parameters using VarEnv for variable resolution
// and context map for structural references (like ${item.includes} arrays)
func (bte *BuildTemplateEngine) resolveToolParams(stepParams config.Ordered[config.Param], context map[string]any, varEnv *util.VariableEnvironment) (map[string]any, error) {

	resolved := map[string]any{
		"defines":      []string{},
		"include_dirs": []string{},
		"extra_flags":  []string{},
		"lib_dirs":     []string{},
		"libs":         []string{},
		"kwargs":       make(map[string]any),
	}

	for _, key := range stepParams.Keys {
		switch v := stepParams.Values[key].Value.(type) {
		case string:
			// Check if this is a reference to a context value (like ${item.includes})
			if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
				ref := strings.TrimPrefix(v, "${")
				ref = strings.TrimSuffix(ref, "}")
				if obj, found := lookupContextPath(context, ref); found {
					switch val := obj.(type) {
					case []any:
						strList := []string{}
						for _, item := range val {
							if str, ok := item.(string); ok {
								strList = append(strList, str)
							}
						}
						resolved[key] = strList
					case []string:
						resolved[key] = val
					case string:
						resolved[key] = val
					default:
						resolved[key] = obj
					}
					continue
				}
				// Reference not found - optional field, use default
				continue
			}
			// Use VarEnv for variable resolution
			value, err := resolveStrict(varEnv, v, "tool parameter "+key)
			if err != nil {
				return nil, err
			}
			resolved[key] = value
		case []string:
			strList := []string{}
			for _, str := range v {
				{
					// Check if this is a reference to a context array
					if strings.HasPrefix(str, "${") && strings.HasSuffix(str, "}") {
						ref := strings.TrimPrefix(str, "${")
						ref = strings.TrimSuffix(ref, "}")
						if obj, found := lookupContextPath(context, ref); found {
							if nestedList := bte.flattenStringList(obj); len(nestedList) > 0 {
								strList = append(strList, nestedList...)
							}
							continue
						}
						// Reference not found - optional field, skip
						continue
					}
					// Use VarEnv for variable resolution
					value, err := resolveStrict(varEnv, str, "tool parameter "+key)
					if err != nil {
						return nil, err
					}
					strList = append(strList, value)
				}
			}
			resolved[key] = strList
		default:
			resolved[key] = v
		}
	}

	return resolved, nil
}

// toStringSlice converts various types to a string slice
// Handles []string, []any, and string types
func toStringSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		result := []string{}
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			} else if nested := toStringSlice(item); len(nested) > 0 {
				result = append(result, nested...)
			}
		}
		return result
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return []string{}
}

// flattenStringList is the method version for backward compatibility
func (bte *BuildTemplateEngine) flattenStringList(value any) []string {
	return toStringSlice(value)
}

// stepContext holds the context and VarEnv for a step expansion
type stepContext struct {
	Context map[string]any
	VarEnv  *util.VariableEnvironment
}

// newStepContext creates a step context from a base context with tool info
func newStepContext(baseContext map[string]any, parentEnv *util.VariableEnvironment, scopeName string, tool struct {
	OutputExt     string
	OutputPattern string
}) stepContext {
	// Create child VarEnv
	env := parentEnv.CreateChild()
	env.PushScope(scopeName)
	env.SetVariable("tool.output_ext", tool.OutputExt, scopeName)
	env.SetVariable("tool.output_pattern", tool.OutputPattern, scopeName)

	// Copy base context and add tool info
	ctx := make(map[string]any, len(baseContext)+1)
	for k, v := range baseContext {
		ctx[k] = v
	}
	ctx["tool"] = map[string]any{
		"output_ext":     tool.OutputExt,
		"output_pattern": tool.OutputPattern,
	}

	return stepContext{Context: ctx, VarEnv: env}
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
