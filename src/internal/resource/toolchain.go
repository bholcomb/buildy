package resource

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// Tool represents an individual tool within a toolchain
type Tool struct {
	Name               string              `yaml:"name"`
	Action             string              `yaml:"action"`
	Command            string              `yaml:"command"`
	CommandsByPlatform map[string]string   `yaml:"-"` // Platform-specific commands (linux, windows, macos, darwin)
	InputExtensions    []string            `yaml:"input_extensions"`
	OutputExtension    string              `yaml:"output_extension"`
	OutputPattern      string              `yaml:"output_pattern"`
	Flags              map[string][]string `yaml:"flags"`
	Supports           map[string]any      `yaml:"supports"`
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

// NewTool creates a new Tool with defaults
func NewTool(name, action, command string, inputExts []string, outputExt string) *Tool {
	return &Tool{
		Name:            name,
		Action:          action,
		Command:         command,
		InputExtensions: inputExts,
		OutputExtension: outputExt,
		OutputPattern:   "{name}",
		Flags:           make(map[string][]string),
		Supports:        make(map[string]any),
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
