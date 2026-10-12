package resource

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"buildy/internal/config"
)

func newEmbeddedToolchainManager(t *testing.T) *ToolchainManager {
	t.Helper()
	tm, err := NewToolchainManagerMulti([]string{t.TempDir()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return tm
}

// The default toolchains, for every platform and architecture, are those buildy chose before
// priorities moved into the toolchain files (from lists by name in Go, then the first by name). A
// platform and architecture not listed has none.
func TestDefaultToolchains(t *testing.T) {
	tm := newEmbeddedToolchainManager(t)
	auto := map[[2]string]string{}
	for _, c := range []struct{ platform, arch, want string }{
		{"linux", "x86_64", "gcc-cpp-linux"},
		{"linux", "arm", "gcc-c-arm-cross"},
		{"windows", "x86_64", "msvc-cpp-windows"},
		{"macos", "x86_64", "clang-cpp-macos"},
		{"macos", "arm64", "clang-cpp-macos-arm64"},
		{"wasm", "wasm32", "emscripten-c"},
	} {
		auto[[2]string{c.platform, c.arch}] = c.want
	}
	byLanguage := map[[3]string]string{}
	for _, c := range []struct{ platform, arch, language, want string }{
		{"linux", "x86_64", "c", "gcc-c-linux"},
		{"linux", "x86_64", "cpp", "gcc-cpp-linux"},
		{"linux", "x86_64", "go", "go-linux"},
		{"linux", "x86_64", "rust", "rust-linux"},
		{"linux", "x86_64", "glsl", "glslc"},
		{"linux", "x86_64", "docs", "doxygen"},
		{"linux", "x86", "glsl", "glslc"},
		{"linux", "x86", "docs", "doxygen"},
		{"linux", "arm64", "glsl", "glslc"},
		{"linux", "arm64", "docs", "doxygen"},
		{"linux", "arm", "c", "gcc-c-arm-cross"},
		{"linux", "arm", "cpp", "gcc-cpp-arm-cross"},
		{"linux", "arm", "glsl", "glslc"},
		{"linux", "arm", "docs", "doxygen"},
		{"linux", "wasm32", "glsl", "glslc"},
		{"linux", "wasm32", "docs", "doxygen"},
		{"windows", "x86_64", "c", "msvc-c-windows"},
		{"windows", "x86_64", "cpp", "msvc-cpp-windows"},
		{"windows", "x86_64", "go", "go-windows"},
		{"windows", "x86_64", "rust", "rust-windows"},
		{"windows", "x86_64", "glsl", "glslc"},
		{"windows", "x86_64", "docs", "doxygen"},
		{"windows", "x86", "glsl", "glslc"},
		{"windows", "x86", "docs", "doxygen"},
		{"windows", "arm64", "glsl", "glslc"},
		{"windows", "arm64", "docs", "doxygen"},
		{"windows", "arm", "glsl", "glslc"},
		{"windows", "arm", "docs", "doxygen"},
		{"windows", "wasm32", "glsl", "glslc"},
		{"windows", "wasm32", "docs", "doxygen"},
		{"macos", "x86_64", "c", "clang-c-macos"},
		{"macos", "x86_64", "cpp", "clang-cpp-macos"},
		{"macos", "x86_64", "go", "go-macos"},
		{"macos", "x86_64", "rust", "rust-macos"},
		{"macos", "x86_64", "glsl", "glslc"},
		{"macos", "x86_64", "docs", "doxygen"},
		{"macos", "x86", "glsl", "glslc"},
		{"macos", "x86", "docs", "doxygen"},
		{"macos", "arm64", "c", "clang-c-macos-arm64"},
		{"macos", "arm64", "cpp", "clang-cpp-macos-arm64"},
		{"macos", "arm64", "go", "go-macos-arm64"},
		{"macos", "arm64", "rust", "rust-macos-arm64"},
		{"macos", "arm64", "glsl", "glslc"},
		{"macos", "arm64", "docs", "doxygen"},
		{"macos", "arm", "glsl", "glslc"},
		{"macos", "arm", "docs", "doxygen"},
		{"macos", "wasm32", "glsl", "glslc"},
		{"macos", "wasm32", "docs", "doxygen"},
		{"wasm", "x86_64", "glsl", "glslc"},
		{"wasm", "x86_64", "docs", "doxygen"},
		{"wasm", "x86", "glsl", "glslc"},
		{"wasm", "x86", "docs", "doxygen"},
		{"wasm", "arm64", "glsl", "glslc"},
		{"wasm", "arm64", "docs", "doxygen"},
		{"wasm", "arm", "glsl", "glslc"},
		{"wasm", "arm", "docs", "doxygen"},
		{"wasm", "wasm32", "c", "emscripten-c"},
		{"wasm", "wasm32", "cpp", "emscripten-cpp"},
		{"wasm", "wasm32", "rust", "rust-wasm"},
		{"wasm", "wasm32", "glsl", "glslc"},
		{"wasm", "wasm32", "docs", "doxygen"},
	} {
		byLanguage[[3]string{c.platform, c.arch, c.language}] = c.want
	}

	name := func(tc *ToolchainConfig) string {
		if tc == nil {
			return ""
		}
		return tc.Name
	}
	for _, platform := range config.Platforms {
		for _, arch := range config.Architectures {
			tc, err := tm.AutoDetect(platform, arch)
			if err != nil || name(tc) != auto[[2]string{platform, arch}] {
				t.Errorf("AutoDetect(%s, %s) = %q, %v; want %q", platform, arch, name(tc), err, auto[[2]string{platform, arch}])
			}
			for _, language := range []string{"c", "cpp", "go", "rust", "glsl", "docs"} {
				want := byLanguage[[3]string{platform, arch, language}]
				tc, err := tm.FindByLanguage(language, platform, arch)
				if err != nil || name(tc) != want {
					t.Errorf("FindByLanguage(%s, %s, %s) = %q, %v; want %q", language, platform, arch, name(tc), err, want)
				}
			}
		}
	}
}

// A toolchain of the workspace's with a higher priority becomes the default; two with the highest
// priority are an error that names both.
func TestToolchainPriority(t *testing.T) {
	toolchain := func(name string, priority int) string {
		return fmt.Sprintf("toolchain:\n  name: %s\n  language: cpp\n  target: {platform: linux, architecture: x86_64}\n  priority: %d\n", name, priority)
	}
	load := func(files map[string]string) *ToolchainManager {
		dir := t.TempDir()
		for name, data := range files {
			if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		tm, err := NewToolchainManagerMulti([]string{dir})
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}

	tm := load(map[string]string{"mine": toolchain("my-gcc", 50)})
	if tc, err := tm.AutoDetect("linux", "x86_64"); err != nil || tc.Name != "my-gcc" {
		t.Errorf("AutoDetect: %v, %v; want my-gcc", tc, err)
	}
	if tc, err := tm.FindByLanguage("cpp", "linux", "x86_64"); err != nil || tc.Name != "my-gcc" {
		t.Errorf("FindByLanguage: %v, %v; want my-gcc", tc, err)
	}

	tm = load(map[string]string{"mine": toolchain("my-gcc", 40)})
	for what, call := range map[string]func() (*ToolchainConfig, error){
		"AutoDetect":     func() (*ToolchainConfig, error) { return tm.AutoDetect("linux", "x86_64") },
		"FindByLanguage": func() (*ToolchainConfig, error) { return tm.FindByLanguage("cpp", "linux", "x86_64") },
	} {
		_, err := call()
		if err == nil || !strings.Contains(err.Error(), "gcc-cpp-linux") || !strings.Contains(err.Error(), "my-gcc") || !strings.Contains(err.Error(), "priority 40") {
			t.Errorf("%s with two of priority 40: %v", what, err)
		}
	}
}

func TestExecSpecFromToolchain(t *testing.T) {
	tm, err := NewToolchainManagerMulti(nil)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := ExecSpecFromToolchain(tm.GetToolchain("docker-cpp-linux"))
	if err != nil {
		t.Fatal(err)
	}
	if !spec.IsDocker() || spec.Image != "gcc:13" {
		t.Errorf("docker-cpp-linux: %+v", spec)
	}
	spec, err = ExecSpecFromToolchain(tm.GetToolchain("gcc-cpp-linux"))
	if err != nil || spec.IsDocker() {
		t.Errorf("gcc-cpp-linux: %+v, %v", spec, err)
	}

	for _, bad := range []string{
		"{type: docker, image: x, working_dir: /workspace}",
		"{type: docker}",
		"{type: vm}",
		"{type: native, colour: red}",
	} {
		data := "toolchain:\n  name: bad\n  execution: " + bad + "\n"
		if _, err := config.LoadToolchain([]byte(data), "bad.yaml"); err == nil {
			t.Errorf("execution %v was accepted", bad)
		}
	}
}
