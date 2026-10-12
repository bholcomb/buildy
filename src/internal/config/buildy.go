package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is a buildy.yaml: the workspace root, a module, or a dependency's override config.
type File struct {
	Path string `yaml:"-" json:"-"`

	Project        *Project                    `yaml:"project"`
	Variables      Variables                   `yaml:"variables"`
	Workspace      *WorkspaceSection           `yaml:"workspace"`
	Dependencies   *DependenciesRef            `yaml:"dependencies"`
	Environment    Environment                 `yaml:"environment"`
	Config         CompileSettings             `yaml:"config"` // the same as environment.compile, merged before it
	Targets        Targets                     `yaml:"targets"`
	Artifacts      Artifacts                   `yaml:"artifacts"`
	Staging        *Staging                    `yaml:"staging"`
	Install        []*Install                  `yaml:"install"`
	Output         Output                      `yaml:"output"`
	Toolchain      string                      `yaml:"toolchain"`
	Platforms      map[string]*PlatformSection `yaml:"platforms"`
	Architectures  map[string]*Level           `yaml:"architectures"`
	Configurations map[string]*Level           `yaml:"configurations"`

	// The legacy sections, never built: an error saying what replaces them
	Library    replaced `yaml:"library"`
	Executable replaced `yaml:"executable"`
	Tasks      replaced `yaml:"tasks"`

	// The filter keys and abstract keywords the file uses, checked once the toolchains are known
	FilterKeys []NameUse `yaml:"-" json:"-"`
	Keywords   []NameUse `yaml:"-" json:"-"`
}

// replaced is a legacy section; having one is an error.
type replaced struct{}

func (replaced) decodeNode(d *decoder, n *yaml.Node, path string) {
	replacement := map[string]string{
		"library":    "targets.static_libraries or targets.shared_libraries",
		"executable": "targets.executables",
		"tasks":      "targets",
	}[path]
	d.errorf(n, "'%s' is not supported: define targets under %s", path, replacement)
}

// Project is a config's metadata.
type Project struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description"`
	Toolchain   string `yaml:"toolchain"`
}

// Variables are user-defined variables, imported environment variables, and per-platform
// variables.
type Variables struct {
	ImportEnvVars []*EnvImport                 `yaml:"import_env_vars"`
	Platforms     map[string]map[string]Scalar `yaml:"platforms"`
	Values        map[string]Scalar            `yaml:",remain"`
}

// EnvImport imports an environment variable, with a default or, for ~, none (required).
type EnvImport struct {
	Pos     Pos            `yaml:"-"`
	Name    string         `yaml:"name,required"`
	Default OptionalScalar `yaml:"default"`
}

// OptionalScalar is a scalar that may be written as ~ (null), which differs from absent.
type OptionalScalar struct {
	Set   bool   // the key is present
	Null  bool   // its value is ~
	Value string // its text otherwise
}

func (o *OptionalScalar) decodeNode(d *decoder, n *yaml.Node, path string) {
	o.Set = true
	switch {
	case isNull(n):
		o.Null = true
	case n.Kind == yaml.ScalarNode:
		o.Value = n.Value
	default:
		d.errorf(n, "'%s' must be a single value or ~, not %s", path, kindName(n))
	}
}

// WorkspaceSection lists a workspace's (or a module's) child modules.
type WorkspaceSection struct {
	Modules  *ModuleList `yaml:"modules"`
	Discover StringList  `yaml:"discover"`
	Exclude  StringList  `yaml:"exclude"`
}

// ModuleList is workspace.modules: a filtered list of module entries, or the legacy map form
// ({common: [...], linux: [...]}), where "common" is always included.
type ModuleList struct {
	Entries Filtered[ModuleEntry]
}

func (m *ModuleList) decodeNode(d *decoder, n *yaml.Node, path string) {
	switch {
	case isNull(n):
	case n.Kind == yaml.MappingNode:
		m.Entries = Filtered[ModuleEntry]{{Branches: decodeBranches[ModuleEntry](d, n, path, true)}}
	default:
		m.Entries.decodeNode(d, n, path)
	}
}

// ModuleEntry is a module: its directory and its config file's name.
type ModuleEntry struct {
	Path   string `yaml:"path,required"`
	Config string `yaml:"config"`
}

func (ModuleEntry) leafKey() string { return "path" }

func (m *ModuleEntry) decodeNode(d *decoder, n *yaml.Node, path string) {
	if n.Kind == yaml.ScalarNode {
		m.Path, _ = d.scalarString(n, path)
	} else {
		type plain ModuleEntry
		d.decodeStruct(n, reflect.ValueOf((*plain)(m)).Elem(), path)
	}
	if m.Config == "" {
		m.Config = "buildy.yaml"
	}
}

// DependenciesRef names the dependency definitions file.
type DependenciesRef struct {
	File string `yaml:"file"`
}

// Environment is the build environment: toolchains and compile, link and configuration
// settings.
type Environment struct {
	Toolchains     map[string]string `yaml:"toolchains"` // "default" or a platform -> toolchain
	Compile        CompileSettings   `yaml:"compile"`
	Link           LinkSettings      `yaml:"link"`
	Configurations map[string]*Level `yaml:"configurations"`
}

// CompileSettings are the compile settings of one level (environment.compile, a configuration,
// a platform, an architecture). Keys other than its fields are abstract keywords (optimization:
// full), which the toolchain's flag_mappings turn into flags.
type CompileSettings struct {
	Pos           Pos                `yaml:"-"`
	CppStandard   string             `yaml:"cpp_standard"`
	CStandard     string             `yaml:"c_standard"`
	OutputPrefix  *string            `yaml:"output_prefix"`
	Defines       Filtered[string]   `yaml:"defines"`
	Flags         Filtered[string]   `yaml:"flags"`
	RemoveFlags   Filtered[string]   `yaml:"remove_flags"`
	RemoveDefines Filtered[string]   `yaml:"remove_defines"`
	Keywords      map[string]Keyword `yaml:",remain"`
}

// Keyword is an abstract keyword's value: a string, or a boolean (symbols: true).
type Keyword string

func (k *Keyword) decodeNode(d *decoder, n *yaml.Node, path string) {
	name := path[strings.LastIndex(path, ".")+1:]
	d.keywords = append(d.keywords, NameUse{Name: name, Path: path, Pos: d.pos(n)})
	switch {
	case n.Kind == yaml.ScalarNode && (n.ShortTag() == "!!str" || n.ShortTag() == "!!bool"):
		*k = Keyword(n.Value)
	default:
		d.errorf(n, "'%s' must be a keyword value (a string or true/false), not %s", path, kindName(n))
	}
}

// LinkSettings are the link settings of one level.
type LinkSettings struct {
	Flags       Filtered[string] `yaml:"flags"`
	RemoveFlags Filtered[string] `yaml:"remove_flags"`
}

// Level is a configuration's, a platform's or an architecture's settings: compile settings
// (directly, or under compile:), link settings and variables.
type Level struct {
	CompileSettings `yaml:",inline"`
	Compile         CompileSettings   `yaml:"compile"`
	Link            LinkSettings      `yaml:"link"`
	Variables       map[string]Scalar `yaml:"variables"`
}

// PlatformSection is a platform's settings, and overrides of the module's targets on it.
type PlatformSection struct {
	Level   `yaml:",inline"`
	Targets Targets `yaml:"targets"`
}

// Targets are a config's targets, by type.
type Targets struct {
	StaticLibraries []*Target `yaml:"static_libraries"`
	SharedLibraries []*Target `yaml:"shared_libraries"`
	Executables     []*Target `yaml:"executables"`
}

// TargetSection is one section of Targets: its key, the target type it builds, its targets.
type TargetSection struct {
	Key     string // static_libraries
	Type    string // static_library
	Targets []*Target
}

// Sections returns the target sections in generation order.
func (t Targets) Sections() []TargetSection {
	return []TargetSection{
		{"static_libraries", "static_library", t.StaticLibraries},
		{"shared_libraries", "shared_library", t.SharedLibraries},
		{"executables", "executable", t.Executables},
	}
}

// Names returns every target's name.
func (t Targets) Names() []string {
	var names []string
	for _, s := range t.Sections() {
		for _, target := range s.Targets {
			names = append(names, target.Name)
		}
	}
	return names
}

// Target is a library or an executable.
type Target struct {
	Pos           Pos              `yaml:"-"`
	Name          string           `yaml:"name,required"`
	Language      string           `yaml:"language"`
	Sources       Filtered[Source] `yaml:"sources"`
	Path          string           `yaml:"path"`
	IncludeDirs   Filtered[string] `yaml:"include_dirs"`
	LibDirs       StringList       `yaml:"lib_dirs"`
	Libs          Filtered[string] `yaml:"libs"`
	Frameworks    StringList       `yaml:"frameworks"`
	Deps          StringList       `yaml:"deps"`
	DependsOn     DependsOn        `yaml:"depends_on"`
	Defines       Filtered[string] `yaml:"defines"`
	Flags         Filtered[string] `yaml:"flags"`
	RemoveFlags   StringList       `yaml:"remove_flags"`
	RemoveDefines StringList       `yaml:"remove_defines"`
	Compile       TargetCompile    `yaml:"compile"`
	Link          LinkSettings     `yaml:"link"`
	OutputPrefix  *string          `yaml:"output_prefix"`
	// Go
	BuildTags StringList `yaml:"build_tags"`
	Ldflags   StringList `yaml:"ldflags"`
	Gcflags   StringList `yaml:"gcflags"`
	Buildmode string     `yaml:"buildmode"`
	// Rust
	Features StringList `yaml:"features"`
	Bin      string     `yaml:"bin"`
}

// TargetCompile is a target's compile: section.
type TargetCompile struct {
	Flags       Filtered[string] `yaml:"flags"`
	RemoveFlags Filtered[string] `yaml:"remove_flags"`
	Defines     Filtered[string] `yaml:"defines"`
}

// Source is a source pattern; an optional one may match no files.
type Source struct {
	Pattern  string `yaml:"pattern,required"`
	Optional bool   `yaml:"optional"`
}

func (Source) leafKey() string { return "pattern" }

func (s *Source) decodeNode(d *decoder, n *yaml.Node, path string) {
	if n.Kind == yaml.ScalarNode {
		s.Pattern, _ = d.scalarString(n, path)
		return
	}
	type plain Source
	d.decodeStruct(n, reflect.ValueOf((*plain)(s)).Elem(), path)
}

// DependsOn is a target's build-order dependencies: a list of targets, or {targets, artifacts}.
type DependsOn struct {
	Targets   StringList `yaml:"targets"`
	Artifacts StringList `yaml:"artifacts"`
}

func (o *DependsOn) decodeNode(d *decoder, n *yaml.Node, path string) {
	switch {
	case isNull(n):
	case n.Kind == yaml.SequenceNode:
		o.Targets.decodeNode(d, n, path)
	default:
		type plain DependsOn
		d.decodeStruct(n, reflect.ValueOf((*plain)(o)).Elem(), path)
	}
}

// Artifacts are a module's file operations.
type Artifacts struct {
	Copy      []*Copy      `yaml:"copy"`
	Transform []*Transform `yaml:"transform"`
	Generate  []*Generate  `yaml:"generate"`
}

// Transform runs a tool on each input file.
type Transform struct {
	Pos     Pos        `yaml:"-"`
	Name    string     `yaml:"name"`
	Tool    string     `yaml:"tool,required"`
	Inputs  StringList `yaml:"inputs,required"`
	Outputs string     `yaml:"outputs,required"`
	Args    StringList `yaml:"args"`
}

// Generate makes files from a template file, or with a tool from each input file.
type Generate struct {
	Pos       Pos             `yaml:"-"`
	Name      string          `yaml:"name"`
	Template  string          `yaml:"template"`
	Output    string          `yaml:"output"`
	Variables Ordered[Scalar] `yaml:"variables"`
	Tool      string          `yaml:"tool"`
	Inputs    StringList      `yaml:"inputs"`
	Outputs   StringList      `yaml:"outputs"`
	Args      StringList      `yaml:"args"`
}

// Copy copies files, a file, or a target's output.
type Copy struct {
	Pos         Pos        `yaml:"-"`
	Name        string     `yaml:"name"`
	Sources     StringList `yaml:"sources"`
	Source      string     `yaml:"source"`
	Target      string     `yaml:"target"`
	Destination string     `yaml:"destination"`
	Dest        string     `yaml:"dest"`
	DependsOn   StringList `yaml:"depends_on"`
}

// Staging assembles outputs into a directory.
type Staging struct {
	Name        string           `yaml:"name"`
	Destination string           `yaml:"destination"`
	UseSymlinks bool             `yaml:"use_symlinks"`
	Contents    []*StagingFolder `yaml:"contents"`
}

// StagingFolder is a folder of a staging area.
type StagingFolder struct {
	Folder    string                `yaml:"folder"`
	Targets   []StagingRef          `yaml:"targets"`
	Artifacts []StagingRef          `yaml:"artifacts"`
	Files     Filtered[StagingFile] `yaml:"files"`
	Contents  []*StagingFolder      `yaml:"contents"`
}

// StagingRef names a target or an artifact: "name", or {name, use_symlink}.
type StagingRef struct {
	Name       string `yaml:"name,required"`
	UseSymlink *bool  `yaml:"use_symlink"`
}

func (r *StagingRef) decodeNode(d *decoder, n *yaml.Node, path string) {
	switch {
	case isNull(n):
		d.errorf(n, "'%s' is empty", path)
	case n.Kind == yaml.ScalarNode:
		r.Name, _ = d.scalarString(n, path)
	default:
		type plain StagingRef
		d.decodeStruct(n, reflect.ValueOf((*plain)(r)).Elem(), path)
	}
}

// StagingFile is a file or glob to stage: "path", or {source, use_symlink}.
type StagingFile struct {
	Source     string `yaml:"source,required"`
	UseSymlink *bool  `yaml:"use_symlink"`
}

func (StagingFile) leafKey() string { return "source" }

func (f *StagingFile) decodeNode(d *decoder, n *yaml.Node, path string) {
	if n.Kind == yaml.ScalarNode {
		f.Source, _ = d.scalarString(n, path)
		return
	}
	type plain StagingFile
	d.decodeStruct(n, reflect.ValueOf((*plain)(f)).Elem(), path)
}

// Install packages a staging area.
type Install struct {
	Pos            Pos    `yaml:"-"`
	Name           string `yaml:"name,required"`
	Staging        string `yaml:"staging"`
	Destination    string `yaml:"destination"`
	Format         string `yaml:"format"`
	FollowSymlinks *bool  `yaml:"follow_symlinks"`
	Filename       string `yaml:"filename"`
}

// Install formats.
var installFormats = []string{"tar.gz", "tgz", "zip", "tar"}

// Output is where the outputs go: the workspace root's for every module, unless a module sets its
// own.
type Output struct {
	BaseDir string `yaml:"base_dir"`
	Pattern string `yaml:"pattern"`
}

// LoadFile reads and validates a buildy.yaml. root is true for the workspace's root config,
// which alone may declare dependencies and must name its project.
func LoadFile(path string, root bool) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	node, err := parse(data, path)
	if err != nil {
		return nil, err
	}
	d := newDecoder(path)
	f := &File{Path: path}
	d.decode(node, reflect.ValueOf(f).Elem(), "")
	f.validate(d, root)
	if err := d.err("buildy config"); err != nil {
		return nil, err
	}
	f.FilterKeys, f.Keywords = d.filterKeys, d.keywords
	return f, nil
}

func (f *File) validate(d *decoder, root bool) {
	if root && (f.Project == nil || strings.TrimSpace(f.Project.Name) == "") {
		d.errorf(nil, "project.name is required in the workspace's root config")
	}
	if f.Dependencies != nil && !root {
		d.errorf(nil, "dependencies: only the workspace's root config declares dependencies (in buildy_config/)")
	}
	if b := f.Output.BaseDir; b != "" && (strings.Contains(b, "..") || filepath.IsAbs(b) || filepath.Clean(b) == ".") {
		d.errorf(nil, "output.base_dir '%s' must be a directory below the workspace root (relative, not '.', without '..')", b)
	}

	f.Environment.Toolchains = normalizeKeys(d, f.Environment.Toolchains, "environment.toolchains", func(k string) (string, bool) {
		if k == "default" {
			return k, true
		}
		return NormalizePlatform(k)
	}, append([]string{"default"}, Platforms...))
	f.Platforms = normalizeKeys(d, f.Platforms, "platforms", NormalizePlatform, Platforms)
	f.Architectures = normalizeKeys(d, f.Architectures, "architectures", NormalizeArchitecture, Architectures)
	f.Variables.Platforms = normalizeKeys(d, f.Variables.Platforms, "variables.platforms", NormalizePlatform, Platforms)
	for _, name := range sortedNames(f.Platforms) {
		if f.Platforms[name] == nil {
			delete(f.Platforms, name)
		}
	}

	for i, imp := range f.Variables.ImportEnvVars {
		if imp == nil {
			d.errorf(nil, "variables.import_env_vars[%d] is empty", i)
		} else if !imp.Default.Set {
			d.errorAt(imp.Pos, "variables.import_env_vars[%d] (%s) needs 'default' (a value, or ~ for a variable that must be set)", i, imp.Name)
		}
	}

	for _, s := range f.Targets.Sections() {
		for i, t := range s.Targets {
			where := fmt.Sprintf("targets.%s[%d] (%s)", s.Key, i, t.Name)
			switch t.Language {
			case "":
				d.errorAt(t.Pos, "%s is missing required 'language'", where)
			case "c", "cpp":
				if len(t.Sources) == 0 {
					d.errorAt(t.Pos, "%s is missing required 'sources'", where)
				}
			case "go", "rust":
				if t.Path == "" {
					d.errorAt(t.Pos, "%s is missing required 'path'", where)
				}
			}
		}
	}
	for _, platform := range sortedNames(f.Platforms) {
		for _, s := range f.Platforms[platform].Targets.Sections() {
			for i, o := range s.Targets {
				if f.target(s.Key, o.Name) == nil {
					d.errorAt(o.Pos, "platforms.%s.targets.%s[%d] overrides '%s', which is not one of the config's targets.%s", platform, s.Key, i, o.Name, s.Key)
				}
			}
		}
	}

	for i, g := range f.Artifacts.Generate {
		where := fmt.Sprintf("artifacts.generate[%d]", i)
		switch {
		case g.Template != "" && g.Tool != "":
			d.errorAt(g.Pos, "%s has both 'template' and 'tool'", where)
		case g.Template != "":
			if g.Output == "" {
				d.errorAt(g.Pos, "%s is missing required 'output'", where)
			}
		case g.Tool != "":
			if len(g.Inputs) == 0 || len(g.Outputs) == 0 {
				d.errorAt(g.Pos, "%s needs 'inputs' and 'outputs'", where)
			}
		default:
			d.errorAt(g.Pos, "%s needs either 'template' (with 'output') or 'tool' (with 'inputs' and 'outputs')", where)
		}
	}
	for i, c := range f.Artifacts.Copy {
		where := fmt.Sprintf("artifacts.copy[%d]", i)
		if len(c.Sources) == 0 && c.Source == "" && c.Target == "" {
			d.errorAt(c.Pos, "%s needs 'sources', 'source' or 'target'", where)
		}
		if c.Destination == "" && c.Dest == "" {
			d.errorAt(c.Pos, "%s needs 'destination'", where)
		}
	}
	for i, inst := range f.Install {
		if inst.Format != "" && !contains(installFormats, inst.Format) {
			d.errorAt(inst.Pos, "install[%d] (%s): format '%s' is not one of: %s", i, inst.Name, inst.Format, strings.Join(installFormats, ", "))
		}
	}
}

// target returns the config's target in a section, by name.
func (f *File) target(section, name string) *Target {
	for _, s := range f.Targets.Sections() {
		if s.Key == section {
			for _, t := range s.Targets {
				if t.Name == name {
					return t
				}
			}
		}
	}
	return nil
}

// normalizeKeys rewrites a map's keys through normalize, reporting keys it rejects and two keys
// that mean the same (darwin and macos).
func normalizeKeys[V any](d *decoder, m map[string]V, where string, normalize func(string) (string, bool), known []string) map[string]V {
	if m == nil {
		return nil
	}
	result := make(map[string]V, len(m))
	from := map[string]string{}
	for _, key := range sortedNames(m) {
		name, ok := normalize(key)
		if !ok {
			msg := fmt.Sprintf("%s: '%s' is not one of: %s", where, key, strings.Join(known, ", "))
			if near := ClosestName(key, known); near != "" {
				msg += fmt.Sprintf(" (did you mean '%s'?)", near)
			}
			d.errorf(nil, "%s", msg)
			continue
		}
		if other, dup := from[name]; dup {
			d.errorf(nil, "%s: '%s' and '%s' are the same", where, other, key)
			continue
		}
		from[name] = key
		result[name] = m[key]
	}
	return result
}

// ConfigurationNames returns the configurations a config declares.
func (f *File) ConfigurationNames() []string {
	names := sortedNames(f.Configurations)
	for _, n := range sortedNames(f.Environment.Configurations) {
		if !contains(names, n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// Inherit returns a module's config with the workspace root's settings merged in, by the merge
// rules (Merge): the variables, the environment, the config section, the output layout, and the
// settings (not the target overrides) of the platforms, architectures and configurations
// sections. Everything else (project, targets, artifacts, staging, install, toolchain) is the
// module's own.
// Neither file changes. The root's config is the root module's own (discovery mode, a standalone
// config): it inherits nothing.
func Inherit(root, module *File) *File {
	if root == nil || filepath.Clean(root.Path) == filepath.Clean(module.Path) {
		return module
	}
	m := *module
	m.Variables = Merge(root.Variables, module.Variables)
	m.Environment = Merge(root.Environment, module.Environment)
	m.Config = Merge(root.Config, module.Config)
	m.Output = Merge(root.Output, module.Output)
	rootPlatforms := map[string]*PlatformSection{}
	for name, p := range root.Platforms {
		rootPlatforms[name] = &PlatformSection{Level: p.Level}
	}
	m.Platforms = Merge(rootPlatforms, module.Platforms)
	m.Architectures = Merge(root.Architectures, module.Architectures)
	m.Configurations = Merge(root.Configurations, module.Configurations)
	return &m
}
