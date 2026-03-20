package resource

import (
	"testing"
)

func TestLoadToolchainConfigFromData_ParsesFlagMappings(t *testing.T) {
	yamlData := []byte(`
toolchain:
  name: "test-gcc"
  description: "Test GCC"
  language: cpp
  target:
    platform: linux
    architecture: x86_64
  host:
    platform: linux
    architecture: x86_64
  execution:
    type: native
  flag_mappings:
    optimization:
      none: ["-O0"]
      speed: ["-O2"]
      full: ["-O3"]
    warnings:
      off: []
      default: ["-Wall"]
    symbols:
      "true": ["-g"]
      "false": []
  tools:
    cpp_compile:
      action: compile
      command: "g++ ${flags} -c ${input} -o ${output}"
      input_extensions: [".cpp"]
      output_extension: ".o"
      flags:
        common: []
        debug: []
        release: []
`)

	tc, err := LoadToolchainConfigFromData(yamlData, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tc.FlagMappings == nil {
		t.Fatal("FlagMappings should not be nil")
	}

	if len(tc.FlagMappings) != 3 {
		t.Errorf("expected 3 keyword mappings, got %d", len(tc.FlagMappings))
	}

	opt, ok := tc.FlagMappings["optimization"]
	if !ok {
		t.Fatal("missing optimization mapping")
	}
	if flags, ok := opt["speed"]; !ok || len(flags) != 1 || flags[0] != "-O2" {
		t.Errorf("optimization.speed: expected [-O2], got %v", flags)
	}

	warn, ok := tc.FlagMappings["warnings"]
	if !ok {
		t.Fatal("missing warnings mapping")
	}
	if flags, ok := warn["off"]; !ok || len(flags) != 0 {
		t.Errorf("warnings.off: expected [], got %v", flags)
	}
}

func TestLoadToolchainConfigFromData_NoFlagMappings(t *testing.T) {
	yamlData := []byte(`
toolchain:
  name: "test-plain"
  description: "Plain toolchain"
  language: c
  target:
    platform: linux
    architecture: x86_64
  host:
    platform: linux
    architecture: x86_64
  execution:
    type: native
  tools:
    c_compile:
      action: compile
      command: "gcc -c ${input} -o ${output}"
      input_extensions: [".c"]
      output_extension: ".o"
      flags:
        common: []
`)

	tc, err := LoadToolchainConfigFromData(yamlData, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tc.FlagMappings != nil {
		t.Errorf("FlagMappings should be nil for toolchain without flag_mappings, got %v", tc.FlagMappings)
	}
}

func TestLoadToolchainConfig_EmbeddedGCCToolchain(t *testing.T) {
	data, err := GetEmbeddedFile("toolchains/gcc-cpp-linux.yaml")
	if err != nil {
		t.Skipf("embedded toolchain not available: %v", err)
	}

	tc, err := LoadToolchainConfigFromData(data, "embedded:gcc-cpp-linux")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tc.FlagMappings == nil {
		t.Fatal("embedded gcc-cpp-linux should have flag_mappings")
	}

	opt, ok := tc.FlagMappings["optimization"]
	if !ok {
		t.Fatal("missing optimization mapping")
	}
	if _, ok := opt["full"]; !ok {
		t.Error("optimization should have a 'full' entry")
	}

	warn, ok := tc.FlagMappings["warnings"]
	if !ok {
		t.Fatal("missing warnings mapping")
	}
	if flags, ok := warn["extra"]; !ok || len(flags) < 2 {
		t.Errorf("warnings.extra should have at least 2 flags, got %v", flags)
	}
}
