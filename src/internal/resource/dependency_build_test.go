package resource

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"buildy/internal/config"
	"buildy/pkg/util"
)

// cmakeBuild prepares a cmake build of configuration in a source tree.
func cmakeBuild(t *testing.T, configuration string) *dependencyBuild {
	t.Helper()
	bsm, err := NewBuildSystemManager()
	if err != nil {
		t.Fatal(err)
	}
	bs := bsm.GetBuildSystem("cmake")
	return newDependencyBuild("dv", bs, bsm.definition("cmake"), "git abc", "/src/dv", "windows", "x86_64",
		configuration, bs.Configurations[configuration], []string{"-DOPT=ON"}, "", util.ExecSpec{}, nil)
}

// Finding 1.1: cmake gets CMAKE_BUILD_TYPE once, and a multi-configuration generator (Visual
// Studio, the default on Windows) gets --config for the build and the install; the build and
// install directories are per configuration.
func TestCmakeCommands(t *testing.T) {
	b := cmakeBuild(t, "release")
	if got := b.phases(); !slices.Equal(got, []string{"configure", "build", "install"}) {
		t.Errorf("phases = %v", got)
	}
	want := map[string][]string{
		"configure": {"cmake", "-S", "/src/dv", "-B", b.buildDir, "-DCMAKE_BUILD_TYPE=Release",
			"-DCMAKE_INSTALL_PREFIX=" + b.installDir, "-DCMAKE_INSTALL_LIBDIR=lib", "-DOPT=ON"},
		"build":   {"cmake", "--build", b.buildDir, "--config", "Release", "--parallel", b.values["jobs"][0]},
		"install": {"cmake", "--install", b.buildDir, "--config", "Release", "--prefix", b.installDir},
	}
	for phase, args := range want {
		command, err := b.command(phase)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(command.Args, args) {
			t.Errorf("%s:\n got %q\nwant %q", phase, command.Args, args)
		}
	}
	if b.buildDir != filepath.Join("/src/dv", "_build", "windows-x86_64-release") ||
		b.installDir != filepath.Join("/src/dv", "_install", "windows-x86_64-release") {
		t.Errorf("dirs: %s %s", b.buildDir, b.installDir)
	}
}

// No built-in build system has a phase that does nothing: make and cargo have no configure.
func TestBuildSystemsHaveNoNoOpPhases(t *testing.T) {
	bsm, err := NewBuildSystemManager()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range bsm.ListBuildSystems() {
		for phaseName, phase := range bsm.GetBuildSystem(name).Phases {
			if text := strings.TrimSpace(phase.Command.Text); text == "true" || text == ":" {
				t.Errorf("%s's %s phase runs %q", name, phaseName, text)
			}
		}
	}
	if bsm.GetBuildSystem("make").Phases["configure"] != nil || bsm.GetBuildSystem("cargo").Phases["configure"] != nil {
		t.Error("make or cargo has a configure phase")
	}
}

// Finding 6.2: a build's marker matches only the same build: another source, configuration,
// argument or environment value is another build.
func TestBuildMarker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping: the build system's programs are shell scripts")
	}
	bin := t.TempDir()
	for _, p := range []string{"cmake"} {
		if err := os.WriteFile(filepath.Join(bin, p), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("CC", "gcc")
	tools := util.NewToolIdentities(util.HashFile)
	marked := func(b *dependencyBuild) *dependencyBuild {
		t.Helper()
		b.sourceDir = t.TempDir()
		b.markerPath = filepath.Join(t.TempDir(), "m.json")
		if err := b.identify(context.Background(), tools); err != nil {
			t.Fatal(err)
		}
		return b
	}
	b := marked(cmakeBuild(t, "release"))
	if b.marker.Env["CC"] != "gcc" || len(b.marker.Tools) != 3 {
		t.Errorf("marker = %+v", b.marker)
	}

	base := b.marker
	for name, change := range map[string]func(m *buildMarker){
		"source":        func(m *buildMarker) { m.Source = "git def" },
		"configuration": func(m *buildMarker) { m.Configuration = "debug" },
		"args":          func(m *buildMarker) { m.Args = nil },
		"env":           func(m *buildMarker) { m.Env = map[string]string{"CC": "clang"} },
		"tool":          func(m *buildMarker) { m.Tools = m.Tools[1:] },
	} {
		other := base
		change(&other)
		if reflect.DeepEqual(other, base) {
			t.Errorf("changing the %s left the marker equal", name)
		}
	}
	if b.upToDate() {
		t.Error("up to date without a marker")
	}
}

// Every placeholder a definition may use has a value in a build of a build system with
// configurations (the resolver adds ${buildy_exe}).
func TestBuildSystemPlaceholdersHaveValues(t *testing.T) {
	b := cmakeBuild(t, "release")
	for _, name := range config.BuildSystemPlaceholders {
		if _, ok := b.values[name]; !ok && name != "buildy_exe" {
			t.Errorf("${%s} has no value", name)
		}
	}
}

// A shell phase is a script whose placeholder values are quoted for the shell, its working
// directory expanded like any phase's.
func TestShellPhase(t *testing.T) {
	bs, err := config.LoadBuildSystem([]byte(`build_system:
  name: plain
  phases:
    build:
      shell: true
      command: "./build.sh ${args} > ${build_dir}/log && cp lib.a ${install_dir}/"
      working_dir: "${build_dir}"
`), "plain.yaml")
	if err != nil {
		t.Fatal(err)
	}
	b := newDependencyBuild("d", bs, "hash", "git abc", filepath.Join("/my src", "d"), "linux", "x86_64",
		"debug", "", []string{"A=1 2"}, "", util.ExecSpec{}, nil)
	command, err := b.command("build")
	if err != nil {
		t.Fatal(err)
	}
	q := func(s string) string {
		quoted, err := util.ShellQuoter(util.ExecSpec{})(s)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}
	want := "./build.sh " + q("A=1 2") + " > " + q(b.buildDir) + "/log && cp lib.a " + q(b.installDir) + "/"
	if command.Script != want || command.Args != nil || command.Dir != b.buildDir {
		t.Errorf("command = %+v\nwant script %s in %s", command, want, b.buildDir)
	}

	_, err = config.LoadBuildSystem([]byte("build_system:\n  name: x\n  phases: {build: {shell: true, command: 'make \"${build_dir}\"'}}\n"), "x.yaml")
	if err == nil || !strings.Contains(err.Error(), "write placeholders outside quotes") {
		t.Errorf("a quoted placeholder in a shell phase: err = %v", err)
	}
}
