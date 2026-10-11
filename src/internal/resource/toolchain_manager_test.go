package resource

import (
	"testing"
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
		if tc.TargetArchitecture != c.architecture {
			t.Errorf("%s targets %s, want %s", tc.Name, tc.TargetArchitecture, c.architecture)
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
