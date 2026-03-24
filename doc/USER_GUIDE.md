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
buildy --clean            # Clean build artifacts
buildy --dry-run          # Show what would be built
```

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
│   ├── dependencies.lock       # Lockfile (auto-generated)
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
  - "src/**/*.cpp"        # Recursive
```

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

### Custom Linker Flags

You can pass custom flags to the linker using the `link:` section, available at both the environment level (all targets) and per-target:

```yaml
environment:
  link:
    flags: ["-Wl,-z,now"]          # Applied to all link commands
    remove_flags: ["-s"]           # Remove specific linker flags

targets:
  shared_libraries:
    - name: mylib
      language: cpp
      sources: ["src/*.cpp"]
      link:
        flags: ["-Wl,--version-script=mylib.map"]   # Per-target linker flags
```

This works identically to the compile flag system — environment flags are merged first, then target-level flags are appended. Use `remove_flags` to strip flags inherited from the environment or toolchain.

### Generated Sources

For source files created by code generators (protobuf, etc.), use `depends_on.artifacts` to create a dependency on the artifact. Buildy will automatically include the artifact's outputs in your source globs, even though those files don't exist yet:

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
        - "${gen_dir}/*.pb.cc"  # Glob will match artifact outputs
      depends_on:
        artifacts: ["protos"]   # Creates build dependency + enables glob matching
```

When a target declares `depends_on.artifacts`:
1. The artifact tasks run before the target's compilation
2. The artifact's output files are added to the "virtual file" list
3. Source globs in the target will match these virtual files

This means you use regular globs for generated files - no need to list each file explicitly. The outputs declared in the artifact's `outputs` field determine what files are available for matching.

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

Define debug and release configurations:

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

Use the `flags` key to pass literal compiler flags. These are applied at every level of the configuration hierarchy and concatenated:

```yaml
environment:
  compile:
    flags: ["-fpermissive"]
  configurations:
    debug:
      flags: ["-fsanitize=address"]
    release:
      flags: ["-flto"]

targets:
  executables:
    - name: myapp
      language: cpp
      sources: ["src/*.cpp"]
      flags: ["-fno-rtti"]
```

In a debug build, `myapp` would receive: `[resolved abstract keywords] + [-fpermissive] + [-fsanitize=address] + [-fno-rtti]`.

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

### Fetched Dependencies

Dependencies can be fetched from Git repositories or URLs. They are stored locally in `.buildy_cache/deps/<name>/` within your project - **not** installed system-wide.

For dependencies that need to be built:
- **Source**: `.buildy_cache/deps/<name>/`
- **Build**: `.buildy_cache/deps/<name>/_build/`
- **Install**: `.buildy_cache/deps/<name>/_install/`

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
    include_dirs: ["${dep_dir}/_install/include"]
    lib_dirs: ["${dep_dir}/_install/lib"]
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

**Supported build systems:** `cmake`, `meson`, `make`, `autoconf`, `buildy`, `none`

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
4. Both debug and release configurations are built automatically
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

Dependencies are defined in `buildy_config/dependencies.yaml`. For larger projects, you can organize them into separate files under `buildy_config/dependencies/`:

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
    include_dirs: ["${dep_dir}/_install/include"]
    lib_dirs: ["${dep_dir}/_install/lib"]
    libs: ["SDL2"]
```

### Lockfiles

Generate a lockfile for reproducible builds:

```bash
buildy --update-lock    # Generate/update lockfile
buildy --require-lock   # Fail if no lockfile exists
```

The lockfile (`buildy_config/dependencies.lock`) pins git commits and checksums.

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

Build fetched dependencies in Docker containers:

**Global default:**

```yaml
environment:
  dependency_builds:
    execution:
      type: docker
      image: "gcc:13"
      volumes:
        - "${PWD}:/workspace"
      working_dir: "/workspace"
      user: "${UID}:${GID}"
```

**Per-dependency override:**

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
      volumes:
        - "${PWD}:/workspace"
```

### Docker Configuration

| Field | Description |
|-------|-------------|
| `type` | `docker` or `native` |
| `image` | Docker image name |
| `volumes` | Volume mounts (supports `${PWD}`, `${UID}`, `${GID}`) |
| `working_dir` | Working directory in container |
| `user` | User to run as (default: `${UID}:${GID}`) |

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
        - "${gen_dir}/*.pb.cc"   # Matches artifact outputs
      depends_on:
        artifacts: ["protos"]    # Enables glob matching + build dependency
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

### Custom Build Systems

Define build systems for fetched dependencies:

```yaml
# buildy_config/build_systems/bazel.yaml
build_system:
  name: bazel
  description: "Google Bazel"

  detection:
    marker_files: ["WORKSPACE", "BUILD.bazel"]

  phases:
    build:
      command: "bazel build //..."
      working_dir: "{source_dir}"
```

### Data Directory Search Paths

Buildy searches for toolchains, templates, packages, and build systems in this order:

1. `./buildy_config/<type>/` - Project-specific
2. `~/.buildy/<type>/` - User-shared
3. Built-in (embedded in binary)

Add custom search paths via CLI:

```bash
buildy --add-data-dir /opt/company/buildy
buildy --package-dir /opt/company/packages
```

## Toolchain Selection

### Built-in Toolchains

**C/C++:**
- `gcc-c-linux`, `gcc-cpp-linux` - GCC on Linux
- `clang-c-linux`, `clang-cpp-linux` - Clang on Linux
- `clang-c-macos`, `clang-cpp-macos` - Apple Clang
- `msvc-c-windows`, `msvc-cpp-windows` - MSVC
- `gcc-c-mingw`, `gcc-cpp-mingw` - MinGW cross-compiler
- `docker-c-linux`, `docker-cpp-linux` - Docker-based GCC
- `emscripten-c`, `emscripten-cpp` - WebAssembly

**Go:** `go-linux`, `go-macos`, `go-windows`

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

## Language-Specific Features

### Go Projects

```yaml
targets:
  executables:
    - name: myapp
      language: go
      path: "cmd/myapp"          # Directory with main package
      build_tags: ["netgo"]
      ldflags: ["-s", "-w"]      # Strip for smaller binary
```

### Rust Projects

```yaml
targets:
  executables:
    - name: myapp
      language: rust
      path: "."                  # Directory with Cargo.toml
      features: ["feature1"]
      bin: "myapp"               # If multiple binaries
```

### C/C++ Libraries for FFI

Go and Rust can produce C-compatible libraries:

```yaml
targets:
  shared_libraries:
    - name: mylib
      language: go          # Creates a C-compatible .so/.dll
      path: "."
```

## IDE Integration

Generate `compile_commands.json` for IDE support:

```bash
buildy --compile-commands
```

This creates `buildy_config/compile_commands.json` which IDEs and language servers use for code intelligence.

## Cache Management

Buildy uses content-addressable caching for incremental builds.

```bash
buildy --cache-stats      # Show cache statistics
buildy --force            # Ignore cache, rebuild all
buildy --cache-dir /path  # Custom cache directory
```

Set cache directory via environment:

```bash
export BUILDY_CACHE_DIR=/path/to/cache
```

Default cache location: `.buildy_cache/` in the project root.

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
buildy --clean                   # Clean build artifacts
buildy --force                   # Force rebuild
buildy --cache-stats             # Show cache info
```

### Dependencies

```bash
buildy --update-lock             # Update lockfile
buildy --require-lock            # Require lockfile
buildy --ignore-lock             # Ignore lockfile
```

### Debugging

```bash
buildy -n 4                      # Verbose level logging
buildy -n 5                      # Debug level logging
buildy --dry-run                 # Show what would build
```

### All Options

| Option | Short | Description |
|--------|-------|-------------|
| `--config` | `-c` | Build configuration (debug/release) |
| `--release` | `-r` | Shortcut for `--config=release` |
| `--platform` | `-p` | Target platform |
| `--architecture` | `-a` | Target architecture |
| `--toolchain` | `-t` | Override toolchain |
| `--target` | | Build specific target(s) |
| `--workers` | `-j` | Parallel workers |
| `--force` | `-f` | Ignore cache |
| `--clean` | | Clean artifacts |
| `--dry-run` | `-d` | Don't execute |
| `--notify` | `-n` | Log level (1=error, 2=warn, 3=info, 4=verbose, 5=debug) |
| `--compile-commands` | | Generate compile_commands.json |
| `--update-lock` | | Update lockfile |
| `--require-lock` | | Require lockfile |
| `--define` | `-D` | Define variable (VAR=value) |
| `--list-toolchains` | | List available toolchains |
| `--cache-dir` | | Cache directory |
| `--cache-stats` | | Show cache statistics |
| `--add-data-dir` | | Additional data directory |

## Build Reports

After each build, buildy prints a summary and saves a detailed JSON report to `.buildy_cache/build_report.json`.

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
