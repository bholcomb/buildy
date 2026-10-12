# Buildy McBuildface

**One YAML file builds your C, C++, Go and Rust code, fetches and builds its dependencies, and
rebuilds only what actually changed.**

Buildy is a build tool for multi-language projects. You list your targets in `buildy.yaml`;
buildy picks the compiler for your platform, runs every step it can in parallel, and caches each
result by the content of its inputs, so touching a file costs nothing and changing a flag rebuilds
exactly what it affects. The same file builds on Linux, Windows and macOS, and buildy itself is a
single binary.

## Example

A library and a program that uses it:

```yaml
# buildy.yaml
project:
  name: hello

environment:
  compile:
    cpp_standard: "c++20"

targets:
  static_libraries:
    - name: greet
      language: cpp
      sources: ["lib/*.cpp"]
      include_dirs: ["include"]

  executables:
    - name: hello
      language: cpp
      sources: ["app/*.cpp"]
      include_dirs: ["include"]
      libs: [greet]
```

```bash
buildy       # debug:   build/linux-x86_64-debug/bin/hello
buildy -r    # release: build/linux-x86_64-release/bin/hello
```

To use a library from elsewhere, define it once and name it in the target's `deps`:

```yaml
# buildy_config/dependencies.yaml
pugixml:
  git: "https://github.com/zeux/pugixml.git"
  ref: "v1.14"
  build: {system: cmake}
  libs: [pugixml]
```

```yaml
    - name: hello
      ...
      deps: [pugixml]
```

The next build clones pugixml at `v1.14`, builds it with CMake for the configuration you are
building, and gives `hello` its headers and library. Later builds use it offline.

buildy writes `build/` and `.buildy_cache/` in your project: add both to `.gitignore`.

## Why buildy?

- **Rebuilds only what changed.** Each step is keyed by its command, the content of its inputs and
  the compiler itself. Touching a file costs nothing, and a link runs only when an object file it
  reads really changed.
- **Dependencies without a package manager.** Fetch from git (a branch, tag or commit) or an
  archive URL checked against its SHA-256, or find them with pkg-config. buildy builds them with
  CMake, Meson, make, autoconf, Cargo or buildy itself, once per configuration, and keeps them
  until you ask for `--update-deps`.
- **One file, every platform.** Filtered lists pick sources, flags, defines, libraries and modules
  per platform, architecture, configuration or toolchain. GCC, Clang, MSVC, MinGW and Emscripten are
  built in, and `debug` and `release` work with no setup.
- **C, C++, Go and Rust side by side.** Go and Rust targets build with `go build` and
  `cargo build` in the same run as the C and C++ ones, and a C program can link a Go library and
  include the header Go writes for it.
- **Workspaces.** Split a project into modules, each with its own `buildy.yaml`. Run `buildy` in
  any directory of the workspace and it builds the whole workspace, with one cache.
- **Assets in the same build.** Compile shaders, convert textures, and stage everything into a
  directory or a `.zip` or `.tar.gz` next to the binaries, cached like the code.
- **Everything is data.** Toolchains, build templates and dependency build systems are YAML. Add a
  compiler or a tool in `buildy_config/toolchains/`, or a build system in
  `buildy_config/build_systems/`, without changing buildy.
- **Plays well with others.** `--compile-commands` writes `compile_commands.json` for clangd and
  editors, `--dry-run` shows the plan, Ctrl-C stops everything a build started, `--timeout` kills a
  stuck step, and a mistake in a config names its file, line and key.

## Getting buildy

Download the archive for your platform from the
[latest release](https://github.com/bholcomb/buildy/releases/latest) and put the `buildy`
executable it holds on your `PATH`:

| Platform | Archive |
|----------|---------|
| Linux x86-64 | `buildy-0.2.0-linux-x64.tar.gz` |
| Windows x64 | `buildy-0.2.0-win-x64.zip` |
| macOS on Apple Silicon | `buildy-0.2.0-osx-arm64.tar.gz` |

`SHA256SUMS` in the release has each archive's checksum. The Linux binary is static and runs on any
distribution. The macOS binary isn't notarized: if you downloaded it with a browser, run
`xattr -d com.apple.quarantine buildy` once before you run it.

To build it from source instead, with Go 1.25 or later:

```bash
git clone https://github.com/bholcomb/buildy.git
cd buildy/src && go build -o ../bin/buildy .
```

## Command line

```
buildy                     Build the workspace you are in (the debug configuration)
buildy -r                  Build release (-c <name> builds any configuration)
buildy --target <name>     Build one target and what it needs
buildy -d                  Dry run: show what would be fetched, built and run
buildy -f                  Rebuild everything, ignoring the cache
buildy --clean             Remove build/ (--clean-all removes the cache and dependencies too)
buildy --update-deps       Fetch git branches and tags again, to where they are now
buildy --compile-commands  Write buildy_config/compile_commands.json for your editor
buildy -n 3                Say more (4 verbose, 5 debug)
```

buildy runs as many tasks at once as the machine has CPUs (`-j` to change it). `-p` and `-a` build
for another platform and architecture. `--list-toolchains` lists the compilers it knows. The
[user guide](doc/USER_GUIDE.md#all-options) has every option.

## Documentation

- [User guide](doc/USER_GUIDE.md): everything buildy does, with examples. Start here.
- Tutorials, each built around one of the [examples](examples/):
  - [Basic](doc/TUTORIAL_BASIC.md): a library and a program (`examples/math`)
  - [Multi-module](doc/TUTORIAL_MULTIMODULE.md): a workspace with a fetched dependency, shaders
    and packaging (`examples/game_engine`)
  - [Advanced](doc/TUTORIAL_ADVANCED.md): fetched dependencies and Docker builds
    (`examples/portable_app`)
- [DSL specification](doc/DSL_SPEC.md): every key of every file
- [How buildy works](doc/ARCHITECTURE.md): the phases of a build, and the design notes

## Status

Buildy is at version 0.2.0. Not there yet:

- A target can't choose its own toolchain: each module has one, and a target of another language
  gets that language's default.
- Rust for Windows builds Cargo's `x86_64-pc-windows-gnu` target, a cross-compile from Linux; the
  MSVC target isn't built in. A C or C++ target can't link a Rust library by name yet.
- No test targets yet, and no lockfile: pin a dependency with a full commit ID or a checksum.
- Dependencies are fetched one at a time, and a `--target` build fetches those of every target.
- Release binaries exist for Linux x64, Windows x64 and Apple Silicon; elsewhere, build from
  source. The end-to-end tests compile C and C++ on Linux and macOS, not yet with MSVC.

## License

MIT. See [LICENSE](LICENSE).
