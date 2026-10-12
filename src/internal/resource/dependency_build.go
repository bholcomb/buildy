package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"buildy/internal/config"
	"buildy/pkg/util"
)

// dependencyBuild is one build of a fetched dependency: one configuration, for one platform and
// architecture, of its source tree, by a build system (doc/design/dependencies.md).
type dependencyBuild struct {
	name       string
	system     *BuildSystemConfig
	definition string // SHA-256 of the build system's definition
	source     string // the fetched tree: "git <commit>" or "sha256 <archive hash>"
	sourceDir  string
	buildDir   string // _build/<platform>-<arch>-<config>
	installDir string // _install/<platform>-<arch>-<config>
	markerPath string
	override   string // build.override's path (system buildy), or ""
	exec       util.ExecSpec
	mounts     []string
	values     map[string][]string // the placeholders of the phases and the output patterns
	marker     buildMarker         // what the build is
}

// buildMarker records a finished build of a dependency: everything that decides what it
// produces. A build whose marker is on disk is not run again (unless --force).
type buildMarker struct {
	Source        string            `json:"source"`       // the fetched tree: its git commit or archive hash
	Dir           string            `json:"dir"`          // the source tree's path: build trees hold absolute paths
	BuildSystem   string            `json:"build_system"` // its name and the SHA-256 of its definition
	Platform      string            `json:"platform"`
	Architecture  string            `json:"architecture"`
	Configuration string            `json:"configuration"`
	BuildType     string            `json:"build_type,omitempty"` // the build system's name for the configuration
	Args          []string          `json:"args,omitempty"`       // build.args, resolved
	Override      string            `json:"override,omitempty"`   // build.override's path and SHA-256
	Execution     util.ExecSpec     `json:"execution"`
	Tools         []string          `json:"tools"`         // what each phase runs: path and hash, or Docker image ID
	Env           map[string]string `json:"env,omitempty"` // the values of the build system's reads_env
}

// inSourceKey names the marker of an in-source build system's tree, which holds one
// configuration's build at a time.
const inSourceKey = "in-source"

// newDependencyBuild prepares the build of configuration of the source tree in sourceDir.
// buildType is the build system's name for the configuration ("" if it has none); args are
// build.args, resolved.
func newDependencyBuild(name string, system *BuildSystemConfig, definition, source, sourceDir, platform, arch, configuration, buildType string, args []string, override string, exec util.ExecSpec, mounts []string) *dependencyBuild {
	key := platform + "-" + arch + "-" + configuration
	b := &dependencyBuild{
		name:       name,
		system:     system,
		definition: definition,
		source:     source,
		sourceDir:  sourceDir,
		buildDir:   filepath.Join(sourceDir, "_build", key),
		installDir: filepath.Join(sourceDir, "_install", key),
		markerPath: filepath.Join(sourceDir, "_build", key+".built.json"),
		override:   override,
		exec:       exec,
		mounts:     mounts,
	}
	if system.InSource {
		b.markerPath = filepath.Join(sourceDir, "_build", inSourceKey+".built.json")
	}
	b.exec.ReadsEnv = append([]string(nil), system.ReadsEnv...)
	b.values = map[string][]string{
		"source_dir":  {b.sourceDir},
		"build_dir":   {b.buildDir},
		"install_dir": {b.installDir},
		"jobs":        {fmt.Sprint(runtime.NumCPU())},
		"args":        args,
		"config":      {configuration},
		"platform":    {platform},
		"arch":        {arch},
		// buildy builds the source tree as a workspace of its own (a directory argument), never
		// as a module of a workspace above it
		"config_file": {b.sourceDir},
	}
	if buildType != "" {
		b.values["build_type"] = []string{buildType}
	}
	if override != "" {
		b.values["config_file"] = []string{override}
	}
	b.marker = buildMarker{
		Source:        source,
		Dir:           sourceDir,
		BuildSystem:   system.Name + " " + definition,
		Platform:      platform,
		Architecture:  arch,
		Configuration: configuration,
		BuildType:     buildType,
		Args:          args,
		Execution:     b.exec,
	}
	return b
}

// phases returns the phases that run, in order: clean (an in-source build system's), configure,
// build and install, those the build system defines.
func (b *dependencyBuild) phases() []string {
	var phases []string
	for _, name := range config.BuildPhases {
		if b.system.Phases[name] != nil {
			phases = append(phases, name)
		}
	}
	return phases
}

// command returns what a phase runs.
func (b *dependencyBuild) command(name string) (util.Command, error) {
	phase := b.system.Phases[name]
	values := make(map[string][]string, len(b.values))
	for k, v := range b.values {
		values[k] = v
	}
	values["args"] = append(append([]string{}, phase.DefaultArgs...), b.values["args"]...)
	var command util.Command
	var err error
	if phase.Shell {
		command.Script, err = phase.Command.ExpandShell(config.MapValues(values), util.ShellQuoter(b.exec))
	} else {
		command.Args, err = phase.Command.Expand(config.MapValues(values))
	}
	if err != nil {
		return util.Command{}, fmt.Errorf("the %s phase of build system %s: %w", name, b.system.Name, err)
	}
	command.Dir = b.sourceDir
	if !phase.WorkingDir.IsZero() {
		if command.Dir, err = b.path(phase.WorkingDir); err != nil {
			return util.Command{}, fmt.Errorf("the working_dir of the %s phase of build system %s: %w", name, b.system.Name, err)
		}
	}
	return command, nil
}

// path expands a path template of the build system: one word.
func (b *dependencyBuild) path(t config.CommandTemplate) (string, error) {
	words, err := t.Expand(config.MapValues(b.values))
	if err != nil {
		return "", err
	}
	if len(words) != 1 {
		return "", fmt.Errorf("%q is %d words, not one path", t.Text, len(words))
	}
	return words[0], nil
}

// identify completes the marker with what the build runs and reads: each phase's program, the
// environment variables the build system reads, and the override config's content.
func (b *dependencyBuild) identify(ctx context.Context, tools *util.ToolIdentities) error {
	b.marker.Tools = nil
	for _, name := range b.phases() {
		command, err := b.command(name)
		if err != nil {
			return err
		}
		id, err := tools.Of(ctx, b.exec, command, b.sourceDir)
		if err != nil {
			return fmt.Errorf("the %s phase of build system %s: %w", name, b.system.Name, err)
		}
		b.marker.Tools = append(b.marker.Tools, name+": "+id)
	}
	b.marker.Env = b.exec.EnvValues()
	if b.override != "" {
		hash, err := util.HashFile(b.override)
		if err != nil {
			return fmt.Errorf("build.override: %w", err)
		}
		b.marker.Override = b.override + " " + hash
	}
	return nil
}

// upToDate reports whether the marker on disk records this build.
func (b *dependencyBuild) upToDate() bool {
	recorded, err := os.ReadFile(b.markerPath)
	if err != nil {
		return false
	}
	want, err := json.MarshalIndent(b.marker, "", "  ")
	return err == nil && bytes.Equal(bytes.TrimSpace(recorded), want)
}

// run runs the build's phases and records it in its marker. An out-of-source build starts from
// empty build and install directories; an in-source build system's clean phase runs first when
// the tree was built before (or a build of it was interrupted). A build that fails or is
// interrupted leaves no marker.
func (b *dependencyBuild) run(ctx context.Context, timeout time.Duration) error {
	_, err := os.Stat(b.markerPath)
	builtBefore := err == nil
	if err := os.MkdirAll(filepath.Dir(b.markerPath), 0o755); err != nil {
		return err
	}
	if b.system.InSource {
		// Says the tree holds a build until a complete one is recorded
		if err := os.WriteFile(b.markerPath, []byte("{}\n"), 0o644); err != nil {
			return err
		}
	} else {
		if err := os.Remove(b.markerPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, dir := range []string{b.buildDir, b.installDir} {
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("emptying %s: %w", dir, err)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
	}

	for _, name := range b.phases() {
		if name == "clean" && !builtBefore {
			continue
		}
		command, err := b.command(name)
		if err != nil {
			return err
		}
		util.LogInfo("Executing %s phase: %s", name, command)
		result, err := util.Run(ctx, b.exec, command, b.sourceDir, b.mounts, timeout)
		if err != nil {
			return fmt.Errorf("phase '%s' failed: %w", name, err)
		}
		if result.ReturnCode != 0 {
			return fmt.Errorf("phase '%s' failed with exit code %d: %s\n%s", name, result.ReturnCode, command, result.Output)
		}
	}

	data, err := json.MarshalIndent(b.marker, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(b.markerPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("recording the build of %s: %w", b.name, err)
	}
	return nil
}

// outputs returns the build system's output directories that exist: include and lib dirs.
func (b *dependencyBuild) outputs() (includeDirs, libDirs []string, err error) {
	for _, l := range []struct {
		dst      *[]string
		patterns []config.CommandTemplate
	}{
		{&includeDirs, b.system.Output.IncludePatterns},
		{&libDirs, b.system.Output.LibPatterns},
	} {
		for _, pattern := range l.patterns {
			dir, err := b.path(pattern)
			if err != nil {
				return nil, nil, fmt.Errorf("the output of build system %s: %w", b.system.Name, err)
			}
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				*l.dst = append(*l.dst, dir)
			}
		}
	}
	return includeDirs, libDirs, nil
}
