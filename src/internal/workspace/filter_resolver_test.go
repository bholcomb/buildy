package workspace

import (
	"testing"
)

func TestResolveFilteredList_PlainStrings(t *testing.T) {
	raw := []any{"a.c", "b.c", "c.c"}
	ctx := BuildContext{Platform: "linux", Architecture: "x86_64", Configuration: "debug"}
	result := ResolveFilteredList(raw, ctx)
	if len(result) != 3 {
		t.Fatalf("expected 3 items, got %v", result)
	}
}

func TestResolveFilteredList_PlatformFilter(t *testing.T) {
	raw := []any{
		"common.c",
		map[string]any{"linux": []any{"linux.c"}},
		map[string]any{"windows": []any{"windows.c"}},
	}
	ctx := BuildContext{Platform: "linux", Architecture: "x86_64", Configuration: "debug"}
	result := ResolveFilteredList(raw, ctx)
	if len(result) != 2 {
		t.Fatalf("expected 2 items, got %v", result)
	}
	if result[0] != "common.c" || result[1] != "linux.c" {
		t.Errorf("expected [common.c linux.c], got %v", result)
	}
}

func TestResolveFilteredList_ConfigurationFilter(t *testing.T) {
	raw := []any{
		map[string]any{"debug": []any{"-g"}},
		map[string]any{"release": []any{"-O3"}},
	}
	ctx := BuildContext{Configuration: "debug"}
	result := ResolveFilteredList(raw, ctx)
	if len(result) != 1 || result[0] != "-g" {
		t.Errorf("expected [-g], got %v", result)
	}
}

func TestResolveFilteredList_ToolchainFilter(t *testing.T) {
	raw := []any{
		"-Wall",
		map[string]any{"gcc-cpp-linux": []any{"-Wno-error"}},
		map[string]any{"clang-cpp-linux": []any{"-Wno-unused"}},
	}
	ctx := BuildContext{Toolchain: "gcc-cpp-linux"}
	result := ResolveFilteredList(raw, ctx)
	if len(result) != 2 || result[0] != "-Wall" || result[1] != "-Wno-error" {
		t.Errorf("expected [-Wall -Wno-error], got %v", result)
	}
}

func TestResolveFilteredList_ArchitectureFilter(t *testing.T) {
	raw := []any{
		map[string]any{"x86_64": []any{"-m64"}},
		map[string]any{"arm64": []any{"-march=armv8-a"}},
	}
	ctx := BuildContext{Architecture: "arm64"}
	result := ResolveFilteredList(raw, ctx)
	if len(result) != 1 || result[0] != "-march=armv8-a" {
		t.Errorf("expected [-march=armv8-a], got %v", result)
	}
}

func TestResolveFilteredList_NoMatch(t *testing.T) {
	raw := []any{
		map[string]any{"macos": []any{"mac_only.c"}},
	}
	ctx := BuildContext{Platform: "linux"}
	result := ResolveFilteredList(raw, ctx)
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}
}

func TestResolveFilteredList_SingleString(t *testing.T) {
	result := ResolveFilteredList("hello", BuildContext{})
	if len(result) != 1 || result[0] != "hello" {
		t.Errorf("expected [hello], got %v", result)
	}
}

func TestResolveFilteredList_StringSlice(t *testing.T) {
	result := ResolveFilteredList([]string{"a", "b"}, BuildContext{})
	if len(result) != 2 {
		t.Errorf("expected 2 items, got %v", result)
	}
}

// --- Nested filter tests ---

func TestResolveFilteredList_NestedConfigUnderPlatform(t *testing.T) {
	raw := []any{
		map[string]any{
			"linux": map[string]any{
				"debug": []any{"-DLINUX_DEBUG"},
			},
		},
	}
	ctx := BuildContext{Platform: "linux", Configuration: "debug"}
	result := ResolveFilteredList(raw, ctx)
	if len(result) != 1 || result[0] != "-DLINUX_DEBUG" {
		t.Errorf("expected [-DLINUX_DEBUG], got %v", result)
	}
}

func TestResolveFilteredList_NestedToolchainUnderConfig(t *testing.T) {
	raw := []any{
		map[string]any{
			"debug": map[string]any{
				"gcc-cpp-linux": []any{"-fsanitize=address"},
			},
		},
	}
	ctx := BuildContext{Configuration: "debug", Toolchain: "gcc-cpp-linux"}
	result := ResolveFilteredList(raw, ctx)
	if len(result) != 1 || result[0] != "-fsanitize=address" {
		t.Errorf("expected [-fsanitize=address], got %v", result)
	}
}

func TestResolveFilteredList_NestedOuterNoMatch(t *testing.T) {
	raw := []any{
		map[string]any{
			"windows": map[string]any{
				"debug": []any{"/DWIN_DEBUG"},
			},
		},
	}
	ctx := BuildContext{Platform: "linux", Configuration: "debug"}
	result := ResolveFilteredList(raw, ctx)
	if len(result) != 0 {
		t.Errorf("expected empty when outer filter doesn't match, got %v", result)
	}
}

func TestResolveFilteredList_NestedInnerNoMatch(t *testing.T) {
	raw := []any{
		map[string]any{
			"linux": map[string]any{
				"release": []any{"-DRELEASE_ONLY"},
			},
		},
	}
	ctx := BuildContext{Platform: "linux", Configuration: "debug"}
	result := ResolveFilteredList(raw, ctx)
	if len(result) != 0 {
		t.Errorf("expected empty when inner filter doesn't match, got %v", result)
	}
}

func TestResolveFilteredList_MixedPlainAndNested(t *testing.T) {
	raw := []any{
		"-Wall",
		map[string]any{
			"debug": map[string]any{
				"gcc-cpp-linux": []any{"-fsanitize=address"},
			},
		},
		"-Wextra",
	}
	ctx := BuildContext{Configuration: "debug", Toolchain: "gcc-cpp-linux"}
	result := ResolveFilteredList(raw, ctx)
	if len(result) != 3 {
		t.Fatalf("expected 3 items, got %v", result)
	}
	if result[0] != "-Wall" || result[1] != "-fsanitize=address" || result[2] != "-Wextra" {
		t.Errorf("got %v", result)
	}
}

func TestResolveFilteredList_NestedArrayWithFilters(t *testing.T) {
	raw := []any{
		map[string]any{
			"linux": []any{
				"-DLINUX",
				map[string]any{"debug": []any{"-DLINUX_DEBUG"}},
			},
		},
	}
	ctx := BuildContext{Platform: "linux", Configuration: "debug"}
	result := ResolveFilteredList(raw, ctx)
	if len(result) != 2 {
		t.Fatalf("expected 2 items, got %v", result)
	}
	if result[0] != "-DLINUX" || result[1] != "-DLINUX_DEBUG" {
		t.Errorf("expected [-DLINUX -DLINUX_DEBUG], got %v", result)
	}
}
