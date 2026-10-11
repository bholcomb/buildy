package resource

import (
	"testing"

	"buildy/internal/config"
)

func newEmbeddedToolchainManager(t *testing.T) *ToolchainManager {
	t.Helper()
	tm, err := NewToolchainManager(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return tm
}

func TestFindByLanguage_MacOS(t *testing.T) {
	tm := newEmbeddedToolchainManager(t)

	cases := []struct {
		language     string
		architecture string
		want         string
	}{
		{"c", "arm64", "clang-c-macos-arm64"},
		{"cpp", "arm64", "clang-cpp-macos-arm64"},
		{"c", "x86_64", "clang-c-macos"},
		{"cpp", "x86_64", "clang-cpp-macos"},
	}

	for _, c := range cases {
		tc := tm.FindByLanguage(c.language, "macos", c.architecture)
		if tc == nil {
			t.Errorf("FindByLanguage(%s, macos, %s): no toolchain, want %s", c.language, c.architecture, c.want)
			continue
		}
		if tc.Name != c.want {
			t.Errorf("FindByLanguage(%s, macos, %s) = %s, want %s", c.language, c.architecture, tc.Name, c.want)
		}
		if tc.Target.Architecture != c.architecture {
			t.Errorf("%s targets %s, want %s", tc.Name, tc.Target.Architecture, c.architecture)
		}
	}
}

func TestAutoDetect_MacOS(t *testing.T) {
	tm := newEmbeddedToolchainManager(t)

	cases := []struct {
		architecture string
		want         string
	}{
		{"arm64", "clang-cpp-macos-arm64"},
		{"x86_64", "clang-cpp-macos"},
	}

	for _, c := range cases {
		tc := tm.AutoDetect("macos", c.architecture)
		if tc == nil {
			t.Errorf("AutoDetect(macos, %s): no toolchain, want %s", c.architecture, c.want)
			continue
		}
		if tc.Name != c.want {
			t.Errorf("AutoDetect(macos, %s) = %s, want %s", c.architecture, tc.Name, c.want)
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
