# Tutorial: Multi-Module Project

This tutorial covers building a larger project with multiple modules, cross-module dependencies, staging, and distribution packaging.

## What You'll Learn

- Organizing a workspace with multiple modules
- Managing dependencies between modules
- Using external packages
- Platform-specific configuration
- Staging build outputs
- Creating distribution packages

## The Example Project

We'll use the `examples/game_engine` project:

```
game_engine/
├── buildy.yaml                 # Root workspace configuration
├── buildy_config/
│   ├── dependencies.yaml       # External dependencies
│   └── packages/
│       └── glfw3.yaml          # Package definition
├── core/
│   └── buildy.yaml             # Core library
├── platform/
│   └── buildy.yaml             # Platform abstraction
├── renderer/
│   └── buildy.yaml             # Rendering library
├── audio/
│   └── buildy.yaml             # Audio library
├── shaders/
│   └── buildy.yaml             # Shader compilation
└── game/
    └── buildy.yaml             # Game executable
```

## Step 1: The Root Workspace

The root `buildy.yaml` defines the workspace structure and shared configuration:

```yaml
project:
  name: game_engine
  version: "1.0.0"
  description: "Cross-platform game engine example"

variables:
  import_env_vars:
    - name: THIRD_PARTY_ROOT
      default: "./third_party"

  ENGINE_VERSION: "1.0.0"
  BUILD_DIR: "build"

workspace:
  modules:
    common:
      - core
      - platform
      - renderer
      - audio
      - shaders
      - game

dependencies:
  file: buildy_config/dependencies.yaml

environment:
  toolchains:
    default: gcc-cpp-linux
    windows: msvc-cpp-windows
    macos: clang-cpp-macos

  compile:
    cpp_standard: "c++17"
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

staging:
  name: game_engine
  destination: "${out_dir}/staging"
  use_symlinks: true

  contents:
    - folder: "."
      files: ["LICENSE.txt", "README.md"]

    - folder: bin
      targets:
        - mygame
        - engine_core
        - engine_platform
        - engine_renderer
        - engine_audio

    - folder: data
      contents:
        - folder: shaders
          artifacts:
            - vertex_shaders
            - fragment_shaders

install:
  - name: game_engine_release
    staging: game_engine
    destination: "${out_dir}/dist"
    format: zip
    filename: "${project_name}-${project_version}-${platform}-${arch}"
```

### Key Concepts

**Workspace modules:**

```yaml
workspace:
  modules:
    common:
      - core
      - platform
      - renderer
      - audio
      - shaders
      - game
```

Modules under `common` are built on all platforms. You can also use `linux:`, `windows:`, or `macos:` for platform-specific modules.

**External dependencies file:**

```yaml
dependencies:
  file: buildy_config/dependencies.yaml
```

This keeps the root file cleaner by moving dependencies to a separate file.

**Platform-specific toolchains:**

```yaml
environment:
  toolchains:
    default: gcc-cpp-linux
    windows: msvc-cpp-windows
    macos: clang-cpp-macos
```

Each platform gets an appropriate compiler automatically.

## Step 2: External Dependencies

The `buildy_config/dependencies.yaml` defines external dependencies:

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
  macos:
    - name: cocoa
      frameworks: ["Cocoa", "IOKit"]

packages:
  - glfw3
```

**System dependencies** use pkg-config or explicit library names, organized by platform.

**Packages** reference package definition files in `buildy_config/packages/`.

## Step 3: Package Definitions

Package definitions specify platform-specific paths and libraries. Here's `buildy_config/packages/glfw3.yaml`:

```yaml
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

  linux-arm64:
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
    frameworks: ["Cocoa", "IOKit", "CoreVideo", "OpenGL"]
```

Settings inherit: `common` -> `platform` -> `platform-arch`

## Step 4: Module Configuration

Each module has its own `buildy.yaml`. Let's examine them:

### Core Library (core/buildy.yaml)

The foundation library with no internal dependencies:

```yaml
project:
  name: engine_core
  description: "Core engine library - types, math, memory, logging"

targets:
  shared_libraries:
    - name: engine_core
      language: cpp
      sources:
        - "src/*.cpp"
      include_dirs: ["include"]
      compile:
        defines: ["ENGINE_CORE_EXPORTS"]
```

### Platform Layer (platform/buildy.yaml)

Depends on core and has platform-specific sources:

```yaml
project:
  name: engine_platform
  description: "Platform abstraction layer"

targets:
  shared_libraries:
    - name: engine_platform
      language: cpp
      sources:
        - "src/common/*.cpp"
      include_dirs: ["include", "../core/include"]
      libs:
        - engine_core
      depends_on:
        targets: ["engine_core"]
      compile:
        defines: ["ENGINE_PLATFORM_EXPORTS"]

platforms:
  linux:
    targets:
      shared_libraries:
        - name: engine_platform
          sources:
            - "src/common/*.cpp"
            - "src/linux/*.cpp"
          compile:
            defines: ["ENGINE_PLATFORM_LINUX"]

  windows:
    targets:
      shared_libraries:
        - name: engine_platform
          sources:
            - "src/common/*.cpp"
            - "src/windows/*.cpp"
          compile:
            defines: ["ENGINE_PLATFORM_WINDOWS"]
```

The `platforms:` section overrides settings for specific platforms, allowing different source files per platform.

### Renderer Library (renderer/buildy.yaml)

Depends on multiple other modules:

```yaml
project:
  name: engine_renderer
  description: "Rendering engine"

targets:
  shared_libraries:
    - name: engine_renderer
      language: cpp
      sources:
        - "src/*.cpp"
      include_dirs:
        - "include"
        - "../core/include"
        - "../platform/include"
      libs:
        - engine_core
        - engine_platform
      depends_on:
        targets:
          - engine_core
          - engine_platform
      compile:
        defines: ["ENGINE_RENDERER_EXPORTS"]
```

### Game Executable (game/buildy.yaml)

The final executable links everything together:

```yaml
project:
  name: game
  description: "Game executable"

targets:
  executables:
    - name: mygame
      language: cpp
      sources:
        - "src/*.cpp"
      include_dirs:
        - "../core/include"
        - "../platform/include"
        - "../renderer/include"
        - "../audio/include"

      packages:
        - glfw3

      libs:
        - engine_renderer
        - engine_audio
        - engine_platform
        - engine_core

      depends_on:
        targets:
          - engine_core
          - engine_platform
          - engine_renderer
          - engine_audio
```

Note the separation:
- `packages`: External dependencies (glfw3)
- `libs`: Internal libraries to link
- `depends_on.targets`: Build ordering

### Shader Compilation (shaders/buildy.yaml)

Transforms GLSL shaders to SPIR-V:

```yaml
project:
  name: engine_shaders
  description: "GLSL shaders compiled to SPIR-V"

artifacts:
  transform:
    - name: vertex_shaders
      tool: glslc
      inputs: "src/*.vert"
      outputs: "${output_dir}/shaders/${basename}.vert.spv"

    - name: fragment_shaders
      tool: glslc
      inputs: "src/*.frag"
      outputs: "${output_dir}/shaders/${basename}.frag.spv"
```

## Step 5: Build the Project

From the game_engine directory:

```bash
cd examples/game_engine
buildy
```

The build:
1. Discovers all modules from `workspace.modules`
2. Resolves dependencies between modules
3. Builds in dependency order (core -> platform -> audio -> renderer -> game)
4. Compiles shaders
5. Creates staging area

## Step 6: Understanding the Build Order

Buildy automatically determines build order from dependencies:

```
engine_core       (no dependencies)
    ↓
engine_platform   (depends on core)
    ↓
engine_audio      (depends on core)
engine_renderer   (depends on core, platform)
    ↓
mygame            (depends on all libraries)
```

All independent modules build in parallel.

## Step 7: Examine the Staging Area

After building, check the staging area:

```
build/linux-x86_64-debug/staging/
├── LICENSE.txt
├── README.md
├── bin/
│   ├── mygame
│   ├── libengine_core.so
│   ├── libengine_platform.so
│   ├── libengine_renderer.so
│   └── libengine_audio.so
└── data/
    └── shaders/
        ├── basic.vert.spv
        └── basic.frag.spv
```

With `use_symlinks: true`, files are symlinked for fast development iteration.

## Step 8: Create Distribution Package

Build the distribution package:

```bash
buildy -c release
```

This creates `build/linux-x86_64-release/dist/game_engine-1.0.0-linux-x86_64.zip`.

The install section:

```yaml
install:
  - name: game_engine_release
    staging: game_engine
    destination: "${out_dir}/dist"
    format: zip
    filename: "${project_name}-${project_version}-${platform}-${arch}"
```

## Step 9: Cross-Platform Builds

The same configuration works on Windows and macOS. The toolchain is selected automatically:

- **Linux**: `gcc-cpp-linux`
- **Windows**: `msvc-cpp-windows`
- **macOS**: `clang-cpp-macos`

Platform-specific modules and source files are filtered automatically.

## Key Takeaways

- **Workspace organization**: List modules under `workspace.modules` by platform
- **Dependencies file**: Separate external dependencies into `buildy_config/dependencies.yaml`
- **Package definitions**: Create platform-specific package files in `buildy_config/packages/`
- **Module dependencies**: Use `libs` for linking and `depends_on.targets` for build order
- **Platform overrides**: Use `platforms:` section for platform-specific settings
- **Staging**: Assemble outputs with symlinks for development
- **Install**: Package staging areas for distribution

## Common Patterns

**Adding a new module:**

1. Create the module directory with source files
2. Add `buildy.yaml` with project and targets
3. Add the module to root workspace's `modules`
4. Add dependencies to other modules if needed

**Adding a new dependency:**

1. If system library: Add to `dependencies.yaml` under `system`
2. If package: Create package YAML and add to `packages` list
3. Reference in target's `packages` field

**Platform-specific code:**

Use the `platforms:` section to override sources, defines, or other settings per platform.

## Next Steps

- [Advanced Tutorial](TUTORIAL_ADVANCED.md) - Fetch dependencies and Docker builds
- [User Guide](USER_GUIDE.md) - Complete feature reference
- [DSL Specification](DSL_SPEC.md) - Full field documentation
