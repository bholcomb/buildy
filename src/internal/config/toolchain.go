package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"buildy/pkg/util"

	"gopkg.in/yaml.v3"
)

// Toolchain is a toolchain file: how to compile, link, transform or build for a target.
type Toolchain struct {
	Pos          Pos                        `yaml:"-" json:"-"`
	Name         string                     `yaml:"name,required" json:"name"`
	Description  string                     `yaml:"description" json:"description"`
	Language     string                     `yaml:"language" json:"language"`
	Target       PlatformArch               `yaml:"target" json:"target"`
	Host         PlatformArch               `yaml:"host" json:"host"`
	Variables    Ordered[string]            `yaml:"variables" json:"variables"`
	Execution    util.ExecSpec              `yaml:"execution" json:"execution"`
	OutputPrefix string                     `yaml:"output_prefix" json:"output_prefix"`
	FlagMappings Ordered[Ordered[[]string]] `yaml:"flag_mappings" json:"flag_mappings"` // keyword -> value -> flags
	Tools        Ordered[*Tool]             `yaml:"tools" json:"tools"`

	Source string `yaml:"-" json:"-"` // the file it was loaded from
}

// PlatformArch is a toolchain's target or host: a platform and an architecture, or "any".
type PlatformArch struct {
	Platform     string `yaml:"platform" json:"platform"`
	Architecture string `yaml:"architecture" json:"architecture"`
}

// Tool is one tool of a toolchain.
type Tool struct {
	Pos             Pos                    `yaml:"-" json:"-"`
	Name            string                 `yaml:"-" json:"name"`
	Action          string                 `yaml:"action,required" json:"action"`
	Command         Command                `yaml:"command,required" json:"command"`
	Shell           bool                   `yaml:"shell" json:"shell,omitempty"`                 // run the command with a shell (sh, cmd.exe on Windows hosts)
	ResponseFile    string                 `yaml:"response_file" json:"response_file,omitempty"` // how the tool reads arguments from an @file: gnu or msvc
	InputExtensions StringList             `yaml:"input_extensions" json:"input_extensions"`
	OutputExtension string                 `yaml:"output_extension" json:"output_extension"`
	OutputPattern   string                 `yaml:"output_pattern" json:"output_pattern"`
	Flags           map[string][]string    `yaml:"flags" json:"flags"` // "common" and configuration names
	Supports        Supports               `yaml:"supports" json:"supports"`
	CommandParams   Ordered[*CommandParam] `yaml:"command_params" json:"command_params"`
}

// Tool actions.
var toolActions = []string{"compile", "link", "transform", "convert", "generate", "build"}

// Response file syntaxes (util.ResponseFileGNU, util.ResponseFileMSVC).
var responseFiles = []string{util.ResponseFileGNU, util.ResponseFileMSVC}

// Link output types.
var outputTypes = []string{"executable", "shared_library", "static_library"}

// Command is a tool's command: one for every platform, or one per platform.
type Command struct {
	Default    CommandTemplate            `json:"default,omitempty"`
	ByPlatform map[string]CommandTemplate `json:"by_platform,omitempty"`
}

func (c *Command) decodeNode(d *decoder, n *yaml.Node, path string) {
	switch {
	case isNull(n):
	case n.Kind == yaml.ScalarNode:
		c.Default.decodeNode(d, n, path)
	case n.Kind == yaml.MappingNode:
		c.ByPlatform = map[string]CommandTemplate{}
		for _, e := range d.entries(n, path) {
			platform, err := CheckPlatform(e.key.Value)
			if err != nil {
				d.errorf(e.key, "'%s': %v", join(path, e.key.Value), err)
				continue
			}
			var t CommandTemplate
			t.decodeNode(d, resolveAlias(e.value), join(path, e.key.Value))
			if !t.IsZero() {
				c.ByPlatform[platform] = t
			}
		}
	default:
		d.errorf(n, "'%s' must be a command or a mapping of platform to command, not %s", path, kindName(n))
	}
}

// For returns the command for a platform.
func (c Command) For(platform string) (CommandTemplate, error) {
	if cmd, ok := c.ByPlatform[platform]; ok {
		return cmd, nil
	}
	if !c.Default.IsZero() {
		return c.Default, nil
	}
	return CommandTemplate{}, fmt.Errorf("has no command for platform %s (it has: %s)", platform, strings.Join(sortedNames(c.ByPlatform), ", "))
}

// all returns every command, the default first, then by platform name.
func (c Command) all() []CommandTemplate {
	var all []CommandTemplate
	if !c.Default.IsZero() {
		all = append(all, c.Default)
	}
	for _, name := range sortedNames(c.ByPlatform) {
		all = append(all, c.ByPlatform[name])
	}
	return all
}

// Expand returns the tool's command for platform with its placeholders filled in by values: an
// argument list, or for a shell tool a script whose values are quoted for the shell that runs
// it where exec says.
func (t *Tool) Expand(platform string, exec util.ExecSpec, values Values) (util.Command, error) {
	tmpl, err := t.Command.For(platform)
	if err != nil {
		return util.Command{}, fmt.Errorf("tool %s %w", t.Name, err)
	}
	if t.Shell {
		script, err := tmpl.ExpandShell(values, util.ShellQuoter(exec))
		if err != nil {
			return util.Command{}, fmt.Errorf("the command of tool %s: %w", t.Name, err)
		}
		return util.Command{Script: script}, nil
	}
	args, err := tmpl.Expand(values)
	if err != nil {
		return util.Command{}, fmt.Errorf("the command of tool %s: %w", t.Name, err)
	}
	return util.Command{Args: args, ResponseFile: t.ResponseFile}, nil
}

// Supports is what a tool supports and how it spells it. A *_flag that is not set has a
// default (-D, -I, -fPIC, -L, -l, "-framework "); one set to "" is no flag at all (MSVC's
// libraries are named as they are).
type Supports struct {
	Defines       bool            `yaml:"defines" json:"defines"`
	DefineFlag    *string         `yaml:"define_flag" json:"define_flag"`
	Includes      bool            `yaml:"includes" json:"includes"`
	IncludeFlag   *string         `yaml:"include_flag" json:"include_flag"`
	PIC           bool            `yaml:"pic" json:"pic"`
	PICFlag       *string         `yaml:"pic_flag" json:"pic_flag"`
	Std           bool            `yaml:"std" json:"std"`
	Dependencies  CommandTemplate `yaml:"dependencies" json:"dependencies"` // dependency file arguments, with ${dep_file}
	OutputType    string          `yaml:"output_type" json:"output_type"`   // link tools: what they produce
	LibDirs       bool            `yaml:"lib_dirs" json:"lib_dirs"`
	LibDirFlag    *string         `yaml:"lib_dir_flag" json:"lib_dir_flag"`
	Libs          bool            `yaml:"libs" json:"libs"`
	LibFlag       *string         `yaml:"lib_flag" json:"lib_flag"`
	LibSuffix     string          `yaml:"lib_suffix" json:"lib_suffix"`
	Frameworks    bool            `yaml:"frameworks" json:"frameworks"`
	FrameworkFlag *string         `yaml:"framework_flag" json:"framework_flag"`
	Implib        bool            `yaml:"implib" json:"implib"`

	// Documented for Go and Rust build tools. Their command_params do the work; buildy doesn't
	// read these.
	BuildTags     bool   `yaml:"build_tags" json:"build_tags"`
	BuildTagsFlag string `yaml:"build_tags_flag" json:"build_tags_flag"`
	Ldflags       bool   `yaml:"ldflags" json:"ldflags"`
	LdflagsFlag   string `yaml:"ldflags_flag" json:"ldflags_flag"`
	Features      bool   `yaml:"features" json:"features"`
	FeaturesFlag  string `yaml:"features_flag" json:"features_flag"`
	Buildmode     bool   `yaml:"buildmode" json:"buildmode"`
	BuildmodeFlag string `yaml:"buildmode_flag" json:"buildmode_flag"`
}

// CommandParam defines how a build tool's command placeholder is filled (Go and Rust).
type CommandParam struct {
	// Sources are the places to look for the value, in order: "tool_params.X", "item.X",
	// "tool.flags.${config}"
	Sources []string `yaml:"sources,required" json:"sources"`
	// Format is the arguments the parameter gives, a command template with ${value} for the
	// value: "-tags ${value}" is two arguments. Without a format the value is one argument.
	Format CommandTemplate `yaml:"format" json:"format"`
	// Join separates a list's values (default " "): the joined value is one argument
	Join string `yaml:"join" json:"join"`
	// ResolveVariables resolves ${...} in the value
	ResolveVariables bool `yaml:"resolve_variables" json:"resolve_variables"`
	// Match keeps only list values equal to it (e.g. "--release" from the flags)
	Match string `yaml:"match" json:"match"`
	// Optional is documented; an empty value is always allowed
	Optional bool `yaml:"optional" json:"optional"`
	// Default is the value when no source has one, or Match matches nothing
	Default string `yaml:"default" json:"default"`
}

// LoadToolchain reads a toolchain file. source names it in errors.
func LoadToolchain(data []byte, source string) (*Toolchain, error) {
	root, err := parse(data, source)
	if err != nil {
		return nil, err
	}
	d := newDecoder(source)
	var file struct {
		Toolchain *Toolchain `yaml:"toolchain,required"`
	}
	d.decode(root, reflect.ValueOf(&file).Elem(), "")
	if file.Toolchain != nil {
		file.Toolchain.validate(d)
	}
	if err := d.err("toolchain"); err != nil {
		return nil, err
	}
	file.Toolchain.Source = source
	return file.Toolchain, nil
}

func (tc *Toolchain) validate(d *decoder) {
	check := func(value, what string, normalize func(string) (string, bool), known []string) string {
		if value == "" || value == "any" {
			return value
		}
		v, ok := normalize(value)
		if !ok {
			d.errorAt(tc.Pos, "toolchain.%s '%s' is not one of: any, %s", what, value, strings.Join(known, ", "))
		}
		return v
	}
	tc.Target.Platform = check(tc.Target.Platform, "target.platform", NormalizePlatform, Platforms)
	tc.Target.Architecture = check(tc.Target.Architecture, "target.architecture", NormalizeArchitecture, Architectures)
	tc.Host.Platform = check(tc.Host.Platform, "host.platform", NormalizePlatform, Platforms)
	tc.Host.Architecture = check(tc.Host.Architecture, "host.architecture", NormalizeArchitecture, Architectures)
	if tc.Execution.Type == "" {
		tc.Execution.Type = "native"
	}
	if err := tc.Execution.Validate(); err != nil {
		d.errorAt(tc.Pos, "toolchain.execution: %v", err)
	}

	linkTools := map[string]string{} // output type -> tool
	buildTool := ""
	byExtension := map[string]string{} // action:extension -> tool
	for _, name := range tc.Tools.Keys {
		tool := tc.Tools.Values[name]
		where := "toolchain.tools." + name
		if tool == nil {
			d.errorf(nil, "'%s' is empty", where)
			continue
		}
		tool.Name = name
		if tool.OutputPattern == "" {
			tool.OutputPattern = "${name}"
		}
		if !contains(toolActions, tool.Action) {
			d.errorAt(tool.Pos, "%s.action '%s' is not one of: %s", where, tool.Action, strings.Join(toolActions, ", "))
		}
		switch tool.Action {
		case "link":
			if !contains(outputTypes, tool.Supports.OutputType) {
				d.errorAt(tool.Pos, "%s is a link tool: supports.output_type must be one of: %s", where, strings.Join(outputTypes, ", "))
			} else if other, ok := linkTools[tool.Supports.OutputType]; ok {
				d.errorAt(tool.Pos, "tools %s and %s both link a %s", other, name, tool.Supports.OutputType)
			} else {
				linkTools[tool.Supports.OutputType] = name
			}
		case "build":
			if buildTool != "" {
				d.errorAt(tool.Pos, "tools %s and %s are both build tools; a toolchain has one", buildTool, name)
			}
			buildTool = name
		default:
			for _, ext := range tool.InputExtensions {
				key := tool.Action + ":" + ext
				if other, ok := byExtension[key]; ok {
					d.errorAt(tool.Pos, "tools %s and %s both %s '%s' files", other, name, tool.Action, ext)
				}
				byExtension[key] = name
			}
		}
		for _, p := range tool.CommandParams.Keys {
			param := tool.CommandParams.Values[p]
			if param == nil {
				d.errorAt(tool.Pos, "'%s.command_params.%s' is empty", where, p)
				continue
			}
			for _, name := range param.Format.Placeholders() {
				if name != "value" {
					d.errorAt(tool.Pos, "'%s.command_params.%s.format' has ${%s}: a format has only ${value}", where, p, name)
				}
			}
		}
		for _, name := range tool.Supports.Dependencies.Placeholders() {
			if name != "dep_file" {
				d.errorAt(tool.Pos, "'%s.supports.dependencies' has ${%s}: it has only ${dep_file}", where, name)
			}
		}
		if tool.ResponseFile != "" && !contains(responseFiles, tool.ResponseFile) {
			d.errorAt(tool.Pos, "%s.response_file '%s' is not one of: %s", where, tool.ResponseFile, strings.Join(responseFiles, ", "))
		}
		if tool.Shell && tool.ResponseFile != "" {
			d.errorAt(tool.Pos, "%s runs with a shell (shell: true): it can't take a response file", where)
		}
		if tool.Shell {
			for _, cmd := range tool.Command.all() {
				if cmd.HasQuotedPlaceholder() {
					d.errorAt(tool.Pos, "%s runs its command with a shell (shell: true): write placeholders outside quotes, buildy quotes their values: %s", where, cmd.Text)
				}
			}
		}
	}
}

// Hash is a hash of the toolchain's whole content: a task's cache key includes its toolchain's,
// so changing a toolchain file reruns its tasks.
func (tc *Toolchain) Hash() string {
	data, err := json.Marshal(tc)
	if err != nil {
		panic(fmt.Sprintf("hashing toolchain %s: %v", tc.Name, err))
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

// Keywords returns the abstract keywords the toolchain maps to flags.
func (tc *Toolchain) Keywords() []string {
	return append([]string{}, tc.FlagMappings.Keys...)
}

// ToolsWithAction returns the tools with one of the actions, in document order.
func (tc *Toolchain) ToolsWithAction(actions ...string) []*Tool {
	var tools []*Tool
	for _, name := range tc.Tools.Keys {
		if tool := tc.Tools.Values[name]; contains(actions, tool.Action) {
			tools = append(tools, tool)
		}
	}
	return tools
}

// ToolFor returns the tool with one of the actions for a file: the one whose input_extensions
// has the file's extension, or the only tool with one of the actions.
func (tc *Toolchain) ToolFor(file string, actions ...string) (*Tool, error) {
	tools := tc.ToolsWithAction(actions...)
	for _, tool := range tools {
		for _, ext := range tool.InputExtensions {
			if strings.HasSuffix(file, ext) {
				return tool, nil
			}
		}
	}
	if len(tools) == 1 {
		return tools[0], nil
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("toolchain '%s' has no %s tool", tc.Name, strings.Join(actions, ", "))
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return nil, fmt.Errorf("none of the tools of toolchain '%s' (%s) takes %s: none lists its extension in input_extensions", tc.Name, strings.Join(names, ", "), file)
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
