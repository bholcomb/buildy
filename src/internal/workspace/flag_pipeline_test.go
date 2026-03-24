package workspace

import (
	"testing"

	"buildy/internal/resource"
)

func makeTestToolchain(flagMappings map[string]map[string][]string) *resource.ToolchainConfig {
	tc := resource.NewToolchainConfig("test-toolchain", "test")
	tc.FlagMappings = flagMappings
	return tc
}

func gccFlagMappings() map[string]map[string][]string {
	return map[string]map[string][]string{
		"optimization": {
			"none": {"-O0"},
			"size": {"-Os"},
			"speed": {"-O2"},
			"full": {"-O3"},
		},
		"warnings": {
			"off":        {},
			"default":    {"-Wall"},
			"extra":      {"-Wall", "-Wextra"},
			"everything": {"-Wall", "-Wextra", "-Wpedantic"},
		},
		"symbols": {
			"true":  {"-g"},
			"false": {},
		},
		"runtime": {
			"debug":   {},
			"release": {},
		},
	}
}

func msvcFlagMappings() map[string]map[string][]string {
	return map[string]map[string][]string{
		"optimization": {
			"none":  {"/Od"},
			"size":  {"/O1"},
			"speed": {"/O2"},
			"full":  {"/Ox"},
		},
		"warnings": {
			"off":        {"/W0"},
			"default":    {"/W3"},
			"extra":      {"/W4"},
			"everything": {"/Wall"},
		},
		"symbols": {
			"true":  {"/Zi"},
			"false": {},
		},
		"runtime": {
			"debug":   {"/MDd"},
			"release": {"/MD"},
		},
	}
}

// --- resolveAbstractKeywords tests ---

func TestResolveAbstractKeywords_SingleOptimization(t *testing.T) {
	merged := map[string]any{
		"optimization": "speed",
	}
	if err := resolveAbstractKeywords(merged, gccFlagMappings()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	flags := ExtractStringList(merged["flags"])
	if len(flags) != 1 || flags[0] != "-O2" {
		t.Errorf("expected [-O2], got %v", flags)
	}
	if _, exists := merged["optimization"]; exists {
		t.Error("optimization key should be consumed")
	}
}

func TestResolveAbstractKeywords_MultipleKeywords(t *testing.T) {
	merged := map[string]any{
		"optimization": "full",
		"warnings":     "extra",
		"symbols":      "true",
	}
	if err := resolveAbstractKeywords(merged, gccFlagMappings()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	flags := ExtractStringList(merged["flags"])
	expected := map[string]bool{"-O3": false, "-Wall": false, "-Wextra": false, "-g": false}
	for _, f := range flags {
		expected[f] = true
	}
	for flag, found := range expected {
		if !found {
			t.Errorf("expected flag %q not found in %v", flag, flags)
		}
	}
}

func TestResolveAbstractKeywords_DeterministicOrder(t *testing.T) {
	mappings := gccFlagMappings()

	// Keywords are sorted alphabetically: optimization, runtime, symbols, warnings.
	// Run 100 times to detect non-deterministic ordering from Go map iteration.
	var reference []string
	for i := 0; i < 100; i++ {
		merged := map[string]any{
			"optimization": "full",
			"warnings":     "extra",
			"symbols":      "true",
			"runtime":      "debug",
		}
		if err := resolveAbstractKeywords(merged, mappings); err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		flags := ExtractStringList(merged["flags"])
		if i == 0 {
			reference = flags
			continue
		}
		if len(flags) != len(reference) {
			t.Fatalf("iteration %d: flag count changed: %v vs %v", i, flags, reference)
		}
		for j := range flags {
			if flags[j] != reference[j] {
				t.Fatalf("iteration %d: flag order changed at index %d: got %v, want %v", i, j, flags, reference)
			}
		}
	}
}

func TestResolveAbstractKeywords_PreservesExistingFlags(t *testing.T) {
	merged := map[string]any{
		"optimization": "none",
		"flags":        []any{"-fpermissive"},
	}
	if err := resolveAbstractKeywords(merged, gccFlagMappings()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	flags := ExtractStringList(merged["flags"])
	if len(flags) != 2 {
		t.Fatalf("expected 2 flags, got %v", flags)
	}
	if flags[0] != "-O0" {
		t.Errorf("resolved flags should be prepended, got %v", flags)
	}
	if flags[1] != "-fpermissive" {
		t.Errorf("existing flags should be preserved, got %v", flags)
	}
}

func TestResolveAbstractKeywords_InvalidValue(t *testing.T) {
	merged := map[string]any{
		"optimization": "turbo",
	}
	err := resolveAbstractKeywords(merged, gccFlagMappings())
	if err == nil {
		t.Fatal("expected error for invalid value")
	}
	if !containsString(err.Error(), "turbo") {
		t.Errorf("error should mention the invalid value: %v", err)
	}
	if !containsString(err.Error(), "optimization") {
		t.Errorf("error should mention the keyword: %v", err)
	}
}

func TestResolveAbstractKeywords_BoolSymbols(t *testing.T) {
	merged := map[string]any{
		"symbols": true,
	}
	if err := resolveAbstractKeywords(merged, gccFlagMappings()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	flags := ExtractStringList(merged["flags"])
	if len(flags) != 1 || flags[0] != "-g" {
		t.Errorf("expected [-g], got %v", flags)
	}
}

func TestResolveAbstractKeywords_EmptyFlagResult(t *testing.T) {
	merged := map[string]any{
		"warnings": "off",
	}
	if err := resolveAbstractKeywords(merged, gccFlagMappings()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	flags := ExtractStringList(merged["flags"])
	if len(flags) != 0 {
		t.Errorf("expected empty flags, got %v", flags)
	}
}

func TestResolveAbstractKeywords_NoMappings(t *testing.T) {
	merged := map[string]any{
		"optimization": "speed",
	}
	if err := resolveAbstractKeywords(merged, nil); err != nil {
		t.Fatalf("unexpected error with nil mappings: %v", err)
	}
	if _, exists := merged["optimization"]; !exists {
		t.Error("keyword should remain when no mappings")
	}
}

func TestResolveAbstractKeywords_MSVC(t *testing.T) {
	merged := map[string]any{
		"optimization": "speed",
		"warnings":     "extra",
		"runtime":      "debug",
	}
	if err := resolveAbstractKeywords(merged, msvcFlagMappings()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	flags := ExtractStringList(merged["flags"])
	expected := map[string]bool{"/O2": false, "/W4": false, "/MDd": false}
	for _, f := range flags {
		expected[f] = true
	}
	for flag, found := range expected {
		if !found {
			t.Errorf("expected flag %q not found in %v", flag, flags)
		}
	}
}

func TestResolveAbstractKeywords_UnknownKeywordsIgnored(t *testing.T) {
	merged := map[string]any{
		"c_standard": "c11",
	}
	if err := resolveAbstractKeywords(merged, gccFlagMappings()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged["c_standard"] != "c11" {
		t.Error("non-keyword config values should be preserved")
	}
}

// --- applyFlagRemovals tests ---

func TestApplyFlagRemovals_Basic(t *testing.T) {
	base := []string{"-Wall", "-Wextra", "-g", "-O2"}
	removals := []string{"-Wextra", "-O2"}
	result := applyFlagRemovals(base, removals)
	if len(result) != 2 || result[0] != "-Wall" || result[1] != "-g" {
		t.Errorf("expected [-Wall -g], got %v", result)
	}
}

func TestApplyFlagRemovals_NoRemovals(t *testing.T) {
	base := []string{"-Wall", "-g"}
	result := applyFlagRemovals(base, nil)
	if len(result) != 2 {
		t.Errorf("expected original flags, got %v", result)
	}
}

func TestApplyFlagRemovals_RemoveAll(t *testing.T) {
	base := []string{"-Wall", "-g"}
	removals := []string{"-Wall", "-g"}
	result := applyFlagRemovals(base, removals)
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}
}

func TestApplyFlagRemovals_RemoveNonexistent(t *testing.T) {
	base := []string{"-Wall"}
	removals := []string{"-Wno-error"}
	result := applyFlagRemovals(base, removals)
	if len(result) != 1 || result[0] != "-Wall" {
		t.Errorf("expected [-Wall], got %v", result)
	}
}

// --- end-to-end pipeline tests ---

func TestFlagPipeline_EnvironmentFlagsReachMergedConfig(t *testing.T) {
	tg := &TaskGenerator{
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "debug",
	}
	tg.CurrentToolchain = makeTestToolchain(gccFlagMappings())

	config := map[string]any{
		"environment": map[string]any{
			"compile": map[string]any{
				"flags":        []any{"-fpermissive"},
				"optimization": "none",
				"warnings":     "extra",
				"symbols":      "true",
			},
		},
	}

	merged, err := tg.getMergedConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	flags := ExtractStringList(merged["flags"])
	flagSet := make(map[string]bool)
	for _, f := range flags {
		flagSet[f] = true
	}
	for _, expected := range []string{"-fpermissive", "-O0", "-Wall", "-Wextra", "-g"} {
		if !flagSet[expected] {
			t.Errorf("expected %q in merged flags %v", expected, flags)
		}
	}
}

func TestFlagPipeline_ConfigurationFlagsAppend(t *testing.T) {
	tg := &TaskGenerator{
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "debug",
	}
	tg.CurrentToolchain = makeTestToolchain(gccFlagMappings())

	config := map[string]any{
		"environment": map[string]any{
			"compile": map[string]any{
				"flags": []any{"-fpermissive"},
			},
			"configurations": map[string]any{
				"debug": map[string]any{
					"flags": []any{"-DDEBUG_MODE"},
				},
			},
		},
	}

	merged, err := tg.getMergedConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	flags := ExtractStringList(merged["flags"])
	flagSet := make(map[string]bool)
	for _, f := range flags {
		flagSet[f] = true
	}
	if !flagSet["-fpermissive"] {
		t.Errorf("environment flags missing from %v", flags)
	}
	if !flagSet["-DDEBUG_MODE"] {
		t.Errorf("configuration flags missing from %v", flags)
	}
}

func TestFlagPipeline_RemoveFlagsAtConfigLevel(t *testing.T) {
	tg := &TaskGenerator{
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "release",
	}
	tg.CurrentToolchain = makeTestToolchain(gccFlagMappings())

	config := map[string]any{
		"environment": map[string]any{
			"compile": map[string]any{
				"warnings": "extra",
			},
			"configurations": map[string]any{
				"release": map[string]any{
					"optimization": "full",
					"remove_flags": []any{"-Wextra"},
				},
			},
		},
	}

	merged, err := tg.getMergedConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	flags := ExtractStringList(merged["flags"])
	for _, f := range flags {
		if f == "-Wextra" {
			t.Errorf("-Wextra should have been removed, got %v", flags)
		}
	}
}

func TestFlagPipeline_InvalidKeywordErrors(t *testing.T) {
	tg := &TaskGenerator{
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "debug",
	}
	tg.CurrentToolchain = makeTestToolchain(gccFlagMappings())

	config := map[string]any{
		"environment": map[string]any{
			"compile": map[string]any{
				"optimization": "turbo",
			},
		},
	}

	_, err := tg.getMergedConfig(config)
	if err == nil {
		t.Fatal("expected error for invalid optimization value")
	}
}

// --- compile: and link: section tests ---

func TestFlagPipeline_EnvironmentLinkFlags(t *testing.T) {
	tg := &TaskGenerator{
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "debug",
	}
	tg.CurrentToolchain = makeTestToolchain(gccFlagMappings())

	config := map[string]any{
		"environment": map[string]any{
			"compile": map[string]any{
				"flags": []any{"-Wall"},
			},
			"link": map[string]any{
				"flags": []any{"-Wl,-rpath,$ORIGIN"},
			},
		},
	}

	merged, err := tg.getMergedConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	compileFlags := ExtractStringList(merged["flags"])
	if len(compileFlags) != 1 || compileFlags[0] != "-Wall" {
		t.Errorf("expected compile flags [-Wall], got %v", compileFlags)
	}

	linkFlags := ExtractStringList(merged["link_flags"])
	if len(linkFlags) != 1 || linkFlags[0] != "-Wl,-rpath,$ORIGIN" {
		t.Errorf("expected link flags [-Wl,-rpath,$ORIGIN], got %v", linkFlags)
	}
}

func TestFlagPipeline_ConfigurationCompileAndLinkSections(t *testing.T) {
	tg := &TaskGenerator{
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "release",
	}
	tg.CurrentToolchain = makeTestToolchain(gccFlagMappings())

	config := map[string]any{
		"environment": map[string]any{
			"compile": map[string]any{
				"flags": []any{"-fvisibility=hidden"},
			},
			"link": map[string]any{
				"flags": []any{"-Wl,-z,now"},
			},
			"configurations": map[string]any{
				"release": map[string]any{
					"optimization": "full",
					"compile": map[string]any{
						"flags": []any{"-flto", "-march=native"},
					},
					"link": map[string]any{
						"flags": []any{"-flto", "-Wl,-O1"},
					},
				},
			},
		},
	}

	merged, err := tg.getMergedConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	compileFlags := ExtractStringList(merged["flags"])
	compileFlagSet := make(map[string]bool)
	for _, f := range compileFlags {
		compileFlagSet[f] = true
	}
	for _, expected := range []string{"-fvisibility=hidden", "-flto", "-march=native", "-O3"} {
		if !compileFlagSet[expected] {
			t.Errorf("expected compile flag %q in %v", expected, compileFlags)
		}
	}

	linkFlags := ExtractStringList(merged["link_flags"])
	linkFlagSet := make(map[string]bool)
	for _, f := range linkFlags {
		linkFlagSet[f] = true
	}
	for _, expected := range []string{"-Wl,-z,now", "-flto", "-Wl,-O1"} {
		if !linkFlagSet[expected] {
			t.Errorf("expected link flag %q in %v", expected, linkFlags)
		}
	}
}

func TestFlagPipeline_ConfigurationRemoveFlagsInSections(t *testing.T) {
	tg := &TaskGenerator{
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "release",
	}
	tg.CurrentToolchain = makeTestToolchain(gccFlagMappings())

	config := map[string]any{
		"environment": map[string]any{
			"compile": map[string]any{
				"flags": []any{"-Wall", "-Wextra", "-Werror"},
			},
			"link": map[string]any{
				"flags": []any{"-Wl,-z,now", "-s"},
			},
			"configurations": map[string]any{
				"release": map[string]any{
					"compile": map[string]any{
						"remove_flags": []any{"-Werror"},
					},
					"link": map[string]any{
						"remove_flags": []any{"-s"},
					},
				},
			},
		},
	}

	merged, err := tg.getMergedConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	compileFlags := ExtractStringList(merged["flags"])
	for _, f := range compileFlags {
		if f == "-Werror" {
			t.Errorf("-Werror should have been removed from compile flags %v", compileFlags)
		}
	}
	compileFlagSet := make(map[string]bool)
	for _, f := range compileFlags {
		compileFlagSet[f] = true
	}
	if !compileFlagSet["-Wall"] || !compileFlagSet["-Wextra"] {
		t.Errorf("non-removed compile flags missing from %v", compileFlags)
	}

	linkFlags := ExtractStringList(merged["link_flags"])
	for _, f := range linkFlags {
		if f == "-s" {
			t.Errorf("-s should have been removed from link flags %v", linkFlags)
		}
	}
	if len(linkFlags) != 1 || linkFlags[0] != "-Wl,-z,now" {
		t.Errorf("expected link flags [-Wl,-z,now], got %v", linkFlags)
	}
}

func TestFlagPipeline_CompileAndLinkFlagsSeparate(t *testing.T) {
	tg := &TaskGenerator{
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "debug",
	}
	tg.CurrentToolchain = makeTestToolchain(gccFlagMappings())

	config := map[string]any{
		"environment": map[string]any{
			"compile": map[string]any{
				"flags": []any{"-fPIC"},
			},
			"link": map[string]any{
				"flags": []any{"-shared"},
			},
		},
	}

	merged, err := tg.getMergedConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	compileFlags := ExtractStringList(merged["flags"])
	linkFlags := ExtractStringList(merged["link_flags"])

	for _, f := range compileFlags {
		if f == "-shared" {
			t.Errorf("link flag -shared leaked into compile flags %v", compileFlags)
		}
	}
	for _, f := range linkFlags {
		if f == "-fPIC" {
			t.Errorf("compile flag -fPIC leaked into link flags %v", linkFlags)
		}
	}
}

func TestFlagPipeline_EnvironmentLinkRemoveFlags(t *testing.T) {
	tg := &TaskGenerator{
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "debug",
	}
	tg.CurrentToolchain = makeTestToolchain(gccFlagMappings())

	config := map[string]any{
		"environment": map[string]any{
			"link": map[string]any{
				"flags":        []any{"-Wl,-z,now", "-s", "-Wl,-O1"},
				"remove_flags": []any{"-s"},
			},
		},
	}

	merged, err := tg.getMergedConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	linkFlags := ExtractStringList(merged["link_flags"])
	for _, f := range linkFlags {
		if f == "-s" {
			t.Errorf("-s should have been removed from link flags %v", linkFlags)
		}
	}
	linkFlagSet := make(map[string]bool)
	for _, f := range linkFlags {
		linkFlagSet[f] = true
	}
	if !linkFlagSet["-Wl,-z,now"] || !linkFlagSet["-Wl,-O1"] {
		t.Errorf("non-removed link flags missing from %v", linkFlags)
	}
}

// --- filter tests inside compile: and link: sections ---

func TestFlagPipeline_FiltersInEnvironmentCompileSection(t *testing.T) {
	tg := &TaskGenerator{
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "debug",
	}
	tg.CurrentToolchain = makeTestToolchain(gccFlagMappings())

	config := map[string]any{
		"environment": map[string]any{
			"compile": map[string]any{
				"flags": []any{
					"-Wall",
					map[string]any{"linux": []any{"-pthread"}},
					map[string]any{"windows": []any{"/utf-8"}},
				},
			},
		},
	}

	merged, err := tg.getMergedConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	flags := ExtractStringList(merged["flags"])
	flagSet := make(map[string]bool)
	for _, f := range flags {
		flagSet[f] = true
	}
	if !flagSet["-Wall"] || !flagSet["-pthread"] {
		t.Errorf("expected [-Wall, -pthread] in %v", flags)
	}
	if flagSet["/utf-8"] {
		t.Errorf("windows flag /utf-8 should not appear on linux, got %v", flags)
	}
}

func TestFlagPipeline_FiltersInConfigurationLinkSection(t *testing.T) {
	tg := &TaskGenerator{
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "release",
	}
	tg.CurrentToolchain = makeTestToolchain(gccFlagMappings())

	config := map[string]any{
		"environment": map[string]any{
			"configurations": map[string]any{
				"release": map[string]any{
					"link": map[string]any{
						"flags": []any{
							"-flto",
							map[string]any{"linux": []any{"-Wl,-z,relro"}},
							map[string]any{"windows": []any{"/LTCG"}},
						},
					},
				},
			},
		},
	}

	merged, err := tg.getMergedConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	linkFlags := ExtractStringList(merged["link_flags"])
	linkFlagSet := make(map[string]bool)
	for _, f := range linkFlags {
		linkFlagSet[f] = true
	}
	if !linkFlagSet["-flto"] || !linkFlagSet["-Wl,-z,relro"] {
		t.Errorf("expected [-flto, -Wl,-z,relro] in %v", linkFlags)
	}
	if linkFlagSet["/LTCG"] {
		t.Errorf("windows flag /LTCG should not appear on linux, got %v", linkFlags)
	}
}

func TestFlagPipeline_FiltersInConfigurationCompileSection(t *testing.T) {
	tg := &TaskGenerator{
		Platform:      "linux",
		Architecture:  "x86_64",
		Configuration: "release",
	}
	tg.CurrentToolchain = makeTestToolchain(gccFlagMappings())

	config := map[string]any{
		"environment": map[string]any{
			"configurations": map[string]any{
				"release": map[string]any{
					"compile": map[string]any{
						"flags": []any{
							"-flto",
							map[string]any{
								"linux": []any{"-march=native"},
							},
						},
					},
				},
			},
		},
	}

	merged, err := tg.getMergedConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	flags := ExtractStringList(merged["flags"])
	flagSet := make(map[string]bool)
	for _, f := range flags {
		flagSet[f] = true
	}
	if !flagSet["-flto"] || !flagSet["-march=native"] {
		t.Errorf("expected [-flto, -march=native] in %v", flags)
	}
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && contains(s, sub))
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
