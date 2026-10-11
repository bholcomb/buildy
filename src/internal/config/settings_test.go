package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// loadText loads buildy.yaml text as a workspace root config.
func loadText(t *testing.T, text string) *File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "buildy.yaml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func gccMappings() Ordered[Ordered[[]string]] {
	var m Ordered[Ordered[[]string]]
	set := func(keyword string, pairs ...any) {
		var values Ordered[[]string]
		for i := 0; i < len(pairs); i += 2 {
			values.Set(pairs[i].(string), pairs[i+1].([]string))
		}
		m.Set(keyword, values)
	}
	set("optimization", "none", []string{"-O0"}, "speed", []string{"-O2"}, "full", []string{"-O3"})
	set("warnings", "off", []string{}, "default", []string{"-Wall"}, "extra", []string{"-Wall", "-Wextra"})
	set("symbols", "true", []string{"-g"}, "false", []string{})
	return m
}

var linuxDebug = Context{Platform: "linux", Architecture: "x86_64", Configuration: "debug", Toolchain: "gcc-cpp-linux"}
var linuxRelease = Context{Platform: "linux", Architecture: "x86_64", Configuration: "release", Toolchain: "gcc-cpp-linux"}

// The flag pipeline, level by level: keywords first (sorted), then each level's flags in merge
// order, removals last.
func TestSettingsPipeline(t *testing.T) {
	cases := []struct {
		name                      string
		yaml                      string
		ctx                       Context
		flags, linkFlags, defines []string
	}{
		{
			name: "environment flags and keywords",
			yaml: `
environment:
  compile: {flags: [-fpermissive], optimization: none, warnings: extra, symbols: true}`,
			ctx:   linuxDebug,
			flags: []string{"-O0", "-g", "-Wall", "-Wextra", "-fpermissive"},
		},
		{
			name: "configuration flags append",
			yaml: `
environment:
  compile: {flags: [-fpermissive]}
  configurations:
    debug: {flags: [-DDEBUG_MODE]}`,
			ctx:   linuxDebug,
			flags: []string{"-fpermissive", "-DDEBUG_MODE"},
		},
		{
			name: "remove_flags removes keyword flags",
			yaml: `
environment:
  compile: {warnings: extra}
  configurations:
    release: {optimization: full, remove_flags: [-Wextra]}`,
			ctx:   linuxRelease,
			flags: []string{"-O3", "-Wall"},
		},
		{
			name: "compile and link sections of a configuration",
			yaml: `
environment:
  compile: {flags: [-fvisibility=hidden]}
  link: {flags: ["-Wl,-z,now"]}
  configurations:
    release:
      optimization: full
      compile: {flags: [-flto, -march=native]}
      link: {flags: [-flto, "-Wl,-O1"]}`,
			ctx:       linuxRelease,
			flags:     []string{"-O3", "-fvisibility=hidden", "-flto", "-march=native"},
			linkFlags: []string{"-Wl,-z,now", "-flto", "-Wl,-O1"},
		},
		{
			name: "removals inside compile and link sections",
			yaml: `
environment:
  compile: {flags: [-Wall, -Wextra, -Werror]}
  link: {flags: ["-Wl,-z,now", -s]}
  configurations:
    release:
      compile: {remove_flags: [-Werror]}
      link: {remove_flags: [-s]}`,
			ctx:       linuxRelease,
			flags:     []string{"-Wall", "-Wextra"},
			linkFlags: []string{"-Wl,-z,now"},
		},
		{
			name: "Mobius: release removes the -O2 its keyword adds",
			yaml: `
environment:
  compile: {warnings: extra}
  configurations:
    release:
      optimization: speed
      defines: [NDEBUG]
      compile:
        remove_flags: [-O2]
        flags: [{linux: [-O3, -flto]}, {macos: [-O3]}]`,
			ctx:     linuxRelease,
			flags:   []string{"-Wall", "-Wextra", "-O3", "-flto"},
			defines: []string{"NDEBUG"},
		},
		{
			name: "filters in compile and link sections",
			yaml: `
environment:
  compile:
    flags: [-fvisibility=hidden, {linux: [-pthread]}, {windows: [/utf-8]}, {debug: {gcc-cpp-linux: [-fsanitize=address], clang-cpp-linux: [-fsanitize=memory]}}]
  link:
    flags: [{linux: [-ldl]}, {macos: [-framework Cocoa]}]`,
			ctx:       linuxDebug,
			flags:     []string{"-fvisibility=hidden", "-pthread", "-fsanitize=address"},
			linkFlags: []string{"-ldl"},
		},
		{
			name: "the config section, then environment.compile, platform, architecture, configuration",
			yaml: `
config: {flags: [-c0]}
environment:
  compile: {flags: [-c1]}
  configurations:
    debug: {flags: [-c5], compile: {flags: [-c6]}}
platforms:
  linux: {flags: [-c2], compile: {flags: [-c3]}}
architectures:
  x86_64: {flags: [-c4]}
configurations:
  debug: {flags: [-c4b]}`,
			ctx:   linuxDebug,
			flags: []string{"-c0", "-c1", "-c2", "-c3", "-c4", "-c4b", "-c5", "-c6"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := loadText(t, c.yaml)
			s, err := f.Settings(c.ctx, gccMappings())
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range []struct {
				what      string
				got, want []string
			}{{"flags", s.Flags, c.flags}, {"link flags", s.LinkFlags, c.linkFlags}, {"defines", s.Defines, c.defines}} {
				if check.want == nil {
					check.want = []string{}
				}
				if !reflect.DeepEqual(check.got, check.want) {
					t.Errorf("%s = %q, want %q", check.what, check.got, check.want)
				}
			}
		})
	}
}

func TestSettingsUnknownKeywordValue(t *testing.T) {
	f := loadText(t, "environment:\n  compile: {optimization: turbo}\n")
	_, err := f.Settings(linuxDebug, gccMappings())
	if err == nil || !strings.Contains(err.Error(), `abstract keyword "optimization" has unresolvable value "turbo"; valid values: [full, none, speed]`) {
		t.Errorf("err = %v", err)
	}
}

// Finding 1.5: a module's environment.compile adds to the root's; it used to replace it, so a
// module that only added a define lost the root's c_standard.
func TestModuleEnvironmentMergesIntoTheRoot(t *testing.T) {
	root := loadText(t, `
variables: {APPDEF: from_root, SHARED: root}
environment:
  compile: {c_standard: c11, warnings: extra, flags: [-froot], defines: [ROOT]}
  link: {flags: [-lroot]}
  configurations:
    debug: {defines: [ROOTDEBUG]}
platforms:
  linux:
    defines: [ROOTLINUX]
`)
	module := loadText(t, `
variables: {SHARED: module}
environment:
  compile: {defines: [MODULE]}
  configurations:
    debug: {symbols: true}
targets:
  executables:
    - {name: app, language: c, sources: [main.c]}
`)
	moduleBefore, _ := json.Marshal(module)
	rootBefore, _ := json.Marshal(root)
	inherited := Inherit(root, module)
	s, err := inherited.Settings(linuxDebug, gccMappings())
	if err != nil {
		t.Fatal(err)
	}
	if s.CStandard != "c11" {
		t.Errorf("c_standard = %q, want the root's c11", s.CStandard)
	}
	if want := []string{"-g", "-Wall", "-Wextra", "-froot"}; !reflect.DeepEqual(s.Flags, want) {
		t.Errorf("flags = %q, want %q", s.Flags, want)
	}
	if want := []string{"ROOT", "MODULE", "ROOTLINUX", "ROOTDEBUG"}; !reflect.DeepEqual(s.Defines, want) {
		t.Errorf("defines = %q, want %q", s.Defines, want)
	}
	if want := []string{"-lroot"}; !reflect.DeepEqual(s.LinkFlags, want) {
		t.Errorf("link flags = %q", s.LinkFlags)
	}
	if inherited.Variables.Values["APPDEF"] != "from_root" || inherited.Variables.Values["SHARED"] != "module" {
		t.Errorf("variables = %v", inherited.Variables.Values)
	}
	// Neither loaded config changed, even when the result is
	*inherited.Environment.Compile.Defines[0].Value = "CHANGED"
	moduleAfter, _ := json.Marshal(module)
	rootAfter, _ := json.Marshal(root)
	if string(moduleAfter) != string(moduleBefore) || string(rootAfter) != string(rootBefore) {
		t.Error("Inherit changed a loaded config")
	}
}

func TestPlatformTargetOverride(t *testing.T) {
	f := loadText(t, `
targets:
  shared_libraries:
    - name: engine_platform
      language: cpp
      sources: [src/common/*.cpp]
      libs: [engine_core]
      compile: {defines: [EXPORTS]}
platforms:
  linux:
    targets:
      shared_libraries:
        - name: engine_platform
          sources: [src/common/*.cpp, src/linux/*.cpp]
          compile: {defines: [LINUX]}
`)
	base := f.Targets.SharedLibraries[0]
	got := base.WithOverride(f.Platforms["linux"].Targets.SharedLibraries[0])
	var sources []string
	for _, s := range got.Sources.Resolve(linuxDebug) {
		sources = append(sources, s.Pattern)
	}
	if want := []string{"src/common/*.cpp", "src/linux/*.cpp"}; !reflect.DeepEqual(sources, want) {
		t.Errorf("sources = %q (an override replaces)", sources)
	}
	if want := []string{"EXPORTS", "LINUX"}; !reflect.DeepEqual(got.Compile.Defines.Resolve(linuxDebug), want) {
		t.Errorf("compile.defines = %q (compile merges)", got.Compile.Defines.Resolve(linuxDebug))
	}
	if got.Language != "cpp" || !reflect.DeepEqual(got.Libs.Resolve(linuxDebug), []string{"engine_core"}) {
		t.Errorf("fields the override doesn't set changed: %+v", got)
	}
	if len(base.Sources) != 1 {
		t.Error("the override changed the loaded target")
	}
}

func TestBuildyFileValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buildy.yaml")
	if err := os.WriteFile(path, []byte(`
library: {}
project: {name: p, version: 1.0}
variables:
  import_env_vars:
    - name: NOPE
environment:
  toolchains: {linxu: gcc-cpp-linux}
platforms:
  macos: {}
  darwin: {}
targets:
  executables:
    - {name: app, language: cpp}
    - {name: tool, language: go}
    - {name: x}
platforms:
  linux:
    targets:
      executables: [{name: nosuch}]
install:
  - {name: pkg, format: rar}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path, true)
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{
		"'library' is not supported: define targets under targets.static_libraries or targets.shared_libraries",
		`'project.version' must be a string, not a number (quote it: "1.0")`,
		"variables.import_env_vars[0] (NOPE) needs 'default'",
		"environment.toolchains: 'linxu' is not one of: default, linux, windows, macos, wasm (did you mean 'linux'?)",
		"duplicate key 'platforms'",
		"targets.executables[0] (app) is missing required 'sources'",
		"targets.executables[1] (tool) is missing required 'path'",
		"targets.executables[2] (x) is missing required 'language'",
		"install[0] (pkg): format 'rar' is not one of: tar.gz, tgz, zip, tar",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q\ndoes not mention %q", err, want)
		}
	}
}

func TestPlatformOverrideOfUnknownTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buildy.yaml")
	if err := os.WriteFile(path, []byte(`
platforms:
  darwin:
    targets:
      executables: [{name: nosuch}]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path, false)
	if err == nil || !strings.Contains(err.Error(), "platforms.macos.targets.executables[0] overrides 'nosuch', which is not one of the config's targets.executables") {
		t.Errorf("err = %v", err)
	}
}

// Finding 1.6: the documented optional-source form is a source, not a filter map.
func TestOptionalSources(t *testing.T) {
	f := loadText(t, `
targets:
  static_libraries:
    - name: lib
      language: c
      sources:
        - src/*.c
        - {pattern: "src/plugins/*.c", optional: true}
        - linux: [{pattern: "src/linux/*.c", optional: true}, src/posix.c]
`)
	got := f.Targets.StaticLibraries[0].Sources.Resolve(linuxDebug)
	want := []Source{{"src/*.c", false}, {"src/plugins/*.c", true}, {"src/linux/*.c", true}, {"src/posix.c", false}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sources = %+v, want %+v", got, want)
	}
	if len(f.FilterKeys) != 1 || f.FilterKeys[0].Name != "linux" {
		t.Errorf("filter keys %+v", f.FilterKeys)
	}
}

func TestCheckKeywords(t *testing.T) {
	f := loadText(t, "environment:\n  compile: {optimisation: full, cpp_standrd: c++17, vectorization: avx2}\n")
	problems := CheckKeywords(f.Keywords, []string{"optimization", "symbols", "vectorization", "warnings"})
	joined := strings.Join(problems, "\n")
	if len(problems) != 2 ||
		!strings.Contains(joined, "unknown key 'environment.compile.optimisation' (did you mean 'optimization'?)") ||
		!strings.Contains(joined, "unknown key 'environment.compile.cpp_standrd' (did you mean 'cpp_standard'?)") {
		t.Errorf("problems: %q", problems)
	}
}
