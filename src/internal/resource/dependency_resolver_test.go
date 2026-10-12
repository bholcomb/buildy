package resource

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	return mustResolver(NewDependencyResolver("linux", "x86_64", "", "debug", filepath.Join(root, ".buildy_cache"), root, util.NewVariableEnvironment(nil)))
}

func mustResolver(dr *DependencyResolver, err error) *DependencyResolver {
	if err != nil {
		panic(err)
	}
	return dr
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

// A dependency sees the documented variables (${arch}, ${config}, ...) and the workspace's; the
// undocumented ${architecture} and ${configuration} are gone, and an unknown variable is an error
// naming the dependency, not an exit from inside the resolver.
func TestDependencyVariables(t *testing.T) {
	root := t.TempDir()
	writeDepFiles(t, root, map[string]string{
		"buildy_config/dependencies.yaml": `
ok:
  version: "1.2"
  include_dirs: ["/x/${platform}-${arch}-${config}", "/y/${name}-${version}", "${workspace_root}/z", "${FROM_ROOT}"]
`,
	})
	env := util.NewVariableEnvironment(nil)
	env.SetVariable("FROM_ROOT", "/root-var", "test")
	dr := mustResolver(NewDependencyResolver("linux", "x86_64", "", "release", filepath.Join(root, ".buildy_cache"), root, env))
	if err := dr.LoadDependencies(root, ""); err != nil {
		t.Fatal(err)
	}
	if err := dr.ResolveAll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	want := []string{"/x/linux-x86_64-release", "/y/ok-1.2", root + "/z", "/root-var"}
	if got := dr.resolved["ok"].IncludeDirs; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("include dirs = %v, want %v", got, want)
	}

	writeDepFiles(t, root, map[string]string{
		"buildy_config/dependencies.yaml": "bad:\n  lib_dirs: [\"/x/${architecture}\"]\n",
	})
	dr = mustResolver(NewDependencyResolver("linux", "x86_64", "", "release", filepath.Join(root, ".buildy_cache"), root, env))
	if err := dr.LoadDependencies(root, ""); err != nil {
		t.Fatal(err)
	}
	if err := dr.ResolveAll(context.Background(), false); err == nil || !strings.Contains(err.Error(), "dependency 'bad'") || !strings.Contains(err.Error(), "architecture") {
		t.Errorf("err = %v", err)
	}
}

// pkg-config's output is split the way it quotes it.
func TestSplitShellWords(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"-I/usr/include/x -pthread\n", []string{"-I/usr/include/x", "-pthread"}},
		{`-I/my\ dir/include -DA="b c"`, []string{"-I/my dir/include", "-DA=b c"}},
		{`'-DX=$Y' "a\"b" ""`, []string{"-DX=$Y", `a"b`, ""}},
		{"  ", nil},
	} {
		got, err := splitShellWords(c.in)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("splitShellWords(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := splitShellWords(`"open`); err == nil {
		t.Error("an unterminated quote was accepted")
	}
}

// Finding 5.5: a pkg-config dependency keeps every flag pkg-config gives, in order.
func TestPkgConfigKeepsEveryFlag(t *testing.T) {
	if _, err := exec.LookPath("pkg-config"); err != nil {
		t.Skip("skipping: pkg-config is not on the PATH")
	}
	// The pkg-config of GitHub's Windows runners is Strawberry Perl's script, which the runner's
	// other perl can't run (Pod::Usage is missing)
	if out, err := exec.Command("pkg-config", "--version").CombinedOutput(); err != nil {
		t.Skipf("skipping: the pkg-config on the PATH doesn't run (pkg-config --version: %v): %s", err, out)
	}
	dir := t.TempDir()
	writeDepFiles(t, dir, map[string]string{"demo.pc": `prefix=/opt/my\ demo
Name: demo
Description: demo
Version: 1
Cflags: -I${prefix}/include -pthread -isystem /opt/sys -DDEMO=1 -fopenmp
Libs: -L${prefix}/lib -ldemo -Wl,--as-needed -pthread -framework Cocoa /opt/libextra.a
`})
	t.Setenv("PKG_CONFIG_PATH", dir)
	t.Setenv("PKG_CONFIG_LIBDIR", dir)
	dep := &ResolvedDependency{}
	if err := newResolver(t.TempDir()).resolvePkgConfig(context.Background(), "demo", dep); err != nil {
		t.Fatal(err)
	}
	if want := []string{"-I/opt/my demo/include", "-pthread", "-isystem", "/opt/sys", "-DDEMO=1", "-fopenmp"}; !slices.Equal(dep.CompileFlags, want) {
		t.Errorf("compile flags = %q, want %q", dep.CompileFlags, want)
	}
	if want := []string{"-L/opt/my demo/lib", "-ldemo", "-Wl,--as-needed", "-pthread", "/opt/libextra.a"}; !slices.Equal(dep.Libs, want) {
		t.Errorf("libs = %q, want %q", dep.Libs, want)
	}
	if !slices.Equal(dep.Frameworks, []string{"Cocoa"}) {
		t.Errorf("frameworks = %q", dep.Frameworks)
	}
}
