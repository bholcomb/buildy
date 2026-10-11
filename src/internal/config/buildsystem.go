package config

import (
	"reflect"
	"strings"
)

// BuildSystem is a build system definition: how to build a fetched dependency (cmake, make, ...).
type BuildSystem struct {
	Name        string            `yaml:"name,required"`
	Description string            `yaml:"description"`
	Detection   Detection         `yaml:"detection"`
	Phases      map[string]*Phase `yaml:"phases"`
	Output      BuildOutput       `yaml:"output"`
}

// Detection says how to recognise a source tree that uses the build system.
type Detection struct {
	MarkerFiles []string `yaml:"marker_files"`
	Priority    int      `yaml:"priority"`
}

// Phase is one phase of a build system (configure, build, test, install, clean).
type Phase struct {
	Command     CommandTemplate `yaml:"command,required"`
	DefaultArgs []string        `yaml:"default_args"`
	Required    bool            `yaml:"required"`
	WorkingDir  string          `yaml:"working_dir"` // default: the source directory
}

// BuildOutput says where a built dependency's headers and libraries are.
type BuildOutput struct {
	IncludePatterns []string `yaml:"include_patterns"`
	LibPatterns     []string `yaml:"lib_patterns"`
}

// BuildPhases are the phases a build system may define.
var BuildPhases = []string{"configure", "build", "test", "install", "clean"}

// LoadBuildSystem reads a build system file.
func LoadBuildSystem(data []byte, source string) (*BuildSystem, error) {
	root, err := parse(data, source)
	if err != nil {
		return nil, err
	}
	d := newDecoder(source)
	var file struct {
		BuildSystem *BuildSystem `yaml:"build_system,required"`
	}
	d.decode(root, reflect.ValueOf(&file).Elem(), "")
	if bs := file.BuildSystem; bs != nil {
		for _, name := range sortedNames(bs.Phases) {
			if !contains(BuildPhases, name) {
				d.unknownKey(nil, "build_system.phases", name, BuildPhases)
			} else if bs.Phases[name] == nil {
				d.errorf(nil, "'build_system.phases.%s' is empty", name)
			}
		}
		if bs.Phases["build"] == nil {
			d.errorf(nil, "build_system.phases has no build phase (phases: %s)", strings.Join(sortedNames(bs.Phases), ", "))
		}
	}
	if err := d.err("build system"); err != nil {
		return nil, err
	}
	return file.BuildSystem, nil
}
