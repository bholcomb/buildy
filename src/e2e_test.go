package main

// End-to-end tests: build the buildy binary once, copy a fixture from testdata/e2e/<case>/ to a
// temporary directory, and run buildy on it. A test skips when a tool it needs (g++, go, cargo,
// docker) is missing. C++ cases run on Linux and macOS.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildyBinary is the binary under test, built by TestMain.
var buildyBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "buildy-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	buildyBinary = filepath.Join(dir, "buildy"+exeSuffix())
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go"+exeSuffix())
	build := exec.Command(goTool, "build", "-o", buildyBinary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building buildy for the end-to-end tests: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// needTools skips the test unless every tool is on the PATH.
func needTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("skipping: %s is not on the PATH", tool)
		}
	}
}

// needCpp skips C++ cases where they don't run yet (Windows) or g++ is missing.
func needCpp(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skipping: the C++ end-to-end cases run on Linux and macOS")
	}
	needTools(t, "g++")
}

// fixture copies testdata/e2e/<name> to a temporary directory and returns the copy.
func fixture(t *testing.T, name string) string {
	t.Helper()
	return fixtureUnder(t, name, "")
}

// fixtureUnder copies testdata/e2e/<name> to a directory parent in a temporary directory.
func fixtureUnder(t *testing.T, name, parent string) string {
	t.Helper()
	src := filepath.Join("testdata", "e2e", name)
	dst := filepath.Join(t.TempDir(), parent, name)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("copying fixture %s: %v", name, err)
	}
	return dst
}

// buildy runs buildy in dir and returns its combined output and error.
func buildy(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(buildyBinary, args...)
	cmd.Dir = dir
	// A fixture is not a VCS checkout; don't let a stray .git above the temp dir break go build
	cmd.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// mustBuildy runs buildy in dir and fails the test if it fails.
func mustBuildy(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := buildy(t, dir, args...)
	if err != nil {
		t.Fatalf("buildy %s in %s failed: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return out
}

// mustFailBuildy runs buildy in dir and fails the test if it succeeds.
func mustFailBuildy(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := buildy(t, dir, args...)
	if err == nil {
		t.Fatalf("buildy %s in %s succeeded, want a failure\n%s", strings.Join(args, " "), dir, out)
	}
	return out
}

// outDir is the default output directory for a configuration.
func outDir(dir, config string) string {
	return filepath.Join(dir, "build", fmt.Sprintf("%s-%s-%s", detectPlatform(), detectArchitecture(), config))
}

// run runs a program and returns its trimmed standard output.
func run(t *testing.T, program string, args ...string) string {
	t.Helper()
	out, err := exec.Command(program, args...).Output()
	if err != nil {
		t.Fatalf("running %s: %v", program, err)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// S3: removing a library source removes its member from the archive.
func TestE2EStaleArchive(t *testing.T) {
	needCpp(t)
	needTools(t, "ar")
	dir := fixture(t, "stale_archive")
	lib := filepath.Join(outDir(dir, "debug"), "lib", "libcalc.a")

	mustBuildy(t, dir)
	if members := run(t, "ar", "t", lib); !strings.Contains(members, "sub.o") {
		t.Fatalf("libcalc.a members %q lack sub.o", members)
	}

	if err := os.Remove(filepath.Join(dir, "lib", "sub.cpp")); err != nil {
		t.Fatal(err)
	}
	mustBuildy(t, dir)
	if members := run(t, "ar", "t", lib); strings.Contains(members, "sub.o") {
		t.Errorf("libcalc.a still holds sub.o after sub.cpp was removed: %q", members)
	}
}

// Finding 6 and S8: editing an extensionless header, or one under a directory with a space,
// rebuilds.
func TestE2EExtensionlessHeader(t *testing.T) {
	needCpp(t)
	dir := fixture(t, "extensionless_header")
	app := filepath.Join(outDir(dir, "debug"), "bin", "app")

	mustBuildy(t, dir)
	if got := run(t, app); got != "111" {
		t.Fatalf("app printed %q, want 111", got)
	}

	writeFile(t, filepath.Join(dir, "config"), "#define VALUE 2\n")
	mustBuildy(t, dir)
	if got := run(t, app); got != "211" {
		t.Errorf("after editing config, app printed %q, want 211", got)
	}

	writeFile(t, filepath.Join(dir, "inc dir", "v.h"), "#define V 2\n")
	mustBuildy(t, dir)
	if got := run(t, app); got != "212" {
		t.Errorf("after editing 'inc dir/v.h', app printed %q, want 212", got)
	}

	// Nothing changed: every task is a cache hit.
	out := mustBuildy(t, dir)
	if !strings.Contains(out, "0 executed") {
		t.Errorf("a no-op build executed tasks:\n%s", out)
	}
}

// Finding 2: two targets built from one source keep their own objects.
func TestE2ESharedSource(t *testing.T) {
	needCpp(t)
	dir := fixture(t, "shared_source")
	mustBuildy(t, dir)
	bin := filepath.Join(outDir(dir, "debug"), "bin")
	if got := run(t, filepath.Join(bin, "one")); got != "1" {
		t.Errorf("one printed %q, want 1", got)
	}
	if got := run(t, filepath.Join(bin, "two")); got != "2" {
		t.Errorf("two printed %q, want 2", got)
	}
}

// S5: "buildy ." and "cd sub && buildy .." build the same objects in the same places.
func TestE2ECwdIndependent(t *testing.T) {
	needCpp(t)
	dir := fixture(t, "cwd_independent")
	objects := func() []string {
		var files []string
		filepath.WalkDir(filepath.Join(dir, "build"), func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(dir, path)
				files = append(files, rel)
			}
			return nil
		})
		return files
	}

	mustBuildy(t, dir, ".")
	fromRoot := objects()
	for _, generated := range []string{"build", ".buildy_cache"} {
		if err := os.RemoveAll(filepath.Join(dir, generated)); err != nil {
			t.Fatal(err)
		}
	}

	mustBuildy(t, filepath.Join(dir, "sub"), "..")
	fromSub := objects()
	if strings.Join(fromRoot, "\n") != strings.Join(fromSub, "\n") {
		t.Errorf("outputs differ:\nbuildy .:\n  %s\ncd sub && buildy ..:\n  %s",
			strings.Join(fromRoot, "\n  "), strings.Join(fromSub, "\n  "))
	}
	for _, stray := range []string{"build", ".buildy_cache"} {
		if _, err := os.Stat(filepath.Join(dir, "sub", stray)); err == nil {
			t.Errorf("buildy .. created sub/%s", stray)
		}
	}
	want := filepath.Join("build", fmt.Sprintf("%s-%s-debug", detectPlatform(), detectArchitecture()), "obj", ".", "app", "src", "main.o")
	if !strings.Contains(strings.Join(fromRoot, "\n"), filepath.Clean(want)) {
		t.Errorf("no object at %s:\n  %s", filepath.Clean(want), strings.Join(fromRoot, "\n  "))
	}
}

// Finding 3: a changed static library relinks the executable.
func TestE2EStaticRelink(t *testing.T) {
	needCpp(t)
	dir := fixture(t, "static_relink")
	app := filepath.Join(outDir(dir, "debug"), "bin", "app")

	mustBuildy(t, dir)
	if got := run(t, app); got != "5" {
		t.Fatalf("app printed %q, want 5", got)
	}
	writeFile(t, filepath.Join(dir, "add.cpp"), "int add(int a, int b) { return a * b; }\n")
	mustBuildy(t, dir)
	if got := run(t, app); got != "6" {
		t.Errorf("after changing add(), app printed %q, want 6: it was not relinked", got)
	}
}

// Finding 4 and S6: editing a package imported from outside the target's directory, or a file it
// embeds, changes the build. Go runs every time and reports "ran", never a cache hit.
func TestE2EGoOutsideImport(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		t.Skip("skipping: there is no built-in Go toolchain for macOS arm64")
	}
	needTools(t, "go")
	dir := fixture(t, "go_outside_import")
	app := filepath.Join(outDir(dir, "debug"), "bin", "app"+exeSuffix())

	mustBuildy(t, dir)
	if got := run(t, app); got != "v1 m1" {
		t.Fatalf("app printed %q, want %q", got, "v1 m1")
	}

	greet := filepath.Join(dir, "internal", "greet", "greet.go")
	data, err := os.ReadFile(greet)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, greet, strings.Replace(string(data), `"v1 "`, `"v2 "`, 1))
	out := mustBuildy(t, dir)
	if got := run(t, app); got != "v2 m1" {
		t.Errorf("after editing internal/greet, app printed %q, want %q", got, "v2 m1")
	}
	if !strings.Contains(out, "0 cache hits") {
		t.Errorf("the Go build was a cache hit:\n%s", out)
	}

	writeFile(t, filepath.Join(dir, "internal", "greet", "message.txt"), "m2")
	mustBuildy(t, dir)
	if got := run(t, app); got != "v2 m2" {
		t.Errorf("after editing the embedded file, app printed %q, want %q", got, "v2 m2")
	}
}

// Finding 5 and S7: the declared Rust outputs are where Cargo writes them, and a deleted
// binary is rebuilt.
func TestE2ERustOutputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping: the built-in Windows Rust toolchain cross-compiles from Linux")
	}
	needTools(t, "cargo")
	dir := fixture(t, "rust_outputs")
	cargoDir := filepath.Join(outDir(dir, "debug"), "cargo", "debug")
	hello := filepath.Join(cargoDir, "hello")

	mustBuildy(t, dir)
	if got := run(t, hello); got != "hello" {
		t.Fatalf("hello printed %q", got)
	}
	if _, err := os.Stat(filepath.Join(cargoDir, "libcalc.a")); err != nil {
		t.Errorf("static library: %v", err)
	}

	if err := os.Remove(hello); err != nil {
		t.Fatal(err)
	}
	mustBuildy(t, dir)
	if _, err := os.Stat(hello); err != nil {
		t.Errorf("the deleted binary was not rebuilt: %v", err)
	}
}

// Finding 9 and S14: a Docker toolchain's commands run in a container that sees the workspace
// at its host path. Runs only with BUILDY_TEST_DOCKER=1.
func TestE2EDockerExecution(t *testing.T) {
	if os.Getenv("BUILDY_TEST_DOCKER") != "1" {
		t.Skip("skipping: set BUILDY_TEST_DOCKER=1 to run the Docker case")
	}
	needTools(t, "docker")
	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Skip("skipping: already inside a container")
	}
	dir := fixture(t, "docker_exec")
	mustBuildy(t, dir, "--toolchain", "busybox-cpp")
	app := filepath.Join(outDir(dir, "debug"), "bin", "app")
	data, err := os.ReadFile(app)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "compiled\n" {
		t.Errorf("app = %q", data)
	}

	// The outputs belong to the invoking user: the next build can replace them.
	writeFile(t, filepath.Join(dir, "main.cpp"), "again\n")
	mustBuildy(t, dir, "--toolchain", "busybox-cpp")
	if data, _ := os.ReadFile(app); string(data) != "again\n" {
		t.Errorf("after an edit app = %q", data)
	}
}

// Finding 7: a module listed under linux: is built for linux and not generated for windows.
func TestE2EPlatformModule(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping: the fixture builds for linux")
	}
	needCpp(t)
	dir := fixture(t, "platform_module")
	out := mustBuildy(t, dir, "--platform", "linux")
	if !strings.Contains(out, "from 1 modules") {
		t.Errorf("linux build didn't load platform-linux:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(outDir(dir, "debug"), "bin", "linuxonly")); err != nil {
		t.Errorf("linuxonly was not built: %v", err)
	}

	out = mustBuildy(t, dir, "--platform", "windows", "--dry-run")
	if strings.Contains(out, "linuxonly") || !strings.Contains(out, "from 0 modules") {
		t.Errorf("windows build generated the linux module:\n%s", out)
	}
}

// Finding 11: an unknown key fails the build, naming the key and the file.
func TestE2EUnknownKey(t *testing.T) {
	dir := fixture(t, "unknown_key")
	out := mustFailBuildy(t, dir)
	for _, want := range []string{"bogus_key", "defnies", "did you mean 'defines'", filepath.Join(dir, "buildy.yaml")} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "build")); err == nil {
		t.Error("the invalid config was built")
	}
}

// Finding 8 and S10: --target app builds calc from another module first, and nothing else;
// after calc changes, --target app relinks against the new calc. An unknown target fails.
func TestE2ETargetClosure(t *testing.T) {
	needCpp(t)
	dir := fixture(t, "target_closure")
	app := filepath.Join(outDir(dir, "debug"), "bin", "app")

	mustBuildy(t, dir, "--target", "app")
	if got := run(t, app); got != "5" {
		t.Fatalf("app printed %q, want 5", got)
	}

	writeFile(t, filepath.Join(dir, "libmod", "add.cpp"), "int add(int a, int b) { return a * b; }\n")
	if err := os.Remove(app); err != nil {
		t.Fatal(err)
	}
	mustBuildy(t, dir, "--target", "app")
	if got := run(t, app); got != "6" {
		t.Errorf("after changing calc, app printed %q, want 6 (a stale libcalc.a was linked)", got)
	}

	out := mustFailBuildy(t, dir, "--target", "nosuch")
	if !strings.Contains(out, "unknown target 'nosuch'") {
		t.Errorf("--target nosuch:\n%s", out)
	}
}

// Finding 10: --dry-run neither fetches a dependency nor runs its build.
func TestE2EDryRunDependency(t *testing.T) {
	needCpp(t)
	needTools(t, "git", "make")
	dir := fixture(t, "dry_run_dep")

	// The dependency is a local git repository, outside the workspace.
	repo := filepath.Join(t.TempDir(), "dep")
	if err := os.Rename(filepath.Join(dir, "dep"), repo); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "add", "."},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "dep"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeFile(t, filepath.Join(dir, "buildy_config", "dependencies.yaml"),
		fmt.Sprintf("mydep:\n  git: %q\n  build: {system: make}\n", repo))

	out := mustBuildy(t, dir, "--dry-run")
	cloned := filepath.Join(dir, ".buildy_cache", "deps", "mydep")
	if _, err := os.Stat(cloned); err == nil {
		t.Errorf("--dry-run cloned the dependency into %s", cloned)
	}
	for _, want := range []string{"would fetch mydep", "would build mydep"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(outDir(dir, "debug"), "bin", "app")); err == nil {
		t.Error("--dry-run built the app")
	}

	// A real build fetches it and runs its Makefile for debug and release.
	mustBuildy(t, dir)
	log, err := os.ReadFile(filepath.Join(cloned, "side_effect.log"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(log), "built"); got != 2 {
		t.Errorf("the dependency was built %d times, want 2 (debug and release)", got)
	}
}

// Findings 1.4 and 1.5: a root variable reaches a module, and a module's environment.compile
// adds to the root's instead of replacing it.
func TestE2ERootSettingsReachModules(t *testing.T) {
	needCpp(t)
	needTools(t, "gcc")
	dir := fixture(t, "root_settings")
	mustBuildy(t, dir)
	if got := run(t, filepath.Join(outDir(dir, "debug"), "bin", "app")); got != "42 1" {
		t.Errorf("app printed %q, want 42 1", got)
	}
}

// Finding 1.6: {pattern: ..., optional: true} is compiled, and may match nothing.
func TestE2EOptionalSources(t *testing.T) {
	needCpp(t)
	needTools(t, "gcc", "ar")
	dir := fixture(t, "optional_sources")
	mustBuildy(t, dir)
	if got := run(t, filepath.Join(outDir(dir, "debug"), "bin", "app")); got != "7" {
		t.Errorf("app printed %q, want 7", got)
	}
	if members := run(t, "ar", "t", filepath.Join(outDir(dir, "debug"), "lib", "libplugins.a")); members != "p.o" {
		t.Errorf("libplugins.a holds %q, want p.o", members)
	}
}

// Finding 1.2: every run generates the same commands, with a filter map's matching keys in the
// order written.
func TestE2EFilterOrderIsStable(t *testing.T) {
	dir := fixture(t, "filter_order")
	commands := func() (compile, link string) {
		mustBuildy(t, dir, "--dry-run", "--platform", "linux", "--architecture", "x86_64")
		data, err := os.ReadFile(filepath.Join(dir, ".buildy_cache", "tasks_linux-x86_64-debug.json"))
		if err != nil {
			t.Fatal(err)
		}
		var graph struct {
			Tasks []struct {
				TaskType string `json:"task_type"`
				Command  struct {
					Args []string `json:"args"`
				} `json:"command"`
			} `json:"tasks"`
		}
		if err := json.Unmarshal(data, &graph); err != nil {
			t.Fatal(err)
		}
		for _, task := range graph.Tasks {
			switch task.TaskType {
			case "compile":
				compile = strings.Join(task.Command.Args, " ")
			case "link":
				link = strings.Join(task.Command.Args, " ")
			}
		}
		return compile, link
	}
	compile, link := commands()
	if !strings.Contains(compile, "-DA -DB -DC -DD -DE") {
		t.Errorf("compile command doesn't define A to E in order: %s", compile)
	}
	if !strings.Contains(link, "-lm -ldl -lpthread") {
		t.Errorf("link command doesn't link m, dl, pthread in order: %s", link)
	}
	for i := 0; i < 10; i++ {
		if c, l := commands(); c != compile || l != link {
			t.Fatalf("run %d generated other commands:\n%s\n%s", i, c, l)
		}
	}
}

// Finding 5.4: typos outside buildy.yaml's keys are errors too, naming the file, the line and
// what was meant: a filter key, an abstract keyword, a key of a workspace toolchain.
func TestE2EConfigTypos(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		wants []string
	}{
		{
			name:  "filter key",
			files: map[string]string{"buildy.yaml": "project: {name: t}\nenvironment:\n  compile:\n    cpp_standard: c++17\n    defines: [{linxu: [X]}]\ntargets:\n  executables:\n    - {name: app, language: cpp, sources: [main.cpp]}\n"},
			wants: []string{"buildy.yaml:5: filter key 'linxu'", "did you mean 'linux'"},
		},
		{
			name:  "abstract keyword",
			files: map[string]string{"buildy.yaml": "project: {name: t}\nenvironment:\n  compile:\n    cpp_standard: c++17\n    optimisation: full\ntargets:\n  executables:\n    - {name: app, language: cpp, sources: [main.cpp]}\n"},
			wants: []string{"buildy.yaml:5: unknown key 'environment.compile.optimisation'", "did you mean 'optimization'"},
		},
		{
			name: "toolchain key",
			files: map[string]string{"buildy_config/toolchains/mine.yaml": `toolchain:
  name: mine
  tools:
    compile:
      action: compile
      command: "cc -c ${input} -o ${output}"
      input_extension: [".c"]
`},
			wants: []string{filepath.Join("buildy_config", "toolchains", "mine.yaml"), "line 7: unknown key 'toolchain.tools.compile.input_extension'", "did you mean 'input_extensions'"},
		},
		{
			name:  "platform name",
			files: map[string]string{},
			wants: []string{"'linxu' is not a platform"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := fixture(t, "config_typos")
			for path, content := range c.files {
				writeFile(t, filepath.Join(dir, path), content)
			}
			args := []string{"--dry-run"}
			if c.name == "platform name" {
				args = append(args, "--platform", "linxu")
			}
			out := mustFailBuildy(t, dir, args...)
			for _, want := range c.wants {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

// Finding 2.1: under a path with spaces and quotes, every kind of task works, a define whose
// value holds a space and quotes arrives intact, and $ORIGIN reaches the linker as written.
func TestE2ESpacedPaths(t *testing.T) {
	needCpp(t)
	needTools(t, "gcc", "ar")
	dir := fixtureUnder(t, "spaced_paths", `it's a "spaced" dir`)
	out := outDir(dir, "debug")
	mustBuildy(t, dir, "--compile-commands")

	app := filepath.Join(out, "bin", "app")
	if got := run(t, app); got != "hello world 5" {
		t.Errorf("app printed %q, want %q", got, "hello world 5")
	}
	if runtime.GOOS == "linux" {
		needTools(t, "readelf")
		if dyn := run(t, "readelf", "-d", app); !strings.Contains(dyn, "[$ORIGIN]") {
			t.Errorf("app's dynamic section has no $ORIGIN run path:\n%s", dyn)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(out, "gen", "version.h")); string(got) != "#define VERSION \"1.0 \"beta\" & co/x\"\n#define ALSO \"1.0 \"beta\" & co/x\"\n" {
		t.Errorf("version.h = %q", got)
	}
	for _, file := range []string{filepath.Join("copied dir", "notes.txt"), filepath.Join("transformed dir", "notes.out")} {
		if _, err := os.Stat(filepath.Join(out, file)); err != nil {
			t.Errorf("%s is missing: %v", file, err)
		}
	}
	stage := filepath.Join(out, "stage dir")
	if target, err := os.Readlink(filepath.Join(stage, "bin", "app")); err != nil || target != app {
		t.Errorf("staged bin/app -> %q (%v), want a symlink to %s", target, err, app)
	}
	if info, err := os.Lstat(filepath.Join(stage, "data dir", "notes.txt")); err != nil || !info.Mode().IsRegular() {
		t.Errorf("staged notes.txt is not a copy: %v", err)
	}

	// The archives hold the files the links point to, under the staging area's paths.
	want := "bin/ bin/app bin/libgreet.so data dir/ data dir/notes.txt"
	if got := strings.Join(tarNames(t, filepath.Join(out, "dist dir", "spaced pkg.tar.gz")), " "); got != want {
		t.Errorf("tar.gz entries %q, want %q", got, want)
	}
	if got := strings.Join(zipNames(t, filepath.Join(out, "dist dir", "spaced pkg.zip")), " "); got != want {
		t.Errorf("zip entries %q, want %q", got, want)
	}

	// compile_commands.json has each compile's arguments, the define as one of them.
	data, err := os.ReadFile(filepath.Join(dir, "buildy_config", "compile_commands.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		Arguments []string `json:"arguments"`
		File      string   `json:"file"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		for _, arg := range e.Arguments {
			found = found || arg == `-DMSG="hello world"`
		}
	}
	if len(entries) != 3 || !found {
		t.Errorf("compile_commands.json has %d entries, the define as one argument: %v\n%s", len(entries), found, data)
	}

	if out := mustBuildy(t, dir); !strings.Contains(out, "0 executed") {
		t.Errorf("a no-op build executed tasks:\n%s", out)
	}
}

func tarNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
}

func zipNames(t *testing.T, path string) []string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

// Finding 2.2: an archive of 1500 objects runs with a response file. buildy runs with a 1 MiB
// stack limit, so Linux lets a program start with 256 KiB of arguments and environment; the
// archive's ~300 KiB command line is over it, and over the 128 KiB that sh -c took as one
// argument.
func TestE2EManyObjects(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping: the limits measured are Linux's")
	}
	needTools(t, "gcc", "ar", "sh")
	dir := fixture(t, "many_objects")
	long := filepath.Join("src", strings.Repeat("d", 100))
	for i := 1; i <= 1500; i++ {
		writeFile(t, filepath.Join(dir, long, fmt.Sprintf("f_%04d.c", i)), fmt.Sprintf("int f_%04d(void) { return %d; }\n", i, i))
	}
	cmd := exec.Command("sh", "-c", `ulimit -s 1024 && exec "$0" "$@"`, buildyBinary, "-j", fmt.Sprint(runtime.NumCPU()), "-n", "3")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("buildy failed: %v\n%s", err, tail(string(out), 40))
	}
	if !strings.Contains(string(out), "Arguments in response file") {
		t.Errorf("the archive ran without a response file:\n%s", tail(string(out), 40))
	}
	if got := run(t, filepath.Join(outDir(dir, "debug"), "bin", "app")); got != "1501" {
		t.Errorf("app printed %q, want 1501", got)
	}
	if members := strings.Fields(run(t, "ar", "t", filepath.Join(outDir(dir, "debug"), "lib", "libmany.a"))); len(members) != 1500 {
		t.Errorf("libmany.a has %d members, want 1500", len(members))
	}
	if rsp, _ := filepath.Glob(filepath.Join(dir, ".buildy_cache", "response_files", "*")); len(rsp) != 0 {
		t.Errorf("response files left behind: %v", rsp)
	}
}

// tail returns the last n lines of text.
func tail(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Finding 4.2: adding a target, or changing another target's define, reruns only that target's
// tasks.
func TestE2EEditingATargetRerunsOnlyIt(t *testing.T) {
	needTools(t, "gcc")
	if runtime.GOOS == "windows" {
		t.Skip("skipping: the C cases run on Linux and macOS")
	}
	dir := t.TempDir()
	config := `project:
  name: edits
environment:
  compile:
    c_standard: c11
targets:
  executables:
    - name: one
      language: c
      sources: [one.c]
    - name: two
      language: c
      sources: [two.c]
`
	writeFile(t, filepath.Join(dir, "buildy.yaml"), config)
	writeFile(t, filepath.Join(dir, "one.c"), "int main(void) { return 0; }\n")
	writeFile(t, filepath.Join(dir, "two.c"), "int main(void) { return 0; }\n")
	writeFile(t, filepath.Join(dir, "three.c"), "int main(void) { return 0; }\n")
	mustBuildy(t, dir)

	config += `    - name: three
      language: c
      sources: [three.c]
`
	writeFile(t, filepath.Join(dir, "buildy.yaml"), config)
	if out := mustBuildy(t, dir); !strings.Contains(out, "Tasks: 6 total, 4 cache hits, 2 executed") {
		t.Errorf("adding a target reran others:\n%s", tail(out, 20))
	}
	config = strings.Replace(config, "sources: [two.c]", "sources: [two.c]\n      defines: [TWO=2]", 1)
	writeFile(t, filepath.Join(dir, "buildy.yaml"), config)
	if out := mustBuildy(t, dir); !strings.Contains(out, "Tasks: 6 total, 5 cache hits, 1 executed") {
		t.Errorf("a define on two reran other tasks:\n%s", tail(out, 20))
	}
}

// buildyEnv runs buildy in dir with extra environment variables, and fails the test if it fails.
func buildyEnv(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(buildyBinary, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GOFLAGS=-buildvcs=false"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("buildy %s in %s failed: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

// Finding 1.3: a header found through CPATH, or another gcc earlier on the PATH, rebuilds.
func TestE2EToolchainIdentity(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping: the case uses gcc on Linux")
	}
	needTools(t, "gcc")
	gcc, _ := exec.LookPath("gcc")
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "buildy.yaml"), `project:
  name: identity
environment:
  compile:
    c_standard: c11
targets:
  executables:
    - name: app
      language: c
      sources: [main.c]
`)
	writeFile(t, filepath.Join(dir, "main.c"), "#include <stdio.h>\n#include \"value.h\"\n#ifndef WRAPPED\n#define WRAPPED 0\n#endif\nint main(void) { printf(\"%d %d\\n\", VALUE, WRAPPED); return 0; }\n")
	writeFile(t, filepath.Join(dir, "one", "value.h"), "#define VALUE 1\n")
	writeFile(t, filepath.Join(dir, "two", "value.h"), "#define VALUE 2\n")
	app := filepath.Join(outDir(dir, "debug"), "bin", "app")

	buildyEnv(t, dir, []string{"CPATH=" + filepath.Join(dir, "one")})
	if got := run(t, app); got != "1 0" {
		t.Fatalf("app printed %q, want 1 0", got)
	}
	buildyEnv(t, dir, []string{"CPATH=" + filepath.Join(dir, "two")})
	if got := run(t, app); got != "2 0" {
		t.Errorf("after changing CPATH the app printed %q, want 2 0", got)
	}

	// A gcc wrapper earlier on the PATH
	wrapper := filepath.Join(dir, "wrapper", "gcc")
	writeFile(t, wrapper, "#!/bin/sh\nexec "+gcc+" -DWRAPPED=1 \"$@\"\n")
	os.Chmod(wrapper, 0o755)
	path := "PATH=" + filepath.Dir(wrapper) + string(os.PathListSeparator) + os.Getenv("PATH")
	buildyEnv(t, dir, []string{"CPATH=" + filepath.Join(dir, "two"), path})
	if got := run(t, app); got != "2 1" {
		t.Errorf("with another gcc on the PATH the app printed %q, want 2 1", got)
	}
	out := buildyEnv(t, dir, []string{"CPATH=" + filepath.Join(dir, "two"), path})
	if !strings.Contains(out, "2 cache hits, 0 executed") {
		t.Errorf("an unchanged build ran tasks:\n%s", tail(out, 20))
	}
}
