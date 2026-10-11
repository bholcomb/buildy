package resource

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"buildy/internal/config"
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

// BuildCommand builds a compile command from the tool's template. If the tool writes a
// dependency file, it also returns the file's path and format.
func (cb *CommandBuilder) BuildCommand(
	tool *Tool,
	source, output string,
	defines, includeDirs, extraFlags []string,
	kwargs map[string]any,
) (command util.Command, depFile, depFormat string, err error) {
	values := map[string][]string{
		"source":    {source},
		"input":     {source},
		"output":    {output},
		"flags":     cb.flags(tool, extraFlags),
		"defines":   nil,
		"includes":  nil,
		"pic":       nil,
		"dep_flags": nil,
	}
	if tool.Supports.Defines {
		values["defines"] = flagged(orDefault(tool.Supports.DefineFlag, "-D"), defines)
	}
	if tool.Supports.Includes {
		values["includes"] = flagged(orDefault(tool.Supports.IncludeFlag, "-I"), includeDirs)
	}
	if isShared, _ := kwargs["is_shared_library"].(bool); tool.Supports.PIC && isShared {
		values["pic"] = []string{orDefault(tool.Supports.PICFlag, "-fPIC")}
	}

	// The dependency file: .json for MSVC's /sourceDependencies, .d for GCC and Clang
	if deps := tool.Supports.Dependencies; !deps.IsZero() {
		if strings.Contains(strings.ToLower(deps.Text), "/sourcedependencies") {
			depFile = strings.TrimSuffix(output, tool.OutputExtension) + ".json"
			depFormat = DepFormatMSVCJSON
		} else {
			depFile = strings.TrimSuffix(output, tool.OutputExtension) + ".d"
			depFormat = DepFormatMake
		}
		values["dep_file"] = []string{depFile}
		if values["dep_flags"], err = deps.Expand(config.MapValues(values)); err != nil {
			return util.Command{}, "", "", fmt.Errorf("tool %s: supports.dependencies: %w", tool.Name, err)
		}
	}

	// The template step's other tool parameters (std, ...)
	for key, value := range kwargs {
		if _, exists := values[key]; !exists {
			values[key] = kwargValues(value)
		}
	}

	command, err = cb.expand(tool, values)
	if err != nil {
		return util.Command{}, "", "", fmt.Errorf("compiling %s: %w", source, err)
	}
	return command, depFile, depFormat, nil
}

// flags returns the tool's flags for the configuration, then extra.
func (cb *CommandBuilder) flags(tool *Tool, extra []string) []string {
	flags := append([]string{}, tool.Flags["common"]...)
	flags = append(flags, tool.Flags[cb.configType]...)
	return append(flags, extra...)
}

// expand fills the tool's command for the build's platform in.
func (cb *CommandBuilder) expand(tool *Tool, values map[string][]string) (util.Command, error) {
	command, err := tool.Expand(cb.platform, cb.toolchain.Execution, config.MapValues(values))
	var missing *config.MissingPlaceholderError
	if errors.As(err, &missing) && missing.Name == "std" {
		err = fmt.Errorf("%w (set cpp_standard or c_standard under environment.compile)", err)
	}
	return command, err
}

// flagged returns flag joined to each value: "-I" and "dir" give "-Idir". A flag that ends in a
// space ("-framework ") is an argument of its own before each value.
func flagged(flag string, values []string) []string {
	var args []string
	for _, v := range values {
		if trimmed := strings.TrimRight(flag, " "); trimmed != flag {
			args = append(args, trimmed, v)
		} else {
			args = append(args, flag+v)
		}
	}
	return args
}

// kwargValues returns a tool parameter's value as arguments: a list's items, or one argument.
func kwargValues(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case string:
		return []string{v}
	}
	return []string{fmt.Sprint(value)}
}

// orDefault returns a flag's spelling: the tool's, or def if the tool doesn't set it.
func orDefault(flag *string, def string) string {
	if flag == nil {
		return def
	}
	return *flag
}

// BuildLinkCommand builds a link command from the tool's template. It also returns the import
// library a Windows DLL's link writes, if any.
func (cb *CommandBuilder) BuildLinkCommand(
	tool *Tool,
	objects []string,
	output string,
	libDirs, libs, frameworks, extraFlags []string,
) (util.Command, string, error) {
	values := map[string][]string{
		"objects":    objects,
		"output":     {output},
		"flags":      cb.flags(tool, extraFlags),
		"lib_dirs":   nil,
		"libs":       nil,
		"frameworks": nil,
	}
	if tool.Supports.LibDirs {
		values["lib_dirs"] = flagged(orDefault(tool.Supports.LibDirFlag, "-L"), libDirs)
	}
	if tool.Supports.Libs {
		libFlag := orDefault(tool.Supports.LibFlag, "-l")
		for _, lib := range libs {
			// A library with an extension is used as named
			if tool.Supports.LibSuffix != "" && !strings.Contains(lib, ".") {
				lib += tool.Supports.LibSuffix
			}
			values["libs"] = append(values["libs"], flagged(libFlag, []string{lib})...)
		}
	}
	if tool.Supports.Frameworks {
		values["frameworks"] = flagged(orDefault(tool.Supports.FrameworkFlag, "-framework "), frameworks)
	}

	// The import library of a DLL (MSVC) goes in lib/ when the DLL goes in bin/
	implib := ""
	if tool.Supports.Implib {
		if strings.HasSuffix(output, ".dll") {
			outputDir := filepath.Dir(output)
			if filepath.Base(outputDir) == "bin" {
				implib = filepath.Join(filepath.Dir(outputDir), "lib", strings.TrimSuffix(filepath.Base(output), ".dll")+".lib")
			} else {
				implib = strings.TrimSuffix(output, ".dll") + ".lib"
			}
		}
		values["implib"] = []string{implib}
	}

	command, err := cb.expand(tool, values)
	if err != nil {
		return util.Command{}, "", fmt.Errorf("linking %s: %w", output, err)
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
