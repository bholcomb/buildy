# Buildy

A modern, data-driven build system for multi-language projects.

## Features

- **Multi-Language Support**: Build C, C++, Go, and Rust projects with a unified configuration
- **Data-Driven**: All build logic defined in YAML - templates, toolchains, and build configurations
- **Content-Addressable Caching**: Fast incremental builds with Git-style sharded cache
- **Parallel Execution**: Automatic dependency resolution and parallel task execution
- **Cross-Platform**: Same `buildy.yaml` works on Linux, Windows, and macOS
- **Extensible**: Add new languages and tools via YAML template and toolchain files

## Quick Start

### Building Buildy

```bash
# Bootstrap build (first time)
./bootstrap.sh

# Subsequent builds
./bin/buildy
```

### Building a Project

```bash
# Build with default settings (debug, auto-detected platform)
buildy

# Build a specific directory
buildy path/to/project/

# Build a specific config file (standalone mode)
buildy path/to/config.yaml

# Build release configuration
buildy -r              # Short form
buildy --config release  # Long form

# Specify platform for cross-compilation
buildy --platform linux --architecture x86_64

# Verbose/debug output
buildy -n 4               # Verbose level
buildy -n 5               # Debug level
```

## Project Configuration

Projects are configured using `buildy.yaml` files. Here's a simple example:

```yaml
project:
  name: my-project
  version: "1.0.0"

environment:
  compile:
    cpp_standard: "c++20"
    warnings: ["-Wall", "-Wextra"]
  
  configurations:
    debug:
      optimization: "-O0"
      flags: ["-g"]
    release:
      optimization: "-O3"
      defines: ["NDEBUG=1"]

targets:
  static_libraries:
    - name: mylib
      language: cpp
      sources: ["src/*.cpp"]
      include_dirs: ["include"]

  executables:
    - name: myapp
      language: cpp
      sources: ["app/*.cpp"]
      libs: [mylib]
      depends_on:
        targets: [mylib]
```

## Target Sections

Buildy uses explicit sections for different target types:

| Section | Output | Supported Languages |
|---------|--------|---------------------|
| `static_libraries` | Static library (.a, .lib) | c, cpp, rust |
| `shared_libraries` | Shared library (.so, .dll) | c, cpp, rust, go |
| `executables` | Executable binary | c, cpp, rust, go |

### Language Field

The `language` field is **required** for all targets:

```yaml
targets:
  executables:
    - name: myapp
      language: cpp    # Required: c, cpp, go, or rust
      sources: ["src/*.cpp"]
```

### Go and Rust Projects

For Go and Rust, typically only `executables` are needed since dependencies are managed by Go modules and Cargo:

```yaml
# Go executable
targets:
  executables:
    - name: myapp
      language: go
      path: "cmd/myapp"      # Directory with go.mod or main package
      ldflags: ["-s", "-w"]  # Optional linker flags

# Rust executable  
targets:
  executables:
    - name: myapp
      language: rust
      path: "."              # Directory with Cargo.toml
      features: ["feature1"] # Optional Cargo features
```

Go/Rust library targets (`shared_libraries`, `static_libraries`) are for C/C++ interop only.

## Workspaces

Multi-module projects use the `workspace` section:

```yaml
# Root buildy.yaml
project:
  name: game-engine

workspace:
  modules:
    - core                    # Built on all platforms
    - renderer
    - audio
    - linux:                  # Linux only
        - platform/linux
    - windows:                # Windows only
        - platform/windows
```

Each subdirectory contains its own `buildy.yaml`. The module list uses filtered list syntax - plain strings are always included, map entries with platform keys are filtered.

## Dependencies

External dependencies are defined in `buildy_config/dependencies.yaml`:

```yaml
# buildy_config/dependencies.yaml
zlib:
  common:
    pkg_config: zlib

pthread:
  linux:
    libs: ["pthread"]

imgui:
  common:
    git: "https://github.com/ocornut/imgui.git"
    ref: "v1.90.1"
    include_dirs: ["${dep_dir}"]

glfw3:
  common:
    url: "https://github.com/glfw/glfw/releases/download/3.4/glfw-3.4.zip"
    checksum: "sha256:abc123..."  # Recommended for reproducibility
    include_dirs: ["${dep_dir}/include"]
```

## Command-Line Options

```
Usage: buildy [options]

Options:
  -c, --config <name>   Build configuration (debug, release)
  -r, --release         Shortcut for --config=release
  -p, --platform <name> Target platform (linux, windows, macos)
  -a, --arch <name>     Target architecture (x86_64, arm64)
  -t, --toolchain <name> Override toolchain selection
  --target <name>       Build specific target(s)
  -j, --workers <n>     Number of parallel workers
  -d, --dry-run         Show what would be built
  --clean               Clean build outputs
  -f, --force           Ignore cache, rebuild all
  --compile-commands    Generate compile_commands.json
  -n, --notify <level>  Log level: 1=error, 2=warning, 3=info, 4=verbose, 5=debug
```

## Project Structure

```
my-project/
├── buildy.yaml              # Project configuration
├── buildy_config/
│   ├── dependencies.yaml    # External dependencies (optional)
│   ├── dependencies/        # Per-dependency configs (optional)
│   │   └── glfw3.yaml
│   ├── dependencies.lock    # Lockfile (auto-generated)
│   └── toolchains/          # Custom toolchains (optional)
│       └── custom-gcc.yaml
├── src/                     # Source files
├── include/                 # Headers
├── build/                   # Output directory (auto-created)
└── .buildy_cache/           # Build cache (git-ignored)
    ├── deps/                # Fetched dependencies
    ├── downloads/           # Cached archives
    └── objects/             # Compiled objects
```

## Built-in Toolchains

Buildy includes toolchains for common compilers:

| Toolchain | Platform | Language | Description |
|-----------|----------|----------|-------------|
| `gcc-c-linux` | Linux | C | GCC C compiler |
| `gcc-cpp-linux` | Linux | C++ | GCC C++ compiler |
| `clang-c-linux` | Linux | C | Clang C compiler |
| `clang-cpp-linux` | Linux | C++ | Clang C++ compiler |
| `clang-c-macos` | macOS | C | Apple Clang C |
| `clang-cpp-macos` | macOS | C++ | Apple Clang C++ |
| `msvc-c-windows` | Windows | C | MSVC C compiler |
| `msvc-cpp-windows` | Windows | C++ | MSVC C++ compiler |
| `gcc-c-mingw` | Linux→Windows | C | MinGW cross-compiler |
| `gcc-cpp-mingw` | Linux→Windows | C++ | MinGW cross-compiler |
| `go-linux` | Linux | Go | Go compiler |
| `go-macos` | macOS | Go | Go compiler |
| `go-windows` | Windows | Go | Go compiler |
| `rust-linux` | Linux | Rust | Cargo/rustc |
| `rust-macos` | macOS | Rust | Cargo/rustc |
| `rust-windows` | Windows | Rust | Cargo/rustc |
| `emscripten-cpp` | Any | C++ | C++ to WebAssembly |
| `glslc` | Any | GLSL | GLSL to SPIR-V |

## Templates vs Toolchains

Buildy separates **what** to build from **how** to build:

| Concept | Purpose | Example |
|---------|---------|---------|
| **Templates** | Define build workflow (steps, inputs, outputs) | "Compile each source, then link them" |
| **Toolchains** | Define actual commands and flags | "Use `gcc -c` for compiling" |

**Templates** are language-agnostic recipes selected by `language` + target type:
- `cpp_executable`, `cpp_static_library`, `cpp_shared_library`
- `c_executable`, `c_static_library`, `c_shared_library`
- `go_executable`, `go_shared_library`
- `rust_executable`, `rust_static_library`, `rust_shared_library`

**Toolchains** are platform-specific implementations that provide the actual commands.

### Data-Driven Command Parameters

Toolchains use declarative `command_params` to define how command placeholders are resolved:

```yaml
# Example from go-linux.yaml
command_params:
  build_tags:
    sources: ["tool_params.build_tags", "item.build_tags"]
    format: "-tags ${value}"
    join: ","
    optional: true
```

This eliminates hardcoded language-specific logic and makes the build system fully extensible via YAML.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        buildy.yaml                          │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                     Config Parser                            │
│  - Parse YAML configuration                                  │
│  - Resolve variables and imports                             │
│  - Merge platform/configuration overrides                    │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    Task Generator                            │
│  - Look up templates by language + target type               │
│  - Apply pre-processing (packages, sources, paths)           │
│  - Expand templates into concrete build tasks                │
│  - Apply post-processing (register targets, scan sources)    │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      Task Graph                              │
│  - Build dependency graph                                    │
│  - Topological sort                                          │
│  - Parallel execution stages                                 │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                       Executor                               │
│  - Check cache for up-to-date outputs                        │
│  - Execute tasks in parallel                                 │
│  - Update cache with results                                 │
└─────────────────────────────────────────────────────────────┘
```

## Documentation

- [DSL Specification](doc/DSL_SPEC.md) - Complete configuration reference

## Examples

See the `examples/` directory:

- `examples/math/` - Simple C++ library and executable
- `examples/game_engine/` - Multi-module C++ project with dependencies

## License

MIT License
