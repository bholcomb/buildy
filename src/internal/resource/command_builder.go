package resource

import (
	"fmt"
	"path/filepath"
	"strings"

	"buildy/pkg/util"
)

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
		templateVars["dep_flags"] = strings.ReplaceAll(depTemplate, "${dep_file}", depFile)
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
		placeholder := "${" + key + "}"
		command = strings.ReplaceAll(command, placeholder, value)
	}

	command = strings.TrimSpace(command)

	return command, depFile, nil
}

// BuildLinkCommand builds a link command using the tool template
// Returns the command string and an optional import library path (for Windows DLLs)
func (cb *CommandBuilder) BuildLinkCommand(
	tool *Tool,
	objects []string,
	output string,
	libDirs, libs, frameworks []string,
) (string, string, error) {
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
		libSuffix := ""
		if ls, ok := tool.Supports["lib_suffix"].(string); ok {
			libSuffix = ls
		}

		libStrs := []string{}
		for _, lib := range libs {
			// Skip adding suffix if library already has an extension
			if libSuffix != "" && !strings.Contains(lib, ".") {
				libStrs = append(libStrs, libFlag+lib+libSuffix)
			} else {
				libStrs = append(libStrs, libFlag+lib)
			}
		}
		templateVars["libs"] = strings.Join(libStrs, " ")
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

	// Add import library path for DLLs (MSVC)
	// The DLL goes in bin/, but the import library should go in lib/
	implib := ""
	if supportsImplib, ok := tool.Supports["implib"].(bool); ok && supportsImplib {
		if strings.HasSuffix(output, ".dll") {
			// Replace bin with lib in the path, handling both path separators
			// Use filepath.Dir/Base to properly handle the path components
			outputDir := filepath.Dir(output)
			outputBase := filepath.Base(output)
			parentDir := filepath.Dir(outputDir)
			dirName := filepath.Base(outputDir)

			if dirName == "bin" {
				// Replace bin with lib
				implib = filepath.Join(parentDir, "lib", strings.TrimSuffix(outputBase, ".dll")+".lib")
			} else {
				// Just change extension if not in bin/
				implib = strings.TrimSuffix(output, ".dll") + ".lib"
			}
		}
		templateVars["implib"] = implib
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
		placeholder := "${" + key + "}"
		command = strings.ReplaceAll(command, placeholder, value)
	}

	command = strings.TrimSpace(command)

	return command, implib, nil
}

// CreateExecutionEnvironmentFromToolchain creates an execution environment from toolchain config
func CreateExecutionEnvironmentFromToolchain(toolchain *ToolchainConfig) *util.ExecutionEnvironment {
	execType := toolchain.ExecutionType

	switch execType {
	case "native":
		return util.NewNativeExecution()
	case "docker":
		return util.NewDockerExecution(toolchain.ExecutionConfig)
	default:
		util.LogWarning("Unknown execution type '%s', falling back to native", execType)
		return util.NewNativeExecution()
	}
}
