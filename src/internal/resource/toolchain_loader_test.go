package resource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"buildy/internal/config"
)

// Every built-in toolchain, template-free, loads through the strict loader.
func TestEmbeddedToolchainsLoad(t *testing.T) {
	tm, err := NewToolchainManagerMulti(nil)
	if err != nil {
		t.Fatal(err)
	}
	gcc := tm.GetToolchain("gcc-cpp-linux")
	if gcc == nil {
		t.Fatal("no gcc-cpp-linux")
	}
	opt, ok := gcc.FlagMappings.Get("optimization")
	if !ok {
		t.Fatal("gcc-cpp-linux has no optimization mapping")
	}
	if flags, _ := opt.Get("full"); len(flags) != 1 || flags[0] != "-O3" {
		t.Errorf("optimization.full = %v, want [-O3]", flags)
	}
	if got := len(tm.ListToolchains()); got < 20 {
		t.Errorf("%d built-in toolchains", got)
	}
}

// A workspace toolchain with a typo fails to load, naming the file, the line and the key, and
// so does the whole toolchain manager: nothing is skipped with a warning.
func TestWorkspaceToolchainTyposAreErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "mine.yaml")
	if err := os.WriteFile(file, []byte(`toolchain:
  name: mine
  tools:
    compile:
      action: compile
      command: "cc -c ${input} -o ${output}"
      input_extension: [".c"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewToolchainManagerMulti([]string{dir})
	if err == nil {
		t.Fatal("a toolchain with an unknown key loaded")
	}
	for _, want := range []string{file, "line 7", "toolchain.tools.compile.input_extension", "did you mean 'input_extensions'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// Two toolchain files in one directory with the same name are an error, not a coin toss.
func TestDuplicateToolchainNamesInADirectory(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.yaml", "b.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("toolchain:\n  name: same\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewToolchainManagerMulti([]string{dir}); err == nil || !strings.Contains(err.Error(), "both named 'same'") {
		t.Errorf("err = %v", err)
	}
}

// Each built-in data file loads on its own, so one test run lists every broken file.
func TestEachEmbeddedFileLoads(t *testing.T) {
	loaders := map[string]func([]byte, string) error{
		"toolchains":    func(d []byte, s string) error { _, err := config.LoadToolchain(d, s); return err },
		"templates":     func(d []byte, s string) error { _, err := config.LoadTemplates(d, s); return err },
		"build_systems": func(d []byte, s string) error { _, err := config.LoadBuildSystem(d, s); return err },
	}
	for dir, load := range loaders {
		files, err := ListEmbeddedFiles(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			data, err := GetEmbeddedFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if err := load(data, file); err != nil {
				t.Error(err)
			}
		}
	}
}
