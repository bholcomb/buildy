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
buildy -c release         # Release build
buildy --config release   # Same as above
```

### Common Options

```bash
buildy -j 8               # Use 8 parallel workers
buildy --verbose          # Verbose output
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

**External packages:**

```yaml
targets:
  executables:
    - name: myapp
      packages:
        - glfw3
        - opengl
```

### Build Configurations

Define debug and release configurations:

```yaml
environment:
  configurations:
    debug:
      optimization: "-O0"
      defines: ["DEBUG=1"]
      flags: ["-g"]
    release:
      optimization: "-O3"
      defines: ["NDEBUG=1"]
      flags: ["-flto"]
```

## Working with Dependencies

### System Dependencies

System dependencies use pkg-config or explicit library names:

```yaml
dependencies:
  system:
    common:
      - name: zlib
        pkg_config: zlib
    linux:
      - name: pthread
        libs: ["pthread"]
    windows:
      - name: winsock
        libs: ["ws2_32"]
    macos:
      - name: cocoa
        frameworks: ["Cocoa", "IOKit"]
```

### Path Dependencies

For libraries at known paths (like SDKs):

```yaml
dependencies:
  paths:
    - name: vulkan_sdk
      path: "${VULKAN_SDK}"
      include_dirs: ["${path}/include"]
      lib_dirs: ["${path}/lib"]
      libs: ["vulkan"]
```

### Packages

Packages are YAML files that define platform-specific library settings:

```yaml
# buildy_config/packages/glfw3.yaml
package:
  name: glfw3
  description: "GLFW window library"

  common:
    defines: ["GLFW_ENABLED"]

  linux:
    include_dirs: ["/usr/include/GLFW"]
    lib_dirs: ["/usr/lib/x86_64-linux-gnu"]
    libs: ["glfw"]

  windows:
    include_dirs: ["${THIRD_PARTY}/GLFW/include"]
    lib_dirs: ["${THIRD_PARTY}/GLFW/lib"]
    libs: ["glfw3"]

  macos:
    include_dirs: ["/opt/homebrew/include"]
    lib_dirs: ["/opt/homebrew/lib"]
    libs: ["glfw"]
    frameworks: ["Cocoa", "IOKit", "CoreVideo"]
```

Use packages in targets:

```yaml
targets:
  executables:
    - name: myapp
      packages: ["glfw3"]
```

### Fetching External Code

Download and build external dependencies. Fetched dependencies are stored locally in `.buildy_cache/deps/<name>/` within your project - they are **not** installed system-wide.

For dependencies that need to be built, the output goes to:
- **Source**: `.buildy_cache/deps/<name>/`
- **Build**: `.buildy_cache/deps/<name>/_build/`
- **Install**: `.buildy_cache/deps/<name>/_install/` (headers and libraries)

**Git repositories:**

```yaml
dependencies:
  fetch:
    - name: imgui
      git: "https://github.com/ocornut/imgui.git"
      ref: "v1.90.1"
      build_system: cmake
      build_phases: [configure, build]
      build_args: ["-DIMGUI_DEMO=OFF"]
```

**URL downloads:**

```yaml
dependencies:
  fetch:
    - name: stb
      url: "https://github.com/nothings/stb/archive/master.tar.gz"
      checksum: "sha256:..."
      type: header_only
      include_dirs: ["${dest}"]
```

**Supported build systems:** `cmake`, `meson`, `make`, `autoconf`, `cargo`, `go_mod`, `auto` (auto-detect)

**Build phases:** `configure`, `build`, `test`, `install`, `clean`

### Building Fetch Dependencies with Buildy Config

Instead of using `build_system`, you can build fetched dependencies using a buildy config file. This is useful when:
- The dependency already has a `buildy.yaml`
- You provide a custom buildy config for the dependency
- You want consistent build handling across your project and dependencies

```yaml
dependencies:
  fetch:
    - name: imgui
      git: "https://github.com/ocornut/imgui.git"
      ref: "v1.90.1"
      config: imgui_build.yaml   # Buildy config file in the dependency's root
```

When `config` is specified:
1. The dependency is fetched as usual
2. Instead of using cmake/meson/etc, buildy processes the config file
3. The dependency becomes a module in your workspace
4. Its targets are available for `depends_on.targets` in your own targets

```yaml
# Your target can now depend on targets from the fetched dependency
targets:
  executables:
    - name: my_app
      sources: ["src/*.cpp"]
      depends_on:
        targets: [imgui]   # Target defined in imgui_build.yaml
```

### External Dependencies File

For cleaner configuration, put dependencies in a separate file:

```yaml
# buildy.yaml
dependencies:
  file: buildy_config/dependencies.yaml
```

```yaml
# buildy_config/dependencies.yaml
system:
  common:
    - name: zlib
      pkg_config: zlib

fetch:
  - name: sdl2
    git: "https://github.com/libsdl-org/SDL.git"
    ref: "release-2.28.5"
    build_system: cmake

packages:
  - glfw3
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
    common:           # All platforms
      - core
      - renderer
      - game
    linux:            # Linux only
      - platform/linux
    windows:          # Windows only
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
    common:
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
dependencies:
  fetch:
    - name: complex_lib
      git: "https://github.com/example/lib.git"
      build_system: cmake
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
buildy --verbose                 # Verbose output
buildy --dry-run                 # Show what would build
```

### All Options

| Option | Short | Description |
|--------|-------|-------------|
| `--config` | `-c` | Build configuration (debug/release) |
| `--platform` | `-p` | Target platform |
| `--architecture` | `-a` | Target architecture |
| `--toolchain` | `-t` | Override toolchain |
| `--target` | | Build specific target(s) |
| `--workers` | `-j` | Parallel workers |
| `--force` | `-f` | Ignore cache |
| `--clean` | | Clean artifacts |
| `--dry-run` | `-n` | Don't execute |
| `--verbose` | `-v` | Verbose output |
| `--compile-commands` | | Generate compile_commands.json |
| `--update-lock` | | Update lockfile |
| `--require-lock` | | Require lockfile |
| `--define` | `-D` | Define variable (VAR=value) |
| `--list-toolchains` | | List available toolchains |
| `--cache-dir` | | Cache directory |
| `--cache-stats` | | Show cache statistics |
| `--add-data-dir` | | Additional data directory |
| `--package-dir` | | Additional package directory |

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
