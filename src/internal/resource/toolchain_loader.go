package resource

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadToolchainConfig loads a toolchain configuration from a YAML file
func LoadToolchainConfig(toolchainFile string) (*ToolchainConfig, error) {
	data, err := os.ReadFile(toolchainFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read toolchain file %s: %w", toolchainFile, err)
	}

	var rawConfig map[string]any
	if err := yaml.Unmarshal(data, &rawConfig); err != nil {
		return nil, fmt.Errorf("failed to parse toolchain YAML %s: %w", toolchainFile, err)
	}

	// Extract toolchain section
	tcData, ok := rawConfig["toolchain"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("toolchain file %s missing 'toolchain' section", toolchainFile)
	}

	tc := NewToolchainConfig("", "")

	// Parse basic fields
	if name, ok := tcData["name"].(string); ok {
		tc.Name = name
	} else {
		return nil, fmt.Errorf("toolchain missing 'name' field")
	}

	if desc, ok := tcData["description"].(string); ok {
		tc.Description = desc
	}

	// Parse language
	if lang, ok := tcData["language"].(string); ok {
		tc.Language = lang
	}

	// Parse target
	if targetData, ok := tcData["target"].(map[string]any); ok {
		if platform, ok := targetData["platform"].(string); ok {
			tc.TargetPlatform = platform
		}
		if arch, ok := targetData["architecture"].(string); ok {
			tc.TargetArchitecture = arch
		}
	}

	// Parse host
	if hostData, ok := tcData["host"].(map[string]any); ok {
		if platform, ok := hostData["platform"].(string); ok {
			tc.HostPlatform = platform
		}
		if arch, ok := hostData["architecture"].(string); ok {
			tc.HostArchitecture = arch
		}
	}

	// Parse execution
	if execData, ok := tcData["execution"].(map[string]any); ok {
		tc.ExecutionConfig = execData
		if execType, ok := execData["type"].(string); ok {
			tc.ExecutionType = execType
		}
	}

	// Parse tools
	if toolsData, ok := tcData["tools"].(map[string]any); ok {
		for toolName, toolDataRaw := range toolsData {
			toolData, ok := toolDataRaw.(map[string]any)
			if !ok {
				continue
			}

			tool := parseToolFromData(toolName, toolData)
			tc.Tools[toolName] = tool
		}
	}

	return tc, nil
}

// LoadToolchainConfigFromData loads a toolchain configuration from YAML data
func LoadToolchainConfigFromData(data []byte, sourceName string) (*ToolchainConfig, error) {
	var rawConfig map[string]any
	if err := yaml.Unmarshal(data, &rawConfig); err != nil {
		return nil, fmt.Errorf("failed to parse toolchain YAML %s: %w", sourceName, err)
	}

	// Extract toolchain section
	tcData, ok := rawConfig["toolchain"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("toolchain data %s missing 'toolchain' section", sourceName)
	}

	tc := NewToolchainConfig("", "")

	// Parse basic fields
	if name, ok := tcData["name"].(string); ok {
		tc.Name = name
	} else {
		return nil, fmt.Errorf("toolchain missing 'name' field")
	}

	if desc, ok := tcData["description"].(string); ok {
		tc.Description = desc
	}

	// Parse language
	if lang, ok := tcData["language"].(string); ok {
		tc.Language = lang
	}

	// Parse target
	if targetData, ok := tcData["target"].(map[string]any); ok {
		if platform, ok := targetData["platform"].(string); ok {
			tc.TargetPlatform = platform
		}
		if arch, ok := targetData["architecture"].(string); ok {
			tc.TargetArchitecture = arch
		}
	}

	// Parse host
	if hostData, ok := tcData["host"].(map[string]any); ok {
		if platform, ok := hostData["platform"].(string); ok {
			tc.HostPlatform = platform
		}
		if arch, ok := hostData["architecture"].(string); ok {
			tc.HostArchitecture = arch
		}
	}

	// Parse execution
	if execData, ok := tcData["execution"].(map[string]any); ok {
		tc.ExecutionConfig = execData
		if execType, ok := execData["type"].(string); ok {
			tc.ExecutionType = execType
		}
	}

	// Parse tools
	if toolsData, ok := tcData["tools"].(map[string]any); ok {
		for toolName, toolDataRaw := range toolsData {
			toolData, ok := toolDataRaw.(map[string]any)
			if !ok {
				continue
			}

			tool := parseToolFromData(toolName, toolData)
			tc.Tools[toolName] = tool
		}
	}

	return tc, nil
}

// LoadToolchainConfigFromDict creates a ToolchainConfig from a dictionary (for testing)
func LoadToolchainConfigFromDict(configDict map[string]any) (*ToolchainConfig, error) {
	tc := NewToolchainConfig("", "")

	if name, ok := configDict["name"].(string); ok {
		tc.Name = name
	} else {
		return nil, fmt.Errorf("toolchain missing 'name' field")
	}

	if desc, ok := configDict["description"].(string); ok {
		tc.Description = desc
	}

	if platform, ok := configDict["target_platform"].(string); ok {
		tc.TargetPlatform = platform
	} else {
		tc.TargetPlatform = "linux"
	}

	if arch, ok := configDict["target_architecture"].(string); ok {
		tc.TargetArchitecture = arch
	} else {
		tc.TargetArchitecture = "x86_64"
	}

	if platform, ok := configDict["host_platform"].(string); ok {
		tc.HostPlatform = platform
	} else {
		tc.HostPlatform = "linux"
	}

	if arch, ok := configDict["host_architecture"].(string); ok {
		tc.HostArchitecture = arch
	} else {
		tc.HostArchitecture = "x86_64"
	}

	if execType, ok := configDict["execution_type"].(string); ok {
		tc.ExecutionType = execType
	} else {
		tc.ExecutionType = "native"
	}

	tc.ExecutionConfig = map[string]any{"type": tc.ExecutionType}

	// Parse tools from list format
	if toolsList, ok := configDict["tools"].([]any); ok {
		for _, toolDataRaw := range toolsList {
			toolData, ok := toolDataRaw.(map[string]any)
			if !ok {
				continue
			}

			toolName, ok := toolData["name"].(string)
			if !ok {
				continue
			}

			tool := &Tool{
				Name:            toolName,
				Action:          "compile",
				OutputPattern:   "{name}",
				Flags:           make(map[string][]string),
				Supports:        make(map[string]any),
				InputExtensions: []string{},
			}

			if action, ok := toolData["action"].(string); ok {
				tool.Action = action
			}
			if command, ok := toolData["command"].(string); ok {
				tool.Command = command
			}
			if outputExt, ok := toolData["output_extension"].(string); ok {
				tool.OutputExtension = outputExt
			}
			if outputPattern, ok := toolData["output_pattern"].(string); ok {
				tool.OutputPattern = outputPattern
			}

			// Parse input_extensions
			if inputExts, ok := toolData["input_extensions"]; ok {
				switch v := inputExts.(type) {
				case string:
					tool.InputExtensions = []string{v}
				case []any:
					for _, ext := range v {
						if extStr, ok := ext.(string); ok {
							tool.InputExtensions = append(tool.InputExtensions, extStr)
						}
					}
				}
			}

			// Parse flags
			if flagsData, ok := toolData["flags"].(map[string]any); ok {
				for flagType, flagsRaw := range flagsData {
					if flagsList, ok := flagsRaw.([]any); ok {
						flags := []string{}
						for _, flag := range flagsList {
							if flagStr, ok := flag.(string); ok {
								flags = append(flags, flagStr)
							}
						}
						tool.Flags[flagType] = flags
					}
				}
			}

			// Parse supports
			if supportsData, ok := toolData["supports"].(map[string]any); ok {
				tool.Supports = supportsData
			}

			// Handle output_type for link tools
			if outputType, ok := toolData["output_type"].(string); ok {
				tool.Supports["output_type"] = outputType
			}

			tc.Tools[toolName] = tool
		}
	}

	return tc, nil
}

// parseToolFromData is a helper to parse a single tool from YAML data
func parseToolFromData(toolName string, toolData map[string]any) *Tool {
	tool := &Tool{
		Name:            toolName,
		Action:          "compile",
		OutputPattern:   "{name}",
		Flags:           make(map[string][]string),
		Supports:        make(map[string]any),
		InputExtensions: []string{},
	}

	if action, ok := toolData["action"].(string); ok {
		tool.Action = action
	}
	if command, ok := toolData["command"].(string); ok {
		tool.Command = command
	}
	if outputExt, ok := toolData["output_extension"].(string); ok {
		tool.OutputExtension = outputExt
	}
	if outputPattern, ok := toolData["output_pattern"].(string); ok {
		tool.OutputPattern = outputPattern
	}

	// Parse input_extensions
	if inputExts, ok := toolData["input_extensions"]; ok {
		switch v := inputExts.(type) {
		case string:
			tool.InputExtensions = []string{v}
		case []any:
			for _, ext := range v {
				if extStr, ok := ext.(string); ok {
					tool.InputExtensions = append(tool.InputExtensions, extStr)
				}
			}
		}
	}

	// Parse flags
	if flagsData, ok := toolData["flags"].(map[string]any); ok {
		for flagType, flagsRaw := range flagsData {
			if flagsList, ok := flagsRaw.([]any); ok {
				flags := []string{}
				for _, flag := range flagsList {
					if flagStr, ok := flag.(string); ok {
						flags = append(flags, flagStr)
					}
				}
				tool.Flags[flagType] = flags
			}
		}
	}

	// Parse supports
	if supportsData, ok := toolData["supports"].(map[string]any); ok {
		tool.Supports = supportsData
	}

	return tool
}
