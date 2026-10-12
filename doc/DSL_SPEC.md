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
- **Explicit, reproducible builds**: fetched dependencies stay at the commit or archive first fetched
- **Highly parallel execution** with content-addressable caching
- **Flexible dependency management** (system, user paths, fetched sources)

## Design Principles

1. **Pure YAML** - Standard syntax, no embedded scripting
2. **Explicit over implicit** - All operations declared, no hidden behavior
3. **Hierarchical configuration** - Workspace → Module → Target with clear override semantics
4. **Multi-language** - C/C++ primary with Go and Rust support, extensible via templates
5. **Flexible dependencies** - System, user paths, fetched sources, package managers
6. **Reproducible by default** - Fetched dependencies move only with `--update-deps`
7. **Configurable cache** - `BUILDY_CACHE_DIR` env var or config, default to project-local
8. **Cross-platform by design** - Same `buildy.yaml` works on Linux, Windows, macOS

### Cross-Platform Rules

| Aspect | Rule |
|--------|------|
| **Paths** | Always use forward slashes in YAML; converted to native separators at runtime |
| **Platform deps** | Use subsections (`common:`, `linux:`, `windows:`, `macos:`) |
| **Executable extensions** | Toolchain handles via `output_pattern` (e.g., `{name}.exe` on Windows) |
| **Commands** | Run as argument lists, without a shell ([Command Syntax](#command-syntax)); a tool that needs one says `shell: true` |

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
    ├── cache_journal.jsonl        # Entries of the tasks that finished, until the run ends
    ├── file_hashes.json           # File hashes, kept with each file's size, times and inode
    ├── deps/                      # Fetched dependency sources
    │   ├── glfw3/                 # Extracted dependency
    │   └── glfw3.extracted.json   # What glfw3/ was extracted from (URL, SHA256)
    ├── downloads/                 # Cached archive downloads, keyed by a hash of the URL
    │   ├── 3f2a…-glfw-3.4.zip           # Downloaded archive
    │   └── 3f2a…-glfw-3.4.zip.meta.json # Download metadata (SHA256, etc.)
    └── response_files/            # Arguments of a command too long for the command line (while it runs)
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

### Output Directory

```yaml
output:
  base_dir: build                                     # the default
  pattern: "${base_dir}/${platform}-${arch}-${config}" # the default
```

Every task's outputs go under the output directory, `${out_dir}`: by default
`build/<platform>-<arch>-<config>/` in the workspace root. `base_dir` is relative to the workspace
root and may not contain `..`; a relative `pattern` is relative to the workspace root too. The
root config's `output:` applies to every module and to the root's own staging and install, so a
workspace has one layout. A module that sets `output:` overrides it for its own outputs, by the
[merge rules](#how-settings-merge).

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
zlib:
  common:
    pkg_config: zlib

pthread:
  linux:
    libs: ["pthread"]

vulkan_sdk:
  common:
    root: "${VULKAN_SDK}"
    include_dirs: ["${root}/include"]
    lib_dirs: ["${root}/lib"]
    libs: ["vulkan"]

imgui:
  common:
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
    include_dirs: ["${dep_install_dir}/include"]   # added anyway: cmake installs there
    lib_dirs: ["${dep_install_dir}/lib"]
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
- Buildy builds the dependency in the configuration being built (`-c`), custom configurations
  included: the override config defines them
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
- **Source**: `.buildy_cache/deps/<name>/` (`${dep_dir}`)
- **Build**: `.buildy_cache/deps/<name>/_build/<platform>-<arch>-<config>/` (`${dep_build_dir}`)
- **Install**: `.buildy_cache/deps/<name>/_install/<platform>-<arch>-<config>/` (`${dep_install_dir}`)
- **Downloads**: `.buildy_cache/downloads/` (cached archives for URL dependencies)

**Fetching and building, once.** A dependency is fetched the first time, then used as it is:

- A git dependency's clone is reused, without the network, while the dependency names the same
  `git` URL and `ref`. Any ref works: a branch, a tag, a full or abbreviated commit ID. The clone is
  checked out detached at the commit the ref resolved to (`-n 3` logs it), and
  `deps/<name>.git.json` records it. A changed `ref` is fetched into the clone; a changed URL starts
  a new clone.
- `--update-deps` fetches what can move: every git ref that isn't a full commit ID (a branch then
  moves to its latest commit), and every archive without a `checksum`. Without it, builds are
  reproducible and work offline.
- A dependency with a `build.system` is built for the configuration being built (`-c`) only, the
  first time it is needed. The finished build is recorded in
  `_build/<platform>-<arch>-<config>.built.json`: the fetched commit or archive hash, the build
  system, the configuration, `build.args`, the override config's content, where it runs, the
  programs its phases run and the environment variables its build system reads. While that record
  matches, the dependency isn't built again; when it doesn't, the build starts again from empty
  build and install directories. A failed or interrupted build leaves no record. `--force` builds
  every dependency again.

See [the design note](design/dependencies.md) for what the record covers and doesn't.

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
- **The build record** - The hash identifies the extracted tree in each build's record

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
    include_dirs: ["${dep_dir}/include"]
    lib_dirs: ["${dep_dir}/build/${platform}-${arch}-${config}/lib"]
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
| `pkg_config` | pkg-config package name | Infers `system` type; every flag of `--cflags` and `--libs` is kept, in order |
| `root` | Base path for pre-built library | Available as `${root}` |
| `include_dirs` | Include directories | Can use `${root}`, `${dep_dir}` |
| `lib_dirs` | Library directories | Can use `${root}`, `${dep_dir}` |
| `libs` | Libraries to link | e.g., `["glfw3", "pthread"]`; an item starting with `-` or an absolute path is passed to the linker as written |
| `frameworks` | macOS frameworks | e.g., `["Cocoa", "IOKit"]` |
| `defines` | Preprocessor definitions | e.g., `["DEBUG=1"]` |
| `build` | Build configuration (see below) | For fetch dependencies |
| `execution` | Docker execution config | For fetch dependencies |

**Build Section:**

```yaml
build:
  system: cmake         # cmake, meson, make, autoconf, cargo, go_mod, buildy, auto, none
  override: custom.yaml # A buildy config in buildy_config/dependencies/ (system buildy only)
  args:                 # Arguments passed to the build system, each item one argument;
    - "-DFOO=ON"        # the dependency's variables (${config}, ${dep_dir}, ...) are resolved
    - "-DBAR=OFF"
  configurations:       # buildy configuration: the build system's, for one it doesn't map
    portable: Release
```

A build system with configurations (cmake, meson, cargo) builds `debug` and `release` as its own
debug and release; any other configuration must be mapped in `build.configurations`, or the build
fails naming it. buildy builds any configuration by its name. make, autoconf and go_mod have no
configurations: pass a configuration's settings in `args` with `${config}`.

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
| `${config}` | Build configuration (`-c`: debug, release, or a custom one) |
| `${toolchain}` | Current toolchain name |
| `${root}` | Value of `root:` field (for system deps) |
| `${dep_dir}` | Fetched source directory (for fetch deps) |
| `${dep_build_dir}` | The dependency's build directory for this configuration (with a `build.system`) |
| `${dep_install_dir}` | The dependency's install directory for this configuration (with a `build.system`) |
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
- A configuration the build system can't map, an unknown build system, `build.override` for a
  build system other than buildy, and `build.args` without a `build.system` are errors
- No warnings or fallbacks - explicit configuration is required

**Downloads** fail when the server can't be connected to within 30 s or sends nothing for 60 s;
a slow download that keeps arriving is never cut off. Each dependency build phase obeys
`--timeout` (no limit by default). Ctrl-C stops a fetch or a build.

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
    flags: ["-Wl,-rpath,$ORIGIN"]         # Extra linker flags for all targets
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

Each item of `flags`, `defines`, `include_dirs`, `lib_dirs` and `libs` is one argument of the
compiler or linker, passed as written: commands run without a shell, and no shell reads the
items. `defines: ['MSG="hello world"']` passes `-DMSG="hello world"`, a string literal, and a path
with spaces needs no quotes. Shell quotes are passed on too: write `-Wl,-rpath,$ORIGIN`, not
`-Wl,-rpath,'$ORIGIN'`, and one flag per item (`["-framework", "Cocoa"]`, not
`["-framework Cocoa"]`).

### How Settings Merge

A module's settings are the workspace root's merged with its own, by three rules:

- **Mappings merge key by key.** A key both set merges its two values by these rules.
- **Lists append**: the root's items, then the module's.
- **Scalars override**: a value the module sets replaces the root's (`output_prefix: ""` counts as
  set).

So a module that adds `environment.compile.defines: [X]` keeps the root's `c_standard`, flags and
defines, and gets `X` after them. What merges: `variables`, `environment` (`toolchains`, `compile`,
`link`, `configurations`), `config`, `output`, and the settings of `platforms`, `architectures`
and `configurations`. A module's targets, artifacts, staging, install, project and toolchain are
its own.

The levels then merge in this order, by the same rules: buildy's
[built-in configuration](#built-in-configurations), `config`, `environment.compile` (and
`environment.link`), the build's platform section, its architecture section, then its
configuration (`configurations.<config>`, then `environment.configurations.<config>`). Each level's
`compile:` section merges into the level first. The abstract keywords' flags come first in the
result, and every `remove_flags` and `remove_defines` applies last. A macro defined more than once
(`NDEBUG` and `NDEBUG=1`, or a target's `X=2` after the module's `X=1`) is passed once, by its last
definition, where that one is: what the compiler would use anyway.

### Built-in Configurations

buildy has its own `debug` and `release` configurations, the lowest level of every config:

| Configuration | Keywords | Defines | GCC/Clang | MSVC |
|---|---|---|---|---|
| `debug` | `optimization: none`, `symbols: true` | | `-O0 -g` | `/Od /Zi` |
| `release` | `optimization: speed` | `NDEBUG` | `-O2 -DNDEBUG` | `/O2 /DNDEBUG` |

A config's own settings merge over them: a keyword set at any level (`environment.compile`,
`config`, `configurations.<name>`, a platform) replaces buildy's, a definition of `NDEBUG`
replaces buildy's, and `remove_defines: [NDEBUG]` removes it. A keyword value the toolchain's
`flag_mappings` don't have is skipped (it is an error only when a config sets it). Other
configurations get nothing from buildy. Transform and generate tools get the keywords too, as
their toolchains map them (glslc: `-O0 -g` in debug, `-O` in release).

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
| `static_libraries` | Static library (.a, .lib) | c, cpp, rust, go |
| `shared_libraries` | Shared library (.so, .dll, .dylib) | c, cpp, rust, go |
| `executables` | Executable binary | c, cpp, rust, go |

**Note:** Go and Rust library targets are for C/C++ interop only. For pure Go/Rust projects, use `executables` - dependencies are handled by Go modules and Cargo respectively. A Go shared library is built with `-buildmode=c-shared` (`lib<name>.so`, `lib<name>.dylib`, `<name>.dll`), a static library with `-buildmode=c-archive` (`lib<name>.a`), both in the output directory's `lib/` with the C header Go writes beside them (`lib<name>.h`, `<name>.h` for the DLL); both need cgo (a C compiler, and `CGO_ENABLED` not `0`). A C or C++ target that includes the header names the library in `depends_on.targets` as well as `libs`, so its compiles run after the library's build: `libs` orders only its link.

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

#### File Patterns

Every file pattern buildy reads (`sources`, `artifacts.copy` `sources`, transform and generate
`inputs`, staging `files`) has the same syntax:

| Syntax | Matches |
|--------|---------|
| `*` | Any characters within one path component |
| `**` (a whole component) | Any number of directories, none included: `src/**/*.c` is `src/a.c`, `src/x/b.c` and `src/x/y/c.c` |
| `?` | One character |
| `[abc]`, `[a-z]`, `[!a]` | One character of a class |
| `{a,b}` | Either alternative: `src/*.{c,h}` |

A pattern is relative to its module's directory (unless absolute) and matches files, never
directories; its files come sorted by path. On Linux and macOS a backslash escapes the next
character (`\*`); on Windows it is a path separator. A path without any of these characters names
one file.

**Patterns over build outputs.** A pattern names the files on disk outside the build's
[output directory](#output-directory), and the outputs the build's tasks declare, wherever they
are: what targets link, what generates and transforms write, what copies and staging produce. So
`${out_dir}/bin/*` names the same files on a clean build as on the next one (the graph is known
before anything runs), a file a previous build left in the output directory is never picked up,
and the task that reads a match runs after the task that produces it. In the output directory a
pattern, or a plain path, that names no output of the build is an error. Outputs are known in
generation order: every module's generates, transforms and targets (modules in path order), then
every module's copies, then every module's staging and install, then the root config's; a source
pattern sees its own module's generated files.

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

**Filterable fields:** `sources`, `defines`, `libs`, `include_dirs`, `flags`, and the Go and Rust targets' `build_tags`, `ldflags`, `gcflags` and `features`

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
| Toolchain | `gcc-cpp-linux`, `clang-cpp-macos` | The toolchain that builds the target (a C target in a module whose toolchain is C++ gets the C toolchain, and its abstract keywords map through it) |

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

### Go and Rust Target Keys

A Go or Rust target names a directory, `path` (the Go package with `main`, or the crate with its
`Cargo.toml`), and the options its tool takes. These are all the keys it has; any other key (a
C/C++ key such as `sources`, `flags`, `defines`, `libs`, `deps` or `output_prefix`, or the other
language's) is an error, as a misspelt key is. A C or C++ target can't have the Go or Rust keys.

| Key | Language | What it gives the tool |
|-----|----------|------------------------|
| `name` | both | The target's name; the executable's or library's file name |
| `language` | both | `go` or `rust` |
| `path` | both | The directory the tool runs in (required) |
| `depends_on` | both | Build order only, as for any target |
| `build_tags` | go | `-tags a,b`: one argument, the tags joined with commas |
| `ldflags` | go | `-ldflags=<value>`: one argument, the items joined with spaces |
| `gcflags` | go | `-gcflags=<value>`: one argument, the items joined with spaces |
| `buildmode` | go | `-buildmode=<mode>` for an executable: `exe` or `pie`. A library's is its type's: `c-shared` for `shared_libraries`, `c-archive` for `static_libraries` |
| `features` | rust | `--features a,b`: one argument, the features joined with commas |
| `bin` | rust | `--bin <name>`: an executable that builds one of its crate's binaries; also its file name (executables only) |

`build_tags`, `ldflags`, `gcflags` and `features` are lists that take
[filters](#filtered-lists) like any other: `build_tags: [{linux: [x11]}, netgo]`,
`ldflags: [{release: ["-s", "-w"]}]`. Variables are resolved in every value:
`"-X main.version=${project_version}"`.

`go build` splits its `-ldflags` and `-gcflags` value into words itself, honouring quotes, so an
item is one or more words as go reads them: `"-X main.version=1.2"` is two words for the linker,
and a value with a space needs go's quotes inside the item, `"-X 'main.msg=hello world'"`.

The configuration decides the rest, from the toolchain's `flags`: `release` adds `-trimpath` to
`go build` and `--release` to `cargo build` (and Cargo's output directory is then `release`);
`debug` adds nothing. A cross-compiling Rust toolchain passes its target triple
(`--target x86_64-pc-windows-gnu`, `--target wasm32-unknown-unknown`); the Go Windows toolchain
sets `GOOS` and `GOARCH`.

```yaml
targets:
  executables:
    - name: myapp
      language: go
      path: "cmd/myapp"
      build_tags: [netgo, {linux: [x11]}]
      ldflags: ["-X main.version=${project_version}", {release: ["-s", "-w"]}]
      gcflags: [{debug: ["all=-N -l"]}]
      buildmode: pie
    - name: tool
      language: rust
      path: "."               # Directory containing Cargo.toml
      features: [cli, {linux: [alsa]}]
      bin: "tool"             # One binary of a crate that has several
```

Go and Rust projects typically only need `executables`: Go modules and Cargo handle their
dependencies. The binary of a Rust executable is built to
`build/<platform>-<arch>-<config>/cargo/<profile>/<name>` (`bin` if set; `debug`, or `release` for
configurations whose flags pass `--release`). Go and Rust builds run their own tool on every build;
see [Tool-Cached Builds](#tool-cached-builds-go-and-rust).

### deps vs libs vs depends_on

These are **intentionally separate** concepts:

| Field | Purpose | Example |
|-------|---------|---------|
| `deps` | External dependencies (provide include paths, lib paths, libs, defines) | `[glfw3, zlib]` |
| `libs` | Internal project libraries to link against (implies build order of the link) | `[engine_core, engine_renderer]` |
| `depends_on.targets` | Build ordering only (no linking): every task of the target, compiles included, runs after the named targets - for code generators, tools, a library whose generated header it includes | `[proto_gen, asset_compiler]` |
| `depends_on.artifacts` | Build ordering (generated headers) | Code generators (protobuf, etc.) |

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
        - "${gen_dir}/*.pb.cc"    # Matches the artifact's declared outputs
      depends_on:
        artifacts: ["protos"]      # protoc runs before every task of my_app
```

Source patterns match the artifact's declared `outputs` even though they don't exist yet (see
[patterns over build outputs](#file-patterns)), and a compile of a generated source runs after its
generate. When a target declares `depends_on.artifacts`, the artifact's tasks also run before
every other task of the target, compiles included: a compile can include a generated header it
doesn't list.

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
      destination: "${out_dir}/data"      # each file by its name: no subdirectories
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

A transform or generate tool's `${flags}` are, in order: the module's abstract keywords as the
tool's toolchain maps them (glslc maps `optimization: speed` to `-O`), the tool's `common` flags,
and the configuration's.

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
- The machine buildy runs on decides, not the platform being built for: on a Linux or macOS host
  symlinks work as expected (building for Windows too), and a **Windows host** always copies
  (symlinks there need administrator rights or Developer Mode).
- A staging symlink points at the absolute path of what it stages. An install stores what links
  point to (`follow_symlinks: true`, the default); `follow_symlinks: false` stores the links
  themselves, which only make sense on the machine that built them, and is an error for a Windows
  package.

### Cache Integration

Staging operations are cached tasks:
- For symlinks: the cache key includes the source path and the operation
- For copies: the cache key includes the source's content hash
- The cache detects when staging needs to update

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
is needed. The archive holds exactly the files the staging section stages in this build (its
tasks' outputs under its destination), never whatever else the directory holds: a file an earlier
build staged that has since left the config, or one put there by hand, is not shipped. buildy
doesn't empty the staging directory, as a destination can be any directory (`${workspace}`), but
the archive doesn't depend on what is in it. Each staged file is an input of the install, so
staging one more or one fewer file builds the archive again.

Entries are the staged files' paths relative to the staging directory, with forward slashes and
no `./` prefix, in name order, each directory before its contents. Every entry has the modification time
2000-01-01 00:00:00 UTC and owner 0, and keeps its permission bits, so the same staging area
always gives the same archive. With `follow_symlinks: true` (the default) a symlink is stored as
the file or directory it points to; with `false`, as a link (an error when building for
Windows).

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

Templates, toolchains and [build systems](#12-build-systems) are searched in one order, highest
priority first:

1. `./buildy_config/toolchains/*.yaml` and `./buildy_config/build_systems/*.yaml` - Project-specific
   (templates have no workspace directory)
2. Each `--add-data-dir <dir>`'s `templates/`, `toolchains/` and `build_systems/`, the last one
   given first
3. `data/templates/`, `data/toolchains/` and `data/build_systems/` next to the buildy executable
   (symlinks resolved)
4. Built-in (embedded in binary)

A definition replaces one of the same name lower in the list, and buildy logs both files at info
level (`-n 3`). Two toolchain or build system files in one directory with the same name are an
error.

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
| `go-macos-arm64` | macOS ARM | Go | Go compiler for Apple Silicon |
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

A module that names no toolchain gets the platform and architecture's default: of the native
toolchains whose `target` is that platform and architecture, the one with the highest `priority`.
A target in another language gets that language's default the same way (of the toolchains for its
language, or else for its language and `target.platform: any`). Two toolchains sharing the
highest priority are an error that names both. The built-in defaults:

| Platform | Default | Then, for its language |
|----------|---------|------------------------|
| linux-x86_64 | `gcc-cpp-linux` (40) | `gcc-c-linux` (30), `clang-cpp-linux` (20), `clang-c-linux` (10) |
| windows-x86_64 | `msvc-cpp-windows` (40) | `msvc-c-windows` (30), `gcc-cpp-mingw` (20), `gcc-c-mingw` (10) |
| macos-x86_64 | `clang-cpp-macos` (20) | `clang-c-macos` (10) |
| macos-arm64 | `clang-cpp-macos-arm64` (20) | `clang-c-macos-arm64` (10) |
| linux-arm | `gcc-c-arm-cross` (10) | `gcc-cpp-arm-cross` (0) |
| wasm-wasm32 | `emscripten-c` (10) | `emscripten-cpp` (0) |

Go and Rust toolchains have priority 0: each is the only one for its language and platform. To
make a toolchain of your own the default, give it a higher priority than the built-in one.

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

  priority: 40                # The default for its target is the highest priority (default 0)

  variables:                  # Toolchain-specific variables
    CC: "gcc"
    CXX: "g++"

  execution:
    type: native              # native or docker

  reads_env: [CPATH, LIBRARY_PATH]   # Environment variables the tools read

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
| `priority` | Of the toolchains for a platform and architecture (and language), the highest is the default ([selection](#toolchain-selection)); default 0 |
| `variables` | Toolchain-specific variables |
| `output_prefix` | Default library filename prefix (e.g., `"lib"` or `""`) |
| `execution` | Execution configuration (native, the default, or docker) |
| `reads_env` | Environment variables the tools read: their values are part of every native task's cache key, so changing one rebuilds (unset differs from empty) |
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
| `command` | [Command template](#command-syntax), or one per platform (required) |
| `shell` | `true` runs the command with a shell ([Shell Tools](#shell-tools)); default `false` |
| `response_file` | How the tool reads arguments from an `@file`: `gnu` or `msvc` ([Response Files](#response-files)) |
| `input_extensions` | File extensions this tool accepts |
| `output_extension` | Extension for output files |
| `output_pattern` | Pattern for output filename (supports `${name}`, `${output_prefix}`) |
| `flags` | Flags by configuration: `common`, then the configuration's (`debug`, `release`, ...). Every tool gets both; a compile, transform or generate tool gets the abstract keywords as its toolchain maps them before them |
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
| `dependencies` | string | Dependency tracking arguments, a [command template](#command-syntax) with `${dep_file}` (e.g., `-MMD -MP -MF ${dep_file}`) |

**Linking supports:**

| Field | Type | Description |
|-------|------|-------------|
| `output_type` | string | `executable`, `shared_library`, or `static_library` |
| `lib_dirs` | bool | Supports library directories |
| `lib_dir_flag` | string | Flag prefix for lib dirs (e.g., `-L`) |
| `libs` | bool | Supports library linking |
| `lib_flag` | string | Flag prefix for libs (e.g., `-l`) |
| `frameworks` | bool | Supports macOS frameworks |
| `framework_flag` | string | Flag for frameworks (default `"-framework "`) |

Each `*_flag` is joined to each value: `include_flag: "-I"` and `inc` give the argument `-Iinc`.
A flag ending in a space is an argument of its own before each value: `"-framework "` and `Cocoa`
give `-framework` `Cocoa`. A flag that isn't set has its default (`-D`, `-I`, `-fPIC`, `-L`,
`-l`, `"-framework "`); one set to `""` is no flag (MSVC: `lib_flag: ""` with `lib_suffix: ".lib"`
gives `glfw3.lib`).

### Command Syntax

A tool's command is a template that buildy splits into words when it loads the toolchain, and
runs as an argument list, **without a shell**:

```yaml
command: "g++ ${dep_flags} -std=${std} ${flags} ${defines} ${includes} -c ${input} -o ${output}"
```

- Words are separated by spaces, tabs and newlines.
- `'...'` keeps its text as written, `$` and `${...}` included. `"..."` keeps spaces in a word
  and fills placeholders in. A backslash is an ordinary character.
- **A placeholder that is a word of its own** (`${includes}`) becomes the placeholder's values,
  each one argument: none when it is empty, several for a list. A path with spaces stays one
  argument.
- **A placeholder inside a word** (`-std=${std}`, `/Fo${output}`, `/OUT:${output}`, `"${x}"`) is
  replaced by its value, and the word stays one argument. It must have exactly one value: an
  empty one, or a list of several, there is an error. Write it as a word of its own to pass any
  number of arguments.
- `$` not followed by `{` is an ordinary character: `-Wl,-rpath,$ORIGIN` reaches the linker as
  written.

| Placeholder | Arguments |
|---|---|
| `${input}`, `${source}`, `${output}`, `${dep_file}`, `${implib}` | one path |
| `${flags}` | the tool's `flags` for `common` and the configuration, then the target's |
| `${defines}`, `${includes}`, `${lib_dirs}`, `${libs}`, `${frameworks}` | one per value, with the tool's `*_flag` |
| `${objects}` | one per object file |
| `${pic}` | `pic_flag` for a shared library's sources, else none |
| `${dep_flags}` | `supports.dependencies` with `${dep_file}` filled in |
| `${std}` and other `tool_params` | one, or one per item of a list |
| `${args}` (transform, generate) | one per item of the artifact's `args` |
| anything else (transform, generate, build) | a variable (toolchain `variables`, `${output_dir}`, ...), one argument |

A placeholder with no value at all is an error naming the tool.

The cache key and `.buildy_cache/tasks_*.json` record the argument list; logs show it as one
line quoted for the host's shell, and `compile_commands.json` has it as `arguments`. `go build`
and `cargo build` run in the target's directory.

### Shell Tools

A tool whose command needs a shell (pipes, redirection, `&&`) says so:

```yaml
tools:
  embed:
    action: generate
    shell: true
    command: "xxd -i ${input} > ${output} && touch ${output}"
```

Its command is shell text. Buildy replaces each placeholder with its values, each quoted for the
shell, and runs it with `sh -c` (Linux and macOS hosts, and always in Docker) or `cmd.exe /c`
(Windows hosts). A placeholder in a shell tool goes outside quotes (buildy quotes its values;
one inside `"..."` is an error), and as in other commands one inside a word must have exactly
one value. cmd.exe can't quote `"`, `%`, `!` or a line break inside quotes, so a value holding
one is an error there. A shell tool takes no response file. No built-in toolchain uses a shell.

### Response Files

When a command's arguments are over what the host lets a program start with (Linux: about ¾ of
a quarter of the stack limit for arguments and environment, usually 1.5 MiB, and 128 KiB for one
argument; macOS: ¾ of `kern.argmax`; Windows: 32000 characters of command line), buildy writes
the arguments after the program to a response file in `.buildy_cache/response_files/` and runs
`program @file`. The tool declares the file's syntax:

| `response_file` | Tools | Syntax |
|---|---|---|
| `gnu` | GCC, Clang, GNU `ar` and `ld`, `llvm-ar` | one argument per line; whitespace, quotes and backslashes escaped with a backslash |
| `msvc` | `cl`, `link`, `lib` | one argument per line, quoted as on a Windows command line; UTF-16 with a byte order mark when not ASCII |

A command over the limit whose tool declares no `response_file` fails, saying so. The built-in
GCC, Clang, MinGW, ARM, Emscripten and Docker toolchains declare `gnu` for their compile and link
tools and MSVC declares `msvc`; the macOS `ar` reads no response files.

### Data-Driven Command Parameters

A `build` tool (Go, Rust) gets a target's options through `command_params`: each parameter fills
the placeholder of its name with arguments. The built-in Go toolchains:

```yaml
tools:
  go_build:
    action: build
    command: "go build ${buildmode} ${flags} -o ${output} ${build_tags} ${ldflags} ${gcflags} ${input}"
    flags:
      common: []
      debug: []
      release: ["-trimpath"]
    command_params:
      buildmode:
        sources: ["tool_params.buildmode", "item.buildmode"]
        format: "-buildmode=${value}"
      build_tags:
        sources: ["tool_params.build_tags", "item.build_tags"]
        format: "-tags ${value}"
        join: ","
      ldflags:
        sources: ["tool_params.ldflags", "item.ldflags"]
        format: "-ldflags=${value}"
      gcflags:
        sources: ["tool_params.gcflags", "item.gcflags"]
        format: "-gcflags=${value}"
```

A parameter takes the values of its first source that has any, joins them with `join` (default a
space) into one value, and gives the arguments of its `format`, a [command template](#command-syntax)
whose only placeholder is `${value}`: `"-tags ${value}"` is two arguments, `-tags` and `a,b`;
`"-ldflags=${value}"` is one, spaces and all. Without a `format` the value is one argument; with no
value (and no `default`), the parameter gives no arguments. Values are used as they are: a target's
were resolved (variables, filters) when it was loaded.

#### Command Parameter Fields

| Field | Description |
|-------|-------------|
| `sources` | Where to look for the value, in order; the first with a value wins (required) |
| `format` | The arguments, a command template with `${value}` |
| `join` | Separator between a list's values (default: a space); the joined value is one `${value}` |
| `match` | Keep only the values equal to this (the Rust toolchains find `--release` in the configuration's flags) |
| `default` | The value when no source gives one, or `match` matches nothing (used as is, without `format`) |

#### Source Paths

Sources are dot-separated paths into the build context:

| Path | Description |
|------|-------------|
| `tool_params.X` | A parameter the template's step passes (the built-in Go library templates pass `buildmode`, the Rust library templates `crate_type`) |
| `item.X` | The target's key `X` ([Go and Rust target keys](#go-and-rust-target-keys)), or `name` |
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
directory if it lies elsewhere, mounted **at their host paths**, and `-w <workspace root>` (or the
command's own directory), so the absolute host paths in its command are valid in the container.
The command's arguments are arguments of `docker run`, after the image; a shell tool's command
runs as `sh -c` in the container. The execution settings are part
of every task's cache key, with the ID of the image: switching a toolchain to another image, or
pulling a new version of it, rebuilds.

A Docker task runs with `--init` and a name, and on a timeout or Ctrl-C buildy stops it with
`docker kill`. A native command runs in a process group of its own, killed as a whole.

### What a Task's Cache Key Holds

Exactly what decides its outputs: its command (arguments and directory) or native operation, its
declared outputs, the content of its declared inputs, its execution settings, the program it runs
(found as it will run: on the `PATH`, its symlinks resolved; its path and content, read once per
run), or the Docker image's ID, and the values of the toolchain's `reads_env` variables. The rest
of the configuration (flags, defines, include directories, standard, output prefix) reaches a task
through its command and outputs: changing one target reruns that target's tasks only. See
[the design note](design/execution.md#the-cache-key).

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
| go | static_library | `go_static_library` |
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
        output: "${output_dir}/obj/${module}/${target}/${source_rel}${source_ext}${tool.output_ext}"
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
   (`output …/gen/version.h is produced by both generate_version_… (module ., target one) and …`),
2. a task depends on a task that doesn't exist,
3. a `${…}` is left unresolved in a command, input or output path (an escaped `\${…}` is kept), or
4. a task lacks its platform, architecture or configuration (an internal error).

A task that reads another task's output is made to depend on it, so the order follows from the
data. There are no setup tasks: the executor creates every output's directory.

### Output Paths

Every output path is a function of the output directory (which already contains
`<platform>-<arch>-<config>`), the module, the target, and the source's path relative to the
module: object files go to `${output_dir}/obj/${module}/${target}/${source_rel}${source_ext}${tool.output_ext}`
(`obj/core/app/src/window.cpp.o`). The source's extension stays in the name, as in CMake, so `a.c`
and `a.cpp` in one target are two objects.
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
| `${source_ext}` | The source's extension (`.cpp`) |
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

Build systems define how to build fetched dependencies (e.g., CMake, Meson, Make projects). Buildy includes built-in definitions for common build systems.

### Built-in Build Systems

| Name | Detection | Configurations (`${build_type}`) | Phases |
|------|-----------|----------------------------------|--------|
| `cmake` | CMakeLists.txt | debug: `Debug`, release: `Release` | configure, build, install |
| `meson` | meson.build | debug: `debug`, release: `release` | configure, build, install |
| `make` | Makefile, GNUmakefile | none | clean, build (in the source tree) |
| `autoconf` | configure, configure.ac | none | configure, build, install (from the build directory) |
| `cargo` | Cargo.toml | debug: `dev`, release: `release` | build |
| `go_mod` | go.mod | none | build |
| `buildy` | buildy.yaml | any, by name (`${config}`) | build |

`build.system: auto` picks the build system by the files in the fetched tree, among the built-in
ones and [your own](#build-system-definition): the highest `detection.priority` wins (buildy 20,
cmake, meson, cargo and go_mod 10, autoconf 8, make 5). Two that detect the tree at the same
priority (a tree with `CMakeLists.txt` and `meson.build`) are an error naming both: set
`build.system`.

cmake configures with `-DCMAKE_BUILD_TYPE=${build_type}` (once) and installs into
`${dep_install_dir}` (`CMAKE_INSTALL_LIBDIR=lib`); its build and install pass `--config
${build_type}`, which a multi-configuration generator (Visual Studio, the default on Windows, or
Xcode) needs and a single-configuration one ignores.

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
    lib_dirs: ["${dep_dir}/build/${platform}-${arch}-${config}/lib"]
    libs: ["glfw3"]
```

**Behavior:**
- Buildy builds the configuration being built (`--config ${config}`), whatever its name: the
  override config defines its configurations
- The override config file is invoked in **standalone mode** (see CLI Reference)
- Source paths in the override config are resolved relative to the fetched source directory
- The nested build writes `${dep_dir}/build/<platform>-<arch>-<config>/`, so `${config}` in
  `lib_dirs` names the right one; its `lib/` and `bin/` are added to the dependency's lib dirs
- It runs again when the override config, the fetched tree or the buildy executable changes

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

A build system's phases run in this order, each one it defines:

| Phase | Runs |
|-------|------|
| `clean` | Only for an `in_source` build system (make), before building a tree that was built before |
| `configure` | If defined |
| `build` | Always; required |
| `install` | If defined, into `${install_dir}` |

A build system doesn't define a phase it doesn't need.

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

### Build System Definition

The built-in definitions are YAML files embedded in buildy
(`src/internal/resource/data/build_systems/`). Definitions of your own go in
`buildy_config/build_systems/*.yaml`, a data directory's `build_systems/`, or `data/build_systems/`
next to the executable ([search order](#search-order)); a dependency names one in `build.system`,
and `build.system: auto` considers it. One with a built-in's name replaces the built-in (logged at
info level, naming both files). Build systems are loaded for a workspace's dependencies, not in
standalone mode.

```yaml
build_system:
  name: cmake
  description: "CMake build system"

  detection:
    marker_files: ["CMakeLists.txt"]
    priority: 10

  configurations:
    debug: Debug
    release: Release

  reads_env: [CC, CXX, CFLAGS, CXXFLAGS, LDFLAGS]

  phases:
    configure:
      command: "cmake -S ${source_dir} -B ${build_dir} -DCMAKE_BUILD_TYPE=${build_type} ${args}"
    build:
      command: "cmake --build ${build_dir} --config ${build_type} --parallel ${jobs}"
    install:
      command: "cmake --install ${build_dir} --config ${build_type} --prefix ${install_dir}"

  output:
    include_patterns: ["${install_dir}/include", "${source_dir}/include"]
    lib_patterns: ["${install_dir}/lib", "${build_dir}"]
```

A complete example of your own, a Makefile that installs into `$(PREFIX)`, is in the
[user guide](USER_GUIDE.md#custom-build-systems).

A definition is loaded strictly, like a toolchain: an unknown key, a value of the wrong type, a
missing `name`, a missing `build` phase, or any rule below broken is an error naming the file
(and the line where there is one), and buildy stops.

### Build System Fields

| Field | Description |
|-------|-------------|
| `name` | Required. The name `build.system` uses. Not `auto` or `none`. One with a built-in's name replaces it |
| `description` | Human-readable description |
| `detection.marker_files` | Files whose presence in the fetched tree selects this build system for `build.system: auto`. None: never detected |
| `detection.priority` | Detection priority (an integer; higher wins; default 0). Two build systems that detect a tree at the highest priority are an error naming both |
| `configurations` | buildy configuration: the build system's, the phases' `${build_type}`. Without it the build system has no configurations, and `build.configurations` on a dependency is an error. With it, a configuration it doesn't map fails unless the dependency maps it in `build.configurations`. Values can't be empty; an empty map is an error |
| `in_source` | Builds in the source tree: one configuration at a time, with the `clean` phase run before another is built (and after a failed build). Requires a `clean` phase |
| `reads_env` | Environment variables its tools read: their values are in each build's record, so a change rebuilds (native builds; a Docker build sees only the execution's `env`) |
| `phases` | `clean`, `configure`, `build`, `install`; `build` is required, `clean` only with `in_source` (and required there). Any other name is an error |
| `output` | Where the built headers and libraries are (below) |

### Phase Fields

| Field | Description |
|-------|-------------|
| `command` | Required. A [command template](#command-syntax), run without a shell |
| `working_dir` | Working directory, a path template (default: `${source_dir}`) |
| `default_args` | Arguments ahead of the dependency's `build.args` in `${args}`, each one argument, passed as written (a placeholder in one is an error: put it in the command) |
| `shell` | `true` runs `command` as a shell script (`sh -c`, also in Docker; `cmd.exe` on a Windows host), for `&&`, pipes and redirection. Placeholder values are quoted for the shell; a placeholder inside double quotes is an error. The build's record holds the shell, not the programs the script runs |

### Output Fields

| Field | Description |
|-------|-------------|
| `include_patterns` | Path templates of directories added to the dependency's include dirs, those that exist, in order |
| `lib_patterns` | Path templates of directories added to the dependency's lib dirs, those that exist, in order |

Directories the dependency's own `include_dirs` and `lib_dirs` already name are not added twice.

### Phase Variables

Available in phase commands, working directories and output patterns. Any other `${name}` is an
error when the definition loads:

| Variable | Description |
|----------|-------------|
| `${source_dir}` | Source directory of the dependency (`${dep_dir}`) |
| `${build_dir}` | `${source_dir}/_build/<platform>-<arch>-<config>` (`${dep_build_dir}`), emptied before each build of an out-of-source build system |
| `${install_dir}` | `${source_dir}/_install/<platform>-<arch>-<config>` (`${dep_install_dir}`), emptied likewise |
| `${build_type}` | The build system's name for the configuration; only in a definition with `configurations` |
| `${config}`, `${platform}`, `${arch}` | What is being built, buildy's names (`release`, `linux`, `x86_64`) |
| `${jobs}` | Number of parallel jobs (the number of CPUs) |
| `${args}` | `default_args`, then the dependency's `build.args`: each item one argument. Phase commands only |
| `${buildy_exe}` | The running buildy executable |
| `${config_file}` | The dependency's `build.override` file, else its source directory (what build system buildy builds) |

A path template (`working_dir`, an output pattern) must expand to one word. A placeholder inside a
word must have one value, as in any [command template](#command-syntax).

---

## 13. Reproducibility

A fetched dependency is pinned by what its definition names:

| Dependency | Pinned by | Moves |
|------------|-----------|-------|
| `git` with a full commit ID | The commit | Never |
| `git` with a tag or branch | The commit fetched first | With `--update-deps` |
| `url` with a `checksum` | The checksum | Never (a different download fails) |
| `url` without a `checksum` | The first download | With `--update-deps` |
| `pkg_config`, `root`, paths | The system | When the system changes |

There is no lockfile yet: `--update-lock` and `--require-lock` don't exist. To pin a git
dependency across machines, give its full commit ID as `ref`; to pin an archive, give its
`checksum`.

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

# Dependencies
buildy --update-deps       # Fetch git branches and tags, and archives without a checksum, again

# Build specific targets and everything they depend on (in any module); nothing else.
# An unknown target name is an error.
buildy --target engine_core
buildy --target game

# IDE integration
buildy --compile-commands  # Generate buildy_config/compile_commands.json for IDE tooling

# Other options
buildy -j 8                # Tasks run at once (each starts when its dependencies finish)
buildy --timeout 30m       # Kill a task or dependency build step that runs longer (no limit by default)
buildy -n 4                # Verbose logging
buildy -n 5                # Debug logging
buildy --dry-run           # Plan: what would be fetched and built, and the tasks in an order they can run in
buildy --clean             # Remove the output directory (build/), nothing else
buildy --clean-all         # The same, and the cache directory (fetched dependencies too)
buildy --force             # Ignore the cache and dependency build records, rebuild all
```

### Workspace Mode vs Standalone Mode

Buildy operates in two modes depending on how it's invoked:

| Invocation | Mode | Behavior |
|------------|------|----------|
| `buildy` | Workspace | Find the workspace the current directory belongs to (below), discover modules, resolve dependencies, build the workspace |
| `buildy path/to/dir/` | Workspace | The same, with that directory as the workspace root |
| `buildy path/to/config.yaml` | Standalone | Build only what's in that config file |

**Which workspace.** Without a path, buildy takes the nearest directory at or above the current one
that has a `buildy.yaml` (a module, or a root), then looks further up, like Cargo: the root is the
**outermost** directory whose `buildy.yaml` includes that module, by listing it in
`workspace.modules` (directly or through child modules, under any filter key) or, without
`workspace.modules`, by discovering it. A `buildy.yaml` that nothing above includes is a workspace
of its own. So `buildy` run in `src/core/` of a workspace that lists `src/core` builds that
**whole workspace**, exactly as `buildy` at its root would: the root's variables, settings and
dependencies apply, the other modules' targets exist, and there is one cache, at the root. Use
`--target` to build part of it. `-n 3` logs the root chosen and why. A `buildy.yaml` above that
can't be read is an error.

**Workspace Mode** (default):
- Discovers all `buildy.yaml` modules in the project (without `workspace.modules`), outside the
  cache directory (`--cache-dir`, `.buildy_cache` by default), `.git`, `venv` and what
  `workspace.exclude` names
- Resolves dependencies from `buildy_config/dependencies/`
- Fetches and builds dependencies that aren't yet, for the configuration being built
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
   b. Fetch: download or clone what isn't fetched yet; build what isn't built yet for the
      configuration being built (each build's record says), and install it
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
