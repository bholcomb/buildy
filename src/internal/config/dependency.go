package config

import (
	"fmt"
	"reflect"
	"strings"

	"buildy/pkg/util"

	"gopkg.in/yaml.v3"
)

// Dependency is a dependency definition: metadata, the fields that apply everywhere, and
// sections for "common", a platform, or a platform and an architecture ("linux-arm64").
type Dependency struct {
	Pos         Pos
	Name        string
	Description string
	Version     string
	// Direct are the fields written at the top level, next to the sections
	Direct   *DependencySection
	Sections map[string]*DependencySection // "common", "linux", "linux-arm64" (normalised)
}

// DependencySection is the fields of one section of a dependency.
type DependencySection struct {
	// Where it comes from: git or url fetch it; pkg_config or root find it on the system
	Git       string `yaml:"git"`
	URL       string `yaml:"url"`
	PkgConfig string `yaml:"pkg_config"`
	Root      string `yaml:"root"`

	// Fetch fields
	Ref      string           `yaml:"ref"`
	Checksum string           `yaml:"checksum"`
	Build    *DependencyBuild `yaml:"build"`

	// What it provides
	IncludeDirs []string `yaml:"include_dirs"`
	LibDirs     []string `yaml:"lib_dirs"`
	Libs        []string `yaml:"libs"`
	Frameworks  []string `yaml:"frameworks"`
	Defines     []string `yaml:"defines"`

	// Where its build runs (native or docker)
	Execution *util.ExecSpec `yaml:"execution"`
}

// DependencyBuild is how a fetched dependency is built.
type DependencyBuild struct {
	System   string   `yaml:"system"`   // cmake, meson, make, autoconf, buildy, none, auto
	Override string   `yaml:"override"` // a buildy config in buildy_config/dependencies/
	Args     []string `yaml:"args"`
}

// SectionName returns the normalised name of a dependency section ("common", a platform, or
// platform-architecture), and whether key is one.
func SectionName(key string) (string, bool) {
	if key == "common" {
		return key, true
	}
	if p, ok := NormalizePlatform(key); ok {
		return p, true
	}
	platform, arch, found := strings.Cut(key, "-")
	if !found {
		return key, false
	}
	p, okP := NormalizePlatform(platform)
	a, okA := NormalizeArchitecture(arch)
	return p + "-" + a, okP && okA
}

// sectionNames are the section names, for suggestions.
func sectionNames() []string {
	names := append([]string{"common"}, Platforms...)
	for _, p := range Platforms {
		for _, a := range Architectures {
			names = append(names, p+"-"+a)
		}
	}
	return names
}

func (dep *Dependency) decodeNode(d *decoder, n *yaml.Node, path string) {
	dep.Name = path
	dep.Pos = d.pos(n)
	if isNull(n) || n.Kind != yaml.MappingNode {
		d.errorf(n, "dependency '%s' must be a mapping, not %s", path, kindName(n))
		return
	}
	fields, _ := structFields(reflect.TypeOf(DependencySection{}))
	direct := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Line: n.Line}
	for _, e := range d.entries(n, path) {
		key := e.key.Value
		switch {
		case key == "description":
			dep.Description, _ = d.scalarString(e.value, join(path, key))
		case key == "version":
			dep.Version, _ = d.scalarString(e.value, join(path, key))
		case fields[key].index != nil:
			direct.Content = append(direct.Content, e.key, e.value)
		default:
			name, ok := SectionName(key)
			if !ok {
				known := append(append(sortedNames(fields), "description", "version"), sectionNames()...)
				d.unknownKey(e.key, path, key, known)
				continue
			}
			if _, dup := dep.Sections[name]; dup {
				d.errorf(e.key, "'%s': section '%s' is written twice (aliases name the same section)", path, name)
				continue
			}
			section := &DependencySection{}
			d.decodeStruct(e.value, reflect.ValueOf(section).Elem(), join(path, key))
			if dep.Sections == nil {
				dep.Sections = map[string]*DependencySection{}
			}
			dep.Sections[name] = section
		}
	}
	if len(direct.Content) > 0 {
		dep.Direct = &DependencySection{}
		d.decodeStruct(direct, reflect.ValueOf(dep.Direct).Elem(), path)
	}
}

// LoadDependencies reads a dependency definitions file: dependency names to definitions, in the
// order written.
func LoadDependencies(data []byte, source string) ([]*Dependency, error) {
	root, err := parse(data, source)
	if err != nil {
		return nil, err
	}
	d := newDecoder(source)
	if root.Kind != yaml.MappingNode {
		d.errorf(root, "the file must be a mapping of dependency names to definitions, not %s", kindName(root))
		return nil, d.err("dependency definitions file")
	}
	var deps []*Dependency
	for _, e := range d.entries(root, "") {
		dep := &Dependency{}
		dep.decodeNode(d, resolveAlias(e.value), e.key.Value)
		for _, s := range dep.sections() {
			if s.Execution != nil {
				if err := s.Execution.Validate(); err != nil {
					d.errorAt(dep.Pos, "dependency '%s': execution: %v", dep.Name, err)
				}
			}
		}
		deps = append(deps, dep)
	}
	if err := d.err("dependency definitions file"); err != nil {
		return nil, err
	}
	return deps, nil
}

// sections returns the dependency's sections, direct fields first.
func (dep *Dependency) sections() []*DependencySection {
	var result []*DependencySection
	if dep.Direct != nil {
		result = append(result, dep.Direct)
	}
	for _, name := range sortedNames(dep.Sections) {
		result = append(result, dep.Sections[name])
	}
	return result
}

// Overrides returns the build.override files the dependency names, in any section.
func (dep *Dependency) Overrides() []string {
	var overrides []string
	for _, s := range dep.sections() {
		if s.Build != nil && s.Build.Override != "" {
			overrides = append(overrides, s.Build.Override)
		}
	}
	return overrides
}

// For merges the sections that apply to a platform and architecture, in order: the direct
// fields, common, the platform's, then platform-architecture's. A later section's scalar fields
// (and build and execution, whole) replace earlier ones; lists append.
func (dep *Dependency) For(platform, arch string) DependencySection {
	merged := DependencySection{}
	add := func(s *DependencySection) {
		if s == nil {
			return
		}
		for _, f := range []struct {
			dst *string
			src string
		}{
			{&merged.Git, s.Git}, {&merged.URL, s.URL}, {&merged.PkgConfig, s.PkgConfig}, {&merged.Root, s.Root},
			{&merged.Ref, s.Ref}, {&merged.Checksum, s.Checksum},
		} {
			if f.src != "" {
				*f.dst = f.src
			}
		}
		if s.Build != nil {
			merged.Build = s.Build
		}
		if s.Execution != nil {
			merged.Execution = s.Execution
		}
		merged.IncludeDirs = append(merged.IncludeDirs, s.IncludeDirs...)
		merged.LibDirs = append(merged.LibDirs, s.LibDirs...)
		merged.Libs = append(merged.Libs, s.Libs...)
		merged.Frameworks = append(merged.Frameworks, s.Frameworks...)
		merged.Defines = append(merged.Defines, s.Defines...)
	}
	add(dep.Direct)
	add(dep.Sections["common"])
	add(dep.Sections[platform])
	add(dep.Sections[fmt.Sprintf("%s-%s", platform, arch)])
	return merged
}
