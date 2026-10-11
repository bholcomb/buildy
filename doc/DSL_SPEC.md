# Buildy DSL Specification

Version: 1.0.0

This document is the complete reference for all buildy configuration fields. For how-to guidance, see the [User Guide](USER_GUIDE.md). For step-by-step tutorials, see:

- [Basic Tutorial](TUTORIAL_BASIC.md) - Single-target project
- [Multi-Module Tutorial](TUTORIAL_MULTIMODULE.md) - Workspace with multiple modules
- [Advanced Tutorial](TUTORIAL_ADVANCED.md) - Fetch dependencies and Docker builds

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

### Platform and Architecture Names

| | Names | Aliases |
|---|---|---|
| Platform | `linux`, `windows`, `macos`, `wasm` | `darwin` is `macos` |
| Architecture | `x86_64`, `x86`, `arm64`, `arm`, `wasm32` | `amd64` is `x86_64`, `aarch64` is `arm64`, `i686` and `i386` are `x86` |

`--platform` and `--architecture`, filter keys, dependency sections and toolchain targets all use
these names, and an alias means its name everywhere (`--platform darwin` builds `macos`, and a
`darwin:` filter matches it). Any other name is an error.

---

## File Structure

A typical project layout:

```
project/
├── buildy.yaml                    # Root configuration
├── buildy_config/
│   ├── dependencies.yaml          # External dependencies (optional)
│   ├── dependencies.lock          # Lockfile (auto-generated)
│   └── dependencies/              # Per-dependency config files (optional)
│       ├── glfw3.yaml             # Dependency definition
│       └── glfw3_build.yaml       # Custom build config for dependency
├── src/
│   ├── core/
│   │   └── buildy.yaml            # Submodule
│   ├── renderer/
│   │   └── buildy.yaml            # Submodule
│   ├── audio/
│   │   └── buildy.yaml            # Submodule
│   ├── platform/
│   │   └── buildy.yaml            # Platform abstraction module
│   └── game/
│       └── buildy.yaml            # Main executable module
├── assets/
│   └── shaders/
│       └── buildy.yaml            # Shader compilation module
└── .buildy_cache/                 # Build cache (git-ignored)
    ├── cache_index.json           # Cache index: what each task last produced
    ├── deps/                      # Fetched dependency sources
    │   ├── glfw3/                 # Extracted dependency
    │   └── glfw3.extracted.json   # What glfw3/ was extracted from (URL, SHA256)
    ├── downloads/                 # Cached archive downloads, keyed by a hash of the URL
    │   ├── 3f2a…-glfw-3.4.zip           # Downloaded archive
    │   └── 3f2a…-glfw-3.4.zip.meta.json # Download metadata (SHA256, etc.)
    └── response_files/            # Compiler response files
```

---

## Top-Level Structure

```yaml
project:           # Metadata (required in the workspace's root config)
variables:         # User-defined variables (optional)
workspace:         # Multi-module coordination (optional)
dependencies:      # External dependencies (optional, root only): {file: ...}
environment:       # Build configuration (optional)
targets:           # What to build (optional)
artifacts:         # File operations (optional)
staging:           # Product assembly (optional)
install:           # Packaging for distribution (optional)
output:            # base_dir and pattern of the output directory (optional)
toolchain:         # Toolchain for the module (optional)
platforms:         # Per-platform settings and target overrides (optional)
architectures:     # Per-architecture settings (optional)
configurations:    # Per-configuration settings (optional)
```

### Strict Validation

Every config file (the workspace root, every module, a dependency's override config) is read by
one loader into typed models, and validated before anything is generated. A key buildy doesn't
read is an error, not something silently ignored, and so are a duplicate key, a value of the
wrong type and a missing required field. Each error names the file, the line and the key path,
with a suggestion for a likely typo:

```
FATAL: Failed to load the workspace: /src/game/buildy.yaml is not a valid buildy config:
  - line 9: unknown key 'targets.executables[0] (app).defnies' (did you mean 'defines'?)
```

Two kinds of name are checked once the toolchains are loaded, since a toolchain can define them:
[filter keys](#filtered-lists) and abstract keywords (`optimisation: full` is an error suggesting
`optimization`).

Required fields: `project.name` in the root config; `name` and `language` on every target;
`sources` on C and C++ targets; `path` on Go and Rust targets. The old `library:`, `executable:`
and `tasks:` sections were never built and are rejected. Dependency definitions in
`buildy_config/` are validated the same way.

Toolchain, template and build system files (built-in or in `buildy_config/`) are read the same
way, by one loader per kind: an unknown key, a duplicate key or a value of the wrong type is an
error naming the file, the line and the key path, and a built-in file that fails to load stops
buildy, never a warning:

```
FATAL: Failed to initialize toolchain manager: buildy_config/toolchains/mine.yaml is not a valid toolchain:
  - line 7: unknown key 'toolchain.tools.compile.input_extension' (did you mean 'input_extensions'?)
```

A string field takes a string: `version: 1.0` is an error (`'project.version' must be a string,
not a number (quote it: "1.0")`), since YAML reads it as the number 1.

Configuration errors found while generating tasks also stop the build, naming the module and the
item: an unknown toolchain (from `--toolchain`, `project.toolchain`, `environment.toolchains`, or an
artifact's `tool`), a transform or generate whose inputs match no files, a staging entry naming an
unknown target or artifact, and an install naming an unknown staging area.

---

## 1. Project Section

Required metadata about the project.

```yaml
project:
  name: game_engine           # Required (in the root config): project identifier
  version: "2.0.0"            # Optional: semantic version
  description: "My project"   # Optional: human-readable description
  toolchain: gcc-cpp-linux    # Optional: toolchain for the module
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
3. Configuration-specific variables (`configurations.debug.variables`, `environment.configurations.debug.variables`)
4. Architecture-specific variables (`architectures.x86_64.variables`)
5. Platform-specific variables (`variables.platforms.linux`, `platforms.linux.variables`)
6. Project-level variables: the module's `variables`
7. Workspace-level variables: the root config's `variables`
8. Built-in variables

The root config's variables, imports and platform variables reach every module (a module's own
override them), and the dependency definitions see the root's. A `${name}` that resolves to
nothing is an error naming where it is used.

### Built-in Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `${platform}` | Target platform | `linux`, `windows`, `macos` |
| `${arch}` | Target architecture | `x86_64`, `arm64` |
| `${config}` | Build configuration | `debug`, `release` |
| `${workspace}` | Workspace root directory | `/home/user/myproject` |
| `${project_name}` | From `project.name` | `game_engine` |
| `${project_version}` | From `project.version` | `2.0.0` |
| `${out_dir}` | Output directory (also `${output_dir}`) | `build/linux-x86_64-debug` |
| `${cache_dir}` | Cache directory | `.buildy_cache` |
| `${gen_dir}` | Generated files directory | `build/.../gen` |

The `${workspace}` variable is particularly useful in modules for referencing paths relative to the workspace root rather than the module directory.

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
    - core                      # Always built (all platforms)
    - renderer                  # Always built
    - audio                     # Always built
    - platform                  # Has its own child modules
    - linux:                    # Linux-only modules
        - tools/linux_profiler
    - windows:                  # Windows-only modules
        - tools/windows_debugger
    - macos:                    # macOS-only modules
        - tools/xcode_integration
```

This uses the same filtered list syntax as other arrays (sources, libs, etc.). Plain strings are always included; map entries with platform/architecture/configuration keys are filtered against the build's `--platform`, `--architecture` and `--config`. The older map form also works, at every level, with `common:` always included:

```yaml
workspace:
  modules:
    common: [core, renderer]
    linux: [tools/linux_profiler]
```

### Intermediate Module (with children)

A module can both define targets AND have child modules:

```yaml
# platform/buildy.yaml
project:
  name: platform_layer

workspace:
  modules:
    - linux:                # Linux-only child module
        - linux             # platform/linux/buildy.yaml
    - windows:              # Windows-only child module
        - windows           # platform/windows/buildy.yaml

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
      libs: ["platform_common"]  # Links and ensures build order
```

### Custom Config Filename

```yaml
workspace:
  modules:
    - path: vendor/imgui
      config: imgui_build.yaml    # Custom filename
    - core                        # Uses default buildy.yaml
```

### Module Resolution Rules

1. **Default filename:** `buildy.yaml`
2. **Paths are relative** to the current buildy.yaml's directory
3. **Each listed path must contain the config file** (error if missing)
4. **Filtering at each level** - root and child modules are selected the same way, by platform, architecture and configuration
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
  file: buildy_config/dependencies.yaml
```

```yaml
# buildy_config/dependencies.yaml
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

### Example Dependencies File

Here's a complete example showing different dependency types:

```yaml
# buildy_config/dependencies.yaml

# System dependency with pkg-config (Linux/macOS)
zlib:
  description: "zlib compression library"
  linux:
    pkg_config: zlib
  macos:
    pkg_config: zlib
  windows:
    root: "${THIRD_PARTY_ROOT}/zlib"
    include_dirs: ["${root}/include"]
    lib_dirs: ["${root}/lib"]
    libs: ["zlibstatic"]

# Platform-specific system libraries
pthread:
  linux:
    libs: ["pthread"]

winsock:
  windows:
    libs: ["ws2_32", "wsock32"]

cocoa:
  macos:
    frameworks: ["Cocoa", "IOKit"]

# Pre-built library with root path
glfw:
  description: "GLFW window library"
  version: "3.4"
  common:
    include_dirs: ["${root}/include"]
    lib_dirs: ["${root}/lib"]
  linux:
    pkg_config: glfw3
  windows:
    root: "${THIRD_PARTY_ROOT}/glfw/${version}"
    libs: ["glfw3"]
  macos:
    root: "/opt/homebrew"
    libs: ["glfw"]
    frameworks: ["Cocoa", "IOKit", "CoreVideo"]

# Fetched dependency with cmake build
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

# Header-only library
stb:
  description: "STB single-header libraries"
  common:
    url: "https://github.com/nothings/stb/archive/master.tar.gz"
    checksum: "sha256:..."
    include_dirs: ["${dep_dir}"]

# Fetched dependency built with custom buildy config
glfw3:
  linux:
    url: "https://github.com/glfw/glfw/releases/download/3.4/glfw-3.4.zip"
    build:
      system: buildy
      override: glfw3_build.yaml   # Config file in buildy_config/dependencies/
    include_dirs: ["${dep_dir}/include"]
    lib_dirs: ["${dep_dir}/build/linux-x86_64-${config}/lib"]
    libs: ["glfw3"]
```

**Note:** When `build.system: buildy` is used:
- Buildy automatically builds **both debug and release** configurations
- The `${config}` variable in `lib_dirs` resolves to the current build configuration
- The override config is invoked in **standalone mode** (no dependency resolution, no module discovery)

### Dependency Types

Dependency types are **inferred** from the fields present in the configuration:

| Inferred Type | Fields Present | Description |
|---------------|----------------|-------------|
| `fetch` | `git:` or `url:` | Downloaded from Git repository or URL |
| `system` | `pkg_config:` or `root:` | System libraries via pkg-config or explicit paths |
| `system` | Only `include_dirs:`, `libs:`, etc. | Explicit path-based dependencies |

### Dependency Configuration Reference

Dependencies are defined in `buildy_config/dependencies.yaml` or organized into separate files in `buildy_config/dependencies/`. Each top-level key is a dependency name.

**Fetched dependencies** are stored locally:
- **Source**: `.buildy_cache/deps/<name>/`
- **Build**: `.buildy_cache/deps/<name>/_build/`
- **Install**: `.buildy_cache/deps/<name>/_install/`
- **Downloads**: `.buildy_cache/downloads/` (cached archives for URL dependencies)

The `${dep_dir}` variable refers to the fetched source directory.

**Download caching and validation:**

When fetching URL dependencies, buildy uses a robust caching and validation process:

1. **Temp file downloads** - Archives are downloaded to a temporary file first, then moved to the cache only after validation passes
2. **Checksum verification** - If a `checksum` is provided, the archive is verified against it
3. **Archive integrity** - The archive is opened and verified to be readable (not corrupted/truncated)
4. **Cache key** - The cached file is named by a hash of the whole URL plus its basename
   (`<hash>-glfw-3.4.zip`), so two URLs ending in the same file name never share a download
5. **Metadata storage** - A `.meta.json` file is stored alongside each cached archive containing:
   - Original URL
   - SHA256 hash (computed from the downloaded file)
   - File size
   - Download timestamp

Example metadata file (`.buildy_cache/downloads/<hash>-glfw-3.4.zip.meta.json`):
```json
{
  "url": "https://github.com/glfw/glfw/releases/download/3.4/glfw-3.4.zip",
  "sha256": "b5ec004b2712fd08e8861dc271428f048775200a2df719ccf575143ba749a3e9",
  "size": 1653725,
  "downloaded_at": "2026-02-24T12:40:57Z",
  "filename": "glfw-3.4.zip"
}
```

The stored SHA256 hash is used for:
- **Re-extraction validation** - If the extracted directory is deleted but the archive remains, the archive is verified against its stored hash before re-extracting
- **Lockfile generation** - The hash is included in `dependencies.lock` for reproducible builds

**Safe extraction:** an archive is extracted into a new temporary directory next to `deps/<name>/`
and renamed into place only when every entry was written, so an interrupted extraction is never
mistaken for a complete one. The extracted directory is reused only while `deps/<name>.extracted.json`
names the same URL and archive SHA256. Every entry is confined to the extraction directory: names
that are absolute or contain `..`, and symlinks or hard links pointing outside it, fail the fetch
with an error naming the entry. Setuid, setgid and sticky bits are dropped. If every entry sits under
one top-level directory (`glfw-3.4/`), that directory is stripped.

**Warning:** If no `checksum` is provided in the dependency configuration, buildy will log a warning encouraging you to add one for reproducible builds.

**Complete dependency schema:**

```yaml
# buildy_config/dependencies.yaml
glfw:
  description: "GLFW - OpenGL/Vulkan window library"
  version: "3.4"
  
  common:
    defines: ["GLFW_INCLUDE_VULKAN"]
    include_dirs: ["${root}/include"]
    lib_dirs: ["${root}/lib"]
  
  linux:
    pkg_config: glfw3
    defines: ["GLFW_EXPOSE_NATIVE_X11"]
  
  windows:
    url: "https://github.com/glfw/glfw/releases/download/3.4/glfw-3.4.zip"
    build:
      system: buildy
      override: glfw.buildy.yaml
    include_dirs: ["${dep_dir}/_install/include"]
    lib_dirs: ["${dep_dir}/_install/lib"]
    libs: ["glfw3"]
    defines: ["GLFW_EXPOSE_NATIVE_WIN32"]
  
  macos:
    root: "/opt/homebrew"
    libs: ["glfw"]
    frameworks: ["Cocoa", "IOKit", "CoreVideo"]
```

**Field Reference:**

| Field | Description | Notes |
|-------|-------------|-------|
| `description` | Human-readable description | Optional |
| `version` | Version string | Available as `${version}` |
| `git` | Git repository URL | Infers `fetch` type |
| `url` | Archive URL (.tar.gz, .zip, etc.) | Infers `fetch` type |
| `ref` | Git ref (tag, branch, commit) | For `git:` dependencies |
| `checksum` | SHA256 checksum for URL downloads | Format: `sha256:<hash>`. Recommended for reproducibility |
| `pkg_config` | pkg-config package name | Infers `system` type |
| `root` | Base path for pre-built library | Available as `${root}` |
| `include_dirs` | Include directories | Can use `${root}`, `${dep_dir}` |
| `lib_dirs` | Library directories | Can use `${root}`, `${dep_dir}` |
| `libs` | Libraries to link | e.g., `["glfw3", "pthread"]` |
| `frameworks` | macOS frameworks | e.g., `["Cocoa", "IOKit"]` |
| `defines` | Preprocessor definitions | e.g., `["DEBUG=1"]` |
| `build` | Build configuration (see below) | For fetch dependencies |
| `execution` | Docker execution config | For fetch dependencies |

**Build Section:**

```yaml
build:
  system: cmake         # cmake, meson, make, autoconf, buildy, none
  override: custom.yaml # Custom build file (for buildy system)
  args:                 # Arguments passed to build system
    - "-DFOO=ON"
    - "-DBAR=OFF"
```

**Platform Sections:**

Dependencies support platform-specific configuration via sections. Sections are merged in order: fields written at the top level (next to the sections) → `common` → `platform` → `platform-arch`. A later section's single values (and `build` and `execution`, whole) replace earlier ones; lists append.

- `common` - All platforms
- `linux`, `windows`, `macos` - Platform-specific
- `linux-x86_64`, `linux-arm64`, etc. - Platform-architecture combinations

Section names use the [platform and architecture names](#platform-and-architecture-names), aliases
included (`darwin`, `linux-aarch64`). Any other key that isn't a field is an error. There is no
`file:` key: every file in `buildy_config/dependencies/` is loaded, so an index of them is
neither needed nor accepted.

**Built-in Variables for Dependencies:**

| Variable | Description |
|----------|-------------|
| `${name}` | Dependency name |
| `${version}` | Dependency version |
| `${platform}` | Current platform (linux, windows, macos) |
| `${arch}` | Current architecture (x86_64, arm64) |
| `${config}` | Build configuration (debug, release) |
| `${toolchain}` | Current toolchain name |
| `${root}` | Value of `root:` field (for system deps) |
| `${dep_dir}` | Fetched source directory (for fetch deps) |
| `${workspace_root}` | Root directory of the workspace |

Environment variables are referenced with uppercase names: `${VULKAN_SDK}`, `${THIRD_PARTY_ROOT}`

**Using Dependencies in Targets:**

```yaml
targets:
  executables:
    - name: my_game
      sources: ["src/*.cpp"]
      deps:              # External dependencies
        - glfw
        - vulkan
      libs:              # Internal project libraries
        - engine_core
```

**Error Handling:**

Buildy follows an "error immediately" philosophy for dependencies:
- If `pkg_config:` is specified but pkg-config is unavailable, buildy errors
- If `root:` path doesn't exist, buildy errors
- No warnings or fallbacks - explicit configuration is required

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
    warnings: everything
    
  link:
    flags: ["-Wl,-rpath,'$ORIGIN'"]       # Extra linker flags for all targets
    remove_flags: ["-s"]                   # Remove specific linker flags
    
  configurations:
    debug:
      optimization: none
      symbols: true
      defines: ["DEBUG=1"]
    release:
      optimization: full
      defines: ["NDEBUG=1"]
      compile:
        flags: ["-flto", "-march=native"]
      link:
        flags: ["-flto", "-Wl,-O1"]
```

A settings level (`environment.compile`, `config`, a configuration, a platform or an architecture
section) takes `cpp_standard`, `c_standard`, `output_prefix`, `defines`, `flags`, `remove_flags`,
`remove_defines`, and abstract keywords: any key some toolchain's `flag_mappings` defines
(`optimization`, `warnings`, `symbols`, `runtime`, or a custom one). A configuration, platform or
architecture section also takes `compile:`, `link:` and `variables:`. Any other key is an error.

### How Settings Merge

A module's settings are the workspace root's merged with its own, by three rules:

- **Mappings merge key by key.** A key both set merges its two values by these rules.
- **Lists append**: the root's items, then the module's.
- **Scalars override**: a value the module sets replaces the root's (`output_prefix: ""` counts as
  set).

So a module that adds `environment.compile.defines: [X]` keeps the root's `c_standard`, flags and
defines, and gets `X` after them. What merges: `variables`, `environment` (`toolchains`, `compile`,
`link`, `configurations`), `config`, and the settings of `platforms`, `architectures` and
`configurations`. A module's targets, artifacts, staging, install, output, project and toolchain
are its own.

The levels then merge in this order, by the same rules: `config`, `environment.compile` (and
`environment.link`), the build's platform section, its architecture section, then its
configuration (`configurations.<config>`, then `environment.configurations.<config>`). Each level's
`compile:` section merges into the level first. The abstract keywords' flags come first in the
result, and every `remove_flags` and `remove_defines` applies last.

A platform section's `targets` override the module's targets of the same name and type: each field
the override sets replaces the target's, except `compile` and `link`, which merge into the target's
by the rules above. An override of a target the module doesn't have is an error.

Merging never changes the loaded configuration: a module's settings are a new value.

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
      include_dirs: ["include", "src"]
      
      # deps: External dependencies (add include_dirs, lib_dirs, libs, defines)
      deps: ["glfw3", "zlib"]
      
      compile:
        standard: "c++20"
        defines: ["ENGINE_EXPORTS"]
        flags: ["-ffast-math"]
      
      link:
        flags: ["-Wl,--version-script=engine.map"]
        remove_flags: []
```

### Shared Libraries

```yaml
targets:
  shared_libraries:
    - name: engine_renderer
      language: cpp           # Required: c | cpp | rust | go
      sources: ["src/*.cpp"]
      include_dirs: ["include"]
      
      # libs: Libraries this shared library links against (also establishes build order)
      libs: ["engine_core"]
      
      # output_prefix: Override the library filename prefix (default: "lib" on Linux/macOS, "" on Windows)
      # output_prefix: ""    # Produces engine_renderer.so instead of libengine_renderer.so
```

#### Output Prefix

The `output_prefix` field controls the filename prefix for shared and static library outputs. The default value is defined by the toolchain (e.g., `"lib"` for GCC/Clang on Linux/macOS, `""` for MSVC on Windows).

The resolution order for `output_prefix` is:
1. **Target-level** — `output_prefix` on the target definition (highest priority)
2. **Environment/config-level** — `output_prefix` in the merged configuration (inheritable)
3. **Toolchain default** — `output_prefix` from the toolchain YAML

To override the default prefix, set `output_prefix` on the target or at the environment level:

```yaml
# Per-target override
targets:
  shared_libraries:
    - name: myplugin
      language: cpp
      sources: ["src/*.cpp"]
      output_prefix: ""       # Produces myplugin.so instead of libmyplugin.so
```

```yaml
# Environment-level (applies to all library targets)
environment:
  compile:
    output_prefix: ""         # No lib prefix for any library in this project
```

| Platform | Toolchain default | Example output |
|----------|-------------------|----------------|
| Linux    | `lib`             | `libfoo.so`, `libfoo.a` |
| macOS    | `lib`             | `libfoo.dylib`, `libfoo.a` |
| Windows  | `""`              | `foo.dll`, `foo.lib` |

Custom toolchains can define their own default by setting `output_prefix` at the toolchain level:

```yaml
toolchain:
  name: "my-custom-toolchain"
  output_prefix: "my_"       # Libraries will be named my_foo.so
  tools:
    ...
```

#### Compile and Link Sections

The `compile:` and `link:` sections provide separate control over compiler and linker flags. Both support `flags` and `remove_flags` sub-keys, and are available at four levels: environment, configuration, platform, and target.

**Environment-level** (applies to all targets):

```yaml
environment:
  compile:
    flags: ["-fvisibility=hidden"]
    # Also supports: cpp_standard, c_standard, warnings, defines, remove_flags
  link:
    flags: ["-Wl,-z,now"]
    remove_flags: ["-s"]
```

**Configuration-level** (per debug/release):

```yaml
environment:
  configurations:
    release:
      optimization: full
      compile:
        flags: ["-flto", "-march=native"]
        remove_flags: ["-Wextra"]       # Remove a flag inherited from environment
      link:
        flags: ["-flto", "-Wl,-O1"]
```

**Target-level** (per-target overrides):

```yaml
targets:
  shared_libraries:
    - name: mylib
      language: cpp
      sources: ["src/*.cpp"]
      compile:
        flags: ["-ffast-math"]
        defines: ["ENGINE_EXPORTS"]
        remove_flags: ["-Werror"]       # This target has warnings we can't fix
      link:
        flags: ["-Wl,--version-script=mylib.map"]
```

The resolution order for both compile and link flags is:
1. **Toolchain** — common and configuration-specific tool flags
2. **Environment-level** — `environment.compile/link.flags` (merged in, `remove_flags` applied)
3. **Configuration-level** — `configurations.<name>.compile/link.flags` (appended, `remove_flags` applied)
4. **Target-level** — `target.compile/link.flags` (appended, `remove_flags` applied)

Compile flags and link flags are completely separate — a flag in `compile.flags` never reaches the linker, and a flag in `link.flags` never reaches the compiler.

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

Either form can sit under a filter key: `- linux: [{pattern: "src/linux/*.c", optional: true}]`.

### Filtered Lists

Many list fields support **filtering** based on four dimensions: **platform**, **architecture**, **configuration**, and **toolchain**. Filtering allows conditional inclusion of items without duplicating entire target definitions.

**Filterable fields:** `sources`, `defines`, `libs`, `include_dirs`, `flags`

**Syntax:**

In a filterable list, items can be:
- **Plain strings** - always included
- **Filter maps** - included only when the filter matches the current build context

```yaml
sources:
  - "src/common.c"        # Always included (plain string)
  - linux:                # Map: included when platform=linux
      - "src/linux.c"
  - windows:              # Map: included when platform=windows
      - "src/windows.c"
```

**Filter Dimensions:**

| Dimension | Examples | Matches |
|-----------|----------|---------|
| Platform | `linux`, `windows`, `macos` | Build target OS |
| Architecture | `x86_64`, `arm64`, `i686` | Target CPU architecture |
| Configuration | `debug`, `release`, or custom | Build configuration name |
| Toolchain | `gcc-cpp-linux`, `clang-cpp-macos` | Active toolchain name |

**Full example with multiple filter dimensions:**

```yaml
targets:
  static_libraries:
    - name: mylib
      language: c
      
      sources:
        # Common sources (always included)
        - "src/core.c"
        - "src/util.c"
        
        # Platform-specific sources
        - linux:
            - "src/posix_io.c"
            - "src/linux_time.c"
        - windows:
            - "src/win32_io.c"
            - "src/win32_time.c"
        - macos:
            - "src/posix_io.c"
            - "src/darwin_time.c"
        
        # Architecture-specific sources
        - x86_64:
            - "src/simd_x64.c"
        - arm64:
            - "src/simd_arm.c"

      defines:
        # Platform defines
        - linux:
            - "_POSIX_C_SOURCE=200809L"
        - windows:
            - "_CRT_SECURE_NO_WARNINGS"
            - "UNICODE"
        
        # Configuration defines
        - debug:
            - "_DEBUG"
            - "ENABLE_ASSERTS"
        - release:
            - "NDEBUG"
      
      flags:
        - debug:
            - "-fsanitize=address"
        - release:
            - "-flto"
```

**Filter evaluation order:**

Filters are evaluated in the order they appear in the YAML array. All matching filters contribute their items to the final list. The same filter key can appear multiple times. Within one map item with several keys, the keys that match contribute in the order they are written, so `{linux: [-DA], debug: [-DB]}` gives `-DA -DB` on every run.

**Filter keys are checked.** A key must be a [platform or architecture](#platform-and-architecture-names) (aliases included), a configuration (`debug`, `release`, or one the workspace declares under `configurations` or `environment.configurations`), or a toolchain name. Anything else is an error, with a suggestion:

```
buildy.yaml:12: filter key 'linxu' in 'targets.executables[0] (app).defines[1]' is not a platform, architecture, configuration or toolchain (did you mean 'linux'?)
```

The same filtered-list form works for `sources`, `defines`, `libs`, `include_dirs` and `flags` of a target, the `flags`, `defines`, `remove_flags` and `remove_defines` of every settings level and its `compile:` and `link:` sections, staging `files`, and `workspace.modules`.

### Executables

```yaml
targets:
  executables:
    - name: game
      language: cpp           # Required: c | cpp | rust | go
      sources: ["src/*.cpp"]
      
      # deps: External dependencies (add include_dirs, lib_dirs, libs, defines)
      deps:
        - glfw3
        - opengl
        - zlib
      
      # libs: Internal libraries to link against (also establishes build order)
      # Order matters for static libraries (dependents before dependencies)
      libs:
        - engine_renderer    # List first if it depends on engine_core
        - engine_core        # List last (base library)
      
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

The binary is built to `build/<platform>-<arch>-<config>/cargo/<profile>/myapp` (`debug`, or
`release` for configurations whose flags pass `--release`). Go and Rust builds run their own tool on
every build; see [Tool-Cached Builds](#tool-cached-builds-go-and-rust).

### deps vs libs vs depends_on

These are **intentionally separate** concepts:

| Field | Purpose | Example |
|-------|---------|---------|
| `deps` | External dependencies (provide include paths, lib paths, libs, defines) | `[glfw3, zlib]` |
| `libs` | Internal project libraries to link against (implies build order) | `[engine_core, engine_renderer]` |
| `depends_on.targets` | Build ordering only (no linking) - for code generators, tools, etc. | `[proto_gen, asset_compiler]` |
| `depends_on.artifacts` | Build ordering + virtual files for globs | Code generators (protobuf, etc.) |

Target names are unique across the workspace: `libs` and `depends_on.targets` refer to targets by
name, and every module's executables and libraries share the output directory's `bin/` and `lib/`.
Two targets with the same name, in one module or in two, are an error naming both modules. A
`depends_on.targets` name that is not a target is an error; a `libs` name that is not a target is
a system library.

A link's inputs include every library it links that the build produces (`libcalc.a`, a shared
library and its import library) and every `deps:` library found in the dependency's `lib_dirs`, so
a changed library relinks whatever links it. System libraries are not hashed.

**Why separate?**

1. **deps are external**: They provide platform-specific settings (includes, libs, defines) from `buildy_config/dependencies.yaml`
2. **libs are internal**: Project libraries you've built - automatically establishes both **linking** and **build order**
3. **depends_on.targets is ordering only**: For non-linking dependencies like code generators or tools that must run first
4. **Explicit is better**: Each field has one clear purpose

**Example: Using packages and libs together**

```yaml
targets:
  executables:
    - name: my_game
      sources: ["src/*.cpp"]
      deps:                # External (adds glfw's include_dirs, lib_dirs, libs)
        - glfw3
      libs:                # Internal (links AND ensures build order)
        - engine_renderer
        - engine_core
      # No depends_on.targets needed for libs - build order is automatic
```

**Example: Depending on a code generator artifact**

For code generation with protobuf or similar tools, use `depends_on.artifacts`. Buildy automatically makes artifact outputs available for glob matching:

```yaml
artifacts:
  generate:
    - name: protos
      tool: protoc
      inputs: "proto/*.proto"
      outputs: ["${gen_dir}/${basename}.pb.h", "${gen_dir}/${basename}.pb.cc"]
      args: ["--cpp_out=${gen_dir}"]

targets:
  executables:
    - name: my_app
      language: cpp
      sources:
        - "src/*.cpp"
        - "${gen_dir}/*.pb.cc"    # Globs can match artifact outputs
      depends_on:
        artifacts: ["protos"]      # Ensures protoc runs first + enables glob matching
```

When a target declares `depends_on.artifacts`:
1. The artifact tasks are added as build dependencies of every task of the target, compiles
   included (artifact runs first)
2. The artifact's `outputs` are registered as "virtual files"
3. Source globs in the target can match these virtual files even though they don't exist yet

**Note:** Artifact dependencies work within a single module. For multi-module projects, the artifact and dependent target should be in the same module.

---

## 7. Artifacts Section

File operations: copy, transform, generate. Use `artifacts` in modules for post-build deployment tasks (like copying native libraries for interop). Use `staging` at the workspace level for assembling distribution packages.

### Copy

Copy files or build target outputs to a destination. Supports both explicit source paths and target references.

**Copy build target output:**

```yaml
artifacts:
  copy:
    # Copy a build target's output to a specific location
    - target: my_native_lib           # Reference target by name
      dest: "${workspace}/dotnet/runtimes/linux-x64/native"
```

**Copy files with glob patterns:**

```yaml
artifacts:
  copy:
    - name: game_assets
      sources: ["assets/**/*.png", "assets/**/*.wav"]
      destination: "${out_dir}/data"
      preserve_structure: true
      incremental: true
```

**Copy with explicit source:**

```yaml
artifacts:
  copy:
    - source: "${out_dir}/lib/libfoo.so"
      dest: "${workspace}/external/lib"
      depends_on: [foo]               # Ensure target builds first
```

| Field | Description |
|-------|-------------|
| `target` | Reference a build target by name - source path is resolved automatically |
| `source` | Explicit source file path (use if not referencing a target) |
| `dest` | Destination path - if directory, filename is appended from source |
| `depends_on` | Build targets that must complete first (auto-added for `target:`) |

**Path resolution:** Relative paths in `dest` are resolved relative to the module directory. Use `${workspace}` to reference the workspace root.

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

A `template` generate is filled in by buildy itself: each variable, in the order written,
replaces every `@NAME@` and `${NAME}` in the file with its value as is (quotes, slashes, `&` and
spaces included). The output keeps the template's permission bits. A `tool` generate runs the
tool once per input file.

Copies and staging symlinks are native too: `cp`, `ln` and `copy` are never run.

---

## 8. Staging Section

The staging section defines how to assemble build outputs into a product directory structure for distribution. **Staging is typically defined at the workspace root level** to create a complete product layout.

**Staging vs Artifacts.copy:**
- Use **staging** (workspace root) for assembling a complete distribution package
- Use **artifacts.copy** (in modules) for post-build deployment tasks like copying native libraries for interop

Staging names targets and artifacts of any module: target and artifact names are unique across
the workspace. A name that is neither is an error.

Staging is useful for:
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

| Format | Extension |
|--------|-----------|
| `tar.gz` (or `tgz`) | .tar.gz |
| `zip` | .zip |
| `tar` | .tar |

Buildy writes the archive itself, the same way on every platform: no `tar`, `zip` or PowerShell
is needed. Entries are the staging area's paths with forward slashes and no `./` prefix,
in name order, each directory before its contents. Every entry has the modification time
2000-01-01 00:00:00 UTC and owner 0, and keeps its permission bits, so the same staging area
always gives the same archive. With `follow_symlinks: true` (the default) a symlink is stored as
the file or directory it points to; with `false`, as a link.

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

Toolchains define how to compile, link, and transform files. They specify the actual commands and flags for a specific compiler/platform combination.

### Templates vs Toolchains

Buildy separates **what** to build from **how** to build:

| Concept | Purpose | Example |
|---------|---------|---------|
| **Templates** | Define build workflow (steps, inputs, outputs) | "Compile each source, then link them" |
| **Toolchains** | Define actual commands and flags | "Use `gcc -c` for compiling" |

Templates are language-agnostic recipes. Toolchains are platform-specific implementations.

### Search Order

1. `./buildy_config/toolchains/*.yaml` - Project-specific
2. `--add-data-dir <dir>`'s `toolchains/*.yaml`
3. Built-in (embedded in binary)

A toolchain replaces one of the same name lower in the list. Two files in one directory with the
same toolchain name are an error.

### Built-in Toolchains

| Toolchain | Platform | Language | Description |
|-----------|----------|----------|-------------|
| `gcc-c-linux` | Linux | C | GCC C compiler |
| `gcc-cpp-linux` | Linux | C++ | GCC C++ compiler |
| `clang-c-linux` | Linux | C | Clang C compiler |
| `clang-cpp-linux` | Linux | C++ | Clang C++ compiler |
| `clang-c-macos` | macOS | C | Apple Clang C |
| `clang-cpp-macos` | macOS | C++ | Apple Clang C++ |
| `clang-c-macos-arm64` | macOS ARM | C | Apple Clang C for Apple Silicon |
| `clang-cpp-macos-arm64` | macOS ARM | C++ | Apple Clang C++ for Apple Silicon |
| `msvc-c-windows` | Windows | C | MSVC C compiler |
| `msvc-cpp-windows` | Windows | C++ | MSVC C++ compiler |
| `gcc-c-mingw` | Linux→Windows | C | MinGW cross-compiler |
| `gcc-cpp-mingw` | Linux→Windows | C++ | MinGW cross-compiler |
| `go-linux` | Linux | Go | Go compiler |
| `go-macos` | macOS | Go | Go compiler |
| `go-windows` | Windows | Go | Go compiler |
| `rust-linux` | Linux | Rust | Cargo/rustc |
| `rust-macos` | macOS | Rust | Cargo/rustc |
| `rust-macos-arm64` | macOS ARM | Rust | Cargo/rustc for Apple Silicon |
| `rust-windows` | Windows | Rust | Cargo/rustc |
| `rust-wasm` | Any | Rust | Rust to WebAssembly |
| `emscripten-c` | Any | C | C to WebAssembly |
| `emscripten-cpp` | Any | C++ | C++ to WebAssembly |
| `glslc` | Any | GLSL | GLSL to SPIR-V shader compiler |

### Toolchain Selection

```yaml
environment:
  toolchains:
    default: gcc-cpp-linux
    linux: clang-cpp-linux
    windows: msvc-cpp-windows
```

CLI override: `buildy --toolchain clang-cpp-linux`

### Toolchain File Structure

A toolchain file defines a complete compiler/build tool configuration:

```yaml
toolchain:
  name: "gcc-cpp-linux"
  description: "GCC C++ compiler for Linux"
  language: cpp

  target:
    platform: linux           # Target platform (linux, windows, macos, any)
    architecture: x86_64      # Target architecture (x86_64, arm64, any)

  host:
    platform: linux           # Host platform
    architecture: x86_64      # Host architecture

  variables:                  # Toolchain-specific variables
    CC: "gcc"
    CXX: "g++"

  execution:
    type: native              # native or docker

  tools:
    cpp_compile:
      action: compile
      command: "..."
    link_executable:
      action: link
      command: "..."
```

| Field | Description |
|-------|-------------|
| `name` | Toolchain identifier (required) |
| `description` | Human-readable description |
| `language` | Primary language (c, cpp, go, rust) |
| `target` | Target platform and architecture (a [platform name](#platform-and-architecture-names) or `any`) |
| `host` | Host platform requirements |
| `variables` | Toolchain-specific variables |
| `output_prefix` | Default library filename prefix (e.g., `"lib"` or `""`) |
| `execution` | Execution configuration (native, the default, or docker) |
| `flag_mappings` | Abstract keywords: keyword -> value -> flags |
| `tools` | Map of tool definitions, in the order written |

A toolchain is unambiguous, or it fails to load: at most one tool per action and input extension,
one link tool per `supports.output_type` (which every link tool declares), and one `build` tool.

### Unified Tool Model

All tools use the same format. The `action` field distinguishes behavior:

| Action | Description | Example |
|--------|-------------|---------|
| `compile` | Source to object file | gcc, clang, msvc |
| `link` | Objects to binary | ld, link.exe |
| `transform` | File conversion | texture compressor, mesh converter |
| `convert` | Asset conversion | texconv, imagemagick |
| `generate` | Create files | protoc, code generators |
| `build` | Single-step module build | Go, Rust/Cargo |

### Tool Definition Fields

```yaml
tools:
  cpp_compile:
    action: compile
    command: "g++ ${flags} ${defines} ${includes} -c ${input} -o ${output}"
    input_extensions: [".cpp", ".cc", ".cxx"]
    output_extension: ".o"
    output_pattern: "${name}.o"
    flags:
      common: []
      debug: []
      release: []
    supports:
      defines: true
      define_flag: "-D"
      includes: true
      include_flag: "-I"
      pic: true
      pic_flag: "-fPIC"
      std: true
      dependencies: "-MMD -MP -MF ${dep_file}"
```

| Field | Description |
|-------|-------------|
| `action` | Tool action type (compile, link, transform, convert, generate, build) (required) |
| `command` | Command template with variable substitution, or one per platform (required) |
| `input_extensions` | File extensions this tool accepts |
| `output_extension` | Extension for output files |
| `output_pattern` | Pattern for output filename (supports `${name}`, `${output_prefix}`) |
| `flags` | Configuration-specific flags (common, debug, release) |
| `supports` | Capability flags and flag formats |
| `command_params` | Data-driven parameter definitions (for `build` action) |

### Tool Supports Fields

The `supports` section declares tool capabilities and flag formats:

**Compilation supports:**

| Field | Type | Description |
|-------|------|-------------|
| `defines` | bool | Supports preprocessor definitions |
| `define_flag` | string | Flag prefix for defines (e.g., `-D`) |
| `includes` | bool | Supports include directories |
| `include_flag` | string | Flag prefix for includes (e.g., `-I`) |
| `pic` | bool | Supports position-independent code |
| `pic_flag` | string | Flag for PIC (e.g., `-fPIC`) |
| `std` | bool | Supports language standard flag |
| `dependencies` | string | Dependency tracking flags (e.g., `-MMD -MP -MF ${dep_file}`) |

**Linking supports:**

| Field | Type | Description |
|-------|------|-------------|
| `output_type` | string | `executable`, `shared_library`, or `static_library` |
| `lib_dirs` | bool | Supports library directories |
| `lib_dir_flag` | string | Flag prefix for lib dirs (e.g., `-L`) |
| `libs` | bool | Supports library linking |
| `lib_flag` | string | Flag prefix for libs (e.g., `-l`) |
| `frameworks` | bool | Supports macOS frameworks |
| `framework_flag` | string | Flag for frameworks (e.g., `-framework`) |

**Go/Rust supports** (descriptive: the tool's `command_params` do the work, buildy doesn't read
these):

| Field | Type | Description |
|-------|------|-------------|
| `build_tags` | bool | Supports build tags |
| `build_tags_flag` | string | Flag for build tags (e.g., `-tags`) |
| `ldflags` | bool | Supports linker flags |
| `ldflags_flag` | string | Flag for ldflags |
| `features` | bool | Supports cargo features |
| `features_flag` | string | Flag for features |
| `buildmode` | bool | Supports Go buildmode |
| `buildmode_flag` | string | Flag for buildmode |

### Variable Syntax

All variables in toolchain commands use the `${var}` syntax:

```yaml
command: "gcc -c ${input} -o ${output} ${flags} ${defines}"
```

### Data-Driven Command Parameters

For tools with `action: build` (like Go and Rust), command parameters can be defined declaratively using the `command_params` section. This eliminates hardcoded language-specific logic.

```yaml
tools:
  go_build:
    action: build
    command: "go build ${buildmode} -o ${output} ${build_tags} ${ldflags} ${gcflags} ${input}"
    
    command_params:
      buildmode:
        sources: ["tool_params.buildmode", "item.buildmode"]
        format: "-buildmode=${value}"
        optional: true
      build_tags:
        sources: ["tool_params.build_tags", "item.build_tags"]
        format: "-tags ${value}"
        join: ","
        optional: true
      ldflags:
        sources: ["tool_params.ldflags", "item.ldflags"]
        format: "-ldflags \"${value}\""
        join: " "
        resolve_variables: true
        optional: true
      gcflags:
        sources: ["tool_params.gcflags", "tool.flags.${config}"]
        format: "${value}"
        quote_if_spaces: true
        optional: true
```

#### Command Parameter Fields

| Field | Description |
|-------|-------------|
| `sources` | List of locations to search for the value (first match wins) |
| `format` | Output format with `${value}` placeholder |
| `join` | Separator for array values (default: space) |
| `optional` | If true, empty values produce empty string (not an error) |
| `match` | Only use value if it matches this string |
| `default` | Value when no source gives one, or `match` matches nothing (used as is) |
| `quote_if_spaces` | Wrap value in quotes if it contains spaces |
| `resolve_variables` | Resolve `${var}` references in the value |

#### Source Paths

Sources are dot-separated paths into the build context:

| Path | Description |
|------|-------------|
| `tool_params.X` | Parameter passed via template's `tool_params` |
| `item.X` | Field from the target configuration |
| `tool.flags.${config}` | Tool flags for current configuration (debug/release) |

The parameters of a `build` tool are variables of the template step's `output` path too: the Rust
toolchains define `profile` (`release` when the configuration's flags pass `--release`, else
`debug`) and `bin_name` (`bin:` if set, else the target name).

### Tool-Cached Builds (Go and Rust)

`go build` and `cargo build` keep correct, content-based caches of their own that account for
imports outside the target directory, `go.mod`/`go.sum`, `go.work`, `vendor/`, `//go:embed`, cgo,
`build.rs`, Cargo path dependencies, environment variables and compiler versions. The Go and Rust
templates set `tool_cached: true` in their metadata, so buildy runs these tools on every build
(a no-op build takes tens of milliseconds) and reports them as executed, never as cache hits. Their
declared outputs must still exist when they finish, and tasks downstream of them (a staging copy,
a C++ link against a Rust static library) hash those outputs, so they still hit when nothing
changed.

Cargo gets its own target directory, `${output_dir}/cargo`, and the Rust templates declare the
files where Cargo writes them: `${output_dir}/cargo/<triple>/<profile>/<name>` for an executable
(`bin:` if set, else the target name), `lib<name>.a` for a static library, and `lib<name>.so`
(`.dylib` on macOS, `<name>.dll` on Windows) for a shared library. Each Rust toolchain describes its
layout with the variables `cargo_triple_dir`, `exe_ext`, `staticlib_name` and `cdylib_name`. If
Cargo names an artifact differently, for instance a crate whose name differs from the target's, the
build fails naming the missing file.

### Execution Configuration

Toolchains specify how commands are executed via the `execution` section:

**Native execution (default):**

```yaml
execution:
  type: native
```

**Native execution with environment variables:**

```yaml
execution:
  type: native
  env:
    GOOS: windows
    GOARCH: amd64
    CGO_ENABLED: "0"
```

**Docker execution:**

```yaml
execution:
  type: docker
  image: "gcc:13"
  user: "${UID}:${GID}"      # The default: files belong to the invoking user
  volumes:                   # Optional extra mounts
    - "/opt/sdk:/opt/sdk:ro"
```

Every task runs where the toolchain that produced it says: a compile or link with its target's
toolchain, a transform or generate with the named tool's toolchain. Copy, staging and install
tasks run natively. A Docker task runs `docker run --rm` with the workspace root, and the cache
directory if it lies elsewhere, mounted **at their host paths**, and `-w <workspace root>`, so
the absolute host paths in its command are valid in the container. The execution settings and
the toolchain's content are part of every task's cache key: switching a toolchain to another
image rebuilds.

#### Execution Configuration Fields

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | `native` or `docker` |
| `env` | map | Environment variables to set |
| `image` | string | Docker image name (docker only, required) |
| `volumes` | list | Extra volume mounts (docker only); the workspace is always mounted at its host path |
| `user` | string | User/group to run as (docker only); the invoking user by default |

Any other key is an error. `working_dir` is gone: commands always run in the workspace root.

#### Docker Variable Expansion

These variables are expanded in Docker configuration:

| Variable | Description |
|----------|-------------|
| `${UID}` | Host user ID |
| `${GID}` | Host group ID |

Using `${UID}:${GID}` ensures files created in the container have correct ownership on the host.

### Custom Toolchain Example

```yaml
# buildy_config/toolchains/mesh-converter.yaml
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
      command: "mesh_tool convert ${flags} -i ${input} -o ${output}"
      input_extensions: [".fbx", ".obj", ".gltf"]
      output_extension: ".mesh"
      output_pattern: "${name}.mesh"
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
      linux: "texconv ${flags} -o ${output_dir} ${input}"
      windows: "texconv.exe ${flags} -o ${output_dir} ${input}"
      macos: "texturetool ${flags} -o ${output} ${input}"
```

A tool with per-platform commands has none for the other platforms: using it there is an error
naming the platforms it has.

A transform or generate artifact runs, for each input file, the toolchain's tool whose
`input_extensions` takes the file (or the toolchain's only transform, convert or compile tool,
for a transform; generate or compile, for a generate).

---

## 11. Build Templates

Build templates define the workflow for building a type of target. They are language-agnostic recipes that specify what steps to perform (compile, link, etc.) and how inputs/outputs flow between them.

### Template Selection

Templates are automatically selected based on `language` and target type:

| Language | Target Type | Template |
|----------|-------------|----------|
| cpp | executable | `cpp_executable` |
| cpp | static_library | `cpp_static_library` |
| cpp | shared_library | `cpp_shared_library` |
| c | executable | `c_executable` |
| c | static_library | `c_static_library` |
| c | shared_library | `c_shared_library` |
| go | executable | `go_executable` |
| go | shared_library | `go_shared_library` |
| rust | executable | `rust_executable` |
| rust | static_library | `rust_static_library` |
| rust | shared_library | `rust_shared_library` |

### Template Structure

Templates define metadata and build steps:

```yaml
templates:
  cpp_executable:
    description: "Compile C++ sources and link into executable"
    metadata:
      language: cpp
      target_types: [executable]
      toolchain_language: cpp
      pre_processing:
        resolve_deps: true
        resolve_sources: true
        resolve_include_dirs: true
      post_processing:
        register_target: true
    
    steps:
      - name: compile
        action: compile
        for_each: source
        output: "${output_dir}/obj/${module}/${target}/${source_rel}${tool.output_ext}"
        inputs: ["${source}"]
        tool_params:
          defines: "${config.defines}"
          include_dirs: "${item.include_dirs}"
          std: "${config.cpp_standard}"
      
      - name: link
        action: link
        output_type: executable
        output: "${output_dir}/bin/${tool.output_pattern}"
        inputs: "${compile.outputs}"
        depends_on: ["${compile.task_ids}"]
        tool_params:
          lib_dirs: ["${output_dir}/lib", "${item.lib_dirs}"]
          libs: "${item.libs}"
```

### Template Metadata

The `metadata` section controls how buildy processes the target:

#### Pre-Processing Options

| Field | Description |
|-------|-------------|
| `resolve_deps` | Resolve the target's `deps` and add their include_dirs, libs and defines |
| `resolve_sources` | Expand source glob patterns to file lists |
| `resolve_include_dirs` | Process include directory paths |
| `resolve_path` | Resolve `item.path` for Go/Rust targets |

#### Post-Processing Options

| Field | Description |
|-------|-------------|
| `register_target` | Register target for dependency resolution |

The other metadata fields: `language` and `target_types` (required; each language and target type
has exactly one template, so two templates claiming the same are an error, and a template replaces
a built-in one by taking its name), `toolchain_language`, `library_output_dir` (default `bin`) and
`tool_cached`.

### Step Fields

| Field | Description |
|-------|-------------|
| `name` | Step identifier (used for references like `${compile.outputs}`) |
| `action` | Tool action to invoke (compile, link, build, transform, generate) |
| `for_each` | Iterate over items (`source` expands to one task per source file) |
| `output` | Output file path template |
| `inputs` | Inputs: references to earlier steps' outputs (`${compile.outputs}`) |
| `depends_on` | Dependencies (string or array of task IDs) |
| `output_type` | For link action: `executable`, `shared_library`, `static_library` |
| `working_dir` | Working directory for step execution |
| `tool_params` | Parameters passed to the tool |

### Multi-Step vs Single-Step Templates

**Multi-step templates** (C/C++): Use `compile` + `link` actions with explicit steps:

```yaml
steps:
  - name: compile
    action: compile
    for_each: source      # Expands to one task per source file
    output: "${output_dir}/obj/${module}/${target}/${source_rel}.o"
    
  - name: link
    action: link
    inputs: "${compile.outputs}"   # References outputs from compile step
    depends_on: ["${compile.task_ids}"]
```

**Single-step templates** (Go/Rust): Use `build` action where the tool handles everything:

```yaml
steps:
  - name: build
    action: build
    output: "${output_dir}/bin/${tool.output_pattern}"
    working_dir: "${item.path}"
    tool_params:
      features: "${item.features}"
      build_tags: "${item.build_tags}"
```

### Task Graph Validation

After generation, before anything runs (under `--dry-run` too), buildy checks the task graph and
fails with an error naming the tasks, modules and targets involved if:

1. two tasks declare the same output
   (`output …/obj/app/one/main.o is produced by both compile_one_… (module ., target one) and …`),
2. a task depends on a task that doesn't exist, or
3. a `${…}` is left unresolved in a command, input or output path (an escaped `\${…}` is kept).

A task that reads another task's output is made to depend on it, so the order follows from the
data. There are no setup tasks: the executor creates every output's directory.

### Output Paths

Every output path is a function of the output directory (which already contains
`<platform>-<arch>-<config>`), the module, the target, and the source's path relative to the
module: object files go to `${output_dir}/obj/${module}/${target}/${source_rel}${tool.output_ext}`.
A `..` in a module path (a module outside the workspace root) becomes `_up`, as in `source_rel`, so
objects stay under `obj/`.
Two targets compiling the same source get separate objects, and nothing depends on the directory
buildy was started from: sources, include directories and outputs are absolute paths, and every
command runs in the workspace root.

### Template Variables

Templates can reference variables from multiple sources:

| Variable | Source |
|----------|--------|
| `${output_dir}` | Build output directory |
| `${module}` | Current module name |
| `${target}` | Current target name |
| `${source}` | Current source file (in `for_each` loops), an absolute path |
| `${source_rel}` | Source path relative to the module directory, without extension (`src/widgets/window`); each `..` becomes `_up` |
| `${tool.output_ext}` | Tool's output extension (e.g., `.o`) |
| `${tool.output_pattern}` | Tool's resolved output pattern (e.g., `libfoo.so`) |
| `${output_prefix}` | Library filename prefix (from target's `output_prefix` or platform default) |
| `${item.X}` | Target configuration field |
| `${config.X}` | Build configuration field |
| `${compile.outputs}` | Outputs from previous step |
| `${compile.task_ids}` | Task IDs from previous step |

### Passing Parameters to Tools

Templates pass parameters to toolchains via `tool_params`:

```yaml
tool_params:
  defines: "${config.defines}"
  include_dirs: "${item.include_dirs}"
  features: "${item.features}"
  ldflags: "${item.ldflags}"
```

For `action: build`, these parameters are resolved using the toolchain's `command_params` definitions.

---

## 12. Build Systems

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
| `buildy` | buildy.yaml | Buildy itself (recursive) |

### Buildy as a Build System

When `build.system: buildy` is specified, buildy invokes itself recursively to build the dependency:

```yaml
glfw3:
  linux:
    url: "https://github.com/glfw/glfw/releases/download/3.4/glfw-3.4.zip"
    build:
      system: buildy
      override: glfw3_build.yaml  # Config file in buildy_config/dependencies/
    include_dirs: ["${dep_dir}/include"]
    lib_dirs: ["${dep_dir}/build/linux-x86_64-${config}/lib"]
    libs: ["glfw3"]
```

**Behavior:**
- Buildy automatically builds **both debug and release** configurations
- The override config file is invoked in **standalone mode** (see CLI Reference)
- Source paths in the override config are resolved relative to the fetched source directory
- The `${config}` variable can be used in `lib_dirs` to reference configuration-specific output paths

**Override config example** (`glfw3_build.yaml`):

```yaml
project:
  name: glfw3

targets:
  static_libraries:
    - name: glfw3
      language: c
      sources: ["src/*.c"]
      include_dirs: ["include", "src"]
```

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

Build systems run natively, or in a Docker container when the dependency has an `execution`
section (the same fields as a toolchain's; the workspace and cache directory are mounted at their
host paths):

```yaml
# buildy_config/dependencies.yaml
complex_lib:
  common:
    git: "https://github.com/example/complex.git"
    build:
      system: cmake
    execution:
      type: docker
      image: "gcc:13"
```

### Custom Build System Definition

Create custom build system definitions in `buildy_config/build_systems/`:

```yaml
# buildy_config/build_systems/bazel.yaml
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
      command: "bazel build //... ${args}"
      working_dir: "${source_dir}"
      default_args: []
      required: true
    
    test:
      command: "bazel test //..."
      working_dir: "${source_dir}"
      required: false
    
    clean:
      command: "bazel clean"
      working_dir: "${source_dir}"
      required: false
  
  output:
    include_patterns:
      - "${source_dir}"
    lib_patterns:
      - "${source_dir}/bazel-bin"
```

### Build System Fields

| Field | Description |
|-------|-------------|
| `name` | Build system identifier |
| `description` | Human-readable description |
| `detection.marker_files` | Files that indicate this build system |
| `detection.priority` | Detection priority (higher wins) |

### Phase Fields

| Field | Description |
|-------|-------------|
| `command` | Command template with variable substitution |
| `working_dir` | Working directory (default: `{source_dir}`) |
| `default_args` | Default arguments (merged with `build_args`) |
| `required` | Whether this phase is required |

### Output Fields

| Field | Description |
|-------|-------------|
| `include_patterns` | Patterns for finding header directories |
| `lib_patterns` | Patterns for finding library directories |

### Search Order

1. `./buildy_config/build_systems/*.yaml` - Project-specific
2. `~/.buildy/build_systems/*.yaml` - User-shared
3. Built-in (embedded in binary)

### Phase Variables

Available in command templates:

| Variable | Description |
|----------|-------------|
| `${source_dir}` | Source directory of the dependency |
| `${build_dir}` | Build output directory (usually `${source_dir}/_build`) |
| `${install_dir}` | Installation prefix directory |
| `${jobs}` | Number of parallel jobs (auto-detected from CPU count) |
| `${args}` | Combined default_args and build_args |

A build system has a `build` phase; its phases are `configure`, `build`, `test`, `install` and
`clean`, and any other is an error.

---

## 13. Lockfile

Optional lockfile for reproducible builds. Located at `buildy_config/dependencies.lock`.

### Generation

```bash
buildy --update-lock    # Generate/update lockfile
buildy --require-lock   # Fail if no lockfile exists
buildy --ignore-lock    # Ignore existing lockfile
```

### Format

```yaml
# buildy_config/dependencies.lock (auto-generated)
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
    fetched_at: "2026-01-20T15:00:00Z"

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

### Lockfile Validation

When a lockfile exists, buildy validates dependencies on each build:

- **Git dependencies**: Current commit hash must match the locked commit
- **URL dependencies**: SHA256 hash (from download metadata) must match the locked checksum

If validation fails, buildy reports which dependency changed and suggests running `buildy --update-lock`.

### Checksum Sources

For URL dependencies, the lockfile checksum comes from:

1. **Config checksum** - If `checksum:` is specified in the dependency config, it's verified during download
2. **Computed hash** - The SHA256 is always computed and stored in the `.meta.json` metadata file

Even if you don't provide a checksum in your config, the lockfile will capture the SHA256 of what was downloaded, enabling reproducible builds via `--require-lock`.

---

## 14. Complete Example

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
    - core
    - renderer
    - game
    - linux:
        - platform/linux
    - windows:
        - platform/windows

dependencies:
  file: buildy_config/dependencies.yaml

environment:
  toolchains:
    default: gcc-linux
    windows: msvc-windows
    macos: clang-macos
    
  compile:
    cpp_standard: "c++20"
    warnings: extra
    
  configurations:
    debug:
      optimization: none
      symbols: true
      defines: ["DEBUG=1"]
    release:
      optimization: full
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

### Dependencies: `buildy_config/dependencies.yaml`

```yaml
zlib:
  description: "zlib compression library"
  common:
    pkg_config: zlib

pthread:
  description: "POSIX threads"
  linux:
    libs: ["pthread"]

dl:
  description: "Dynamic linking library"
  linux:
    libs: ["dl"]

winsock:
  description: "Windows sockets"
  windows:
    libs: ["ws2_32"]

vulkan_sdk:
  description: "Vulkan SDK"
  common:
    root: "${VULKAN_SDK}"
    include_dirs: ["${root}/include"]
    lib_dirs: ["${root}/lib"]
    libs: ["vulkan"]

imgui:
  description: "Dear ImGui"
  common:
    git: "https://github.com/ocornut/imgui.git"
    ref: "v1.90.1"
    include_dirs: ["${dep_dir}"]

stb:
  description: "STB single-header libraries"
  common:
    git: "https://github.com/nothings/stb.git"
    ref: "master"
    include_dirs: ["${dep_dir}"]

glfw3:
  description: "GLFW window library"
  linux:
    pkg_config: glfw3
  windows:
    root: "${THIRD_PARTY_ROOT}/GLFW3"
    include_dirs: ["${root}/include"]
    lib_dirs: ["${root}/lib"]
    libs: ["glfw3"]
  macos:
    root: "/opt/homebrew"
    include_dirs: ["${root}/include"]
    lib_dirs: ["${root}/lib"]
    libs: ["glfw"]
    frameworks: ["Cocoa", "IOKit", "CoreVideo"]
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
      include_dirs: ["include"]
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
      include_dirs: ["include"]
      deps: ["vulkan_sdk", "imgui"]
      # Note: Static libraries don't link against other static libs at archive time.
      # Consumers (executables) list all needed libs in their libs: section.
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
      include_dirs: ["../core/include", "../renderer/include"]
      
      # deps: External dependencies (adds include_dirs, lib_dirs, libs)
      deps:
        - glfw3
      
      # libs: Internal libraries (links AND ensures build order)
      # Order matters for static libs - dependents before dependencies
      libs:
        - engine_renderer  # Depends on engine_core, so list first
        - engine_core      # Base library, list last
```

---

## 15. CLI Reference

```bash
# Basic build (platform/architecture auto-detected from host)
buildy

# Build specific directory
buildy path/to/project/

# Standalone mode - build a specific config file
buildy path/to/config.yaml

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

# Build specific targets and everything they depend on (in any module); nothing else.
# An unknown target name is an error.
buildy --target engine_core
buildy --target game

# IDE integration
buildy --compile-commands  # Generate buildy_config/compile_commands.json for IDE tooling

# Other options
buildy --jobs 8            # Parallel jobs
buildy -n 4                # Verbose logging
buildy -n 5                # Debug logging
buildy --dry-run           # Plan: what would be fetched, built and run (does none of it)
buildy --clean             # Clean build outputs
buildy --force             # Ignore cache, rebuild all
```

### Workspace Mode vs Standalone Mode

Buildy operates in two modes depending on how it's invoked:

| Invocation | Mode | Behavior |
|------------|------|----------|
| `buildy` | Workspace | Discover modules, resolve dependencies, build workspace |
| `buildy path/to/dir/` | Workspace | Same as above, using specified directory |
| `buildy path/to/config.yaml` | Standalone | Build only what's in that config file |

**Workspace Mode** (default):
- Discovers all `buildy.yaml` modules in the project
- Resolves dependencies from `buildy_config/dependencies/`
- Builds both debug and release versions of fetched dependencies
- Generates a full task graph with proper ordering

**Standalone Mode** (explicit config file):
- Only builds what's defined in the specified config file
- Does not discover other modules
- Does not resolve or fetch dependencies
- Source paths are resolved relative to the current working directory
- Used internally for building dependencies with `build.system: buildy`

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
ln -s buildy_config/compile_commands.json compile_commands.json
```

---

## 16. Build Flow

```
1. Load: parse and validate the root buildy.yaml and every module the build context
   (--platform, --architecture, --config) selects, recursively
2. Prepare: resolve dependencies
   a. System: pkg-config or explicit paths
   b. Fetch: download or clone, detect the build system, build debug and release
   (--dry-run plans this step: it reports what it would fetch and build, and uses what is on disk)
3. Generate: the tasks of every module, all targets first, then copies, staging and install
4. Validate: one producer per output, every dependency a task, no unresolved ${...};
   add the edges from producers to the tasks that read their outputs
5. Select: with --target, the named targets' final tasks and everything they depend on
6. Execute in parallel stages with content-addressable caching (--dry-run prints the stages)
7. Generate build report (JSON + terminal summary)
```

## 17. Build Reports

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

A detailed JSON report is saved to `.buildy_cache/build_report_<platform>-<arch>-<config>.json`, and the generated task graph to `.buildy_cache/tasks_<platform>-<arch>-<config>.json`, so builds for different platforms and configurations don't overwrite each other's:

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
  "toolchain": "clang-cpp-linux",
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
