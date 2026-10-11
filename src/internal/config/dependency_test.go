package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestDependencyDefinitions(t *testing.T) {
	deps, err := LoadDependencies([]byte(`
glfw:
  version: "3.4"
  defines: [TOP]
  common: {defines: [COMMON], include_dirs: [inc]}
  darwin: {root: /opt/homebrew, defines: [MAC]}
  macos-aarch64: {lib_dirs: [/opt/homebrew/lib]}
  linux:
    url: https://example.com/glfw.zip
    build: {system: buildy, override: glfw_build.yaml}
    defines: [LINUX]
`), "deps.yaml")
	if err != nil {
		t.Fatal(err)
	}
	glfw := deps[0]
	if glfw.Name != "glfw" || glfw.Version != "3.4" {
		t.Errorf("%+v", glfw)
	}
	// The direct fields, common, the platform, then platform-architecture: the top-level
	// fields and an explicit common section both apply (they raced for the "common" slot)
	mac := glfw.For("macos", "arm64")
	if want := []string{"TOP", "COMMON", "MAC"}; !reflect.DeepEqual(mac.Defines, want) {
		t.Errorf("macos defines = %v, want %v", mac.Defines, want)
	}
	if mac.Root != "/opt/homebrew" || !reflect.DeepEqual(mac.LibDirs, []string{"/opt/homebrew/lib"}) {
		t.Errorf("macos = %+v", mac)
	}
	linux := glfw.For("linux", "x86_64")
	if linux.URL == "" || linux.Build == nil || linux.Build.Override != "glfw_build.yaml" ||
		!reflect.DeepEqual(linux.Defines, []string{"TOP", "COMMON", "LINUX"}) {
		t.Errorf("linux = %+v", linux)
	}
	if got := glfw.Overrides(); !reflect.DeepEqual(got, []string{"glfw_build.yaml"}) {
		t.Errorf("overrides = %v", got)
	}
}

func TestDependencyDefinitionErrors(t *testing.T) {
	for _, c := range []struct{ yaml, want string }{
		// Mobius and Monstro index their definitions this way; it was never a feature
		{"sqlite3:\n  file: dependencies/sqlite3.yaml\n", "line 2: unknown key 'sqlite3.file'"},
		{"x:\n  linxu: {libs: [z]}\n", "unknown key 'x.linxu' (did you mean 'linux'?)"},
		{"x:\n  linux:\n    libz: [z]\n", "unknown key 'x.linux.libz' (did you mean 'libs'?)"},
		{"x:\n  url: u\n  build: {sytem: cmake}\n", "unknown key 'x.build.sytem' (did you mean 'system'?)"},
		{"x:\n  url: u\n  execution: {type: docker}\n", "dependency 'x': execution: docker execution needs an image"},
		{"x:\n  extract: true\n", "unknown key 'x.extract'"},
		{"x:\n  linux: {libs: [a]}\n  linux-amd64: {libs: [b]}\n  linux-x86_64: {libs: [c]}\n", "section 'linux-x86_64' is written twice"},
		{"x: 3\n", "dependency 'x' must be a mapping"},
		{"- x\n", "must be a mapping of dependency names"},
	} {
		_, err := LoadDependencies([]byte(c.yaml), "deps.yaml")
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "deps.yaml") {
			t.Errorf("%q: err = %v, want %q", c.yaml, err, c.want)
		}
	}
}
