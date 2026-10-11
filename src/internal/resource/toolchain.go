package resource

import (
	"fmt"
	"strings"

	"buildy/internal/config"
	"buildy/pkg/util"
)

// ToolchainConfig is a loaded toolchain (config.Toolchain).
type ToolchainConfig = config.Toolchain

// Tool is one tool of a toolchain.
type Tool = config.Tool

// ResolveCommandParams resolves a build tool's command parameters and returns each placeholder's
// arguments. A parameter's sources are looked up in order:
//   - "tool_params.*": the template step's tool parameters
//   - "item.*": the target
//   - "tool.flags.*": the tool's flags ("tool.flags.${config}" for the configuration's)
//
// resolve resolves ${...} in the values of a parameter with resolve_variables.
func ResolveCommandParams(t *Tool, toolParams, itemConfig map[string]any, configuration string, resolve func(string) (string, error)) (map[string][]string, error) {
	result := make(map[string][]string, t.CommandParams.Len())
	for _, name := range t.CommandParams.Keys {
		args, err := resolveParamValue(t, t.CommandParams.Values[name], toolParams, itemConfig, configuration, resolve)
		if err != nil {
			return nil, fmt.Errorf("command parameter %s of tool %s: %w", name, t.Name, err)
		}
		result[name] = args
	}
	return result, nil
}

// resolveParamValue resolves a single command parameter to its arguments: none when it has no
// value, else its format with ${value} filled in (the value itself without a format).
func resolveParamValue(t *Tool, param *config.CommandParam, toolParams, itemConfig map[string]any, configuration string, resolve func(string) (string, error)) ([]string, error) {
	values, ok := paramValues(t, param, toolParams, itemConfig, configuration)
	if !ok {
		if param.Default == "" {
			return nil, nil
		}
		return []string{param.Default}, nil
	}

	joinSep := param.Join
	if joinSep == "" {
		joinSep = " "
	}
	joined := strings.Join(values, joinSep)
	if param.ResolveVariables {
		var err error
		if joined, err = resolve(joined); err != nil {
			return nil, err
		}
	}
	if param.Format.IsZero() {
		return []string{joined}, nil
	}
	return param.Format.Expand(config.MapValues(map[string][]string{"value": {joined}}))
}

// paramValues returns the values of the first source that has some (only those equal to match,
// if the parameter has one).
func paramValues(t *Tool, param *config.CommandParam, toolParams, itemConfig map[string]any, configuration string) ([]string, bool) {
	for _, source := range param.Sources {
		rawValue, found := lookupSource(t, strings.ReplaceAll(source, "${config}", configuration), toolParams, itemConfig)
		if !found {
			continue
		}
		values := util.ExtractStringList(rawValue)
		if len(values) == 0 {
			return nil, false
		}
		if param.Match == "" {
			return values, true
		}
		var matched []string
		for _, v := range values {
			if v == param.Match {
				matched = append(matched, v)
			}
		}
		return matched, len(matched) > 0
	}
	return nil, false
}

// lookupSource looks up a value from the source path
func lookupSource(t *Tool, source string, toolParams, itemConfig map[string]any) (any, bool) {
	parts := strings.Split(source, ".")
	switch parts[0] {
	case "tool_params":
		if len(parts) < 2 {
			return nil, false
		}
		return util.LookupPath(toolParams, parts[1:])
	case "item":
		if len(parts) < 2 {
			return nil, false
		}
		return util.LookupPath(itemConfig, parts[1:])
	case "tool":
		if len(parts) >= 3 && parts[1] == "flags" {
			if flags, ok := t.Flags[parts[2]]; ok {
				return flags, true
			}
		}
	}
	return nil, false
}
