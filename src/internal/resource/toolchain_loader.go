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

	// Parse variables
	if varsData, ok := tcData["variables"].(map[string]any); ok {
		for key, val := range varsData {
			if strVal, ok := val.(string); ok {
				tc.Variables[key] = strVal
			}
		}
	}

	// Parse flag_mappings
	tc.FlagMappings = parseFlagMappings(tcData)

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

	// Parse variables
	if varsData, ok := tcData["variables"].(map[string]any); ok {
		for key, val := range varsData {
			if strVal, ok := val.(string); ok {
				tc.Variables[key] = strVal
			}
		}
	}

	// Parse flag_mappings
	tc.FlagMappings = parseFlagMappings(tcData)

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
				OutputPattern:   "${name}",
				Flags:           make(map[string][]string),
				Supports:        make(map[string]any),
				InputExtensions: []string{},
			}

			if action, ok := toolData["action"].(string); ok {
				tool.Action = action
			}
			// Handle command as either a string or a map of platform-specific commands
			if command, ok := toolData["command"].(string); ok {
				tool.Command = command
			} else if cmdMap, ok := toolData["command"].(map[string]any); ok {
				tool.CommandsByPlatform = make(map[string]string)
				for platform, cmdRaw := range cmdMap {
					if cmdStr, ok := cmdRaw.(string); ok {
						tool.CommandsByPlatform[platform] = cmdStr
					}
				}
				// Set default Command to linux if available, else first available
				if cmd, ok := tool.CommandsByPlatform["linux"]; ok {
					tool.Command = cmd
				} else {
					for _, cmd := range tool.CommandsByPlatform {
						tool.Command = cmd
						break
					}
				}
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
		OutputPattern:   "${name}",
		Flags:           make(map[string][]string),
		Supports:        make(map[string]any),
		CommandParams:   make(map[string]CommandParam),
		ManifestFiles:   []string{},
		InputExtensions: []string{},
	}

	if action, ok := toolData["action"].(string); ok {
		tool.Action = action
	}
	// Handle command as either a string or a map of platform-specific commands
	if command, ok := toolData["command"].(string); ok {
		tool.Command = command
	} else if cmdMap, ok := toolData["command"].(map[string]any); ok {
		tool.CommandsByPlatform = make(map[string]string)
		for platform, cmdRaw := range cmdMap {
			if cmdStr, ok := cmdRaw.(string); ok {
				tool.CommandsByPlatform[platform] = cmdStr
			}
		}
		// Set default Command to linux if available, else first available
		if cmd, ok := tool.CommandsByPlatform["linux"]; ok {
			tool.Command = cmd
		} else {
			for _, cmd := range tool.CommandsByPlatform {
				tool.Command = cmd
				break
			}
		}
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

	// Parse command_params
	if paramsData, ok := toolData["command_params"].(map[string]any); ok {
		for paramName, paramDataRaw := range paramsData {
			paramData, ok := paramDataRaw.(map[string]any)
			if !ok {
				continue
			}
			param := parseCommandParam(paramData)
			tool.CommandParams[paramName] = param
		}
	}

	// Parse manifest_files
	if manifestData, ok := toolData["manifest_files"]; ok {
		switch v := manifestData.(type) {
		case string:
			tool.ManifestFiles = []string{v}
		case []any:
			for _, f := range v {
				if fStr, ok := f.(string); ok {
					tool.ManifestFiles = append(tool.ManifestFiles, fStr)
				}
			}
		}
	}

	return tool
}

// parseFlagMappings parses the flag_mappings section from toolchain YAML data.
// flag_mappings maps abstract keywords (e.g., "optimization") to value->flags mappings
// (e.g., "speed" -> ["-O2"]). This is fully data-driven: the Go code makes no
// assumptions about which keywords exist.
func parseFlagMappings(tcData map[string]any) map[string]map[string][]string {
	mappingsRaw, ok := tcData["flag_mappings"].(map[string]any)
	if !ok {
		return nil
	}

	result := make(map[string]map[string][]string, len(mappingsRaw))
	for keyword, valuesRaw := range mappingsRaw {
		valuesMap, ok := valuesRaw.(map[string]any)
		if !ok {
			continue
		}
		valueFlags := make(map[string][]string, len(valuesMap))
		for value, flagsRaw := range valuesMap {
			switch f := flagsRaw.(type) {
			case []any:
				flags := make([]string, 0, len(f))
				for _, flag := range f {
					if s, ok := flag.(string); ok {
						flags = append(flags, s)
					}
				}
				valueFlags[value] = flags
			case []string:
				valueFlags[value] = f
			}
		}
		result[keyword] = valueFlags
	}
	return result
}

// parseCommandParam parses a command parameter definition from YAML data
func parseCommandParam(data map[string]any) CommandParam {
	param := CommandParam{
		Optional: true, // Default to optional
		Join:     " ",  // Default join separator
	}

	// Parse sources
	if sources, ok := data["sources"].([]any); ok {
		for _, s := range sources {
			if str, ok := s.(string); ok {
				param.Sources = append(param.Sources, str)
			}
		}
	}

	// Parse format
	if format, ok := data["format"].(string); ok {
		param.Format = format
	}

	// Parse join
	if join, ok := data["join"].(string); ok {
		param.Join = join
	}

	// Parse quote_if_spaces
	if quoteIfSpaces, ok := data["quote_if_spaces"].(bool); ok {
		param.QuoteIfSpaces = quoteIfSpaces
	}

	// Parse resolve_variables
	if resolveVars, ok := data["resolve_variables"].(bool); ok {
		param.ResolveVariables = resolveVars
	}

	// Parse match
	if match, ok := data["match"].(string); ok {
		param.Match = match
	}

	// Parse optional (default is true)
	if optional, ok := data["optional"].(bool); ok {
		param.Optional = optional
	}

	return param
}
