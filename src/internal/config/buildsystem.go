package config

import (
	"fmt"
	"reflect"
	"strings"
)

// BuildSystemPlaceholders are the placeholders a build system's commands, working directories
// and output patterns may use (doc/design/dependencies.md):
//
//   - source_dir, build_dir, install_dir: the fetched tree, and this configuration's build and
//     install directories under it;
//   - config, platform, arch: what is being built, buildy's names;
//   - build_type: the build system's name for the configuration, only with configurations:;
//   - jobs: the number of CPUs;
//   - args: default_args, then the dependency's build.args (commands only: it is a list);
//   - buildy_exe: the running buildy executable;
//   - config_file: the dependency's build.override, else its source directory (for buildy).
var BuildSystemPlaceholders = []string{
	"source_dir", "build_dir", "install_dir",
	"config", "platform", "arch", "build_type",
	"jobs", "args", "buildy_exe", "config_file",
}

// BuildSystem is a build system definition: how to build a fetched dependency (cmake, make, ...).
// Its phases run in order: configure (if defined), build, install (if defined). See
// doc/design/dependencies.md.
type BuildSystem struct {
	Name        string            `yaml:"name,required"`
	Description string            `yaml:"description"`
	Detection   Detection         `yaml:"detection"`
	Phases      map[string]*Phase `yaml:"phases"`
	// Configurations maps a buildy configuration to the build system's (cmake: debug to Debug),
	// the phases' ${build_type}. Without it the build system has no configurations: its phases
	// get only ${config}, buildy's name.
	Configurations map[string]string `yaml:"configurations"`
	// InSource says the build system builds in the source tree (make): the tree holds one
	// configuration's build at a time, and the clean phase runs before every build.
	InSource bool `yaml:"in_source"`
	// ReadsEnv are the environment variables the build system's tools read (CC, CFLAGS, ...):
	// their values are in the build's marker, so changing one rebuilds the dependency.
	ReadsEnv StringList  `yaml:"reads_env"`
	Output   BuildOutput `yaml:"output"`
}

// Detection says how to recognise a source tree that uses the build system.
type Detection struct {
	MarkerFiles []string `yaml:"marker_files"`
	Priority    int      `yaml:"priority"`
}

// Phase is one phase of a build system.
type Phase struct {
	Command     CommandTemplate `yaml:"command,required"`
	DefaultArgs []string        `yaml:"default_args"`
	WorkingDir  CommandTemplate `yaml:"working_dir"` // default: the source directory
}

// BuildOutput says where a built dependency's headers and libraries are: each pattern that names
// an existing directory is added to the dependency's include or lib dirs, in order.
type BuildOutput struct {
	IncludePatterns []CommandTemplate `yaml:"include_patterns"`
	LibPatterns     []CommandTemplate `yaml:"lib_patterns"`
}

// BuildPhases are the phases a build system may define, in the order they run. Only build is
// required; clean runs only before an in-source build, and is required there.
var BuildPhases = []string{"clean", "configure", "build", "install"}

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
		switch bs.Name {
		case "":
			d.errorf(nil, "build_system.name is empty")
		case "auto", "none":
			d.errorf(nil, "build_system.name can't be '%s': build.system '%s' means something else", bs.Name, bs.Name)
		}
		if bs.Phases["build"] == nil {
			d.errorf(nil, "build_system.phases has no build phase (phases: %s)", strings.Join(sortedNames(bs.Phases), ", "))
		}
		if bs.InSource && bs.Phases["clean"] == nil {
			d.errorf(nil, "build_system.in_source needs a clean phase: it runs before every build of the source tree")
		}
		if !bs.InSource && bs.Phases["clean"] != nil {
			d.errorf(nil, "build_system.phases.clean is run only for an in_source build system (others build each configuration in a new build directory)")
		}
		if bs.Configurations != nil && len(bs.Configurations) == 0 {
			d.errorf(nil, "build_system.configurations is empty: leave it out for a build system without configurations")
		}
		for _, name := range sortedNames(bs.Configurations) {
			if bs.Configurations[name] == "" {
				d.errorf(nil, "build_system.configurations.%s is empty", name)
			}
		}
		for _, name := range sortedNames(bs.Phases) {
			phase := bs.Phases[name]
			if phase == nil || !contains(BuildPhases, name) {
				continue
			}
			where := "build_system.phases." + name
			checkPlaceholders(d, bs, phase.Command, where+".command", true)
			checkPlaceholders(d, bs, phase.WorkingDir, where+".working_dir", false)
			for i, arg := range phase.DefaultArgs {
				if strings.Contains(arg, "${") {
					d.errorf(nil, "'%s.default_args[%d]' is %q: default arguments are passed as written, placeholders go in the command", where, i, arg)
				}
			}
		}
		for i, pattern := range bs.Output.IncludePatterns {
			checkPlaceholders(d, bs, pattern, fmt.Sprintf("build_system.output.include_patterns[%d]", i), false)
		}
		for i, pattern := range bs.Output.LibPatterns {
			checkPlaceholders(d, bs, pattern, fmt.Sprintf("build_system.output.lib_patterns[%d]", i), false)
		}
	}
	if err := d.err("build system"); err != nil {
		return nil, err
	}
	return file.BuildSystem, nil
}

// checkPlaceholders records a problem for each placeholder of t a build system doesn't give: an
// unknown name, ${build_type} without configurations, or ${args} (a list) in a path.
func checkPlaceholders(d *decoder, bs *BuildSystem, t CommandTemplate, where string, command bool) {
	for _, name := range t.Placeholders() {
		switch {
		case !contains(BuildSystemPlaceholders, name):
			d.errorf(nil, "'%s' has ${%s}: a build system's placeholders are ${%s}", where, name, strings.Join(BuildSystemPlaceholders, "}, ${"))
		case name == "build_type" && bs.Configurations == nil:
			d.errorf(nil, "'%s' has ${build_type}, but the build system has no configurations: map buildy's in build_system.configurations, or use ${config}", where)
		case name == "args" && !command:
			d.errorf(nil, "'%s' has ${args}: it is a list of arguments, for commands only", where)
		}
	}
}
