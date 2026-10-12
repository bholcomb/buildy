# Buildy User Guide

Buildy is a multi-language build system for C, C++, Go, and Rust projects. It uses YAML configuration files to define builds that work across Linux, Windows, and macOS from a single configuration.

This guide covers how to use buildy effectively. For the complete field reference, see [DSL_SPEC.md](DSL_SPEC.md).

## Prerequisites

- **buildy** executable in your PATH
- A C/C++ compiler (GCC, Clang, or MSVC), Go, or Rust toolchain as needed
- Docker (optional, for containerized builds)

## Getting Started

### Your First Build

The simplest buildy project needs only a `buildy.yaml` file:

```yaml
project:
  name: hello
  version: "1.0.0"

targets:
  executables:
    - name: hello
      language: cpp
      sources: ["src/*.cpp"]
```

Build it:

```bash
buildy
```

Buildy auto-detects your platform and architecture, selects an appropriate toolchain, and builds in debug mode by default.

### Build Configurations

```bash
buildy                    # Debug build (default)
buildy -r                 # Release build (shortcut)
buildy -c release         # Release build (explicit)
buildy --config release   # Same as above
```

### Common Options

```bash
buildy -j 8               # Use 8 parallel workers
buildy -n 3               # Info level logging (default is 2=warning)
buildy -n 4               # Verbose level logging
buildy -n 5               # Debug level logging
buildy --force            # Ignore cache, rebuild everything
buildy --clean            # Remove the output directory (build/), nothing else
buildy --dry-run          # Show what would be built
```

buildy can run anywhere inside a workspace: from a module's directory (or below it) it builds the
whole workspace that lists the module, the same as from the root. See
[Which workspace](DSL_SPEC.md#workspace-mode-vs-standalone-mode).

## Project Structure

### Single-Module Project

A minimal project:

```
myproject/
├── buildy.yaml
└── src/
    └── main.cpp
```

### Multi-Module Project

Larger projects use workspaces with multiple modules:

```
myproject/
├── buildy.yaml                 # Root workspace
├── buildy_config/
│   ├── dependencies.yaml       # External dependencies
│   └── packages/
│       └── sdl2.yaml           # Package definitions
├── core/
│   └── buildy.yaml             # Core library module
├── renderer/
│   └── buildy.yaml             # Renderer module
└── game/
    └── buildy.yaml             # Main executable module
```

## Core Concepts

Every config file (buildy.yaml, dependency definitions, toolchains, templates) is validated
strictly before anything is built: a misspelled or unsupported key, a value of the wrong type, a
filter key that names no platform, architecture, configuration or toolchain, and an abstract
keyword no toolchain knows are errors naming the file, the line and the key (with a suggestion),
not settings silently ignored. See [Strict Validation](DSL_SPEC.md#strict-validation).

### Project Metadata

Every `buildy.yaml` starts with project metadata:

```yaml
project:
  name: myproject        # Required
  version: "1.0.0"       # Optional
  description: "..."     # Optional
```

### Variables

Variables let you parameterize your build configuration.

**User-defined variables:**

```yaml
variables:
  ENGINE_VERSION: "2.0.0"
  DATA_DIR: "assets"
```

**Importing environment variables:**

Environment variables must be explicitly imported:

```yaml
variables:
  import_env_vars:
    - name: VULKAN_SDK
      default: ~                 # Required - error if not set
    - name: BUILD_NUMBER
      default: "dev"             # Optional with default
```

Variables of the root `buildy.yaml` (and its imported environment variables) reach every module;
a module's own variables override them.

**Platform-specific variables:**

```yaml
variables:
  platforms:
    linux:
      LIB_EXT: ".so"
    windows:
      LIB_EXT: ".dll"
    macos:
      LIB_EXT: ".dylib"
```

**Using variables:**

Reference variables with `${name}`:

```yaml
targets:
  executables:
    - name: myapp
      defines: ["VERSION=\"${ENGINE_VERSION}\""]
```

**Built-in variables:**

| Variable | Description |
|----------|-------------|
| `${platform}` | Target platform (linux, windows, macos) |
| `${arch}` | Target architecture (x86_64, arm64) |
| `${config}` | Build configuration (debug, release) |
| `${workspace}` | Workspace root directory |
| `${project_name}` | From project.name |
| `${project_version}` | From project.version |
| `${out_dir}` | Build output directory |
| `${cache_dir}` | Cache directory |
| `${gen_dir}` | Generated files directory |

### Targets

Targets define what to build. They're organized by type:

```yaml
targets:
  static_libraries:
    - name: mylib
      language: cpp
      sources: ["src/*.cpp"]

  shared_libraries:
    - name: myshared
      language: cpp
      sources: ["src/*.cpp"]

  executables:
    - name: myapp
      language: cpp
      sources: ["main.cpp"]
```

**Supported languages:** `c`, `cpp`, `go`, `rust`

### Source Patterns

Use glob patterns to specify source files:

```yaml
sources:
  - "src/*.cpp"           # All .cpp files in src/
  - "src/**/*.cpp"        # Recursive: src/a.cpp, src/x/b.cpp, src/x/y/c.cpp
  - "src/*.{c,cpp}"       # Alternatives
```

`**` matches any number of directories, none included. Patterns match files, never directories,
and the same syntax applies to copies, transform and generate inputs and staging files (see
[File Patterns](DSL_SPEC.md#file-patterns)).

For optional source directories that may not exist:

```yaml
sources:
  - "src/core/*.cpp"                              # Required
  - {pattern: "src/plugins/*.cpp", optional: true}  # Optional
```

### Library Output Prefix

The default library filename prefix is defined by the toolchain (e.g., `"lib"` for GCC/Clang, `""` for MSVC). You can override it per-target or at the environment level:

```yaml
# Per-target override
targets:
  shared_libraries:
    - name: myplugin
      language: cpp
      sources: ["src/*.cpp"]
      output_prefix: ""       # Produces myplugin.so instead of libmyplugin.so

# Or globally in the environment section
environment:
  compile:
    output_prefix: ""         # No lib prefix for any library in this project
```

### Separate Compile and Link Flags

Use the `compile:` and `link:` sections to pass flags specifically to the compiler or linker. Both support `flags` and `remove_flags`, and work at the environment, configuration, and target level:

```yaml
environment:
  compile:
    flags: ["-fvisibility=hidden"]
  link:
    flags: ["-Wl,-z,now"]

  configurations:
    release:
      compile:
        flags: ["-flto", "-march=native"]
      link:
        flags: ["-flto", "-Wl,-O1"]

targets:
  shared_libraries:
    - name: mylib
      language: cpp
      sources: ["src/*.cpp"]
      compile:
        flags: ["-ffast-math"]
        remove_flags: ["-Werror"]       # This target has warnings we can't fix
      link:
        flags: ["-Wl,--version-script=mylib.map"]
```

Compile flags and link flags are completely separate — a flag in `compile:` never reaches the linker, and vice versa. Use `remove_flags` within either section to strip flags inherited from higher levels.

A module's settings add to the root's: mappings merge key by key, lists append and single values
override, so a module that adds `environment.compile.defines` keeps the root's standard, flags and
defines. See [How Settings Merge](DSL_SPEC.md#how-settings-merge).

### Generated Sources

For source files created by code generators (protobuf, etc.), list them with a pattern: source patterns match the outputs an artifact declares, even though those files don't exist yet. Use `depends_on.artifacts` so the generator also runs before the target's other compiles, which may include its headers:

```yaml
artifacts:
  generate:
    - name: protos
      tool: protoc
      inputs: "proto/*.proto"
      outputs: ["${gen_dir}/${basename}.pb.cc", "${gen_dir}/${basename}.pb.h"]
      args: ["--cpp_out=${gen_dir}"]

targets:
  executables:
    - name: myapp
      language: cpp
      sources:
        - "src/*.cpp"
        - "${gen_dir}/*.pb.cc"  # Matches the artifact's declared outputs
      depends_on:
        artifacts: ["protos"]   # protoc runs before every task of myapp
```

When a target declares `depends_on.artifacts`, the artifact tasks run before every task of the
target, compiles included (a compile can include a generated header it doesn't list).

Naming an artifact the module doesn't define is an error. A task that reads another task's output
(a compile of a generated source, a staging copy of a library) always runs after it, whether or
not anything declares the dependency.

This means you use regular patterns for generated files - no need to list each file explicitly. A pattern into the output directory (`${gen_dir}`, `${out_dir}`) names only what the build's tasks declare as outputs, never a file an earlier build left there; one that names nothing is an error. The same holds for copies and staging: `files: ["${out_dir}/bin/*"]` stages the build's binaries on a clean build too.

**Note:** Artifact dependencies currently work within a single module. For multi-module projects, both the artifact and the dependent target should be in the same module.

### Include Directories

C/C++ targets specify include directories as a simple list:

```yaml
targets:
  static_libraries:
    - name: mylib
      language: cpp
      sources: ["src/*.cpp"]
      include_dirs: ["include", "src/internal"]
```

### Linking Libraries

**Internal libraries** (from your project):

```yaml
targets:
  executables:
    - name: myapp
      language: cpp
      sources: ["main.cpp"]
      libs:
        - myrenderer    # Link order matters for static libs
        - mycore
      depends_on:
        targets: ["mycore", "myrenderer"]
```

**External dependencies:**

```yaml
targets:
  executables:
    - name: myapp
      deps:
        - glfw3
        - opengl
```

### Build Configurations

buildy has its own `debug` and `release` configurations, so a config without any still builds
`-c release` optimized: `debug` is `optimization: none` and `symbols: true` (`-O0 -g` with GCC),
`release` is `optimization: speed` and defines `NDEBUG` (`-O2 -DNDEBUG`). Whatever a config sets
wins: a keyword set at any level replaces buildy's, and a definition of `NDEBUG` (`NDEBUG=1`)
replaces buildy's; `remove_defines: [NDEBUG]` drops it. Other configurations (`-c portable`) get
nothing from buildy.

Define or adjust debug and release configurations:

```yaml
environment:
  configurations:
    debug:
      optimization: none
      warnings: extra
      symbols: true
      defines: ["DEBUG=1"]
    release:
      optimization: full
      warnings: default
      symbols: false
      defines: ["NDEBUG=1"]
      flags: ["-flto"]
```

## Compiler Flags

Buildy uses a three-layer flag pipeline to build the final set of compiler flags for each target:

1. **Toolchain mechanical flags** -- flags baked into the toolchain definition (e.g., `/nologo`, `/EHsc`, `/FS` for MSVC). These are always applied and cannot be overridden.
2. **Abstract keywords** -- high-level settings like `optimization`, `warnings`, `symbols`, and `runtime` that are resolved to concrete compiler flags by the toolchain.
3. **Raw flags** -- literal compiler flags from your `buildy.yaml` (`flags` key).

All three layers are concatenated in that order to produce the final flag list.

### Abstract Keywords

Abstract keywords let you write toolchain-agnostic configurations. Buildy resolves them to concrete flags using the active toolchain's `flag_mappings`.

| Keyword | Values | Description |
|---------|--------|-------------|
| `optimization` | `none`, `size`, `speed`, `full` | Optimization level |
| `warnings` | `off`, `default`, `extra`, `everything` | Warning level |
| `symbols` | `true`, `false` | Debug symbols |
| `runtime` | `debug`, `release` | Runtime library (MSVC only) |

Example -- these abstract keywords produce the same result on GCC, Clang, and MSVC:

```yaml
environment:
  configurations:
    debug:
      optimization: none
      warnings: extra
      symbols: true
    release:
      optimization: full
      warnings: default
      symbols: false
```

On GCC/Clang, `optimization: full` resolves to `-O3`. On MSVC, it resolves to `/Ox`. If a keyword value is not recognized by the toolchain, the build fails with an error listing valid values.

### Raw Flags

Use the `flags` key to pass literal compiler flags. These are applied at every level of the configuration hierarchy and concatenated. For flags that need to go specifically to the compiler or linker, use the `compile:` and `link:` sections (see "Separate Compile and Link Flags" above).

```yaml
environment:
  compile:
    flags: ["-fpermissive"]
  configurations:
    debug:
      compile:
        flags: ["-fsanitize=address"]
      link:
        flags: ["-fsanitize=address"]    # ASan needs both compile and link
    release:
      compile:
        flags: ["-flto"]
      link:
        flags: ["-flto"]

targets:
  executables:
    - name: myapp
      language: cpp
      sources: ["src/*.cpp"]
      compile:
        flags: ["-fno-rtti"]
```

In a debug build, `myapp` would receive compile flags: `[resolved abstract keywords] + [-fpermissive] + [-fsanitize=address] + [-fno-rtti]`, and link flags: `[-fsanitize=address]`.

Note: Top-level `flags:` at the environment or configuration level is a shorthand for compile flags. For clarity, prefer using the explicit `compile:` section.

Each item of `flags` (and of `defines`, `include_dirs`, `lib_dirs` and `libs`) is one argument of
the compiler or linker, passed exactly as written: buildy runs commands without a shell. So a
path with spaces needs no quotes, `defines: ['MSG="hello world"']` defines `MSG` as the string
`"hello world"`, and `-Wl,-rpath,$ORIGIN` is written without the shell quotes a Makefile would
need (quotes would reach the linker). Write one flag per item: `["-framework", "Cocoa"]`, not
`["-framework Cocoa"]`.

### Flag Removal

Use `remove_flags` and `remove_defines` to exclude flags or defines inherited from higher levels. This is useful when a specific target or configuration needs to opt out of a global setting:

```yaml
environment:
  compile:
    warnings: extra
    defines: ["FEATURE_A=1"]

  configurations:
    release:
      remove_flags: ["-Wextra"]

targets:
  executables:
    - name: legacy_module
      language: cpp
      sources: ["src/*.cpp"]
      remove_flags: ["-Wall"]
      remove_defines: ["FEATURE_A=1"]
```

Removals apply after all flags are merged, so they can remove flags from any source -- abstract keywords, environment flags, or configuration flags.

### Filtered Flags

Flags support the same filter syntax as other list values, allowing platform, architecture, configuration, and toolchain conditions:

```yaml
environment:
  compile:
    flags:
      - "-fvisibility=hidden"
      - linux:
          - "-pthread"
      - windows:
          - "/utf-8"
```

Filters can be nested to combine conditions:

```yaml
environment:
  compile:
    flags:
      - debug:
          gcc-cpp-linux:
            - "-fsanitize=address"
          clang-cpp-linux:
            - "-fsanitize=memory"
```

This applies `-fsanitize=address` only when building in debug mode with the GCC toolchain.

### Custom Toolchain Keywords

Abstract keywords are fully data-driven. Custom toolchains can define their own keywords by adding a `flag_mappings` section:

```yaml
toolchain:
  name: "my-custom-compiler"
  flag_mappings:
    optimization:
      none: ["-O0"]
      full: ["-O3"]
    vectorization:
      off: []
      sse4: ["-msse4.2"]
      avx2: ["-mavx2"]
  tools:
    # ...
```

Users can then write `vectorization: avx2` in their `buildy.yaml` and it will resolve to `-mavx2`.

## Working with Dependencies

Dependencies are defined in `buildy_config/dependencies.yaml`. Each dependency has a name and platform-specific settings.

### System Dependencies

System dependencies use pkg-config or explicit library names:

```yaml
# buildy_config/dependencies.yaml
zlib:
  common:
    pkg_config: zlib

pthread:
  linux:
    libs: ["pthread"]

winsock:
  windows:
    libs: ["ws2_32"]

cocoa:
  macos:
    frameworks: ["Cocoa", "IOKit"]
```

### Path Dependencies

For libraries at known paths (like SDKs):

```yaml
vulkan_sdk:
  common:
    root: "${VULKAN_SDK}"
    include_dirs: ["${root}/include"]
    lib_dirs: ["${root}/lib"]
    libs: ["vulkan"]
```

### Dependencies

Dependencies are defined in `buildy_config/dependencies.yaml` with platform-specific settings:

```yaml
# buildy_config/dependencies.yaml
glfw3:
  description: "GLFW window library"
  version: "3.4"

  common:
    defines: ["GLFW_ENABLED"]
    include_dirs: ["${root}/include"]
    lib_dirs: ["${root}/lib"]

  linux:
    pkg_config: glfw3

  windows:
    root: "${THIRD_PARTY}/GLFW"
    libs: ["glfw3"]

  macos:
    root: "/opt/homebrew"
    libs: ["glfw"]
    frameworks: ["Cocoa", "IOKit", "CoreVideo"]
```

Use dependencies in targets:

```yaml
targets:
  executables:
    - name: myapp
      deps: ["glfw3"]
```

pkg-config's flags are kept whole and in order: `--cflags` (`-I`, `-D`, `-pthread`, `-isystem`,
...) go ahead of the target's compile flags, and `--libs` (`-L`, `-l`, `-Wl,...`, `-pthread`,
library paths) where the libraries go in the link command.

### Fetched Dependencies

Dependencies can be fetched from Git repositories or URLs. They are stored locally in `.buildy_cache/deps/<name>/` within your project - **not** installed system-wide.

For dependencies that need to be built:
- **Source**: `.buildy_cache/deps/<name>/` (`${dep_dir}`)
- **Build**: `.buildy_cache/deps/<name>/_build/<platform>-<arch>-<config>/` (`${dep_build_dir}`)
- **Install**: `.buildy_cache/deps/<name>/_install/<platform>-<arch>-<config>/` (`${dep_install_dir}`)

A dependency is fetched once and built once per configuration:

- **Only the configuration being built.** `buildy -c release` builds a dependency's release build
  and links it; debug is built when you build debug. The build system's output directories for
  that configuration (cmake: `${dep_install_dir}/include` and `/lib`) are added to the
  dependency's include and lib dirs.
- **Only when something changed.** A finished build is recorded next to its build directory; the
  next build skips it while the fetched commit or archive, the build system, the configuration,
  `build.args`, the override config, the programs it runs and the environment variables its build
  system reads are the same. A failed or interrupted build is built again. `buildy --force`
  rebuilds every dependency.
- **Offline once fetched.** A git dependency is fetched again only when its `git` URL or `ref`
  changes. A branch or tag stays at the commit first fetched until you run
  `buildy --update-deps`, which also downloads archives that have no `checksum` again.
- **Any ref.** `ref` may be a branch, a tag, or a full or abbreviated commit ID; the clone is
  checked out at that commit (`-n 3` logs it).
- **Configurations.** cmake, meson and cargo build `debug` and `release` as their own. For any
  other configuration, say which of theirs it is:

  ```yaml
  mylib:
    git: "https://github.com/example/mylib.git"
    build:
      system: cmake
      configurations: {portable: Release}   # buildy -c portable builds it as Release
  ```

  buildy dependencies build any configuration by name. make, autoconf and go_mod have no
  configurations: pass settings in `build.args`, which can use `${config}`.

See [the design note](design/dependencies.md) for the details: what a build's record holds, the
git policy, and the timeouts.

**Git repository:**

```yaml
# buildy_config/dependencies.yaml
imgui:
  description: "Dear ImGui"
  version: "1.90.1"
  common:
    git: "https://github.com/ocornut/imgui.git"
    ref: "v1.90.1"
    build:
      system: cmake
      args: ["-DIMGUI_DEMO=OFF"]
    include_dirs: ["${dep_install_dir}/include"]   # added anyway: cmake installs there
    lib_dirs: ["${dep_install_dir}/lib"]
    libs: ["imgui"]
```

**URL download (header-only):**

```yaml
stb:
  description: "STB single-header libraries"
  common:
    url: "https://github.com/nothings/stb/archive/master.tar.gz"
    checksum: "sha256:..."
    include_dirs: ["${dep_dir}"]
```

**Supported build systems:** `cmake`, `meson`, `make`, `autoconf`, `cargo`, `go_mod`, `buildy`,
your own ([Custom Build Systems](#custom-build-systems)), `auto` (detected from the fetched files),
`none`

### Building with Buildy Config

Instead of external build systems, you can build fetched dependencies using a users buildy config:

```yaml
# buildy_config/dependencies/glfw3.yaml
glfw3:
  description: "GLFW window library"
  linux:
    url: "https://github.com/glfw/glfw/releases/download/3.4/glfw-3.4.zip"
    build:
      system: buildy
      override: glfw3_build.yaml  # Custom buildy config in same directory
    include_dirs: ["${dep_dir}/include"]
    lib_dirs: ["${dep_dir}/build/linux-x86_64-${config}/lib"]
    libs: ["glfw3"]
```

When `build.system: buildy` is specified with an `override`:

1. The dependency source is fetched as usual to `.buildy_cache/deps/<name>/`
2. Buildy invokes itself recursively in **standalone mode** to build the dependency
3. The override config file defines targets that compile the dependency source
4. The configuration being built is built (`--config ${config}`), custom ones included
5. The library paths use `${config}` to reference the correct build output

**Standalone Mode:** When buildy is invoked with an explicit config file path (instead of a directory), it operates in standalone mode:
- Only builds what's defined in that specific config file
- Does not discover modules or resolve dependencies
- Source paths are resolved relative to the working directory

**Example override config** (`glfw3_build.yaml`):

```yaml
project:
  name: glfw3
  description: "GLFW static library build"

environment:
  compile:
    c_standard: "c11"

targets:
  static_libraries:
    - name: glfw3
      language: c
      sources:
        - "src/context.c"
        - "src/init.c"
        # ... more source files
      include_dirs:
        - "include"
        - "src"
```

### Dependencies File Organization

Dependencies are defined in `buildy_config/dependencies.yaml`. For larger projects, you can organize them into separate files under `buildy_config/dependencies/`; every file there is loaded, so `dependencies.yaml` doesn't list them (a `name: {file: ...}` entry is an error):

```
buildy_config/
  dependencies.yaml         # Simple projects
  dependencies/             # Or organized by category
    graphics.yaml
    audio.yaml
    networking.yaml
```

**Example dependencies.yaml:**

```yaml
# System dependencies
zlib:
  description: "zlib compression library"
  linux:
    pkg_config: zlib
  macos:
    pkg_config: zlib
  windows:
    root: "${THIRD_PARTY}/zlib"
    include_dirs: ["${root}/include"]
    lib_dirs: ["${root}/lib"]
    libs: ["zlibstatic"]

# Fetched dependency
sdl2:
  description: "Simple DirectMedia Layer"
  common:
    git: "https://github.com/libsdl-org/SDL.git"
    ref: "release-2.28.5"
    build:
      system: cmake
    include_dirs: ["${dep_install_dir}/include"]
    lib_dirs: ["${dep_install_dir}/lib"]
    libs: ["SDL2"]
```

### Pinning

A fetched dependency stays at the commit or archive first fetched until `--update-deps`. To pin
one across machines, give a git dependency's full commit ID as `ref`, or an archive's `checksum`.
There is no lockfile yet.

## Multi-Module Projects

### Workspace Configuration

Define modules in the root `buildy.yaml`:

```yaml
workspace:
  modules:
    - core                    # All platforms
    - renderer
    - game
    - linux:                  # Linux only
        - platform/linux
    - windows:                # Windows only
        - platform/windows
```

Filter keys are matched against the build's platform, architecture and configuration
(`--platform`, `--architecture`, `--config`), the same way in child modules' `workspace.modules`.

### Module Dependencies

Modules can depend on targets from other modules:

```yaml
# game/buildy.yaml
targets:
  executables:
    - name: mygame
      language: cpp
      sources: ["src/*.cpp"]
      libs:
        - engine_renderer
        - engine_core
      depends_on:
        targets: ["engine_core", "engine_renderer"]
```

### Custom Config Filenames

Use non-standard config filenames:

```yaml
workspace:
  modules:
    - path: vendor/imgui
      config: imgui_build.yaml
```

## Building with Docker

Docker provides reproducible build environments across different machines.

### Docker Toolchains

Use a Docker-based toolchain for your project builds:

```yaml
environment:
  toolchains:
    default: docker-cpp-linux
```

Built-in Docker toolchains: `docker-c-linux`, `docker-cpp-linux`

### Docker for Dependency Builds

Build a fetched dependency in a Docker container with its `execution` section:

```yaml
# buildy_config/dependencies.yaml
complex_lib:
  common:
    git: "https://github.com/example/lib.git"
    build:
      system: cmake
    execution:
      type: docker
      image: "ubuntu:22.04"
```

### Docker Configuration

| Field | Description |
|-------|-------------|
| `type` | `docker` or `native` |
| `image` | Docker image name |
| `volumes` | Extra volume mounts (supports `${UID}`, `${GID}`) |
| `user` | User to run as (default: `${UID}:${GID}`) |
| `env` | Environment variables for the commands |

The workspace root, and the cache directory if it lies elsewhere, are mounted at their host paths,
so commands see the same absolute paths in the container as outside it. A Docker toolchain
(`--toolchain docker-cpp-linux`) runs every compile and link of its targets in its image; the
image is part of each task's cache key.

## Artifacts and Post-Processing

### Copy Operations

Copy files after building:

```yaml
artifacts:
  copy:
    - name: assets
      sources: ["assets/**/*.png"]
      destination: "${out_dir}/data"
      preserve_structure: true

    - target: mylib           # Copy build target output
      dest: "${workspace}/external/lib"
```

### Transform Operations

Transform files using external tools:

```yaml
artifacts:
  transform:
    - name: textures
      tool: texconv
      inputs: "assets/textures/*.png"
      outputs: "${out_dir}/textures/${basename}.dds"
      args: ["-f", "BC7_UNORM"]
```

### Generate Operations

Generate files from templates or tools:

```yaml
artifacts:
  generate:
    - name: version_header
      template: "src/version.h.in"
      output: "${gen_dir}/version.h"
      variables:
        VERSION: "${project_version}"

    - name: protos
      tool: protoc
      inputs: "proto/*.proto"
      outputs: ["${gen_dir}/${basename}.pb.h", "${gen_dir}/${basename}.pb.cc"]
      args: ["--cpp_out=${gen_dir}"]
```

Targets can depend on generate artifacts to use the generated files. Use globs to include the artifact's outputs:

```yaml
targets:
  static_libraries:
    - name: mylib
      language: cpp
      sources:
        - "src/*.cpp"
        - "${gen_dir}/*.pb.cc"   # Matches the artifact's declared outputs
      depends_on:
        artifacts: ["protos"]    # protoc runs before every task of mylib
```

## Staging and Distribution

### Staging Areas

Staging assembles build outputs into a distribution layout:

```yaml
staging:
  name: myproduct
  destination: "${out_dir}/staging"
  use_symlinks: true       # Fast iteration during development

  contents:
    - folder: "."
      files: ["LICENSE.txt", "README.md"]

    - folder: bin
      targets:
        - myapp
        - mylib

    - folder: data
      contents:
        - folder: textures
          files: ["assets/textures/*.png"]
        - folder: shaders
          artifacts: ["compiled_shaders"]
```

### Creating Distribution Packages

Package staging areas for distribution:

```yaml
install:
  - name: release
    staging: myproduct
    destination: "${out_dir}/dist"
    format: tar.gz           # tar.gz | zip | tar
    filename: "${project_name}-${project_version}-${platform}-${arch}"
```

Buildy writes the archive itself (no `tar`, `zip` or PowerShell needed), the same on every
platform, and the same bytes for the same files: entries in name order, without a `./` prefix,
with a fixed date. Copies, staging symlinks and template files are done by buildy too.

## Customization

### Custom Toolchains

Create project-specific toolchains in `buildy_config/toolchains/`:

```yaml
# buildy_config/toolchains/mesh-converter.yaml
toolchain:
  name: "mesh-converter"
  description: "Convert mesh files"

  target:
    platform: any
    architecture: any

  tools:
    convert:
      action: transform
      command: "mesh_tool -i ${input} -o ${output}"
      input_extensions: [".fbx", ".obj"]
      output_extension: ".mesh"
```

A transform or generate tool gets the module's abstract keywords as its own toolchain's
`flag_mappings` map them, then its `common` flags, then the configuration's (`debug`, `release`),
like a compile tool.

A tool's command runs without a shell: buildy splits it into words, and each placeholder that is a
word of its own becomes its values, each one argument (`${flags}` may be several, an empty one
none), while one inside a word (`-o${output}`) must have one value. A tool that needs pipes or
`&&` sets `shell: true`, and buildy quotes the values for the shell. A tool that reads arguments
from an `@file` says so with `response_file: gnu` or `msvc`, and buildy uses one when a command
line is too long for the system. See [Command Syntax](DSL_SPEC.md#command-syntax).

### Custom Build Systems

A library built by something buildy doesn't know (SCons, a Makefile with an install target, a
script) gets a build system definition of your own: a YAML file in `buildy_config/build_systems/`
(or a data directory, below). A dependency names it in `build.system` like a built-in one.

This one builds a Makefile that installs into `$(PREFIX)`, the way many small C libraries do:

```yaml
# buildy_config/build_systems/prefix_make.yaml
build_system:
  name: prefix_make
  description: "make, then make install PREFIX=..."

  # buildy's configurations as this Makefile's CFLAGS: ${build_type} in the phases
  configurations:
    debug: "-O0 -g"
    release: "-O2 -DNDEBUG"

  # Environment variables its tools read: a change rebuilds the dependency
  reads_env: [CC, AR]

  # make builds in the source tree, one configuration at a time: clean runs before another
  in_source: true

  phases:
    clean:
      command: "make clean"
    build:
      command: "make -j${jobs} CFLAGS=${build_type} ${args}"
    install:
      command: "make install PREFIX=${install_dir}"

  # What exists of these is added to the dependency's include and lib dirs
  output:
    include_patterns: ["${install_dir}/include"]
    lib_patterns: ["${install_dir}/lib"]
```

```yaml
# buildy_config/dependencies.yaml
tinyfmt:
  git: "https://example.com/tinyfmt.git"
  ref: "v1.2.0"
  build:
    system: prefix_make
  libs: [tinyfmt]
```

`buildy` builds it with `make -j8 'CFLAGS=-O0 -g'` and installs it into
`.buildy_cache/deps/tinyfmt/_install/<platform>-<arch>-debug/`; `buildy -c release` runs
`make clean`, then builds and installs with `-O2 -DNDEBUG` into `..._install/...-release/`. The app
gets `-I` and `-L` for the install directory from `output`, so the dependency needs only `libs`.

How a definition works:

- **Commands** are [command templates](DSL_SPEC.md#command-syntax), run without a shell: a
  placeholder alone as a word is its values, each one argument (`${args}`), one inside a word
  (`CFLAGS=${build_type}`) its value as text, and the word stays one argument. A phase that needs
  `&&`, pipes or redirection sets `shell: true`, and buildy quotes the values for the shell.
- **Placeholders**: `${source_dir}`, `${build_dir}`, `${install_dir}`, `${config}`, `${platform}`,
  `${arch}`, `${build_type}` (only with `configurations`), `${jobs}`, `${args}` (phase commands
  only), `${buildy_exe}` and `${config_file}`; see [the reference](DSL_SPEC.md#phase-variables).
  Any other name is an error when the file loads.
- **Configurations**: without `configurations`, the build system has none, and `${config}` is
  buildy's name. With it, a configuration it doesn't map (`-c portable`) fails until the
  dependency maps it in `build.configurations`.
- **Rebuilds**: the definition's content is in each build's record: editing the file rebuilds the
  dependencies it builds, the next time each one is built.
- **Detection**: with `detection.marker_files` and `priority`, `build.system: auto` considers it
  too. Two build systems that detect a tree at the same priority are an error naming both (set
  `build.system`, or change a priority).

Loading is strict: an unknown key, a missing `name` or `build` phase, an unknown placeholder or a
`clean` phase without `in_source` stops buildy with the file and the line. A definition with a
built-in's name replaces the built-in one, and buildy says so at `-n 3`:

```
Build system 'make' from buildy_config/build_systems/make.yaml replaces the one from built-in build_systems/make.yaml
```

### Data Directory Search Paths

Templates, toolchains and build systems are searched in these places, in this order; a definition
replaces one of the same name from an earlier place (logged at `-n 3`), and two definitions of
one name in the same directory are an error (toolchains and build systems):

1. Built-in (embedded in the binary)
2. `data/templates/`, `data/toolchains/` and `data/build_systems/` next to the buildy executable
   (the real file, symlinks resolved; not the current directory)
3. Each `--add-data-dir <dir>`'s `templates/`, `toolchains/` and `build_systems/`, in order
4. The workspace's `buildy_config/toolchains/` and `buildy_config/build_systems/` (templates have
   no workspace directory)

```bash
buildy --add-data-dir /opt/company/buildy
```

Build systems are loaded when buildy builds a workspace's dependencies, not for a config file
built on its own (standalone mode, which has none).

## Toolchain Selection

### Built-in Toolchains

**C/C++:**
- `gcc-c-linux`, `gcc-cpp-linux` - GCC on Linux
- `clang-c-linux`, `clang-cpp-linux` - Clang on Linux
- `clang-c-macos`, `clang-cpp-macos` - Apple Clang
- `clang-c-macos-arm64`, `clang-cpp-macos-arm64` - Apple Clang on Apple Silicon
- `msvc-c-windows`, `msvc-cpp-windows` - MSVC
- `gcc-c-mingw`, `gcc-cpp-mingw` - MinGW cross-compiler
- `docker-c-linux`, `docker-cpp-linux` - Docker-based GCC
- `emscripten-c`, `emscripten-cpp` - WebAssembly

**Go:** `go-linux`, `go-macos`, `go-macos-arm64`, `go-windows`

**Rust:** `rust-linux`, `rust-macos`, `rust-macos-arm64`, `rust-windows`, `rust-wasm`

**Assets:** `glslc` (shader compilation), `texconv`, `compressonator`, `imagemagick`

### Platform-Specific Toolchains

```yaml
environment:
  toolchains:
    default: gcc-cpp-linux
    linux: clang-cpp-linux
    windows: msvc-cpp-windows
    macos: clang-cpp-macos
```

### Per-Target Override

Not implemented yet: a target's `toolchain:` is rejected as an unknown key. A target gets its
module's toolchain, or its language's default when that is another language.

```yaml
targets:
  static_libraries:
    - name: perf_critical
      language: cpp
      toolchain: clang-cpp-linux   # Override for this target
```

### CLI Override

```bash
buildy --toolchain clang-cpp-linux
buildy --list-toolchains   # Show available toolchains
```

### The Default Toolchain

Without any of these, a module gets its platform's default: the toolchain for that platform and
architecture with the highest `priority:` (GCC C++ on Linux, MSVC C++ on Windows, Apple Clang C++ on
macOS). A toolchain of your own in `buildy_config/toolchains/` becomes the default by declaring a
higher priority; two with the same highest priority are an error.

## Language-Specific Features

### Go Projects

```yaml
targets:
  executables:
    - name: myapp
      language: go
      path: "cmd/myapp"          # Directory with the main package
      build_tags: [netgo, {linux: [x11]}]
      ldflags: ["-X main.version=${project_version}", {release: ["-s", "-w"]}]
      gcflags: [{debug: ["all=-N -l"]}]
      buildmode: pie             # exe (go's default) or pie
```

`build_tags` becomes `-tags netgo,x11`, `ldflags` one `-ldflags=...` argument and `gcflags` one
`-gcflags=...` argument; go splits those two into words itself, so a value with a space is quoted
inside the item as go reads it: `"-X 'main.msg=hello world'"`. A release build adds `-trimpath`.

### Rust Projects

```yaml
targets:
  executables:
    - name: myapp
      language: rust
      path: "."                  # Directory with Cargo.toml
      features: [cli, {linux: [alsa]}]
      bin: "myapp"               # If multiple binaries
```

`features` becomes `--features cli,alsa` and `bin` `--bin myapp`; a release build adds
`--release`. The Windows and WebAssembly toolchains pass their `--target` triple.

These are all the keys a Go or Rust target takes, besides `name`, `language` and `depends_on`
(the [DSL spec](DSL_SPEC.md#go-and-rust-target-keys) has the table). Any other key, a C/C++ one
such as `sources` or `flags`, or the other language's, is an error, and so is a Go or Rust key on
a C or C++ target. The lists take filters, as `sources` and `defines` do.

Cargo builds into `build/<platform>-<arch>-<config>/cargo/`; the binary is
`cargo/<profile>/myapp` (`debug` or `release`). Go and Rust builds run `go build` and
`cargo build` every time and let the tool's own cache decide what to rebuild: it knows about
imports outside the target directory, embedded files, `Cargo.toml`, path dependencies and build
scripts. They show as executed, never as cache hits; `buildy --dry-run -n 3` shows the command
each would run.

### C/C++ Libraries for FFI

Go and Rust can produce C-compatible libraries:

```yaml
targets:
  shared_libraries:
    - name: mylib
      language: go          # -buildmode=c-shared: lib/libmylib.so (.dylib, mylib.dll)
      path: "."
  static_libraries:
    - name: myarchive
      language: go          # -buildmode=c-archive: lib/libmyarchive.a
      path: "."
```

Go writes the C header beside the library (`lib/libmylib.h`). A Go library's build mode is its
type's; it takes no `buildmode`. Go libraries need cgo: a C compiler on the `PATH`, and
`CGO_ENABLED` not set to `0`. A Rust library is the crate's `staticlib` or `cdylib`, as its
`Cargo.toml` declares.

## IDE Integration

Generate `compile_commands.json` for IDE support:

```bash
buildy --compile-commands
```

This creates `buildy_config/compile_commands.json` which IDEs and language servers use for code intelligence. Each entry has the compile's `arguments`, exactly as buildy runs them.

## Cache Management

Buildy uses content-addressable caching for incremental builds. A task's cache key covers exactly
what decides its outputs: its command (its argument list and directory, or for a copy, symlink,
template or archive, what buildy does), its declared outputs, the content of every declared input,
where it runs (natively, or in which Docker image, with the toolchain's `env`), the program it runs
(its path and content: another or an upgraded compiler rebuilds), and the values of the environment
variables its toolchain reads (`reads_env`: `CPATH`, `SDKROOT`, `MACOSX_DEPLOYMENT_TARGET`,
`INCLUDE`...). The rest of the configuration reaches a task through its command, so changing one
target, or adding one, reruns that target's tasks and no others. A task is skipped (a cache hit)
only when:

- the cache has an entry for that key,
- every declared output still exists with the content recorded when the task last ran, and
- every input the compiler reported in its dependency file (`-MMD`'s `.d` file, MSVC's
  `/sourceDependencies` JSON) is unchanged. All of them count, including headers without an
  extension; a reported header that didn't exist counts as changed when it appears.

Every task also follows these rules when it runs:

- Each declared input must exist; otherwise the task fails, naming the input and the task that
  should have produced it.
- Its declared outputs are deleted first, so an archiver that appends (`ar rcs`) never keeps the
  members of removed sources.
- If it exits 0 without producing a declared output, it fails:
  `link_app__root exited 0 but did not produce declared output …`.

The cache stores no copies of outputs; it records what each task last produced. Each task's entry
is appended to `cache_journal.jsonl` as it finishes, and at the end of the run, interrupted or not,
the newest entry of each task goes into `cache_index.json`. Files are read once per run, and not at
all when their size, times and inode say they haven't changed since an earlier run
(`file_hashes.json`). See [the design note](design/execution.md).

```bash
buildy --cache-stats      # Show cache statistics
buildy --force            # Ignore the cache and dependency build records, rebuild all
buildy --cache-dir /path  # Custom cache directory
```

The cache directory is `.buildy_cache/` in the workspace root by default. A relative `--cache-dir`
is relative to the workspace root (not the current directory), an absolute one is used as it is.

## CLI Reference

### Build Commands

```bash
buildy                           # Build with defaults
buildy -c release                # Release build
buildy --target myapp            # Build specific target
buildy --platform linux --arch x86_64  # Specify platform/arch
```

### Cache and Clean

```bash
buildy --clean                   # Remove what buildy built in the workspace
buildy --clean-all               # The same, and the cache directory (fetched dependencies too)
buildy --force                   # Force rebuild
buildy --cache-stats             # Show cache info
```

`--clean` works from anywhere in the workspace and removes the output directory, inside the
workspace root: `build/` (`output.base_dir`) with every configuration's outputs, and the staging
and install directories in it (where they are by default); or, for an `output.pattern` outside
`base_dir`, the output directory of the build context given (`-c`, `-p`, `-a`). What staging,
installs and copies put outside the output directory (`dist/`, `${workspace}/bin`) stays: such a
directory can hold your own files, even under the same names. It never removes a directory buildy
didn't configure (a `build/`, `dist/` or `staging/` of your own), anything outside the workspace
root, or an output directory holding a module's config. Outside any workspace it refuses and
removes nothing. `--clean-all` also removes the cache directory, if it is inside the workspace
root. See [the design note](design/layout.md#--clean).

### Dependencies

```bash
buildy --update-deps             # Fetch git branches and tags, and archives without a checksum, again
buildy --force                   # Build every fetched dependency again (and every task)
```

A download fails when the server can't be reached within 30 s or sends nothing for 60 s; a slow
download that keeps arriving is never cut off.

### Debugging

```bash
buildy -n 4                      # Verbose level logging
buildy -n 5                      # Debug level logging
buildy --dry-run                 # Show what would build
```

What a command prints, such as compiler warnings, is shown under its task at the default level,
each task's output in one piece however many tasks run at once; a failed task shows its command
line and its output.

### Stopping a Build

Ctrl-C (SIGINT or SIGTERM) stops a build at any point, while dependencies are fetched or built
too. The running commands are killed with every process they started (Docker containers with
`docker kill`), their partial outputs are removed, the tasks that finished keep their cache
entries, and buildy exits with 130. A second Ctrl-C exits at once.

There is no time limit by default; `--timeout 30m` kills a task or dependency build step that runs
longer, the same way.

### All Options

| Option | Short | Description |
|--------|-------|-------------|
| `--config` | `-c` | Build configuration (debug/release) |
| `--release` | `-r` | Shortcut for `--config=release` |
| `--platform` | `-p` | Target platform |
| `--architecture` | `-a` | Target architecture |
| `--toolchain` | `-t` | Override toolchain |
| `--target` | | Build a target and everything it depends on, in any module (repeatable); an unknown name is an error |
| `--workers` | `-j` | How many tasks run at once; each task starts as soon as its dependencies have finished |
| `--timeout` | | Kill a task, or a dependency build step, that runs longer than this Go duration (`90s`, `30m`, `2h`); no limit by default |
| `--force` | `-f` | Ignore the cache and the dependency build records: rebuild everything |
| `--update-deps` | | Fetch the dependencies that can move again: git branches and tags, archives without a checksum |
| `--clean` | | Remove what buildy built in the workspace (below), and nothing else |
| `--clean-all` | | `--clean`, and the cache directory: fetched dependencies, their builds, the cache records |
| `--dry-run` | `-d` | Plan only: generate and validate the tasks, list the dependency fetches and builds and the tasks in an order they can run in, each with what it waits for (and, with `-n 3`, its command); fetch, build and run nothing |
| `--notify` | `-n` | Log level (1=error, 2=warn, 3=info, 4=verbose, 5=debug) |
| `--compile-commands` | | Generate compile_commands.json |
| `--define` | `-D` | Define variable (VAR=value) |
| `--list-toolchains` | | List available toolchains |
| `--cache-dir` | | Cache directory: relative to the workspace root, or absolute (default `.buildy_cache`) |
| `--cache-stats` | | Show cache statistics |
| `--add-data-dir` | | Additional data directory: its `templates/`, `toolchains/` and `build_systems/` ([search paths](#data-directory-search-paths)) |

## Build Reports

After each build, buildy prints a summary and saves a detailed JSON report to `.buildy_cache/build_report_<platform>-<arch>-<config>.json` (the task graph goes to `tasks_<platform>-<arch>-<config>.json`).

```
================================================================================
                              BUILD REPORT
================================================================================
Status: SUCCESS
Duration: 2.345s

Tasks:
  Total:       24
  Cache hits:  18 (75.0%)
  Cache misses: 6
================================================================================
```

## Tutorials

For step-by-step tutorials, see:

- [Basic Tutorial](TUTORIAL_BASIC.md) - Single-target project
- [Multi-Module Tutorial](TUTORIAL_MULTIMODULE.md) - Workspace with multiple modules
- [Advanced Tutorial](TUTORIAL_ADVANCED.md) - Fetch dependencies and Docker builds

## Reference

For complete field documentation, see [DSL_SPEC.md](DSL_SPEC.md).
