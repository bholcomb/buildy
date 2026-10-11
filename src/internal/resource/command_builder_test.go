package resource

import (
	"strings"
	"testing"

	"buildy/internal/config"
)

func builtinToolchain(t *testing.T, name string) *ToolchainConfig {
	t.Helper()
	tm, err := NewToolchainManagerMulti(nil)
	if err != nil {
		t.Fatal(err)
	}
	tc := tm.GetToolchain(name)
	if tc == nil {
		t.Fatalf("no toolchain %s", name)
	}
	return tc
}

func argv(args []string) string {
	return strings.Join(args, " | ")
}

// The built-in toolchains' commands become argument lists: a path with spaces, a define with
// quotes, the dependency file's arguments, and $ORIGIN each stay what they are.
func TestBuildCommandGivesArguments(t *testing.T) {
	gcc := builtinToolchain(t, "gcc-cpp-linux")
	tool, err := gcc.ToolFor("main.cpp", "compile")
	if err != nil {
		t.Fatal(err)
	}
	cb := NewCommandBuilder(gcc, nil, "linux", "debug")
	cmd, depFile, _, err := cb.BuildCommand(tool, "/my ws/main.cpp", "/my ws/out/main.o",
		[]string{`MSG="hello world"`}, []string{"/my ws/inc dir"}, []string{"-Wall"},
		map[string]any{"std": "c++17", "is_shared_library": true})
	if err != nil {
		t.Fatal(err)
	}
	want := `g++ | -MMD | -MP | -MF | /my ws/out/main.d | -std=c++17 | -Wall | -DMSG="hello world" | -fPIC | -I/my ws/inc dir | -c | /my ws/main.cpp | -o | /my ws/out/main.o`
	if got := argv(cmd.Args); got != want || cmd.Script != "" || depFile != "/my ws/out/main.d" {
		t.Errorf("compile command\n  %s\nwant\n  %s", got, want)
	}

	link := gcc.ToolsWithAction("link")
	var exe *Tool
	for _, l := range link {
		if l.Supports.OutputType == "executable" {
			exe = l
		}
	}
	cmd, _, err = cb.BuildLinkCommand(exe, []string{"/my ws/a.o", "/my ws/b.o"}, "/my ws/bin/app",
		[]string{"/my ws/lib"}, []string{"calc", "m"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = `g++ | -Wl,-rpath,$ORIGIN | /my ws/a.o | /my ws/b.o | -L/my ws/lib | -lcalc | -lm | -o | /my ws/bin/app`
	if got := argv(cmd.Args); got != want {
		t.Errorf("link command\n  %s\nwant\n  %s", got, want)
	}

	if _, _, _, err := cb.BuildCommand(tool, "/a.cpp", "/a.o", nil, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "${std}") || !strings.Contains(err.Error(), "cpp_standard") {
		t.Errorf("a compile without a standard: %v", err)
	}
}

// MSVC's placeholders inside words (/Fo${output}, /OUT:${output}) stay one argument each, and a
// framework flag ending in a space is an argument of its own.
func TestBuildCommandWordsAndSeparateFlags(t *testing.T) {
	msvc := builtinToolchain(t, "msvc-cpp-windows")
	tool, err := msvc.ToolFor("main.cpp", "compile")
	if err != nil {
		t.Fatal(err)
	}
	cb := NewCommandBuilder(msvc, nil, "windows", "release")
	cmd, _, _, err := cb.BuildCommand(tool, `C:\my ws\main.cpp`, `C:\my ws\out\main.obj`, nil, nil, nil, map[string]any{"std": "c++17"})
	if err != nil {
		t.Fatal(err)
	}
	want := `cl.exe | /sourceDependencies | C:\my ws\out\main.json | /std:c++17 | /nologo | /EHsc | /c | C:\my ws\main.cpp | /FoC:\my ws\out\main.obj`
	if got := argv(cmd.Args); got != want {
		t.Errorf("cl command\n  %s\nwant\n  %s", got, want)
	}

	var link *Tool
	for _, l := range msvc.ToolsWithAction("link") {
		if l.Supports.OutputType == "executable" {
			link = l
		}
	}
	cmd, _, err = cb.BuildLinkCommand(link, []string{`C:\ws\a.obj`}, `C:\ws\bin\app.exe`, []string{`C:\my libs`}, []string{"glfw3", "user32.lib"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = `link.exe | /nologo | C:\ws\a.obj | /LIBPATH:C:\my libs | glfw3.lib | user32.lib | /OUT:C:\ws\bin\app.exe`
	if got := argv(cmd.Args); got != want {
		t.Errorf("link command\n  %s\nwant\n  %s", got, want)
	}

	clang := builtinToolchain(t, "clang-c-macos-arm64")
	var exe *Tool
	for _, l := range clang.ToolsWithAction("link") {
		if l.Supports.OutputType == "executable" {
			exe = l
		}
	}
	cmd, _, err = NewCommandBuilder(clang, nil, "macos", "debug").BuildLinkCommand(exe, []string{"/a.o"}, "/app", nil, nil, []string{"Cocoa", "IOKit"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := argv(cmd.Args); !strings.Contains(got, "-framework | Cocoa | -framework | IOKit") {
		t.Errorf("frameworks are not separate arguments: %s", got)
	}
}

// A tool with shell: true runs its command as a script, every value quoted for the shell.
func TestBuildCommandShellTool(t *testing.T) {
	tc, err := config.LoadToolchain([]byte(`toolchain:
  name: gen
  tools:
    compile:
      action: compile
      shell: true
      command: "gen ${flags} ${input} | tee ${output} > /dev/null"
      input_extensions: [".x"]
`), "gen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tool, _ := tc.ToolFor("a.x", "compile")
	cmd, _, _, err := NewCommandBuilder(tc, nil, "linux", "debug").BuildCommand(tool, "/my ws/it's.x", "/out/a.o", nil, nil, []string{"-v"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := `gen -v '/my ws/it'\''s.x' | tee /out/a.o > /dev/null`; cmd.Script != want || cmd.Args != nil {
		t.Errorf("script %q, want %q", cmd.Script, want)
	}

	_, err = config.LoadToolchain([]byte(`toolchain:
  name: gen
  tools:
    compile:
      action: compile
      shell: true
      command: "gen \"${input}\""
`), "gen.yaml")
	if err == nil || !strings.Contains(err.Error(), "outside quotes") {
		t.Errorf("a quoted placeholder in a shell tool: %v", err)
	}
}

// Command parameters give arguments: "-ldflags ${value}"-style formats are several words, and a
// quoted ${value} is one argument whatever it holds.
func TestCommandParamsGiveArguments(t *testing.T) {
	goTC := builtinToolchain(t, "go-linux")
	tool := goTC.ToolsWithAction("build")[0]
	params, err := ResolveCommandParams(tool, map[string]any{"ldflags": []string{"-s", "-X main.v=1 2"}, "build_tags": []string{"a", "b"}}, nil, "debug",
		func(s string) (string, error) { return s, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := argv(params["ldflags"]); got != "-ldflags | -s -X main.v=1 2" {
		t.Errorf("ldflags %s", got)
	}
	if got := argv(params["build_tags"]); got != "-tags | a,b" {
		t.Errorf("build_tags %s", got)
	}
	if len(params["buildmode"]) != 0 {
		t.Errorf("an unset parameter gave %q", params["buildmode"])
	}
}

// A tool declares how it reads a response file; the declaration reaches its commands.
func TestToolResponseFile(t *testing.T) {
	gcc := builtinToolchain(t, "gcc-c-linux")
	for _, tool := range gcc.ToolsWithAction("compile", "link") {
		if tool.ResponseFile != "gnu" {
			t.Errorf("gcc-c-linux tool %s: response_file %q, want gnu", tool.Name, tool.ResponseFile)
		}
	}
	tool, _ := gcc.ToolFor("a.c", "compile")
	cmd, _, _, err := NewCommandBuilder(gcc, nil, "linux", "debug").BuildCommand(tool, "/a.c", "/a.o", nil, nil, nil, map[string]any{"std": "c11"})
	if err != nil || cmd.ResponseFile != "gnu" {
		t.Errorf("command %+v, %v: no response file syntax", cmd, err)
	}
	for _, bad := range []string{"response_file: rsp", "response_file: gnu\n      shell: true"} {
		_, err := config.LoadToolchain([]byte("toolchain:\n  name: x\n  tools:\n    c:\n      action: compile\n      command: \"cc ${input}\"\n      "+bad+"\n"), "x.yaml")
		if err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// Every macOS toolchain links shared libraries and executables with the target's frameworks
// (GLFW needs Cocoa, IOKit and CoreVideo).
func TestMacOSLinksTakeFrameworks(t *testing.T) {
	for _, name := range []string{"clang-c-macos", "clang-c-macos-arm64", "clang-cpp-macos", "clang-cpp-macos-arm64"} {
		tc := builtinToolchain(t, name)
		for _, link := range tc.ToolsWithAction("link") {
			if link.Supports.OutputType == "static_library" {
				continue
			}
			cmd, _, err := NewCommandBuilder(tc, nil, "macos", "release").BuildLinkCommand(link, []string{"/a.o"}, "/out", []string{"/opt/lib"}, []string{"glfw3"}, []string{"Cocoa", "IOKit"}, nil)
			if err != nil {
				t.Fatalf("%s %s: %v", name, link.Name, err)
			}
			got := argv(cmd.Args)
			if !strings.Contains(got, "-lglfw3 | -framework | Cocoa | -framework | IOKit | -o | /out") {
				t.Errorf("%s %s links %s", name, link.Name, got)
			}
		}
	}
}
