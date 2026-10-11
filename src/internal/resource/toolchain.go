package resource

import (
	"strings"

	"buildy/internal/config"
	"buildy/pkg/util"
)

// ToolchainConfig is a loaded toolchain (config.Toolchain).
type ToolchainConfig = config.Toolchain

// Tool is one tool of a toolchain.
type Tool = config.Tool

// ResolveCommandParams resolves a build tool's command parameters and returns each placeholder's
// value. A parameter's sources are looked up in order:
//   - "tool_params.*": the template step's tool parameters
//   - "item.*": the target
//   - "tool.flags.*": the tool's flags ("tool.flags.${config}" for the configuration's)
func ResolveCommandParams(t *Tool, toolParams, itemConfig map[string]any, configuration string) map[string]string {
	result := make(map[string]string, t.CommandParams.Len())
	for _, name := range t.CommandParams.Keys {
		result[name] = resolveParamValue(t, t.CommandParams.Values[name], toolParams, itemConfig, configuration)
	}
	return result
}

// resolveParamValue resolves a single command parameter
func resolveParamValue(t *Tool, param *config.CommandParam, toolParams, itemConfig map[string]any, configuration string) string {
	var rawValue any
	var found bool

	// Try each source in order until we find a value
	for _, source := range param.Sources {
		rawValue, found = lookupSource(t, strings.ReplaceAll(source, "${config}", configuration), toolParams, itemConfig)
		if found {
			break
		}
	}
	if !found {
		return param.Default
	}

	values := util.ExtractStringList(rawValue)
	if len(values) == 0 {
		return param.Default
	}

	// If Match is specified, keep the values equal to it
	if param.Match != "" {
		matched := []string{}
		for _, v := range values {
			if v == param.Match {
				matched = append(matched, v)
			}
		}
		if len(matched) == 0 {
			return param.Default
		}
		values = matched
	}

	joinSep := param.Join
	if joinSep == "" {
		joinSep = " "
	}
	joined := strings.Join(values, joinSep)

	if param.QuoteIfSpaces && strings.ContainsRune(joined, ' ') {
		joined = "'" + joined + "'"
	}
	if param.Format != "" {
		return strings.ReplaceAll(param.Format, "${value}", joined)
	}
	return joined
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

// HashToolchainConfig is a hash of the toolchain's whole content, for cache keys.
func HashToolchainConfig(tc *ToolchainConfig) string {
	if tc == nil {
		return ""
	}
	return tc.Hash()
}
