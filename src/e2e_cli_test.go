package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// --version prints the version alone, outside any workspace: CI compares it with a release tag.
func TestE2EVersion(t *testing.T) {
	out := mustBuildy(t, t.TempDir(), "--version")
	if want := "buildy " + Version + " ("; !strings.HasPrefix(out, want) || strings.Count(out, "\n") != 1 {
		t.Errorf("buildy --version printed %q, want one line starting %q", out, want)
	}
}

// --list-toolchains prints its table at the default log level, outside any workspace too, and
// inside one lists the workspace's own toolchains.
func TestE2EListToolchains(t *testing.T) {
	out := mustBuildy(t, t.TempDir(), "--list-toolchains")
	containsAll(t, "buildy --list-toolchains outside a workspace", out, "NAME", "gcc-cpp-linux", "msvc-cpp-windows", "rust-wasm")

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "buildy.yaml"), "project:\n  name: listed\n")
	writeFile(t, filepath.Join(dir, "buildy_config", "toolchains", "mine.yaml"), `toolchain:
  name: my-converter
  description: "A converter of the workspace's own"
  target: {platform: any, architecture: any}
  tools:
    convert:
      action: transform
      command: "convert ${input} ${output}"
      input_extensions: [".in"]
      output_extension: ".out"
`)
	out = mustBuildy(t, dir, "--list-toolchains", "-n", "1")
	containsAll(t, "buildy --list-toolchains -n 1 in a workspace", out, "gcc-cpp-linux", "my-converter", "A converter of the workspace's own")
}

// --cache-stats prints at the default log level.
func TestE2ECacheStats(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "buildy.yaml"), "project:\n  name: stats\n")
	out := mustBuildy(t, dir, "--cache-stats", "-n", "1")
	containsAll(t, "buildy --cache-stats -n 1", out, "Cache directory:", "Entries:")
}

// The user guide's first build: a C++ executable and nothing else, no cpp_standard. The compiler
// builds with its own default standard.
func TestE2EFirstBuild(t *testing.T) {
	needCpp(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "buildy.yaml"), `project:
  name: hello
  version: "1.0.0"

targets:
  executables:
    - name: hello
      language: cpp
      sources: ["src/*.cpp"]
`)
	writeFile(t, filepath.Join(dir, "src", "main.cpp"), "#include <iostream>\nint main() { std::cout << \"hello\\n\"; }\n")
	mustBuildy(t, dir)
	if got := run(t, filepath.Join(outDir(dir, "debug"), "bin", "hello")); got != "hello" {
		t.Errorf("hello printed %q", got)
	}
}
