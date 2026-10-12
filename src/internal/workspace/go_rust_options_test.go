package workspace

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// argvOf returns a task's directory and arguments, with the workspace root and the output
// directory written as <root> and <out>, the arguments separated by " | ".
func argvOf(task *BuildTask, root, outDir string) string {
	replace := func(s string) string {
		s = strings.ReplaceAll(s, outDir, "<out>")
		return filepath.ToSlash(strings.ReplaceAll(s, root, "<root>"))
	}
	args := make([]string, len(task.Command.Args))
	for i, arg := range task.Command.Args {
		args[i] = replace(arg)
	}
	return replace(task.Command.Dir) + ": " + strings.Join(args, " | ")
}

// checkArgv generates root's tasks for each context and compares each target's command with
// the one wanted.
func checkArgv(t *testing.T, root string, cases []argvCase) {
	t.Helper()
	for _, c := range cases {
		tasks, err := generateTasksFor(t, root, c.platform, c.arch, c.config)
		if err != nil {
			t.Fatalf("%s-%s-%s: %v", c.platform, c.arch, c.config, err)
		}
		outDir := filepath.Join(root, "build", c.platform+"-"+c.arch+"-"+c.config)
		for _, target := range sortedKeys(c.want) {
			if got := argvOf(outputOf(t, tasks, target), root, outDir); got != c.want[target] {
				t.Errorf("%s-%s-%s %s:\n got %s\nwant %s", c.platform, c.arch, c.config, target, got, c.want[target])
			}
		}
	}
}

type argvCase struct {
	platform, arch, config string
	want                   map[string]string // target -> its command (argvOf)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Every built-in Go toolchain passes a target's options as go build takes them: the tags joined
// with commas, all the ldflags as one argument (each item's own quoting kept for go to read),
// the gcflags as one argument, and -trimpath in release. A shared library is c-shared, a static
// library c-archive, each named as the platform names libraries.
func TestGoOptionsReachTheCommand(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"buildy.yaml": `project: {name: opts, version: 1.2.3}
targets:
  executables:
    - name: app
      language: go
      path: cmd/app
      build_tags: [a, b]
      ldflags: ["-s", "-X main.version=${project_version}", "-X 'main.msg=hello world'"]
      gcflags: ["all=-N", "-l"]
    - {name: plain, language: go, path: cmd/plain}
    - {name: hardened, language: go, path: cmd/plain, buildmode: pie}
  shared_libraries:
    - {name: clib, language: go, path: clib, build_tags: [a]}
  static_libraries:
    - {name: carc, language: go, path: clib}
`})
	app := "-tags | a,b | -ldflags=-s -X main.version=1.2.3 -X 'main.msg=hello world' | -gcflags=all=-N -l | ."
	checkArgv(t, root, []argvCase{
		{"linux", "x86_64", "debug", map[string]string{
			"app":      "<root>/cmd/app: go | build | -o | <out>/bin/app | " + app,
			"plain":    "<root>/cmd/plain: go | build | -o | <out>/bin/plain | .",
			"hardened": "<root>/cmd/plain: go | build | -buildmode=pie | -o | <out>/bin/hardened | .",
			"clib":     "<root>/clib: go | build | -buildmode=c-shared | -o | <out>/lib/libclib.so | -tags | a | .",
			"carc":     "<root>/clib: go | build | -buildmode=c-archive | -o | <out>/lib/libcarc.a | .",
		}},
		{"linux", "x86_64", "release", map[string]string{
			"app":  "<root>/cmd/app: go | build | -trimpath | -o | <out>/bin/app | " + app,
			"clib": "<root>/clib: go | build | -buildmode=c-shared | -trimpath | -o | <out>/lib/libclib.so | -tags | a | .",
			"carc": "<root>/clib: go | build | -buildmode=c-archive | -trimpath | -o | <out>/lib/libcarc.a | .",
		}},
		{"macos", "x86_64", "release", map[string]string{
			"app":  "<root>/cmd/app: go | build | -trimpath | -o | <out>/bin/app | " + app,
			"clib": "<root>/clib: go | build | -buildmode=c-shared | -trimpath | -o | <out>/lib/libclib.dylib | -tags | a | .",
			"carc": "<root>/clib: go | build | -buildmode=c-archive | -trimpath | -o | <out>/lib/libcarc.a | .",
		}},
		{"macos", "arm64", "debug", map[string]string{
			"app":  "<root>/cmd/app: go | build | -o | <out>/bin/app | " + app,
			"clib": "<root>/clib: go | build | -buildmode=c-shared | -o | <out>/lib/libclib.dylib | -tags | a | .",
			"carc": "<root>/clib: go | build | -buildmode=c-archive | -o | <out>/lib/libcarc.a | .",
		}},
		{"windows", "x86_64", "debug", map[string]string{
			"app":      "<root>/cmd/app: go | build | -o | <out>/bin/app.exe | " + app,
			"hardened": "<root>/cmd/plain: go | build | -buildmode=pie | -o | <out>/bin/hardened.exe | .",
			"clib":     "<root>/clib: go | build | -buildmode=c-shared | -o | <out>/lib/clib.dll | -tags | a | .",
			"carc":     "<root>/clib: go | build | -buildmode=c-archive | -o | <out>/lib/libcarc.a | .",
		}},
	})
}

// Every built-in Rust toolchain passes a target's features joined with commas, --release in
// release, --lib for a library, --bin for an executable that names its binary, and its target
// triple when it cross-compiles.
func TestRustOptionsReachTheCommand(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"buildy.yaml": `project: {name: opts}
targets:
  executables:
    - {name: app, language: rust, path: app, features: [fancy, extra]}
    - {name: tool, language: rust, path: app, bin: other}
  static_libraries:
    - {name: calc, language: rust, path: calc, features: [double]}
  shared_libraries:
    - {name: plugin, language: rust, path: plugin}
`})
	native := func(release string) map[string]string {
		return map[string]string{
			"app":    "<root>/app: cargo | build | " + release + "--target-dir | <out>/cargo | --features | fancy,extra",
			"tool":   "<root>/app: cargo | build | " + release + "--target-dir | <out>/cargo | --bin | other",
			"calc":   "<root>/calc: cargo | build | " + release + "--lib | --target-dir | <out>/cargo | --features | double",
			"plugin": "<root>/plugin: cargo | build | " + release + "--lib | --target-dir | <out>/cargo",
		}
	}
	cross := func(triple, release string) map[string]string {
		return map[string]string{
			"app":    "<root>/app: cargo | build | " + release + "--target | " + triple + " | --target-dir | <out>/cargo | --features | fancy,extra",
			"tool":   "<root>/app: cargo | build | " + release + "--target | " + triple + " | --target-dir | <out>/cargo | --bin | other",
			"calc":   "<root>/calc: cargo | build | " + release + "--lib | --target | " + triple + " | --target-dir | <out>/cargo | --features | double",
			"plugin": "<root>/plugin: cargo | build | " + release + "--lib | --target | " + triple + " | --target-dir | <out>/cargo",
		}
	}
	checkArgv(t, root, []argvCase{
		{"linux", "x86_64", "debug", native("")},
		{"linux", "x86_64", "release", native("--release | ")},
		{"macos", "x86_64", "release", native("--release | ")},
		{"macos", "arm64", "debug", native("")},
		{"windows", "x86_64", "debug", cross("x86_64-pc-windows-gnu", "")},
		{"windows", "x86_64", "release", cross("x86_64-pc-windows-gnu", "--release | ")},
		{"wasm", "wasm32", "release", cross("wasm32-unknown-unknown", "--release | ")},
	})
}

// A target's Go and Rust options take filters, as its other lists do: by platform, by
// configuration.
func TestGoAndRustOptionsTakeFilters(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"buildy.yaml": `project: {name: filters}
targets:
  executables:
    - name: app
      language: go
      path: app
      build_tags: [{linux: [x11]}, {windows: [win32]}, base]
      ldflags: [{release: ["-s", "-w"]}, "-X main.v=1"]
      gcflags: [{debug: ["all=-N -l"]}]
    - {name: rs, language: rust, path: rs, features: [{linux: [alsa]}, {release: [lto]}]}
`})
	checkArgv(t, root, []argvCase{
		{"linux", "x86_64", "debug", map[string]string{
			"app": "<root>/app: go | build | -o | <out>/bin/app | -tags | x11,base | -ldflags=-X main.v=1 | -gcflags=all=-N -l | .",
			"rs":  "<root>/rs: cargo | build | --target-dir | <out>/cargo | --features | alsa",
		}},
		{"linux", "x86_64", "release", map[string]string{
			"app": "<root>/app: go | build | -trimpath | -o | <out>/bin/app | -tags | x11,base | -ldflags=-s -w -X main.v=1 | .",
			"rs":  "<root>/rs: cargo | build | --release | --target-dir | <out>/cargo | --features | alsa,lto",
		}},
		{"windows", "x86_64", "debug", map[string]string{
			"app": "<root>/app: go | build | -o | <out>/bin/app.exe | -tags | win32,base | -ldflags=-X main.v=1 | -gcflags=all=-N -l | .",
			"rs":  "<root>/rs: cargo | build | --target | x86_64-pc-windows-gnu | --target-dir | <out>/cargo",
		}},
	})
}
