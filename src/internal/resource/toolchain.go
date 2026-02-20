package resource

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// CommandParam defines how a command placeholder is resolved
type CommandParam struct {
	// Sources is an ordered list of places to look for the value
	// Examples: "tool_params.features", "item.features", "tool.flags.${config}"
	Sources []string `yaml:"sources"`
	// Format is how to format the final value (use ${value} as placeholder)
	// Example: "--features ${value}" or "-tags ${value}"
	Format string `yaml:"format"`
	// Join is the separator for multiple values (default: " ")
	Join string `yaml:"join"`
	// QuoteIfSpaces wraps the value in quotes if it contains spaces
	QuoteIfSpaces bool `yaml:"quote_if_spaces"`
	// ResolveVariables runs variable resolution on the value (for ${config} etc.)
	ResolveVariables bool `yaml:"resolve_variables"`
	// Match extracts a specific value from an array (e.g., "--release" from flags)
	Match string `yaml:"match"`
	// Optional means empty string if not found (default: true)
	Optional bool `yaml:"optional"`
}

// Tool represents an individual tool within a toolchain
type Tool struct {
	Name               string                  `yaml:"name"`
	Action             string                  `yaml:"action"`
	Command            string                  `yaml:"command"`
	CommandsByPlatform map[string]string       `yaml:"-"` // Platform-specific commands (linux, windows, macos, darwin)
	InputExtensions    []string                `yaml:"input_extensions"`
	OutputExtension    string                  `yaml:"output_extension"`
	OutputPattern      string                  `yaml:"output_pattern"`
	Flags              map[string][]string     `yaml:"flags"`
	Supports           map[string]any          `yaml:"supports"`
	CommandParams      map[string]CommandParam `yaml:"-"` // Declarative command parameter definitions
	ManifestFiles      []string                `yaml:"-"` // Files to track for cache invalidation (e.g., go.mod, Cargo.toml)
}

// GetCommand returns the command for the given platform, falling back to the default Command
func (t *Tool) GetCommand(platform string) string {
	if t.CommandsByPlatform != nil {
		// Check for exact platform match
		if cmd, ok := t.CommandsByPlatform[platform]; ok {
			return cmd
		}
		// Handle darwin/macos alias
		if platform == "darwin" {
			if cmd, ok := t.CommandsByPlatform["macos"]; ok {
				return cmd
			}
		}
		if platform == "macos" {
			if cmd, ok := t.CommandsByPlatform["darwin"]; ok {
				return cmd
			}
		}
	}
	return t.Command
}

// ResolveCommandParams resolves all command parameters and returns a map of placeholder -> value
// The context provides values for source lookups:
//   - "tool_params.*": from toolParams map
//   - "item.*": from itemConfig map
//   - "tool.flags.*": from tool.Flags
//   - "config": current configuration name (debug/release)
func (t *Tool) ResolveCommandParams(toolParams, itemConfig map[string]any, configuration string) map[string]string {
	result := make(map[string]string)

	for paramName, param := range t.CommandParams {
		value := t.resolveParamValue(param, toolParams, itemConfig, configuration)
		result[paramName] = value
	}

	return result
}

// resolveParamValue resolves a single command parameter
func (t *Tool) resolveParamValue(param CommandParam, toolParams, itemConfig map[string]any, configuration string) string {
	var rawValue any
	var found bool

	// Try each source in order until we find a value
	for _, source := range param.Sources {
		// Replace ${config} in source path
		resolvedSource := source
		if configuration != "" {
			resolvedSource = replaceConfigVar(source, configuration)
		}

		rawValue, found = t.lookupSource(resolvedSource, toolParams, itemConfig)
		if found {
			break
		}
	}

	if !found {
		if param.Optional {
			return ""
		}
		return ""
	}

	// Convert to string(s)
	var values []string
	switch v := rawValue.(type) {
	case string:
		if v == "" {
			return ""
		}
		values = []string{v}
	case []string:
		values = v
	case []any:
		for _, item := range v {
			if str, ok := item.(string); ok {
				values = append(values, str)
			}
		}
	}

	if len(values) == 0 {
		return ""
	}

	// If Match is specified, filter to matching values
	if param.Match != "" {
		matched := []string{}
		for _, v := range values {
			if v == param.Match {
				matched = append(matched, v)
			}
		}
		if len(matched) == 0 {
			return ""
		}
		values = matched
	}

	// Join values
	joinSep := param.Join
	if joinSep == "" {
		joinSep = " "
	}
	joined := ""
	for i, v := range values {
		if i > 0 {
			joined += joinSep
		}
		joined += v
	}

	// Apply quoting if needed
	if param.QuoteIfSpaces && containsSpace(joined) {
		joined = "'" + joined + "'"
	}

	// Apply format
	finalValue := joined
	if param.Format != "" {
		finalValue = replaceValue(param.Format, joined)
	}

	return finalValue
}

// lookupSource looks up a value from the source path
func (t *Tool) lookupSource(source string, toolParams, itemConfig map[string]any) (any, bool) {
	parts := splitDot(source)
	if len(parts) == 0 {
		return nil, false
	}

	switch parts[0] {
	case "tool_params":
		if len(parts) < 2 {
			return nil, false
		}
		return lookupPath(toolParams, parts[1:])
	case "item":
		if len(parts) < 2 {
			return nil, false
		}
		return lookupPath(itemConfig, parts[1:])
	case "tool":
		if len(parts) >= 3 && parts[1] == "flags" {
			flagKey := parts[2]
			if flags, ok := t.Flags[flagKey]; ok {
				return flags, true
			}
		}
		return nil, false
	}

	return nil, false
}

// Helper functions for parameter resolution

func replaceConfigVar(s, config string) string {
	result := s
	for {
		idx := findSubstring(result, "${config}")
		if idx < 0 {
			break
		}
		result = result[:idx] + config + result[idx+9:]
	}
	return result
}

func replaceValue(format, value string) string {
	result := format
	for {
		idx := findSubstring(result, "${value}")
		if idx < 0 {
			break
		}
		result = result[:idx] + value + result[idx+8:]
	}
	return result
}

func findSubstring(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func containsSpace(s string) bool {
	for _, c := range s {
		if c == ' ' {
			return true
		}
	}
	return false
}

func splitDot(s string) []string {
	var parts []string
	current := ""
	for _, c := range s {
		if c == '.' {
			if current != "" {
				parts = append(parts, current)
			}
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

func lookupPath(m map[string]any, path []string) (any, bool) {
	if len(path) == 0 || m == nil {
		return nil, false
	}

	current := any(m)
	for _, key := range path {
		if currentMap, ok := current.(map[string]any); ok {
			if val, exists := currentMap[key]; exists {
				current = val
			} else {
				return nil, false
			}
		} else {
			return nil, false
		}
	}

	return current, true
}

// NewTool creates a new Tool with defaults
func NewTool(name, action, command string, inputExts []string, outputExt string) *Tool {
	return &Tool{
		Name:            name,
		Action:          action,
		Command:         command,
		InputExtensions: inputExts,
		OutputExtension: outputExt,
		OutputPattern:   "${name}",
		Flags:           make(map[string][]string),
		Supports:        make(map[string]any),
		CommandParams:   make(map[string]CommandParam),
		ManifestFiles:   []string{},
	}
}

// ToolchainConfig represents a toolchain configuration loaded from YAML
type ToolchainConfig struct {
	Name               string            `yaml:"name"`
	Description        string            `yaml:"description"`
	Language           string            `yaml:"language"` // Programming language this toolchain supports (e.g., "go", "c++", "rust")
	TargetPlatform     string            `yaml:"-"`
	TargetArchitecture string            `yaml:"-"`
	HostPlatform       string            `yaml:"-"`
	HostArchitecture   string            `yaml:"-"`
	ExecutionType      string            `yaml:"-"`
	ExecutionConfig    map[string]any    `yaml:"-"`
	Tools              map[string]*Tool  `yaml:"-"`
	Variables          map[string]string `yaml:"-"` // Toolchain-specific variables for command substitution
}

// NewToolchainConfig creates a new ToolchainConfig
func NewToolchainConfig(name, description string) *ToolchainConfig {
	return &ToolchainConfig{
		Name:            name,
		Description:     description,
		ExecutionConfig: make(map[string]any),
		Tools:           make(map[string]*Tool),
		Variables:       make(map[string]string),
	}
}

// HashToolchainConfig produces a deterministic hash of the toolchain content.
// It intentionally avoids raw map marshalling by converting to sorted slices.
func HashToolchainConfig(tc *ToolchainConfig) string {
	if tc == nil {
		return ""
	}

	type flagEntry struct {
		Key    string   `json:"key"`
		Values []string `json:"values"`
	}

	type toolEntry struct {
		Name            string      `json:"name"`
		Action          string      `json:"action"`
		Command         string      `json:"command"`
		InputExtensions []string    `json:"input_extensions"`
		OutputExtension string      `json:"output_extension"`
		OutputPattern   string      `json:"output_pattern"`
		Flags           []flagEntry `json:"flags"`
		Supports        []flagEntry `json:"supports"`
	}

	tools := make([]toolEntry, 0, len(tc.Tools))
	for name, tool := range tc.Tools {
		flags := make([]flagEntry, 0, len(tool.Flags))
		for k, vals := range tool.Flags {
			sortedVals := append([]string{}, vals...)
			sort.Strings(sortedVals)
			flags = append(flags, flagEntry{Key: k, Values: sortedVals})
		}
		sort.Slice(flags, func(i, j int) bool { return flags[i].Key < flags[j].Key })

		supports := make([]flagEntry, 0, len(tool.Supports))
		for k, v := range tool.Supports {
			supports = append(supports, flagEntry{Key: k, Values: []string{fmt.Sprintf("%v", v)}})
		}
		sort.Slice(supports, func(i, j int) bool { return supports[i].Key < supports[j].Key })

		tools = append(tools, toolEntry{
			Name:            name,
			Action:          tool.Action,
			Command:         tool.Command,
			InputExtensions: append([]string{}, tool.InputExtensions...),
			OutputExtension: tool.OutputExtension,
			OutputPattern:   tool.OutputPattern,
			Flags:           flags,
			Supports:        supports,
		})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	execEntries := []flagEntry{}
	for k, v := range tc.ExecutionConfig {
		execEntries = append(execEntries, flagEntry{Key: k, Values: []string{fmt.Sprintf("%v", v)}})
	}
	sort.Slice(execEntries, func(i, j int) bool { return execEntries[i].Key < execEntries[j].Key })

	payload := map[string]any{
		"name":             tc.Name,
		"description":      tc.Description,
		"target_platform":  tc.TargetPlatform,
		"target_arch":      tc.TargetArchitecture,
		"host_platform":    tc.HostPlatform,
		"host_arch":        tc.HostArchitecture,
		"execution_type":   tc.ExecutionType,
		"execution_config": execEntries,
		"tools":            tools,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return tc.Name // Fallback to name-based hash
	}

	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}
