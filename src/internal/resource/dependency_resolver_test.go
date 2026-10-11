package resource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"buildy/pkg/util"
)

func writeDepFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func newResolver(root string) *DependencyResolver {
	return NewDependencyResolver("linux", "x86_64", "", "debug", filepath.Join(root, ".buildy_cache"), root, util.NewVariableEnvironment(nil))
}

// Dependency definitions are validated; a file named as a dependency's build.override is a buildy
// config, not definitions, and is skipped.
func TestLoadDependenciesValidates(t *testing.T) {
	root := t.TempDir()
	writeDepFiles(t, root, map[string]string{
		"buildy_config/dependencies.yaml": "zlib:\n  linux:\n    libs: [z]\n",
		"buildy_config/dependencies/glfw.yaml": `
glfw:
  version: "3.4"
  common:
    defines: [GLFW]
  linux:
    url: "https://example.com/glfw.zip"
    build: {system: buildy, override: glfw_build.yaml}
  macos-arm64:
    root: /opt/homebrew
`,
		"buildy_config/dependencies/glfw_build.yaml": "project: {name: glfw}\ntargets: {}\n",
	})
	dr := newResolver(root)
	if err := dr.LoadDependencies(root, ""); err != nil {
		t.Fatal(err)
	}
	if dr.configs["zlib"] == nil || dr.configs["glfw"] == nil {
		t.Errorf("configs = %v", dr.configs)
	}
	if dr.configs["project"] != nil || dr.configs["targets"] != nil {
		t.Error("the override config was loaded as dependency definitions")
	}

	for name, content := range map[string]string{
		"unknown field":          "x:\n  linux:\n    libz: [z]\n",
		"unknown build key":      "x:\n  url: u\n  build: {sytem: cmake}\n",
		"working_dir":            "x:\n  url: u\n  execution: {type: docker, image: gcc:13, working_dir: /workspace}\n",
		"section not a mapping":  "x:\n  linux: [z]\n",
		"dependency not mapping": "x: 3\n",
	} {
		root := t.TempDir()
		writeDepFiles(t, root, map[string]string{"buildy_config/dependencies.yaml": content})
		err := newResolver(root).LoadDependencies(root, "")
		if err == nil || !strings.Contains(err.Error(), "dependencies.yaml") {
			t.Errorf("%s: got %v", name, err)
		}
	}

	// A dependency defined twice is an error naming both files.
	root = t.TempDir()
	writeDepFiles(t, root, map[string]string{
		"buildy_config/dependencies.yaml":   "x:\n  libs: [x]\n",
		"buildy_config/dependencies/x.yaml": "x:\n  libs: [x]\n",
	})
	if err := newResolver(root).LoadDependencies(root, ""); err == nil || !strings.Contains(err.Error(), "x.yaml") {
		t.Errorf("duplicate definition: got %v", err)
	}

	// dependencies.file names the definitions file.
	root = t.TempDir()
	writeDepFiles(t, root, map[string]string{"deps/mine.yaml": "y:\n  libs: [y]\n"})
	dr = newResolver(root)
	if err := dr.LoadDependencies(root, "deps/mine.yaml"); err != nil || dr.configs["y"] == nil {
		t.Errorf("dependencies.file: %v, %v", err, dr.configs)
	}
	if err := newResolver(root).LoadDependencies(root, "deps/missing.yaml"); err == nil {
		t.Error("a missing dependencies.file was accepted")
	}
}
