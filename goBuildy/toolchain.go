package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tool represents an individual tool within a toolchain
type Tool struct {
	Name            string              `yaml:"name"`
	Action          string              `yaml:"action"`
	Command         string              `yaml:"command"`
	InputExtensions []string            `yaml:"input_extensions"`
	OutputExtension string              `yaml:"output_extension"`
	OutputPattern   string              `yaml:"output_pattern"`
	Flags           map[string][]string `yaml:"flags"`
	Supports        map[string]any      `yaml:"supports"`
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
	Name               string           `yaml:"name"`
	Description        string           `yaml:"description"`
	TargetPlatform     string           `yaml:"-"`
	TargetArchitecture string           `yaml:"-"`
	HostPlatform       string           `yaml:"-"`
	HostArchitecture   string           `yaml:"-"`
	ExecutionType      string           `yaml:"-"`
	ExecutionConfig    map[string]any   `yaml:"-"`
	Tools              map[string]*Tool `yaml:"-"`
}

// NewToolchainConfig creates a new ToolchainConfig
func NewToolchainConfig(name, description string) *ToolchainConfig {
	return &ToolchainConfig{
		Name:            name,
		Description:     description,
		ExecutionConfig: make(map[string]any),
		Tools:           make(map[string]*Tool),
	}
}

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

			tool := &Tool{
				Name:            toolName,
				Action:          "compile",
				OutputPattern:   "{name}",
				Flags:           make(map[string][]string),
				Supports:        make(map[string]any),
				InputExtensions: []string{},
			}

			// Parse tool fields
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

			// Parse input_extensions (can be string or list)
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

// ToolMatcher matches files to appropriate tools based on action and extension
type ToolMatcher struct {
	toolchain *ToolchainConfig
	toolMap   map[string]*Tool // key: "action:extension"
}

// NewToolMatcher creates a new ToolMatcher
func NewToolMatcher(toolchain *ToolchainConfig) (*ToolMatcher, error) {
	tm := &ToolMatcher{
		toolchain: toolchain,
		toolMap:   make(map[string]*Tool),
	}

	if err := tm.buildToolMap(); err != nil {
		return nil, err
	}

	return tm, nil
}

// buildToolMap builds the action+extension lookup map
func (tm *ToolMatcher) buildToolMap() error {
	for _, tool := range tm.toolchain.Tools {
		// Only build map for non-link actions
		// Link tools are handled separately by FindLinkTool()
		if tool.Action != "link" {
			for _, ext := range tool.InputExtensions {
				key := tool.Action + ":" + ext
				if existing, exists := tm.toolMap[key]; exists {
					return fmt.Errorf(
						"ambiguous tool definition in toolchain '%s': "+
							"both '%s' and '%s' handle action='%s' with extension='%s'",
						tm.toolchain.Name, existing.Name, tool.Name, tool.Action, ext,
					)
				}
				tm.toolMap[key] = tool
			}
		}
	}
	return nil
}

// FindTool finds a tool that matches the action and file extension
func (tm *ToolMatcher) FindTool(action, filePath string) *Tool {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return nil
	}

	key := action + ":" + ext
	tool := tm.toolMap[key]

	if tool != nil {
		log.Printf("Matched %s (%s) -> tool '%s'", filePath, action, tool.Name)
	} else {
		log.Printf("No tool found for action='%s' extension='%s'", action, ext)
	}

	return tool
}

// FindLinkTool finds a tool for linking based on output type
func (tm *ToolMatcher) FindLinkTool(outputType string) *Tool {
	// Look for tool with action='link' and matching output type
	for _, tool := range tm.toolchain.Tools {
		if tool.Action == "link" {
			// Check if tool name or supports indicates it handles this output type
			if strings.Contains(tool.Name, outputType) {
				log.Printf("Matched link tool for '%s' -> '%s'", outputType, tool.Name)
				return tool
			}
			if supportedType, ok := tool.Supports["output_type"].(string); ok && supportedType == outputType {
				log.Printf("Matched link tool for '%s' -> '%s'", outputType, tool.Name)
				return tool
			}
		}
	}

	log.Printf("WARNING: No link tool found for output_type='%s'", outputType)
	return nil
}

// ToolchainManager manages toolchain selection and loading
type ToolchainManager struct {
	toolchainsDirs []string
	toolchains     map[string]*ToolchainConfig
}

// NewToolchainManager creates a new ToolchainManager from a single directory
func NewToolchainManager(toolchainsDir string) (*ToolchainManager, error) {
	return NewToolchainManagerMulti([]string{toolchainsDir})
}

// NewToolchainManagerMulti creates a new ToolchainManager from multiple directories
func NewToolchainManagerMulti(toolchainsDirs []string) (*ToolchainManager, error) {
	tm := &ToolchainManager{
		toolchainsDirs: toolchainsDirs,
		toolchains:     make(map[string]*ToolchainConfig),
	}

	if err := tm.loadToolchains(); err != nil {
		return nil, err
	}

	return tm, nil
}

// loadToolchains loads all toolchain configurations from all toolchain directories
func (tm *ToolchainManager) loadToolchains() error {
	// Load toolchains from each directory in order
	for _, toolchainsDir := range tm.toolchainsDirs {
		// Check if directory exists
		if _, err := os.Stat(toolchainsDir); os.IsNotExist(err) {
			log.Printf("WARNING: Toolchains directory not found: %s", toolchainsDir)
			continue // Not fatal, try next directory
		}

		// Read all .yaml files
		entries, err := os.ReadDir(toolchainsDir)
		if err != nil {
			log.Printf("WARNING: Failed to read toolchains directory %s: %v", toolchainsDir, err)
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			if filepath.Ext(entry.Name()) == ".yaml" {
				tcFile := filepath.Join(toolchainsDir, entry.Name())
				tc, err := LoadToolchainConfig(tcFile)
				if err != nil {
					log.Printf("ERROR: Failed to load toolchain %s: %v", tcFile, err)
					continue
				}

				if _, exists := tm.toolchains[tc.Name]; exists {
					log.Printf("WARNING: Toolchain '%s' from %s overrides existing toolchain", tc.Name, toolchainsDir)
				}

				tm.toolchains[tc.Name] = tc
				log.Printf("Loaded toolchain: %s - %s", tc.Name, tc.Description)
			}
		}
	}

	return nil
}

// GetToolchain gets a toolchain by name
func (tm *ToolchainManager) GetToolchain(name string) *ToolchainConfig {
	return tm.toolchains[name]
}

// AutoDetect auto-detects the best toolchain for the given platform/architecture
func (tm *ToolchainManager) AutoDetect(platform, architecture string) *ToolchainConfig {
	// Collect matching toolchains
	var matches []*ToolchainConfig
	for _, tc := range tm.toolchains {
		if tc.TargetPlatform == platform &&
			tc.TargetArchitecture == architecture &&
			tc.ExecutionType == "native" {
			matches = append(matches, tc)
		}
	}

	if len(matches) == 0 {
		log.Printf("WARNING: No native toolchain found for %s-%s", platform, architecture)
		return nil
	}

	// Define platform-specific preferred toolchains
	preferredToolchains := map[string][]string{
		"linux":   {"gcc-linux", "clang-linux"},
		"windows": {"msvc-windows", "gcc-mingw", "clang-windows"},
		"macos":   {"clang-macos", "gcc-macos"},
		"darwin":  {"clang-macos", "gcc-macos"}, // darwin is macOS
	}

	// Try to find preferred toolchain for this platform
	if preferences, ok := preferredToolchains[platform]; ok {
		for _, preferredName := range preferences {
			for _, tc := range matches {
				if tc.Name == preferredName {
					log.Printf("Auto-detected toolchain: %s", tc.Name)
					return tc
				}
			}
		}
	}

	// Fallback: sort matches by name for deterministic selection
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Name < matches[j].Name
	})

	log.Printf("Auto-detected toolchain: %s (fallback)", matches[0].Name)
	return matches[0]
}

// ListToolchains lists all available toolchains with descriptions
func (tm *ToolchainManager) ListToolchains() []struct {
	Name        string
	Description string
} {
	result := []struct {
		Name        string
		Description string
	}{}

	for _, tc := range tm.toolchains {
		result = append(result, struct {
			Name        string
			Description string
		}{
			Name:        tc.Name,
			Description: tc.Description,
		})
	}

	return result
}

// CommandBuilder builds commands using tool-based approach
type CommandBuilder struct {
	toolchain   *ToolchainConfig
	toolMatcher *ToolMatcher
	configType  string
}

// NewCommandBuilder creates a new CommandBuilder
func NewCommandBuilder(toolchain *ToolchainConfig, toolMatcher *ToolMatcher, configType string) *CommandBuilder {
	return &CommandBuilder{
		toolchain:   toolchain,
		toolMatcher: toolMatcher,
		configType:  configType,
	}
}

// BuildCommand builds a command using the tool template
func (cb *CommandBuilder) BuildCommand(
	tool *Tool,
	source, output string,
	defines, includeDirs, extraFlags []string,
	kwargs map[string]any,
) (string, string, error) {
	if defines == nil {
		defines = []string{}
	}
	if includeDirs == nil {
		includeDirs = []string{}
	}
	if extraFlags == nil {
		extraFlags = []string{}
	}
	if kwargs == nil {
		kwargs = make(map[string]any)
	}

	// Get flags for current configuration
	commonFlags := tool.Flags["common"]
	configFlags := tool.Flags[cb.configType]
	allFlags := append([]string{}, commonFlags...)
	allFlags = append(allFlags, configFlags...)
	allFlags = append(allFlags, extraFlags...)

	// Build template variables
	templateVars := map[string]string{
		"source": source,
		"input":  source,
		"output": output,
		"flags":  strings.Join(allFlags, " "),
	}

	// Add defines if tool supports them
	if supportsDefines, ok := tool.Supports["defines"].(bool); ok && supportsDefines && len(defines) > 0 {
		defineFlag := "-D"
		if df, ok := tool.Supports["define_flag"].(string); ok {
			defineFlag = df
		}
		defineStrs := []string{}
		for _, d := range defines {
			defineStrs = append(defineStrs, defineFlag+d)
		}
		templateVars["defines"] = strings.Join(defineStrs, " ")
	} else {
		templateVars["defines"] = ""
	}

	// Add includes if tool supports them
	if supportsIncludes, ok := tool.Supports["includes"].(bool); ok && supportsIncludes && len(includeDirs) > 0 {
		includeFlag := "-I"
		if inf, ok := tool.Supports["include_flag"].(string); ok {
			includeFlag = inf
		}
		includeStrs := []string{}
		for _, inc := range includeDirs {
			includeStrs = append(includeStrs, includeFlag+inc)
		}
		templateVars["includes"] = strings.Join(includeStrs, " ")
	} else {
		templateVars["includes"] = ""
	}

	// Add PIC flag if tool supports it and requested
	if supportsPIC, ok := tool.Supports["pic"].(bool); ok && supportsPIC {
		if isShared, ok := kwargs["is_shared_library"].(bool); ok && isShared {
			picFlag := "-fPIC"
			if pf, ok := tool.Supports["pic_flag"].(string); ok {
				picFlag = pf
			}
			templateVars["pic"] = picFlag
		} else {
			templateVars["pic"] = ""
		}
	} else {
		templateVars["pic"] = ""
	}

	// Handle dependency generation if tool supports it
	depFile := ""
	if depTemplate, ok := tool.Supports["dependencies"].(string); ok && depTemplate != "" {
		// Use .json extension for MSVC /sourceDependencies, .d for GCC/Clang
		if strings.Contains(depTemplate, "/sourceDependencies") ||
			strings.Contains(strings.ToLower(depTemplate), "/sourcedependencies") {
			depFile = strings.TrimSuffix(output, tool.OutputExtension) + ".json"
		} else {
			depFile = strings.TrimSuffix(output, tool.OutputExtension) + ".d"
		}

		templateVars["dep_file"] = depFile
		templateVars["dep_flags"] = strings.ReplaceAll(depTemplate, "{dep_file}", depFile)
	} else {
		templateVars["dep_flags"] = ""
	}

	// Add any additional kwargs
	for key, value := range kwargs {
		if _, exists := templateVars[key]; !exists {
			templateVars[key] = fmt.Sprintf("%v", value)
		}
	}

	// Build command from template
	command := tool.Command
	for key, value := range templateVars {
		placeholder := "{" + key + "}"
		command = strings.ReplaceAll(command, placeholder, value)
	}

	command = strings.TrimSpace(command)

	return command, depFile, nil
}

// BuildLinkCommand builds a link command using the tool template
func (cb *CommandBuilder) BuildLinkCommand(
	tool *Tool,
	objects []string,
	output string,
	libDirs, libs, frameworks []string,
) (string, error) {
	if libDirs == nil {
		libDirs = []string{}
	}
	if libs == nil {
		libs = []string{}
	}
	if frameworks == nil {
		frameworks = []string{}
	}

	// Build template variables
	templateVars := map[string]string{
		"objects": strings.Join(objects, " "),
		"output":  output,
	}

	// Add library directories if tool supports them
	if supportsLibDirs, ok := tool.Supports["lib_dirs"].(bool); ok && supportsLibDirs && len(libDirs) > 0 {
		libDirFlag := "-L"
		if ldf, ok := tool.Supports["lib_dir_flag"].(string); ok {
			libDirFlag = ldf
		}
		libDirStrs := []string{}
		for _, d := range libDirs {
			libDirStrs = append(libDirStrs, libDirFlag+d)
		}
		templateVars["lib_dirs"] = strings.Join(libDirStrs, " ")
	} else {
		templateVars["lib_dirs"] = ""
	}

	// Add libraries if tool supports them
	if supportsLibs, ok := tool.Supports["libs"].(bool); ok && supportsLibs && len(libs) > 0 {
		libFlag := "-l"
		if lf, ok := tool.Supports["lib_flag"].(string); ok {
			libFlag = lf
		}

		if libFlag != "" {
			libStrs := []string{}
			for _, lib := range libs {
				libStrs = append(libStrs, libFlag+lib)
			}
			templateVars["libs"] = strings.Join(libStrs, " ")
		} else {
			// No prefix (e.g., MSVC style)
			templateVars["libs"] = strings.Join(libs, " ")
		}
	} else {
		templateVars["libs"] = ""
	}

	// Add frameworks if tool supports them (macOS only)
	if supportsFrameworks, ok := tool.Supports["frameworks"].(bool); ok && supportsFrameworks && len(frameworks) > 0 {
		frameworkFlag := "-framework "
		if ff, ok := tool.Supports["framework_flag"].(string); ok {
			frameworkFlag = ff
		}
		frameworkStrs := []string{}
		for _, fw := range frameworks {
			frameworkStrs = append(frameworkStrs, frameworkFlag+fw)
		}
		templateVars["frameworks"] = strings.Join(frameworkStrs, " ")
	} else {
		templateVars["frameworks"] = ""
	}

	// Get flags for current configuration
	commonFlags := tool.Flags["common"]
	configFlags := tool.Flags[cb.configType]
	allFlags := append([]string{}, commonFlags...)
	allFlags = append(allFlags, configFlags...)
	templateVars["flags"] = strings.Join(allFlags, " ")

	// Build command
	command := tool.Command
	for key, value := range templateVars {
		placeholder := "{" + key + "}"
		command = strings.ReplaceAll(command, placeholder, value)
	}

	command = strings.TrimSpace(command)

	return command, nil
}
