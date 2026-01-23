# Buildy DSL Specification

Version: 1.0.0

## Overview

Buildy is a multi-language build system designed for:

- **C, C++, Go, and Rust** projects with data-driven task generation
- **Cross-platform builds** (Linux, Windows, macOS) from a single configuration
- **Explicit, reproducible builds** with optional lockfiles
- **Highly parallel execution** with content-addressable caching
- **Flexible dependency management** (system, user paths, fetched sources)

## Design Principles

1. **Pure YAML** - Standard syntax, no embedded scripting
2. **Explicit over implicit** - All operations declared, no hidden behavior
3. **Hierarchical configuration** - Workspace → Module → Target with clear override semantics
4. **Multi-language** - C/C++ primary with Go and Rust support, extensible via templates
5. **Flexible dependencies** - System, user paths, fetched sources, package managers
6. **Reproducible by default** - Lockfiles optional but encouraged, warnings for system deps
7. **Configurable cache** - `BUILDY_CACHE_DIR` env var or config, default to project-local
8. **Cross-platform by design** - Same `buildy.yaml` works on Linux, Windows, macOS

### Cross-Platform Rules

| Aspect | Rule |
|--------|------|
| **Paths** | Always use forward slashes in YAML; converted to native separators at runtime |
| **Platform deps** | Use subsections (`common:`, `linux:`, `windows:`, `macos:`) |
| **Executable extensions** | Toolchain handles via `output_pattern` (e.g., `{name}.exe` on Windows) |
| **Shell commands** | Abstract tools preferred; platform subsections when shell is unavoidable |

---

## File Structure

A typical project layout:

```
project/
├── buildy.yaml                    # Root configuration
├── buildy/
│   ├── dependencies.yaml          # External dependencies (optional)
│   ├── dependencies.lock          # Lockfile (auto-generated)
│   ├── packages/
│   │   └── glfw3.yaml             # Package definitions
│   └── toolchains/
│       └── mesh-converter.yaml    # Custom tool definitions
├── core/
│   └── buildy.yaml                # Submodule
├── renderer/
│   └── buildy.yaml                # Submodule
└── platform/
    ├── buildy.yaml                # Intermediate module (has children)
    ├── linux/
    │   └── buildy.yaml            # Leaf module
    └── windows/
        └── buildy.yaml            # Leaf module
```

---

## Top-Level Structure

```yaml
project:           # Metadata (required)
variables:         # User-defined variables (optional)
workspace:         # Multi-module coordination (optional)
dependencies:      # External dependencies (optional, root only)
environment:       # Build configuration (optional)
targets:           # What to build (optional)
artifacts:         # File operations (optional)
staging:           # Product assembly (optional)
install:           # Packaging for distribution (optional)
```

---

## 1. Project Section

Required metadata about the project.

```yaml
project:
  name: game_engine           # Required: project identifier
  version: "2.0.0"            # Optional: semantic version
  description: "My project"   # Optional: human-readable description
```

---

## 2. Variables Section

Define and import variables for use throughout the configuration.

### Importing Environment Variables

Environment variables **must be explicitly imported** before use. This makes builds self-documenting.

```yaml
variables:
  import_env_vars:
    - name: THIRD_PARTY_ROOT
      default: "./third_party"    # Use this if env var not set
    - name: VULKAN_SDK
      default: ~                  # null (~) = required, error if not set
    - name: HOME
      default: ~                  # Required
    - name: BUILD_NUMBER
      default: "dev"              # Default for local builds
```

### Import Rules

1. **All environment variables must be listed** - unlisted env vars cannot be referenced
2. **Each import must specify `default:`** - either a string value or `~` (null) for required
3. **Required vars (`default: ~`) cause immediate error if not set** - fail fast
4. **After import, use unified `${VAR}` syntax** - no special prefix needed

### Regular Variables

```yaml
variables:
  import_env_vars:
    - name: THIRD_PARTY_ROOT
      default: "./third_party"

  # User-defined variables (can reference imported env vars)
  ENGINE_VERSION: "1.0.0"
  DATA_DIR: "assets"
  LIBS_PATH: "${THIRD_PARTY_ROOT}/libs"  # Reference imported var
```

### Platform-Specific Variables

```yaml
variables:
  platforms:
    linux:
      LIB_PREFIX: "lib"
      LIB_EXT: ".so"
    windows:
      LIB_PREFIX: ""
      LIB_EXT: ".dll"
    macos:
      LIB_PREFIX: "lib"
      LIB_EXT: ".dylib"
```

### Variable Resolution Order

Priority from highest to lowest:

1. CLI `--define VAR=value`
2. Imported environment variables (via `import_env_vars`)
3. Configuration-specific variables (`configurations.debug.variables`)
4. Architecture-specific variables (`architectures.x86_64.variables`)
5. Platform-specific variables (`variables.platforms.linux`)
6. Project-level variables
7. Workspace-level variables
8. Built-in variables

### Built-in Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `${platform}` | Target platform | `linux`, `windows`, `macos` |
| `${arch}` | Target architecture | `x86_64`, `arm64` |
| `${config}` | Build configuration | `debug`, `release` |
| `${project_name}` | From `project.name` | `game_engine` |
| `${project_version}` | From `project.version` | `2.0.0` |
| `${out_dir}` | Output directory | `build/linux-x86_64-debug` |
| `${cache_dir}` | Cache directory | `.buildy_cache` |
| `${gen_dir}` | Generated files directory | `build/.../gen` |

### Error Messages

```
ERROR: Required environment variable 'VULKAN_SDK' is not set.
       Defined in: variables.import_env_vars (buildy.yaml:12)
       
       To fix: export VULKAN_SDK=/path/to/vulkan/sdk
```

---

## 3. Workspace Section

Define hierarchical module structure. Modules are **explicitly listed** (no auto-discovery).

### Root Workspace

```yaml
workspace:
  modules:
    common:                    # Built on all platforms
      - core
      - renderer
      - audio
      - platform               # Has its own child modules
    linux:                     # Linux-only modules
      - tools/linux_profiler
    windows:                   # Windows-only modules
      - tools/windows_debugger
    macos:                     # macOS-only modules
      - tools/xcode_integration
```

### Intermediate Module (with children)

A module can both define targets AND have child modules:

```yaml
# platform/buildy.yaml
project:
  name: platform_layer

workspace:
  modules:
    linux:
      - linux              # platform/linux/buildy.yaml
    windows:
      - windows            # platform/windows/buildy.yaml

targets:
  shared_libraries:
    - name: platform_common
      language: cpp
      sources: ["src/*.cpp"]
```

### Leaf Module (no children)

No `workspace.modules` section = leaf module:

```yaml
# platform/linux/buildy.yaml
project:
  name: platform_linux

targets:
  shared_libraries:
    - name: platform_linux
      language: cpp
      sources: ["src/*.cpp"]
      depends_on:
        targets: ["platform_common"]
```

### Custom Config Filename

```yaml
workspace:
  modules:
    common:
      - path: vendor/imgui
        config: imgui_build.yaml  # Custom filename
      - core                      # Uses default buildy.yaml
```

### Module Resolution Rules

1. **Default filename:** `buildy.yaml`
2. **Paths are relative** to the current buildy.yaml's directory
3. **Each listed path must contain the config file** (error if missing)
4. **Platform filtering at each level** - only descend into modules matching current platform
5. **No `workspace.modules`** = leaf module
6. **Targets can reference parent/sibling targets** via dependency names

---

## 4. Dependencies Section

Unified section for all external dependencies. Defined **only at the root level**.

### External File Reference

For cleaner configuration, reference an external file:

```yaml
# buildy.yaml
dependencies:
  file: buildy/dependencies.yaml
```

```yaml
# buildy/dependencies.yaml
system:
  common:
    - name: zlib
      pkg_config: zlib
  linux:
    - name: pthread
      libs: ["pthread"]

paths:
  - name: vulkan_sdk
    path: "${VULKAN_SDK}"
    include_dirs: ["${path}/include"]
    lib_dirs: ["${path}/lib"]
    libs: ["vulkan"]

fetch:
  - name: imgui
    git: "https://github.com/ocornut/imgui.git"
    ref: "v1.90.1"
```

### Inline Dependencies

Alternatively, define directly in `buildy.yaml`:

```yaml
dependencies:
  system:
    common:
      - name: zlib
        pkg_config: zlib
    linux:
      - name: pthread
        libs: ["pthread"]
      - name: dl
        libs: ["dl"]
    windows:
      - name: winsock
        libs: ["ws2_32", "wsock32"]
    macos:
      - name: cocoa
        frameworks: ["Cocoa", "IOKit"]

  paths:
    - name: glfw
      path: "${THIRD_PARTY_ROOT}/glfw/${platform}"
      include_dirs: ["${path}/include"]
      lib_dirs: ["${path}/lib"]
      libs: ["glfw3"]

  fetch:
    - name: imgui
      git: "https://github.com/ocornut/imgui.git"
      ref: "v1.90.1"
      build_system: auto      # auto | cmake | meson | make | cargo | go_mod | none
      build_phases: [configure, build]  # Phases to run (default: configure, build)
      build_args: ["-DIMGUI_DEMO=OFF"]  # Extra args passed to build system
      
    - name: stb
      url: "https://github.com/nothings/stb/archive/master.tar.gz"
      checksum: "sha256:..."
      type: header_only
      include_dirs: ["${dest}"]
      
    - name: some_lib
      git: "https://github.com/example/some_lib.git"
      build_system: cmake
      execution:              # Per-dependency execution override
        type: docker
        image: "gcc:13"

  packages:                   # Future: package manager support
    - name: fmt
      version: "10.1.0"
      manager: conan          # conan | vcpkg
```

### Dependency Types

| Type | Description | Example |
|------|-------------|---------|
| `system` | System libraries via pkg-config or explicit paths | zlib, pthread |
| `paths` | User-provided paths with variable expansion | Vulkan SDK |
| `fetch` | Downloaded from git or URL | imgui, stb |
| `packages` | Package files with platform-specific settings | glfw3, opengl |

### Packages

Packages are standalone YAML files that define platform-specific include paths, library paths, library names, defines, and frameworks. They're ideal for complex dependencies that vary significantly across platforms.

**Declaring packages in dependencies:**

```yaml
# buildy/dependencies.yaml
system:
  - name: zlib
    pkg_config: zlib

# Packages: reference package files by name
packages:
  - glfw3      # Resolves to buildy/packages/glfw3.yaml
  - opengl     # Resolves to buildy/packages/opengl.yaml
```

**Using packages in targets:**

```yaml
targets:
  executables:
    - name: my_game
      sources: ["src/*.cpp"]
      packages:           # External packages (adds include_dirs, lib_dirs, libs, defines)
        - glfw3
        - opengl
      libs:               # Internal project libraries
        - engine_core
```

**Package search order:**

1. `./buildy/packages/*.yaml` - Project-specific
2. `~/.buildy/packages/*.yaml` - User-shared
3. System locations (`/usr/share/buildy/packages/`, etc.)
4. Built-in (embedded in binary)

**Custom search paths (optional):**

```yaml
# buildy/dependencies.yaml
package_paths:                    # Additional search paths (checked first)
  - "${THIRD_PARTY_ROOT}/packages"
  - "/opt/company/buildy/packages"

packages:
  - glfw3
  - proprietary_lib              # Found in company path
```

**Package file format:**

```yaml
# buildy/packages/glfw3.yaml
package:
  name: glfw3
  description: "GLFW - OpenGL window and input library"
  
  common:
    defines: ["GLFW_ENABLED"]
  
  linux:
    include_dirs: ["/usr/include/GLFW"]
    lib_dirs: ["/usr/lib/x86_64-linux-gnu"]
    libs: ["glfw"]
    defines: ["GLFW_EXPOSE_NATIVE_X11"]
    
  linux-arm64:                # Platform-arch override
    lib_dirs: ["/usr/lib/aarch64-linux-gnu"]
  
  windows:
    include_dirs: ["${THIRD_PARTY_ROOT}/GLFW3/include"]
    lib_dirs: ["${THIRD_PARTY_ROOT}/GLFW3/lib"]
    libs: ["glfw3"]
    defines: ["GLFW_EXPOSE_NATIVE_WIN32"]
  
  macos:
    include_dirs: ["/opt/homebrew/include"]
    lib_dirs: ["/opt/homebrew/lib"]
    libs: ["glfw"]
    frameworks: ["Cocoa", "IOKit", "CoreVideo"]
```

**Inheritance order:** `common` → `platform` → `platform-arch`

**What packages provide:**

| Field | Description |
|-------|-------------|
| `include_dirs` | Added to compiler include path (`-I`) |
| `lib_dirs` | Added to linker library path (`-L`) |
| `libs` | Libraries to link (`-l`) |
| `defines` | Preprocessor definitions (`-D`) |
| `frameworks` | macOS frameworks (`-framework`) |

---

## 5. Environment Section

Build configuration: toolchains, compiler settings, build configurations.

```yaml
environment:
  toolchains:
    default: gcc-linux        # Default for all platforms
    linux: clang-linux        # Override for Linux
    windows: msvc-windows     # Override for Windows
    macos: clang-macos        # Override for macOS
    
  compile:
    cpp_standard: "c++20"
    c_standard: "c17"
    warnings: ["-Wall", "-Wextra", "-Wpedantic"]
    
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

---

## 6. Targets Section

Define what to build. Targets are organized into explicit sections by type.

### Target Sections

| Section | Output Type | Languages |
|---------|-------------|-----------|
| `static_libraries` | Static library (.a, .lib) | c, cpp, rust |
| `shared_libraries` | Shared library (.so, .dll, .dylib) | c, cpp, rust, go |
| `executables` | Executable binary | c, cpp, rust, go |

**Note:** Go and Rust library targets are for C/C++ interop only. For pure Go/Rust projects, use `executables` - dependencies are handled by Go modules and Cargo respectively.

### Static Libraries

```yaml
targets:
  static_libraries:
    - name: engine_core
      language: cpp           # Required: c | cpp | rust
      sources: ["src/*.cpp"]
      public_headers: ["include/*.h"]
      include_dirs:
        public: ["include"]   # Exported to consumers
        private: ["src"]      # Internal only
      
      # packages: External packages (add include_dirs, lib_dirs, libs, defines)
      packages: ["glfw3"]
      
      # depends_on: Build ordering
      depends_on:
        deps: ["zlib"]        # From dependencies section
        targets: []           # Other targets (build order)
      
      compile:
        standard: "c++20"
        defines: ["ENGINE_EXPORTS"]
        flags: ["-ffast-math"]
```

### Shared Libraries

```yaml
targets:
  shared_libraries:
    - name: engine_renderer
      language: cpp           # Required: c | cpp | rust | go
      sources: ["src/*.cpp"]
      public_headers: ["include/*.h"]
      include_dirs:
        public: ["include"]
      
      # libs: Libraries this shared library links against
      libs: ["engine_core"]
      
      depends_on:
        targets: ["engine_core"]
```

### Source Patterns

The `sources` field accepts glob patterns to match source files. By default, if a glob pattern matches no files, the build will **fail** with an error. This helps catch configuration mistakes early.

**Simple string patterns:**

```yaml
sources:
  - "src/*.cpp"           # All .cpp files in src/
  - "src/**/*.cpp"        # All .cpp files recursively
```

**Optional patterns (may match zero files):**

For patterns that may legitimately match zero files (e.g., platform-specific sources, optional plugin directories), use the object form with `optional: true`:

```yaml
sources:
  - "src/*.cpp"                                    # Required - error if no matches
  - {pattern: "src/plugins/*.cpp", optional: true} # Optional - no error if empty
  - {pattern: "src/platform/linux/*.cpp", optional: true}
```

**Mixing required and optional patterns:**

```yaml
targets:
  static_libraries:
    - name: my_lib
      language: cpp
      sources:
        - "src/core/*.cpp"                              # Must have at least one file
        - {pattern: "src/optional/*.cpp", optional: true}  # May be empty
        - {pattern: "contrib/*.cpp", optional: true}       # May be empty
```

| Syntax | Behavior |
|--------|----------|
| `"pattern"` | **Required** - build fails if pattern matches no files |
| `{pattern: "...", optional: true}` | **Optional** - empty matches are silently skipped |
| `{pattern: "...", optional: false}` | Same as string form (default) |

### Executables

```yaml
targets:
  executables:
    - name: game
      language: cpp           # Required: c | cpp | rust | go
      sources: ["src/*.cpp"]
      
      # packages: External packages (add include_dirs, lib_dirs, libs, defines)
      packages:
        - glfw3
        - opengl
      
      # libs: Internal libraries to link against
      # Order matters for static libraries (dependents before dependencies)
      libs:
        - engine_renderer    # List first if it depends on engine_core
        - engine_core        # List last (base library)
      
      # depends_on: Build ordering
      depends_on:
        targets: ["engine_core", "engine_renderer"]  # Build order
        deps: ["zlib"]                               # From dependencies section
      
      runtime_deps: ["game_assets", "shaders"]  # Artifacts needed at runtime
```

### Go Executables

Go projects typically only need `executables` since dependencies are managed by Go modules:

```yaml
targets:
  executables:
    - name: myapp
      language: go
      path: "cmd/myapp"       # Directory containing go.mod or main package
      build_tags: []
      ldflags: ["-s", "-w"]   # For release builds
```

### Rust Executables

Rust projects typically only need `executables` since dependencies are managed by Cargo:

```yaml
targets:
  executables:
    - name: myapp
      language: rust
      path: "."               # Directory containing Cargo.toml
      features: ["feature1"]
      bin: "myapp"            # Binary name if multiple binaries in crate
```

### packages vs libs vs depends_on

These are **intentionally separate** concepts:

| Field | Purpose | Example |
|-------|---------|---------|
| `packages` | External packages (provide include paths, libs, defines) | `[glfw3, opengl]` |
| `libs` | Internal project libraries to link against | `[engine_core, engine_renderer]` |
| `depends_on.targets` | Build ordering - these must complete first | Code generators, libraries, tools |
| `depends_on.deps` | External dependencies (from `dependencies:` section) | System libs, fetched sources |

**Why separate?**

1. **Packages are external**: They provide platform-specific settings (includes, libs, defines)
2. **libs are internal**: Project libraries you've built
3. **depends_on is ordering**: Ensures targets are built first (may or may not involve linking)
4. **Explicit is better**: Each field has one clear purpose

**Example: Using packages and libs together**

```yaml
targets:
  executables:
    - name: my_game
      sources: ["src/*.cpp"]
      packages:            # External (adds glfw's include_dirs, lib_dirs, libs)
        - glfw3
      libs:                # Internal (your project's libraries)
        - engine_renderer
        - engine_core
      depends_on:
        targets:           # Build order
          - engine_core
          - engine_renderer
```

**Example: Depending on a code generator (no linking)**

```yaml
targets:
  executables:
    - name: my_app
      sources: ["src/*.cpp", "${gen_dir}/*.cpp"]
      depends_on:
        targets: ["my_codegen"]  # Build order only - tool runs first
      # No libs or packages needed for the code generator itself
```

### Per-Target Toolchain Override

```yaml
targets:
  static_libraries:
    - name: perf_critical
      language: cpp
      toolchain: clang-linux      # Override default toolchain
      sources: ["src/*.cpp"]
```

---

## 7. Artifacts Section

File operations: copy, transform, generate, install.

### Copy

```yaml
artifacts:
  copy:
    - name: game_assets
      sources: ["assets/**/*.png", "assets/**/*.wav"]
      destination: "${out_dir}/data"
      preserve_structure: true
      incremental: true
```

### Transform

```yaml
artifacts:
  transform:
    - name: compress_textures
      tool: texconv
      inputs: "assets/textures/*.png"
      outputs: "${out_dir}/textures/${basename}.dds"
      args: ["-f", "BC7_UNORM"]
```

### Generate

```yaml
artifacts:
  generate:
    - name: version_header
      template: "src/version.h.in"
      output: "${gen_dir}/version.h"
      variables:
        VERSION: "${project_version}"
        GIT_HASH: "${git_commit}"
        
    - name: protobuf
      tool: protoc
      inputs: "proto/*.proto"
      outputs: ["${gen_dir}/${basename}.pb.h", "${gen_dir}/${basename}.pb.cc"]
      args: ["--cpp_out=${gen_dir}"]
```

---

## 8. Staging Section

The staging section defines how to assemble build outputs into a product directory structure. This is useful for:
- Testing the final product layout before packaging
- Fast developer iteration with symlinks
- Collecting executables, libraries, and assets into a deliverable structure

### Hierarchical Folder Structure

Staging uses a folder-based hierarchy that mirrors the output layout directly:

```yaml
staging:
  name: my_product
  destination: "${out_dir}/staging"
  use_symlinks: true              # Default for all contents (default: false = copy)

  contents:
    # Root level files (folder: "." or omit folder)
    - folder: "."
      files: ["LICENSE.txt", "README.md"]

    # Binaries folder - executables and shared libraries
    - folder: bin
      targets:
        - game
        - launcher
        - engine_core
        - engine_renderer

    # Data folder with nested subfolders
    - folder: data
      contents:
        - folder: shaders
          artifacts:
            - vertex_shaders
            - fragment_shaders

        - folder: textures
          files: ["assets/textures/*.png"]

        - folder: meshes
          files: ["assets/meshes/*.mesh"]

        - folder: config
          files: ["config/*.ini"]

    # Scripts with symlinks for live editing
    - folder: scripts
      files:
        - source: "scripts/*.lua"
          use_symlink: true
```

### Folder Entry Keys

Each folder entry can contain:

| Key | Description |
|-----|-------------|
| `folder` | Destination folder name (use `"."` for root) |
| `targets` | Build targets (executables or libraries - auto-detected) |
| `artifacts` | Named artifact outputs from transform/generate |
| `files` | Source file paths or glob patterns |
| `contents` | Nested folder entries (for subfolders) |

### Targets

The `targets` list references build targets by name. Buildy auto-detects whether each is an executable or library:

```yaml
- folder: bin
  targets:
    - game              # Executable
    - engine_core       # Shared library (auto-detected)
    - name: launcher    # Extended form with options
      use_symlink: false
```

### Files

Files can be specified as simple strings or with options:

```yaml
files:
  - "docs/README.md"              # Simple path
  - "assets/textures/*.png"       # Glob pattern
  - source: "scripts/*.lua"       # Extended form
    use_symlink: true             # Override symlink setting
```

### Symlink Behavior

Symlinks provide fast developer iteration:
- Changes to source files are immediately reflected in the staging area
- No copy overhead during development
- Controlled by `use_symlinks` at staging level with per-item override

| Setting | Behavior |
|---------|----------|
| `use_symlinks: true` (staging) | Default to symlinks for all contents |
| `use_symlinks: false` (staging) | Default to copy for all contents (default) |
| `use_symlink: true/false` (per item) | Override the staging default |

**Platform Notes:**
- **Linux/macOS**: Symlinks work as expected
- **Windows**: Always uses copy (symlinks require admin or Developer Mode)

### Cache Integration

Staging operations are cached tasks:
- For symlinks: cache key includes source path and symlink flag
- For copies: cache key includes source content hash
- The cache automatically detects when staging needs to update

---

## 9. Install Section

The install section creates distributable packages from staging areas.

### Basic Packaging

```yaml
install:
  - name: release_package
    staging: my_product           # Reference staging area by name
    destination: "${out_dir}/dist"
    format: tar.gz                # tar.gz | zip | tar
    follow_symlinks: true         # Dereference symlinks (default: true)
    
    # Optional: filename pattern
    filename: "${project_name}-${project_version}-${platform}-${arch}"
```

### Supported Formats

| Format | Extension | Symlink Handling |
|--------|-----------|------------------|
| `tar.gz` | .tar.gz | Uses `-h` flag to dereference symlinks |
| `zip` | .zip | Follows symlinks by default |
| `tar` | .tar | Uses `-h` flag to dereference symlinks |

### Filename Variables

The `filename` field supports these variables:
- `${project_name}` - From project.name
- `${project_version}` - From project.version
- `${platform}` - Target platform (linux, windows, macos)
- `${arch}` - Target architecture (x86_64, arm64)
- `${config}` - Build configuration (debug, release)

**Example output:** `my_game-1.0.0-linux-x86_64.tar.gz`

---

## 10. Toolchain System

Toolchains define how to compile, link, and transform files.

### Search Order

1. `./buildy/toolchains/*.yaml` - Project-specific
2. `~/.buildy/toolchains/*.yaml` - User-shared
3. Built-in (embedded in binary)

### Toolchain Selection

```yaml
environment:
  toolchains:
    default: gcc-linux
    linux: clang-linux
    windows: msvc-windows
```

CLI override: `buildy --toolchain clang-linux`

### Unified Tool Model

All tools use the same format. The `action` field distinguishes behavior:

| Action | Description | Example |
|--------|-------------|---------|
| `compile` | Source to object file | gcc, clang, msvc |
| `link` | Objects to binary | ld, link.exe |
| `transform` | File conversion | texture compressor, mesh converter |
| `generate` | Create files | protoc, code generators |
| `build` | Full module build | Go compiler |

### Custom Toolchain Example

```yaml
# buildy/toolchains/mesh-converter.yaml
toolchain:
  name: "mesh-converter"
  description: "Convert mesh files to runtime format"
  
  target:
    platform: any
    architecture: any
  
  host:
    platform: any
    architecture: any
  
  execution:
    type: native
  
  tools:
    convert_mesh:
      action: transform
      command: "mesh_tool convert {flags} -i {input} -o {output}"
      input_extensions: [".fbx", ".obj", ".gltf"]
      output_extension: ".mesh"
      output_pattern: "{name}.mesh"
      flags:
        common: []
        debug: ["--validate"]
        release: ["--optimize", "--compress"]
```

### Cross-Platform Commands

```yaml
tools:
  compress:
    action: transform
    command:
      linux: "texconv {flags} -o {output_dir} {input}"
      windows: "texconv.exe {flags} -o {output_dir} {input}"
      macos: "texturetool {flags} -o {output} {input}"
```

---

## 11. Build Systems

Build systems define how to build fetched dependencies (e.g., CMake, Meson, Make projects). Buildy includes built-in definitions for common build systems and supports custom definitions.

### Built-in Build Systems

| Name | Detection | Description |
|------|-----------|-------------|
| `cmake` | CMakeLists.txt | CMake build system |
| `meson` | meson.build | Meson build system |
| `make` | Makefile, GNUmakefile | GNU Make |
| `autoconf` | configure, configure.ac | GNU Autoconf/Automake |
| `cargo` | Cargo.toml | Rust Cargo |
| `go_mod` | go.mod | Go modules |

### Build Phases

Each build system defines phases that can be selectively executed:

| Phase | Description | Required |
|-------|-------------|----------|
| `configure` | Configure/setup the build | Depends on system |
| `build` | Compile the project | Yes |
| `test` | Run tests | No |
| `install` | Install to prefix | No |
| `clean` | Clean build artifacts | No |

### Using Build Systems

In the `fetch` section of dependencies:

```yaml
dependencies:
  fetch:
    - name: zlib
      url: "https://github.com/madler/zlib/archive/v1.3.1.tar.gz"
      build_system: auto           # Auto-detect (default)
      build_phases: [configure, build]  # Phases to run
      build_args: ["-DZLIB_BUILD_EXAMPLES=OFF"]  # Extra args
```

### Execution Environment

Build systems can run natively or in Docker containers for reproducibility.

**Global default:**

```yaml
environment:
  dependency_builds:
    execution:
      type: docker
      image: "buildy/build-env:latest"
    default_phases: [configure, build]
```

**Per-dependency override:**

```yaml
dependencies:
  fetch:
    - name: complex_lib
      git: "https://github.com/example/complex.git"
      build_system: cmake
      execution:
        type: docker
        image: "gcc:13"
        volumes:
          - "${PWD}:/workspace"
```

### Custom Build System Definition

Create custom build system definitions in `buildy/build_systems/`:

```yaml
# buildy/build_systems/bazel.yaml
build_system:
  name: bazel
  description: "Google Bazel build system"
  
  detection:
    marker_files: ["WORKSPACE", "BUILD.bazel"]
    priority: 10
  
  phases:
    configure:
      command: "true"  # No configure step
      required: false
    
    build:
      command: "bazel build //... {args}"
      working_dir: "{source_dir}"
      required: true
    
    test:
      command: "bazel test //..."
      working_dir: "{source_dir}"
      required: false
    
    clean:
      command: "bazel clean"
      working_dir: "{source_dir}"
      required: false
  
  output:
    include_patterns:
      - "{source_dir}"
    lib_patterns:
      - "{source_dir}/bazel-bin"
```

### Search Order

1. `./buildy/build_systems/*.yaml` - Project-specific
2. `~/.buildy/build_systems/*.yaml` - User-shared
3. Built-in (embedded in binary)

### Phase Variables

Available in command templates:

| Variable | Description |
|----------|-------------|
| `{source_dir}` | Source directory of the dependency |
| `{build_dir}` | Build output directory (usually `{source_dir}/_build`) |
| `{install_dir}` | Installation prefix directory |
| `{jobs}` | Number of parallel jobs (auto-detected from CPU count) |
| `{args}` | Combined default_args and build_args |

---

## 12. Lockfile

Optional lockfile for reproducible builds. Located at `buildy/dependencies.lock`.

### Generation

```bash
buildy --update-lock    # Generate/update lockfile
buildy --require-lock   # Fail if no lockfile exists
buildy --ignore-lock    # Ignore existing lockfile
```

### Format

```yaml
# buildy/dependencies.lock (auto-generated)
version: 1
generated: "2026-01-22T10:30:00Z"
platform: linux-x86_64

dependencies:
  imgui:
    git: "https://github.com/ocornut/imgui.git"
    ref: "v1.90.1"
    commit: "abc123def456..."
    fetched_at: "2026-01-20T15:00:00Z"
    
  stb:
    url: "https://github.com/nothings/stb/archive/master.tar.gz"
    checksum: "sha256:789xyz..."

system_warnings:
  - "zlib: using system library /usr/lib/libz.so.1"
```

### Reproducibility Summary

| Dependency Type | Reproducibility | Warning |
|-----------------|-----------------|---------|
| `fetch` with lockfile | Full | None |
| `fetch` without lockfile | Partial (ref may move) | Optional |
| `paths` | User-controlled | None |
| `system` | Platform-dependent | Once per build |

---

## 13. Complete Example

### Root: `buildy.yaml`

```yaml
project:
  name: game_engine
  version: "2.0.0"
  description: "Cross-platform game engine"

variables:
  import_env_vars:
    - name: THIRD_PARTY_ROOT
      default: "./third_party"
    - name: VULKAN_SDK
      default: ~

workspace:
  modules:
    common:
      - core
      - renderer
      - game
    linux:
      - platform/linux
    windows:
      - platform/windows

dependencies:
  file: buildy/dependencies.yaml

environment:
  toolchains:
    default: gcc-linux
    windows: msvc-windows
    macos: clang-macos
    
  compile:
    cpp_standard: "c++20"
    warnings: ["-Wall", "-Wextra"]
    
  configurations:
    debug:
      optimization: "-O0"
      defines: ["DEBUG=1"]
      flags: ["-g"]
    release:
      optimization: "-O3"
      defines: ["NDEBUG=1"]
      flags: ["-flto"]

artifacts:
  copy:
    - name: game_assets
      sources: ["assets/**"]
      destination: "${out_dir}/data"
      
  generate:
    - name: shaders
      tool: glslc
      inputs: ["shaders/*.vert", "shaders/*.frag"]
      outputs: "${out_dir}/shaders/${basename}.spv"
```

### Dependencies: `buildy/dependencies.yaml`

```yaml
system:
  common:
    - name: zlib
      pkg_config: zlib
  linux:
    - name: pthread
      libs: ["pthread"]
    - name: dl
      libs: ["dl"]
  windows:
    - name: winsock
      libs: ["ws2_32"]

paths:
  - name: vulkan_sdk
    path: "${VULKAN_SDK}"
    include_dirs: ["${path}/include"]
    lib_dirs: ["${path}/lib"]
    libs: ["vulkan"]

fetch:
  - name: imgui
    git: "https://github.com/ocornut/imgui.git"
    ref: "v1.90.1"
    type: source
    
  - name: stb
    git: "https://github.com/nothings/stb.git"
    ref: "master"
    type: header_only

# Packages: resolved from buildy/packages/*.yaml
packages:
  - glfw3
```

### Submodule: `core/buildy.yaml`

```yaml
project:
  name: engine_core

targets:
  static_libraries:
    - name: engine_core
      language: cpp
      sources: ["src/*.cpp"]
      public_headers: ["include/*.h"]
      include_dirs:
        public: ["include"]
      depends_on:
        deps: ["zlib", "stb"]
```

### Submodule: `renderer/buildy.yaml`

```yaml
project:
  name: engine_renderer

targets:
  static_libraries:
    - name: engine_renderer
      language: cpp
      sources: ["src/*.cpp"]
      include_dirs:
        public: ["include"]
      depends_on:
        targets: ["engine_core"]  # Build order - engine_core must be built first
        deps: ["vulkan_sdk", "imgui"]
      # Note: libs not needed for static library - consumers link transitively
```

### Submodule: `game/buildy.yaml`

```yaml
project:
  name: game

targets:
  executables:
    - name: game
      language: cpp
      sources: ["src/*.cpp"]
      include_dirs:
        private: ["../core/include", "../renderer/include"]
      
      # packages: External packages (adds include_dirs, lib_dirs, libs)
      packages:
        - glfw3
      
      # libs: Internal libraries (order matters for static libs)
      libs:
        - engine_renderer  # Depends on engine_core, so list first
        - engine_core      # Base library, list last
      
      # depends_on: Build ordering
      depends_on:
        targets:
          - engine_core
          - engine_renderer
```

---

## 14. CLI Reference

```bash
# Basic build (platform/architecture auto-detected from host)
buildy

# Specify configuration
buildy --config release
buildy --config debug

# Specify platform/architecture (auto-detected by default)
buildy --platform linux --arch x86_64
buildy --platform macos --arch arm64
buildy --platform windows --arch x86_64

# Override toolchain
buildy --toolchain clang-linux

# Define variables
buildy --define VERSION=1.2.3 --define BUILD_NUMBER=42

# Lockfile operations
buildy --update-lock       # Generate/update lockfile
buildy --require-lock      # Fail if no lockfile
buildy --ignore-lock       # Ignore lockfile

# Build specific targets
buildy --target engine_core
buildy --target game

# IDE integration
buildy --compile-commands  # Generate buildy/compile_commands.json for IDE tooling

# Other options
buildy --jobs 8            # Parallel jobs
buildy --verbose           # Verbose output
buildy --dry-run           # Show what would be built
buildy --clean             # Clean build outputs
buildy --force             # Ignore cache, rebuild all
```

### Platform Auto-Detection

By default, buildy auto-detects the host platform and architecture:

| Host OS | Detected Platform | Detected Architecture |
|---------|-------------------|----------------------|
| Linux | `linux` | `x86_64` or `arm64` |
| macOS | `macos` | `x86_64` or `arm64` |
| Windows | `windows` | `x86_64` or `x86` |

Use `--platform` and `--architecture` to override for cross-compilation.

### IDE Integration

The `--compile-commands` flag generates a `compile_commands.json` file in the `buildy/` directory. This file is used by IDEs and language servers (e.g., clangd, ccls) for code intelligence features like:

- Auto-completion
- Go to definition
- Find references
- Error highlighting

```bash
# Generate compile_commands.json
buildy --compile-commands

# Symlink to project root for IDE discovery (optional)
ln -s buildy/compile_commands.json compile_commands.json
```

---

## 13. Build Flow

```
1. Parse root buildy.yaml
2. Process import_env_vars, resolve all variables
3. For each module in workspace.modules (filtered by platform):
   a. Parse module's buildy.yaml
   b. Recursively process child modules
   c. Collect all targets and artifacts
4. Resolve dependencies:
   a. System: pkg-config or explicit paths
   b. Paths: Variable expansion
   c. Fetch: Download/cache, detect build system, build if needed
5. Check/generate lockfile (if requested)
6. Build unified dependency graph across all targets
7. Execute in parallel stages with content-addressable caching
8. Generate build report (JSON + terminal summary)
9. Warn once about system library reproducibility (at end)
```

## 16. Build Reports

After each build, buildy generates a comprehensive build report with statistics and timing information.

### Terminal Summary

A summary is always printed to the terminal:

```
================================================================================
                              BUILD REPORT
================================================================================
Status: SUCCESS
Duration: 2.345s (task gen: 0.123s, execution: 2.222s)

Platform: linux / x86_64
Configuration: debug
Toolchain: clang-linux

Tasks:
  Total:       24
  Cache hits:   18 (75.0%)
  Cache misses:  6
  Failed:        0

By Type:
  compile:  20 tasks
  link:      4 tasks
================================================================================
```

### JSON Report

A detailed JSON report is saved to `.buildy_cache/build_report.json`:

```json
{
  "start_time": "2025-01-22T10:30:00Z",
  "end_time": "2025-01-22T10:30:02Z",
  "total_duration_ms": 2345,
  "task_gen_duration_ms": 123,
  "exec_duration_ms": 2222,
  "success": true,
  "platform": "linux",
  "architecture": "x86_64",
  "configuration": "debug",
  "toolchain": "clang-linux",
  "cache_hits": 18,
  "cache_misses": 6,
  "failed_tasks": [],
  "tasks_by_type": {
    "compile": 20,
    "link": 4
  }
}
```

This report can be used for:
- CI/CD pipeline integration
- Build performance monitoring
- Debugging build issues
- Historical build analysis
