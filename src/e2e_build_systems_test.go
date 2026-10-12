package main

// End-to-end tests of build system definitions of your own (doc/design/dependencies.md): a
// fetched dependency built by a build system that isn't built in, and where definitions are
// searched. The remotes are local git repositories; nothing uses the network.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"buildy/pkg/util"
)

// plainSystem is a build system that isn't built in: build.sh records its arguments, compiles
// plain.c into libplain.a with the build type's value, and a shell phase installs the library and
// its header.
const plainSystem = `# A library built by its build.sh
build_system:
  name: plain
  description: "build.sh and a static library"

  detection:
    marker_files: [build.sh]
    priority: 10

  configurations:
    debug: dbg
    release: opt

  reads_env: [PLAIN_EXTRA]

  phases:
    configure:
      command: "sh ${source_dir}/build.sh configure ${build_type} ${config} ${platform} ${arch} ${jobs} ${buildy_exe} ${config_file} ${source_dir} ${build_dir} ${install_dir} ${args}"
      default_args: [--plain]
      working_dir: "${build_dir}"

    build:
      command: "sh ${source_dir}/build.sh build ${build_type}"
      working_dir: "${build_dir}"

    install:
      shell: true
      command: "mkdir -p ${install_dir}/include ${install_dir}/lib && cp ${source_dir}/plain.h ${install_dir}/include/ && cp libplain.a ${install_dir}/lib/"
      working_dir: "${build_dir}"

  output:
    include_patterns: ["${install_dir}/include"]
    lib_patterns: ["${install_dir}/lib"]
`

const plainBuildScript = `set -e
step=$1
shift
case $step in
configure)
  for a in "$@"; do echo "$a"; done > placeholders.txt ;;
build)
  case $1 in
  dbg) v=1 ;;
  opt) v=2 ;;
  *) echo "unknown build type $1"; exit 1 ;;
  esac
  cc -c -DPLAIN_VALUE=$v "$(dirname "$0")/plain.c" -o plain.o
  ar rcs libplain.a plain.o ;;
esac
`

// plainWorkspace makes a git repository of the plain library and a workspace whose app links it,
// with the plain build system in buildy_config/build_systems/; it returns the workspace.
func plainWorkspace(t *testing.T, build string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "plain")
	writeFile(t, filepath.Join(repo, "build.sh"), plainBuildScript)
	writeFile(t, filepath.Join(repo, "plain.h"), "int plain_value(void);\n")
	writeFile(t, filepath.Join(repo, "plain.c"), "int plain_value(void) { return PLAIN_VALUE; }\n")
	gitRepo(t, repo)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "buildy.yaml"), `project:
  name: plainapp
environment:
  compile:
    c_standard: c11
targets:
  executables:
    - name: app
      language: c
      sources: [main.c]
      deps: [plain]
`)
	writeFile(t, filepath.Join(dir, "main.c"), "#include <stdio.h>\n#include \"plain.h\"\nint main(void) { printf(\"%d\\n\", plain_value()); return 0; }\n")
	writeFile(t, filepath.Join(dir, "buildy_config", "dependencies.yaml"),
		fmt.Sprintf("plain:\n  git: %q\n  build: %s\n  libs: [plain]\n", fileURL(repo), build))
	writeFile(t, filepath.Join(dir, "buildy_config", "build_systems", "plain.yaml"), plainSystem)
	return dir
}

// A dependency built by a build system of the workspace's: detected by build.system auto, each
// configuration in its own build directory, every placeholder filled in, a shell phase, the
// output patterns giving the app the header and library; a no-op build runs nothing of it, an
// edited definition or a variable it reads rebuilds it, an unmapped configuration fails.
func TestE2EUserBuildSystem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping: the build system runs sh")
	}
	needTools(t, "git", "cc", "ar", "gcc")
	dir := plainWorkspace(t, `{system: auto, args: ["EXTRA=${config}"]}`)
	app := func(config string) string {
		return run(t, filepath.Join(outDir(dir, config), "bin", "app"))
	}
	src := filepath.Join(dir, ".buildy_cache", "deps", "plain")
	key := func(config string) string {
		return fmt.Sprintf("%s-%s-%s", detectPlatform(), detectArchitecture(), config)
	}

	out := mustBuildy(t, dir, "-n", "3")
	if !strings.Contains(out, "Auto-detected build system: plain for plain") {
		t.Errorf("build.system auto didn't pick plain:\n%s", out)
	}
	if got := app("debug"); got != "1" {
		t.Errorf("the debug app printed %s, want 1 (plain's dbg build)", got)
	}
	exe, err := filepath.EvalSymlinks(buildyBinary)
	if err != nil {
		t.Fatal(err)
	}
	buildDir := filepath.Join(src, "_build", key("debug"))
	data, err := os.ReadFile(filepath.Join(buildDir, "placeholders.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dbg", "debug", detectPlatform(), detectArchitecture(), fmt.Sprint(runtime.NumCPU()), exe, src,
		src, buildDir, filepath.Join(src, "_install", key("debug")), "--plain", "EXTRA=debug"}
	if got := strings.Split(strings.TrimSpace(string(data)), "\n"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("the configure phase got\n %q\nwant\n %q", got, want)
	}

	// release has its own build and install directories, and debug's stay
	mustBuildy(t, dir, "-c", "release")
	if got := app("release"); got != "2" {
		t.Errorf("the release app printed %s, want 2 (plain's opt build)", got)
	}
	for _, config := range []string{"debug", "release"} {
		for _, f := range []string{
			filepath.Join("_build", key(config), "libplain.a"),
			filepath.Join("_build", key(config)+".built.json"),
			filepath.Join("_install", key(config), "lib", "libplain.a"),
			filepath.Join("_install", key(config), "include", "plain.h"),
		} {
			if _, err := os.Stat(filepath.Join(src, f)); err != nil {
				t.Errorf("%s: %v", config, err)
			}
		}
	}

	// A no-op build runs nothing of the dependency's
	out = mustBuildy(t, dir, "-c", "release", "-n", "3")
	if strings.Contains(out, "Executing") || strings.Contains(out, "Building dependency") || !strings.Contains(out, "Dependency plain (release) is up to date") {
		t.Errorf("a no-op build ran the dependency's build:\n%s", out)
	}

	// Editing the definition rebuilds, and so does a variable it reads
	systemFile := filepath.Join(dir, "buildy_config", "build_systems", "plain.yaml")
	writeFile(t, systemFile, plainSystem+"# edited\n")
	if out = mustBuildy(t, dir, "-c", "release"); !strings.Contains(out, "Building dependency: plain (release, using plain)") {
		t.Errorf("editing the definition didn't rebuild the dependency:\n%s", out)
	}
	if out = buildyEnv(t, dir, []string{"PLAIN_EXTRA=1"}, "-c", "release"); !strings.Contains(out, "Building dependency: plain (release, using plain)") {
		t.Errorf("a variable in reads_env didn't rebuild the dependency:\n%s", out)
	}

	// A configuration the definition doesn't map fails, naming it
	out = mustFailBuildy(t, dir, "-c", "portable")
	if !strings.Contains(out, "build system plain has no configuration for buildy's 'portable'") {
		t.Errorf("an unmapped configuration:\n%s", out)
	}
}

// A definition with a built-in's name replaces it and says so; two build systems detecting a
// tree at one priority, and a definition with an unknown key, are errors naming the files.
func TestE2EUserBuildSystemErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping: the build system runs sh")
	}
	needTools(t, "git", "cc", "ar", "gcc")
	dir := plainWorkspace(t, "{system: auto}")
	systems := filepath.Join(dir, "buildy_config", "build_systems")

	makeFile := filepath.Join(systems, "make.yaml")
	writeFile(t, makeFile, strings.Replace(strings.Replace(plainSystem, "name: plain", "name: make", 1), "build.sh]", "Makefile]", 1))
	out := mustBuildy(t, dir, "-n", "3")
	if !strings.Contains(out, "Build system 'make' from "+makeFile+" replaces the one from built-in build_systems/make.yaml") {
		t.Errorf("replacing the built-in make wasn't logged:\n%s", out)
	}
	if err := os.Remove(makeFile); err != nil {
		t.Fatal(err)
	}

	other := filepath.Join(systems, "plain2.yaml")
	writeFile(t, other, strings.Replace(plainSystem, "name: plain", "name: plain2", 1))
	out = mustFailBuildy(t, dir, "-f")
	if !strings.Contains(out, "build systems plain (") || !strings.Contains(out, "plain2 ("+other) || !strings.Contains(out, "at the same priority (10)") {
		t.Errorf("a detection tie:\n%s", out)
	}

	writeFile(t, other, "build_system:\n  name: plain2\n  phases:\n    build: {comand: make}\n")
	out = mustFailBuildy(t, dir)
	if !strings.Contains(out, other) || !strings.Contains(out, "unknown key 'build_system.phases.build.comand' (did you mean 'command'?)") {
		t.Errorf("an unknown key:\n%s", out)
	}
}

// Build systems are searched like toolchains: next to the executable (found from the PATH, not
// the current directory), then each --add-data-dir, then the workspace, each replacing the
// one before.
func TestE2EBuildSystemSearchOrder(t *testing.T) {
	bin := t.TempDir()
	exe := filepath.Join(bin, "buildy"+exeSuffix())
	data, err := os.ReadFile(buildyBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatal(err)
	}
	define := func(dir, where string) string {
		path := filepath.Join(dir, "plain.yaml")
		writeFile(t, path, strings.Replace(plainSystem, "build.sh and a static library", where, 1))
		return path
	}
	define(filepath.Join(bin, "data", "build_systems"), "exe")
	// buildy names the data directory by its executable's real path: symlinks resolved, and on
	// Windows short (8.3) names in full, as a temporary directory's often are
	realBin, err := util.RealPath(bin)
	if err != nil {
		t.Fatal(err)
	}
	byExe := filepath.Join(realBin, "data", "build_systems", "plain.yaml")
	extra := t.TempDir()
	byData := define(filepath.Join(extra, "build_systems"), "data dir")
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "buildy.yaml"), "project:\n  name: order\n")
	byWorkspace := define(filepath.Join(ws, "buildy_config", "build_systems"), "workspace")
	// The current directory's data/ is not the executable's
	writeFile(t, filepath.Join(ws, "data", "build_systems", "broken.yaml"), "not: a build system\n")

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd := exec.Command("buildy"+exeSuffix(), "-n", "3", "--add-data-dir", extra)
	cmd.Dir = ws
	outBytes, err := cmd.CombinedOutput()
	out := string(outBytes)
	if err != nil {
		t.Fatalf("buildy: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Loaded build system: plain - exe (" + byExe + ")",
		"Build system 'plain' from " + byData + " replaces the one from " + byExe,
		"Build system 'plain' from " + byWorkspace + " replaces the one from " + byData,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}
}

// A dependency's execution section runs a user-defined build system's phases in Docker.
func TestE2EUserBuildSystemDocker(t *testing.T) {
	if os.Getenv("BUILDY_TEST_DOCKER") != "1" {
		t.Skip("skipping: set BUILDY_TEST_DOCKER=1 to run the Docker case")
	}
	needTools(t, "docker", "git")
	needCpp(t)
	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Skip("skipping: already inside a container")
	}
	repo := filepath.Join(t.TempDir(), "boxed")
	writeFile(t, filepath.Join(repo, "README"), "a tree\n")
	gitRepo(t, repo)
	dir := t.TempDir()
	writeDepUser(t, dir, "boxed")
	writeFile(t, filepath.Join(dir, "buildy_config", "build_systems", "boxed.yaml"), `build_system:
  name: boxed
  phases:
    build:
      shell: true
      command: "test -f /.dockerenv && echo ${config} > ${install_dir}/where"
`)
	writeFile(t, filepath.Join(dir, "buildy_config", "dependencies.yaml"), fmt.Sprintf(`boxed:
  git: %q
  build: {system: boxed}
  execution: {type: docker, image: busybox}
`, fileURL(repo)))
	mustBuildy(t, dir)
	where := filepath.Join(dir, ".buildy_cache", "deps", "boxed", "_install",
		fmt.Sprintf("%s-%s-debug", detectPlatform(), detectArchitecture()), "where")
	if data, err := os.ReadFile(where); err != nil || string(data) != "debug\n" {
		t.Errorf("the phase in Docker wrote %q, %v", data, err)
	}
	// The outputs belong to the invoking user: the next build can replace them
	mustBuildy(t, dir, "-f")
}
