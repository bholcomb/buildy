# Tutorial: Multi-Module Project

This tutorial walks through `examples/game_engine`: a workspace of several modules that depend on
each other, a dependency fetched and built by buildy, shaders compiled with a tool, and the
results staged and packed into a zip.

## What You'll Learn

- Organizing a workspace into modules
- Linking libraries across modules
- Platform-specific sources with filtered lists
- A fetched dependency built from a buildy config of your own
- Transforming assets (GLSL shaders to SPIR-V)
- Staging build outputs and packaging them

## The Example Project

```
game_engine/
├── buildy.yaml                     # The workspace root
├── buildy_config/
│   ├── dependencies.yaml           # System dependencies
│   └── dependencies/
│       ├── glfw3.yaml              # GLFW: fetched and built
│       └── glfw3_build.yaml        # The buildy config that builds GLFW
├── src/
│   ├── core/buildy.yaml            # engine_core
│   ├── platform/buildy.yaml        # engine_platform
│   ├── renderer/buildy.yaml        # engine_renderer
│   ├── audio/buildy.yaml           # engine_audio
│   └── game/buildy.yaml            # mygame
└── assets/
    └── shaders/buildy.yaml         # SPIR-V shaders
```

To build it you need a C++ compiler, `glslc` on the `PATH`, and the network the first time (see
the example's [README](../examples/game_engine/README.md)).

## Step 1: The Root Workspace

The root `buildy.yaml` lists the modules and holds what every module shares:

```yaml
# buildy.yaml
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
    - src/core
    - src/platform
    - src/renderer
    - src/audio
    - assets/shaders
    - src/game

dependencies:
  file: buildy_config/dependencies.yaml

environment:
  compile:
    cpp_standard: "c++17"
    warnings: extra
    
  configurations:
    debug:
      optimization: none
      symbols: true
    release:
      optimization: full

# Assemble the final product structure
staging:
  name: game_engine
  destination: "${out_dir}/staging"
  use_symlinks: true

  contents:
    # Root level files
    - folder: "."
      files: ["README.md"]

    # Binaries - executables and shared libraries together
    - folder: bin
      targets:
        - mygame
        - engine_core
        - engine_platform
        - engine_renderer
        - engine_audio

    # Data folder with subfolders
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

# Package for distribution
install:
  - name: game_engine_release
    staging: game_engine
    destination: "${out_dir}/dist"
    format: zip
    filename: "${project_name}-${project_version}-${platform}-${arch}"
```

- **`workspace.modules`** lists each module's directory. A module is a directory with its own
  `buildy.yaml`. An entry can be filtered (`- linux: [platform/linux]`) to build a module on one
  platform only.
- **`environment`** reaches every module: each module's settings add to the root's. A module
  that sets its own `defines` keeps the root's standard and warnings.
- **No toolchain is named**: each platform gets its default, GCC on Linux, MSVC on Windows and
  Apple Clang on macOS (`environment.toolchains` would choose others, per platform).
- **`variables`** are `${...}` values. `THIRD_PARTY_ROOT` is imported from the environment, with a
  default.

buildy runs from any directory of the workspace: in `src/game`, `buildy` builds the whole
workspace, the same as from the root.

## Step 2: The Modules

### Core library

```yaml
# src/core/buildy.yaml
project:
  name: engine_core
  description: "Core engine library - types, math, memory, logging"

variables:
  MODULE_NAME: "engine_core"

targets:
  shared_libraries:
    - name: engine_core
      language: cpp
      sources:
        - "src/*.cpp"
      include_dirs: ["include"]
      defines:
        - "ENGINE_CORE_EXPORTS"
        # Configuration-specific logging
        - debug:
            - "ENGINE_ENABLE_LOGGING=1"
            - "ENGINE_ENABLE_ASSERTS=1"
```

A shared library with no internal dependencies. Its `defines` add `ENGINE_ENABLE_LOGGING=1` in a
debug build only.

### Platform layer

```yaml
# src/platform/buildy.yaml
project:
  name: engine_platform
  description: "Platform abstraction layer - window, input, filesystem"

variables:
  MODULE_NAME: "engine_platform"

targets:
  shared_libraries:
    - name: engine_platform
      language: cpp
      sources:
        # Common sources (all platforms)
        - "src/common/*.cpp"
        # Platform-specific sources using filtered list syntax
        - linux:
            - "src/linux/*.cpp"
        - windows:
            - "src/windows/*.cpp"
        - macos:
            - "src/macos/*.cpp"
      include_dirs: ["include", "../core/include"]
      libs:
        - engine_core
      defines:
        - "ENGINE_PLATFORM_EXPORTS"
        # Platform-specific defines
        - linux:
            - "ENGINE_PLATFORM_LINUX"
        - windows:
            - "ENGINE_PLATFORM_WINDOWS"
        - macos:
            - "ENGINE_PLATFORM_MACOS"
```

`sources` and `defines` are filtered lists: the plain items apply everywhere, the items under
`linux:` only when building for Linux, and so on. A Linux build compiles `src/common/` and
`src/linux/`; a Windows build `src/common/` and `src/windows/`.

`libs: [engine_core]` names a target of another module. buildy links it and builds it first;
target names are unique in a workspace, so no path is needed.

### Renderer and audio

```yaml
# src/renderer/buildy.yaml
project:
  name: engine_renderer
  description: "Rendering engine - graphics, shaders, materials"

variables:
  MODULE_NAME: "engine_renderer"

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
      defines:
        - "ENGINE_RENDERER_EXPORTS"
        # Debug rendering features
        - debug:
            - "ENGINE_RENDERER_DEBUG_DRAW=1"
```

```yaml
# src/audio/buildy.yaml
project:
  name: engine_audio
  description: "Audio engine - sounds, music, 3D audio, effects"

variables:
  MODULE_NAME: "engine_audio"

targets:
  shared_libraries:
    - name: engine_audio
      language: cpp
      sources:
        - "src/*.cpp"
      include_dirs: ["include", "../core/include"]
      libs:
        - engine_core
      defines:
        - "ENGINE_AUDIO_EXPORTS"
        # Debug audio features
        - debug:
            - "ENGINE_AUDIO_DEBUG=1"
```

### The game

```yaml
# src/game/buildy.yaml
project:
  name: game
  description: "Game executable"

variables:
  GAME_NAME: "MyGame"

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
      
      # deps: External dependencies (provide include_dirs, lib_dirs, libs, defines)
      deps:
        - glfw3
      
      # libs: Internal libraries to link against (also establishes build order)
      libs:
        - engine_renderer
        - engine_audio
        - engine_platform
        - engine_core

      defines:
        - debug:
            - "GAME_DEBUG=1"
```

Two kinds of dependency:

- `libs`: libraries of the workspace (or system libraries by name), linked in this order.
- `deps`: dependencies defined in `buildy_config/`. Each adds its include directories, library
  directories, libraries and defines to the target.

### Shaders

```yaml
# assets/shaders/buildy.yaml
project:
  name: engine_shaders
  description: "GLSL shaders compiled to SPIR-V"

# Transform is a top-level section under artifacts, not under targets
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

A transform runs a tool on each input: here `glslc` from the built-in `glslc` toolchain, once per
shader, into `build/<platform>-<arch>-<config>/shaders/`. Like a compile, each transform is cached
by the content of its input.

## Step 3: Dependencies

`buildy_config/dependencies.yaml` defines system dependencies:

```yaml
# buildy_config/dependencies.yaml
# External dependencies for game_engine project
# 
# Dependencies can be defined here or in separate files under dependencies/
# See dependencies/glfw3.yaml for an example of a separate dependency file
#
# Platform filtering: Dependencies only have settings for platforms where they
# apply. If a dependency has no section for the current platform, it provides
# no include_dirs, libs, etc. (effectively a no-op for that platform).

zlib:
  description: "zlib compression library"
  linux:
    include_dirs: ["/usr/include"]
    lib_dirs: ["/usr/lib/x86_64-linux-gnu"]
    libs: ["z"]
  linux-arm64:
    lib_dirs: ["/usr/lib/aarch64-linux-gnu"]
  macos:
    pkg_config: zlib
  windows:
    root: "${THIRD_PARTY_ROOT}/zlib"
    include_dirs: ["${root}/include"]
    lib_dirs: ["${root}/lib"]
    libs: ["zlibstatic"]

pthread:
  description: "POSIX threads (Linux only)"
  linux:
    libs: ["pthread"]

dl:
  description: "Dynamic linking library (Linux only)"
  linux:
    libs: ["dl"]

winsock:
  description: "Windows sockets (Windows only)"
  windows:
    libs: ["ws2_32"]

cocoa:
  description: "macOS Cocoa frameworks (macOS only)"
  macos:
    frameworks: ["Cocoa", "IOKit"]
```

Each has settings per platform (`linux`, `windows`, `macos`, `linux-arm64`, or `common` for all). A
dependency gives nothing on a platform it has no section for. No target of this example uses
these: they show the forms. A dependency no target uses is never looked up.

Every file in `buildy_config/dependencies/` is loaded too. `glfw3.yaml` defines GLFW:

```yaml
# buildy_config/dependencies/glfw3.yaml
# GLFW - OpenGL/Vulkan window and input library
# https://www.glfw.org/
#
# This example demonstrates fetching and building GLFW from source
# using a custom buildy script on Linux and Windows.
# macOS uses the system-installed GLFW via Homebrew.

glfw3:
  description: "GLFW - OpenGL window and input library"
  version: "3.4"
  
  common:
    defines: ["GLFW_ENABLED"]
  
  linux:
    url: "https://github.com/glfw/glfw/releases/download/3.4/glfw-3.4.zip"
    checksum: "sha256:b5ec004b2712fd08e8861dc271428f048775200a2df719ccf575143ba749a3e9"
    build:
      system: buildy
      override: glfw3_build.yaml
    # Headers are in the fetched source, library is in the dep's build output
    include_dirs: ["${dep_dir}/include"]
    lib_dirs: ["${dep_dir}/build/linux-x86_64-${config}/lib"]
    libs: ["glfw3"]
    defines: ["GLFW_EXPOSE_NATIVE_X11"]
  
  windows:
    url: "https://github.com/glfw/glfw/releases/download/3.4/glfw-3.4.zip"
    checksum: "sha256:b5ec004b2712fd08e8861dc271428f048775200a2df719ccf575143ba749a3e9"
    build:
      system: buildy
      override: glfw3_build.yaml
    # Headers are in the fetched source, library is in the dep's build output
    include_dirs: ["${dep_dir}/include"]
    lib_dirs: ["${dep_dir}/build/windows-x86_64-${config}/lib"]
    libs: ["glfw3"]
    defines: ["GLFW_EXPOSE_NATIVE_WIN32"]
  
  macos:
    root: "/opt/homebrew"
    include_dirs: ["${root}/include"]
    lib_dirs: ["${root}/lib"]
    libs: ["glfw"]
    frameworks: ["Cocoa", "IOKit", "CoreVideo", "OpenGL"]
    defines: ["GLFW_EXPOSE_NATIVE_COCOA"]
```

On Linux and Windows, buildy downloads the GLFW release archive (checking its SHA-256), extracts it
to `.buildy_cache/deps/glfw3/`, and builds it with `build.system: buildy`: buildy builds the
dependency with the config named by `override`, for the configuration being built. On macOS it
uses Homebrew's GLFW. `${dep_dir}` is where the dependency was extracted.

`glfw3_build.yaml` is an ordinary buildy config, run in the extracted source:

```yaml
# buildy_config/dependencies/glfw3_build.yaml
# Custom buildy build script for GLFW 3.4
# Builds GLFW as a static library without requiring CMake
#
# GLFW source structure:
#   src/       - Core source files
#   include/   - Public headers (GLFW/)
#
# Output:
#   Library is built to the workspace build output directory.
#   Headers are used directly from the fetched source.

project:
  name: glfw3
  description: "GLFW static library build"

environment:
  compile:
    c_standard: "c11"
    warnings: default
  
  configurations:
    debug:
      optimization: none
      symbols: true
    release:
      optimization: speed

targets:
  static_libraries:
    - name: glfw3
      language: c
      
      sources:
        # Common sources (all platforms)
        - "src/context.c"
        - "src/init.c"
        - "src/input.c"
        - "src/monitor.c"
        - "src/platform.c"
        - "src/vulkan.c"
        - "src/window.c"
        - "src/egl_context.c"
        - "src/osmesa_context.c"
        - "src/null_init.c"
        - "src/null_monitor.c"
        - "src/null_window.c"
        - "src/null_joystick.c"
        
        # Linux-specific sources
        - linux:
            - "src/posix_time.c"
            - "src/posix_thread.c"
            - "src/posix_module.c"
            - "src/posix_poll.c"
            - "src/linux_joystick.c"
            - "src/x11_init.c"
            - "src/x11_monitor.c"
            - "src/x11_window.c"
            - "src/xkb_unicode.c"
            - "src/glx_context.c"
        
        # Windows-specific sources
        - windows:
            - "src/win32_init.c"
            - "src/win32_joystick.c"
            - "src/win32_module.c"
            - "src/win32_monitor.c"
            - "src/win32_thread.c"
            - "src/win32_time.c"
            - "src/win32_window.c"
            - "src/wgl_context.c"

      defines:
        # Linux platform defines
        - linux:
            - "_GLFW_X11"
            - "_POSIX_C_SOURCE=200809L"
        # Windows platform defines
        - windows:
            - "_GLFW_WIN32"
            - "_CRT_SECURE_NO_WARNINGS"
            - "UNICODE"
            - "_UNICODE"
        # Configuration-specific defines
        - debug:
            - "_DEBUG"
        - release:
            - "NDEBUG"
      
      include_dirs:
        - "include"
        - "src"
```

The dependency is fetched once and built once per configuration; later builds use it offline, and
skip its build until something about it changes.

## Step 4: Build the Project

```bash
cd examples/game_engine
buildy
```

The first build downloads and builds GLFW, then builds the workspace. Task generation sorts the
tasks by what they need; each task starts as soon as the tasks it depends on have finished:

```
engine_core                  (no dependencies)
├── engine_platform          (core)
├── engine_audio             (core)
└── engine_renderer          (core, platform)
    └── mygame               (all four, and GLFW)
shaders                      (nothing: compiled alongside)
```

Compiles don't wait for libraries; only links do. Run it:

```bash
./build/linux-x86_64-debug/bin/mygame
```

`buildy -n 3` shows more of what happens: the toolchain of each module, the dependency's commit or
archive, and why a task runs.

## Step 5: Staging and the Package

The root's `staging` section assembles a directory with the game, its libraries, this example's
README and the shaders:

```
build/linux-x86_64-debug/staging/
├── README.md
├── bin/
│   ├── mygame
│   ├── libengine_audio.so
│   ├── libengine_core.so
│   ├── libengine_platform.so
│   └── libengine_renderer.so
└── data/
    └── shaders/
        ├── basic.frag.spv
        ├── basic.vert.spv
        └── ...
```

With `use_symlinks: true`, the entries are symlinks to the build outputs (copies on Windows).
`data/textures` and `data/meshes` stay empty: the example has no `.png` or `.mesh` files.

The `install` section packs the staging directory into
`build/linux-x86_64-debug/dist/game_engine-1.0.0-linux-x86_64.zip`, the same bytes for the same
files. `buildy -r` builds the release configuration and its zip in `build/linux-x86_64-release/`.

## Step 6: Other Platforms

The same files build on Windows (MSVC) and macOS (Apple Clang): the filtered lists pick each
platform's sources and defines, and the toolchain turns `optimization`, `warnings` and `symbols`
into its own flags.

## Key Takeaways

- **Workspace**: `workspace.modules` in the root lists the modules; each has its own `buildy.yaml`
- **Shared settings**: the root's `environment` reaches every module
- **Linking across modules**: `libs` names targets of any module; buildy orders the build
- **Filtered lists**: `- linux: [...]` items apply to one platform (or architecture, configuration
  or toolchain)
- **Dependencies**: defined in `buildy_config/`, used by targets through `deps`
- **Transforms**: run a tool per file, cached like compiles
- **Staging and install**: assemble the product, then pack it

## Next Steps

- [Advanced Tutorial](TUTORIAL_ADVANCED.md) - Fetched dependencies and Docker builds
- [User Guide](USER_GUIDE.md) - Every feature, with examples
- [DSL Specification](DSL_SPEC.md) - Every key
