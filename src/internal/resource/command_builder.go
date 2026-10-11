package resource

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"buildy/pkg/util"
)

// CommandBuilder builds commands using tool-based approach
type CommandBuilder struct {
	toolchain   *ToolchainConfig
	toolMatcher *ToolMatcher
	platform    string
	configType  string
}

// NewCommandBuilder creates a new CommandBuilder
func NewCommandBuilder(toolchain *ToolchainConfig, toolMatcher *ToolMatcher, platform, configType string) *CommandBuilder {
	return &CommandBuilder{
		toolchain:   toolchain,
		toolMatcher: toolMatcher,
		platform:    platform,
		configType:  configType,
	}
}

// Dependency file formats a compile tool can write (see supports.dependencies).
const (
	DepFormatMake     = "make"      // gcc/clang -MD/-MMD: a Makefile rule
	DepFormatMSVCJSON = "msvc-json" // cl.exe /sourceDependencies
)

// BuildCommand builds a command using the tool template. If the tool writes a dependency file,
// it also returns the file's path and format.
func (cb *CommandBuilder) BuildCommand(
	tool *Tool,
	source, output string,
	defines, includeDirs, extraFlags []string,
	kwargs map[string]any,
) (command, depFile, depFormat string, err error) {
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
	if tool.Supports.Defines && len(defines) > 0 {
		defineFlag := orDefault(tool.Supports.DefineFlag, "-D")
		defineStrs := []string{}
		for _, d := range defines {
			defineStrs = append(defineStrs, defineFlag+d)
		}
		templateVars["defines"] = strings.Join(defineStrs, " ")
	} else {
		templateVars["defines"] = ""
	}

	// Add includes if tool supports them
	if tool.Supports.Includes && len(includeDirs) > 0 {
		includeFlag := orDefault(tool.Supports.IncludeFlag, "-I")
		includeStrs := []string{}
		for _, inc := range includeDirs {
			includeStrs = append(includeStrs, includeFlag+inc)
		}
		templateVars["includes"] = strings.Join(includeStrs, " ")
	} else {
		templateVars["includes"] = ""
	}

	// Add PIC flag if tool supports it and requested
	if tool.Supports.PIC {
		if isShared, ok := kwargs["is_shared_library"].(bool); ok && isShared {
			templateVars["pic"] = orDefault(tool.Supports.PICFlag, "-fPIC")
		} else {
			templateVars["pic"] = ""
		}
	} else {
		templateVars["pic"] = ""
	}

	// Handle dependency generation if tool supports it
	if depTemplate := tool.Supports.Dependencies; depTemplate != "" {
		// Use .json extension for MSVC /sourceDependencies, .d for GCC/Clang
		if strings.Contains(strings.ToLower(depTemplate), "/sourcedependencies") {
			depFile = strings.TrimSuffix(output, tool.OutputExtension) + ".json"
			depFormat = DepFormatMSVCJSON
		} else {
			depFile = strings.TrimSuffix(output, tool.OutputExtension) + ".d"
			depFormat = DepFormatMake
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

	command, err = cb.fill(tool, templateVars)
	if err != nil {
		return "", "", "", fmt.Errorf("compiling %s: %w", source, err)
	}

	return command, depFile, depFormat, nil
}

// fill returns the tool's command for the build's platform with its placeholders filled, in
// the order of their names, so the result never depends on map order.
func (cb *CommandBuilder) fill(tool *Tool, vars map[string]string) (string, error) {
	command, err := tool.Command.For(cb.platform)
	if err != nil {
		return "", fmt.Errorf("tool %s %w", tool.Name, err)
	}
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		command = strings.ReplaceAll(command, "${"+name+"}", vars[name])
	}
	command = strings.TrimSpace(command)
	if err := checkPlaceholders(tool, command); err != nil {
		return "", err
	}
	return command, nil
}

func orDefault(value, def string) string {
	if value == "" {
		return def
	}
	return value
}

// checkPlaceholders fails if a ${...} placeholder of the tool's command was given no value.
func checkPlaceholders(tool *Tool, command string) error {
	start := strings.Index(command, "${")
	if start < 0 {
		return nil
	}
	end := strings.Index(command[start:], "}")
	name := command[start:]
	if end >= 0 {
		name = command[start : start+end+1]
	}
	hint := ""
	if name == "${std}" {
		hint = " (set cpp_standard or c_standard under environment.compile)"
	}
	return fmt.Errorf("the command of tool %s has no value for %s%s: %s", tool.Name, name, hint, command)
}

// BuildLinkCommand builds a link command using the tool template
// Returns the command string and an optional import library path (for Windows DLLs)
func (cb *CommandBuilder) BuildLinkCommand(
	tool *Tool,
	objects []string,
	output string,
	libDirs, libs, frameworks, extraFlags []string,
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
	if extraFlags == nil {
		extraFlags = []string{}
	}

	// Build template variables
	templateVars := map[string]string{
		"objects": strings.Join(objects, " "),
		"output":  output,
	}

	// Add library directories if tool supports them
	if tool.Supports.LibDirs && len(libDirs) > 0 {
		libDirFlag := orDefault(tool.Supports.LibDirFlag, "-L")
		libDirStrs := []string{}
		for _, d := range libDirs {
			libDirStrs = append(libDirStrs, libDirFlag+d)
		}
		templateVars["lib_dirs"] = strings.Join(libDirStrs, " ")
	} else {
		templateVars["lib_dirs"] = ""
	}

	// Add libraries if tool supports them
	if tool.Supports.Libs && len(libs) > 0 {
		libFlag := orDefault(tool.Supports.LibFlag, "-l")
		libSuffix := tool.Supports.LibSuffix

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
	if tool.Supports.Frameworks && len(frameworks) > 0 {
		frameworkFlag := orDefault(tool.Supports.FrameworkFlag, "-framework ")
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
	if tool.Supports.Implib {
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
	allFlags = append(allFlags, extraFlags...)
	templateVars["flags"] = strings.Join(allFlags, " ")

	command, err := cb.fill(tool, templateVars)
	if err != nil {
		return "", "", fmt.Errorf("linking %s: %w", output, err)
	}

	return command, implib, nil
}

// ExecSpecFromToolchain returns where the toolchain's commands run, from its execution section
// (validated when the toolchain was loaded).
func ExecSpecFromToolchain(toolchain *ToolchainConfig) (util.ExecSpec, error) {
	if err := toolchain.Execution.Validate(); err != nil {
		return util.ExecSpec{}, fmt.Errorf("toolchain %s: %w", toolchain.Name, err)
	}
	return toolchain.Execution, nil
}
