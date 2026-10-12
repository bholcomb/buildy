package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// decodeInto decodes YAML text into v with a fresh decoder and returns the decoder.
func decodeInto(t *testing.T, text string, v any) *decoder {
	t.Helper()
	root, err := parse([]byte(text), "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d := newDecoder("test.yaml")
	d.decode(root, reflect.ValueOf(v).Elem(), "")
	return d
}

func wantProblems(t *testing.T, d *decoder, wants ...string) {
	t.Helper()
	err := d.err("test file")
	if err == nil {
		t.Fatalf("no problems, want %q", wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("problems %q do not mention %q", err, want)
		}
	}
}

func TestStrictDecoding(t *testing.T) {
	type inner struct {
		Name  string   `yaml:"name,required"`
		Flags []string `yaml:"flags"`
		On    bool     `yaml:"on"`
	}
	var v struct {
		Items []inner `yaml:"items"`
		Count int     `yaml:"count"`
	}
	d := decodeInto(t, `
items:
  - name: a
    flgas: [x]
  - name: b
    on: "yes"
  - flags: -x
count: many
count: 2
`, &v)
	wantProblems(t, d,
		"line 4: unknown key 'items[0] (a).flgas' (did you mean 'flags'?)",
		"line 6: 'items[1] (b).on' must be true or false, not a string",
		"line 7: 'items[2]' is missing required 'name'",
		"line 7: 'items[2].flags' must be a list, not a string",
		"line 8: 'count' must be a whole number, not a string",
		"line 9: duplicate key 'count'",
	)
}

func TestNumbersWhereStringsBelongAreErrors(t *testing.T) {
	var v struct {
		Version string `yaml:"version"`
	}
	d := decodeInto(t, "version: 1.0\n", &v)
	wantProblems(t, d, `'version' must be a string, not a number (quote it: "1.0")`)
}

func TestMergeKeysAndAliases(t *testing.T) {
	var v struct {
		Base map[string]string `yaml:"base"`
		Use  map[string]string `yaml:"use"`
	}
	d := decodeInto(t, `
base: &b {a: "1", b: "2"}
use:
  <<: *b
  b: "3"
`, &v)
	if err := d.err("test"); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"a": "1", "b": "3"}; !reflect.DeepEqual(v.Use, want) {
		t.Errorf("use = %v, want %v", v.Use, want)
	}
}

// Finding 1.2: a filter map with several matching keys contributes them in the order written,
// on every run (Go's map order once made 20 runs give 8 different compile commands).
func TestFilteredListKeepsDocumentOrder(t *testing.T) {
	text := `
flags:
  - -first
  - {linux: [-DA], debug: [-DB], x86_64: [-DC], gcc: [-DD]}
  - release: [-DR]
  - zzz: [-DZ]
    linux: [-DL]
    aaa: [-DY]
    debug: [-DG]
`
	ctx := Context{Platform: "linux", Architecture: "x86_64", Configuration: "debug", Toolchain: "gcc"}
	want := []string{"-first", "-DA", "-DB", "-DC", "-DD", "-DL", "-DG"}
	for i := 0; i < 200; i++ {
		var v struct {
			Flags Filtered[string] `yaml:"flags"`
		}
		d := decodeInto(t, text, &v)
		if err := d.err("test"); err != nil {
			t.Fatal(err)
		}
		if got := v.Flags.Resolve(ctx); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: %v, want %v", i, got, want)
		}
	}
}

func TestNestedFiltersAndAliases(t *testing.T) {
	var v struct {
		Flags Filtered[string] `yaml:"flags"`
	}
	d := decodeInto(t, `
flags:
  - debug:
      gcc-cpp-linux: [-fsanitize=address]
      clang-cpp-linux: [-fsanitize=memory]
  - darwin: -mmacos
  - aarch64: [-arm]
`, &v)
	if err := d.err("test"); err != nil {
		t.Fatal(err)
	}
	got := v.Flags.Resolve(Context{Platform: "macos", Architecture: "arm64", Configuration: "debug", Toolchain: "clang-cpp-linux"})
	if want := []string{"-fsanitize=memory", "-mmacos", "-arm"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	var keys []string
	for _, use := range d.filterKeys {
		keys = append(keys, use.Name)
	}
	if want := []string{"debug", "gcc-cpp-linux", "clang-cpp-linux", "macos", "arm64"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("filter keys %v, want %v", keys, want)
	}
}

func TestCheckFilterKeys(t *testing.T) {
	uses := []NameUse{
		{Name: "linxu", Path: "sources[1]", Pos: Pos{"m.yaml", 3}},
		{Name: "linux"}, {Name: "arm64"}, {Name: "portable"}, {Name: "gcc-cpp-linux"},
		{Name: "relase", Path: "defines[0]", Pos: Pos{"m.yaml", 7}},
		{Name: "elsewhere", Path: "libs[0]", Pos: Pos{"m.yaml", 9}},
	}
	problems := CheckFilterKeys(uses, Names{
		Configurations: []string{"debug", "release", "portable"},
		Toolchains:     []string{"gcc-cpp-linux"},
	})
	if len(problems) != 3 {
		t.Fatalf("problems: %q", problems)
	}
	for i, want := range []string{
		"m.yaml:3: filter key 'linxu' in 'sources[1]' is not a platform, architecture, configuration or toolchain (did you mean 'linux'?)",
		"m.yaml:7: filter key 'relase' in 'defines[0]'",
		"(did you mean 'release'?)",
	} {
		if !strings.Contains(strings.Join(problems, "\n"), want) {
			t.Errorf("problem %d: %q does not mention %q", i, problems, want)
		}
	}
}

// Finding 1.5: the merge rules. Nothing the merge returns aliases its inputs, and the inputs are
// unchanged.
func TestMergeRules(t *testing.T) {
	type settings struct {
		Std     string            `yaml:"std"`
		Prefix  *string           `yaml:"prefix"`
		Flags   []string          `yaml:"flags"`
		Vars    map[string]string `yaml:"vars"`
		Ordered Ordered[string]   `yaml:"ordered"`
		Nested  *settings         `yaml:"nested"`
	}
	empty := ""
	base := settings{Std: "c11", Flags: []string{"-a"}, Vars: map[string]string{"x": "1", "y": "2"},
		Nested: &settings{Flags: []string{"-n1"}}}
	base.Ordered.Set("k1", "b1")
	base.Ordered.Set("k2", "b2")
	over := settings{Prefix: &empty, Flags: []string{"-b"}, Vars: map[string]string{"y": "3"},
		Nested: &settings{Std: "c17", Flags: []string{"-n2"}}}
	over.Ordered.Set("k3", "o3")
	over.Ordered.Set("k1", "o1")

	merged := Merge(base, over)
	if merged.Std != "c11" || merged.Prefix == nil || *merged.Prefix != "" {
		t.Errorf("scalars: %+v", merged)
	}
	if !reflect.DeepEqual(merged.Flags, []string{"-a", "-b"}) {
		t.Errorf("lists append: %v", merged.Flags)
	}
	if !reflect.DeepEqual(merged.Vars, map[string]string{"x": "1", "y": "3"}) {
		t.Errorf("maps merge key by key: %v", merged.Vars)
	}
	if !reflect.DeepEqual(merged.Ordered.Keys, []string{"k1", "k2", "k3"}) || merged.Ordered.Values["k1"] != "o1" {
		t.Errorf("ordered maps: %+v", merged.Ordered)
	}
	if merged.Nested.Std != "c17" || !reflect.DeepEqual(merged.Nested.Flags, []string{"-n1", "-n2"}) {
		t.Errorf("nested: %+v", merged.Nested)
	}

	merged.Flags[0] = "changed"
	merged.Vars["x"] = "changed"
	merged.Nested.Flags[0] = "changed"
	if base.Flags[0] != "-a" || base.Vars["x"] != "1" || base.Nested.Flags[0] != "-n1" {
		t.Errorf("the merge result aliases its input: %+v", base)
	}
	if len(over.Flags) != 1 || len(base.Nested.Flags) != 1 {
		t.Errorf("the merge changed its input")
	}
}

func TestToolchainValidation(t *testing.T) {
	_, err := LoadToolchain([]byte(`
toolchain:
  name: bad
  target: {platform: linxu, architecture: amd64}
  tools:
    one:
      action: link
      command: "ld"
    two:
      action: compile
      command: {linux: cc, beos: cc}
      input_extensions: [.c]
    three:
      action: compile
      command: cc
      input_extensions: [.c]
    four:
      action: complie
      command: x
`), "bad.yaml")
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{
		"toolchain.target.platform 'linxu' is not one of",
		"line 7: toolchain.tools.one is a link tool: supports.output_type must be one of",
		"'toolchain.tools.two.command.beos': 'beos' is not a platform",
		"tools two and three both compile '.c' files",
		"toolchain.tools.four.action 'complie' is not one of",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not mention %q", err, want)
		}
	}
}

func TestToolchainArchitectureAliases(t *testing.T) {
	tc, err := LoadToolchain([]byte("toolchain:\n  name: x\n  target: {platform: darwin, architecture: aarch64}\n"), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if tc.Target.Platform != "macos" || tc.Target.Architecture != "arm64" {
		t.Errorf("target = %+v", tc.Target)
	}
	if tc.Execution.Type != "native" {
		t.Errorf("execution type %q", tc.Execution.Type)
	}
}

// A transform toolchain with one tool per extension picks the tool for the file, never a tool
// at random (Monstro's glslangValidator has three).
func TestToolForFile(t *testing.T) {
	tc, err := LoadToolchain([]byte(`
toolchain:
  name: shaders
  tools:
    vert: {action: transform, command: "v ${input}", input_extensions: [.vert]}
    frag: {action: transform, command: "f ${input}", input_extensions: [.frag]}
`), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{"a.vert": "vert", "b.frag": "frag"} {
		tool, err := tc.ToolFor(file, "transform")
		if err != nil || tool.Name != want {
			t.Errorf("ToolFor(%s) = %v, %v; want %s", file, tool, err, want)
		}
	}
	if _, err := tc.ToolFor("c.comp", "transform"); err == nil {
		t.Error("a file no tool takes found a tool")
	}
}

func TestTemplateValidation(t *testing.T) {
	_, err := LoadTemplates([]byte(`
templates:
  t:
    metadata: {language: cpp, target_types: [program]}
    steps:
      - {name: s, action: compile, for_each: file, tool_params: {x: {a: b}}}
`), "t.yaml")
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{
		"templates.t.metadata.target_types: 'program' is not one of",
		"for_each 'file' is not supported",
		"'templates.t.steps[0] (s).tool_params.x' must be a string, a list of strings or a boolean",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not mention %q", err, want)
		}
	}
}

func TestBuildSystemValidation(t *testing.T) {
	_, err := LoadBuildSystem([]byte(`
build_system:
  name: x
  phases:
    biuld: {command: make}
`), "b.yaml")
	if err == nil || !strings.Contains(err.Error(), "unknown key 'build_system.phases.biuld' (did you mean 'build'?)") ||
		!strings.Contains(err.Error(), "no build phase") {
		t.Errorf("err = %v", err)
	}
}

// An in-source build system needs a clean phase, and only it may have one; a configuration
// mapping has values.
func TestBuildSystemPhases(t *testing.T) {
	for _, c := range []struct{ yaml, want string }{
		{"in_source: true\n  phases: {build: {command: make}}", "in_source needs a clean phase"},
		{"phases: {build: {command: make}, clean: {command: make clean}}", "clean is run only for an in_source build system"},
		{"phases: {build: {command: make}, test: {command: make check}}", "unknown key 'build_system.phases.test'"},
		{"configurations: {debug: \"\"}\n  phases: {build: {command: make}}", "configurations.debug is empty"},
	} {
		_, err := LoadBuildSystem([]byte("build_system:\n  name: x\n  "+c.yaml+"\n"), "b.yaml")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.yaml, err, c.want)
		}
	}
}

// A definition's placeholders are checked when it loads: an unknown one, ${build_type} without
// configurations, ${args} in a path and placeholders in default_args are errors naming where.
func TestBuildSystemPlaceholders(t *testing.T) {
	for _, c := range []struct{ yaml, want string }{
		{"phases: {build: {command: \"make ${prefix}\"}}", "'build_system.phases.build.command' has ${prefix}: a build system's placeholders are ${source_dir}"},
		{"phases: {build: {command: make, working_dir: \"${src_dir}\"}}", "'build_system.phases.build.working_dir' has ${src_dir}"},
		{"phases: {build: {command: make}}\n  output: {lib_patterns: [\"${dep_dir}/lib\"]}", "'build_system.output.lib_patterns[0]' has ${dep_dir}"},
		{"phases: {build: {command: \"make BT=${build_type}\"}}", "has ${build_type}, but the build system has no configurations"},
		{"phases: {build: {command: make, working_dir: \"${args}\"}}", "'build_system.phases.build.working_dir' has ${args}: it is a list"},
		{"phases: {build: {command: make, default_args: [\"X=${config}\"]}}", "'build_system.phases.build.default_args[0]' is \"X=${config}\": default arguments are passed as written"},
		{"phases: {build: {command: \"make ${\"}}", "unterminated placeholder"},
		{"configurations: {}\n  phases: {build: {command: make}}", "build_system.configurations is empty"},
	} {
		_, err := LoadBuildSystem([]byte("build_system:\n  name: x\n  "+c.yaml+"\n"), "b.yaml")
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "b.yaml") {
			t.Errorf("%s: err = %v, want %q", c.yaml, err, c.want)
		}
	}
	for _, name := range []string{"auto", "none", "\"\""} {
		_, err := LoadBuildSystem([]byte("build_system:\n  name: "+name+"\n  phases: {build: {command: make}}\n"), "b.yaml")
		if err == nil || !strings.Contains(err.Error(), "build_system.name") {
			t.Errorf("name %s: err = %v", name, err)
		}
	}
	if _, err := LoadBuildSystem([]byte("build_system:\n  phases: {build: {command: make}}\n"), "b.yaml"); err == nil || !strings.Contains(err.Error(), "missing required 'name'") {
		t.Errorf("no name: err = %v", err)
	}

	// Every placeholder, in a command; ${build_type} with configurations
	all := "${" + strings.Join(BuildSystemPlaceholders, "} ${") + "}"
	bs, err := LoadBuildSystem([]byte("build_system:\n  name: x\n  configurations: {debug: d}\n  phases: {build: {command: \"tool "+all+"\"}}\n"), "b.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := bs.Phases["build"].Command.Placeholders(); len(got) != len(BuildSystemPlaceholders) {
		t.Errorf("placeholders = %v", got)
	}
}

// A filtered list of one item may be written as the item, as before.
func TestFilteredListOfOne(t *testing.T) {
	var v struct {
		Sources Filtered[Source] `yaml:"sources"`
		Defines Filtered[string] `yaml:"defines"`
		Libs    Filtered[string] `yaml:"libs"`
	}
	d := decodeInto(t, "sources: main.c\ndefines: {pattern: x}\nlibs: {linux: [m]}\n", &v)
	if got := v.Sources.Resolve(Context{}); len(got) != 1 || got[0].Pattern != "main.c" {
		t.Errorf("sources = %+v", got)
	}
	wantProblems(t, d, "'defines' must be a list, not a mapping", "'libs' must be a list, not a mapping")
}

// A discover or exclude pattern that can't match is an error when the config loads, not a warning
// while discovering (which skipped it).
func TestWorkspacePatternsAreChecked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buildy.yaml")
	if err := os.WriteFile(path, []byte("project: {name: p}\nworkspace:\n  exclude: [\"third_party/[a\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for what, load := range map[string]func() error{
		"LoadFile":             func() error { _, err := LoadFile(path, true); return err },
		"LoadWorkspaceSection": func() error { _, err := LoadWorkspaceSection(path); return err },
	} {
		if err := load(); err == nil || !strings.Contains(err.Error(), "workspace.exclude: 'third_party/[a' is not a valid pattern") {
			t.Errorf("%s: got %v", what, err)
		}
	}
}

// The Go and Rust supports flags and the command parameters' optional and resolve_variables were
// read by nothing; they are gone, and a toolchain that still has one fails to load.
func TestRemovedBuildToolKeysAreErrors(t *testing.T) {
	for _, key := range []string{
		"supports: {build_tags: true}", "supports: {build_tags_flag: -tags}", "supports: {ldflags: true}",
		"supports: {features_flag: --features}", "supports: {buildmode: true}",
		"command_params: {tags: {sources: [item.build_tags], optional: true}}",
		"command_params: {tags: {sources: [item.build_tags], resolve_variables: true}}",
	} {
		_, err := LoadToolchain([]byte("toolchain:\n  name: x\n  tools:\n    b:\n      action: build\n      command: \"go build\"\n      "+key+"\n"), "x.yaml")
		if err == nil || !strings.Contains(err.Error(), "unknown key") {
			t.Errorf("%s: %v, want an unknown key", key, err)
		}
	}
}

// A target key its language doesn't read is an error, not ignored: a Go target takes the Go keys,
// a Rust target the Rust keys, other targets neither; a Go library's build mode is its type's.
func TestTargetKeysOfAnotherLanguageAreErrors(t *testing.T) {
	for _, c := range []struct{ yaml, want string }{
		{"executables: [{name: a, language: go, path: ., features: [x]}]", "a go target has no 'features' (it takes: name, language, depends_on, path, build_tags, ldflags, gcflags, buildmode)"},
		{"executables: [{name: a, language: go, path: ., bin: x}]", "a go target has no 'bin'"},
		{"executables: [{name: a, language: go, path: ., sources: [a.go]}]", "a go target has no 'sources'"},
		{"executables: [{name: a, language: go, path: ., flags: [-race]}]", "a go target has no 'flags'"},
		{"executables: [{name: a, language: go, path: ., defines: [A]}]", "a go target has no 'defines'"},
		{"executables: [{name: a, language: go, path: ., output_prefix: x}]", "a go target has no 'output_prefix'"},
		{"executables: [{name: a, language: go, path: ., compile: {flags: [-x]}}]", "a go target has no 'compile'"},
		{"executables: [{name: a, language: rust, path: ., build_tags: [x]}]", "a rust target has no 'build_tags' (it takes: name, language, depends_on, path, features, bin)"},
		{"executables: [{name: a, language: rust, path: ., libs: [m]}]", "a rust target has no 'libs'"},
		{"executables: [{name: a, language: cpp, sources: [a.cpp], ldflags: [-s]}]", "'ldflags' is a key of go targets, not of cpp targets"},
		{"executables: [{name: a, language: c, sources: [a.c], features: [x]}]", "'features' is a key of rust targets, not of c targets"},
		{"executables: [{name: a, language: go, path: ., buildmode: c-shared}]", "buildmode 'c-shared' is not one of: exe, pie"},
		{"shared_libraries: [{name: a, language: go, path: ., buildmode: c-archive}]", "a Go shared library's build mode is its type's"},
		{"static_libraries: [{name: a, language: go, path: ., buildmode: c-archive}]", "a Go static library's build mode is its type's"},
		{"static_libraries: [{name: a, language: rust, path: ., bin: a}]", "'bin' names an executable's binary"},
	} {
		path := filepath.Join(t.TempDir(), "buildy.yaml")
		if err := os.WriteFile(path, []byte("targets:\n  "+c.yaml+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadFile(path, false)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s:\n got %v\nwant %s", c.yaml, err, c.want)
		}
	}

	// A platform's override of a Go target is checked against Go's keys
	path := filepath.Join(t.TempDir(), "buildy.yaml")
	if err := os.WriteFile(path, []byte(`targets:
  executables: [{name: a, language: go, path: ., build_tags: [x], buildmode: pie}]
platforms:
  linux:
    targets:
      executables: [{name: a, build_tags: [y], features: [z]}]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path, false)
	if err == nil || !strings.Contains(err.Error(), "platforms.linux.targets.executables[0] (a): a go target has no 'features'") || strings.Contains(err.Error(), "has no 'build_tags'") {
		t.Errorf("override: %v", err)
	}
}
