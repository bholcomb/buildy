# Game engine example

A workspace of six modules that build a small game engine (stubs) and a game that uses it:

| Module | What it builds |
|--------|----------------|
| `src/core` | `engine_core`, a shared library |
| `src/platform` | `engine_platform`: common sources, plus Linux or Windows ones (filtered lists) |
| `src/renderer`, `src/audio` | `engine_renderer`, `engine_audio`, linked to the libraries above |
| `assets/shaders` | GLSL shaders compiled to SPIR-V with `glslc` (transforms) |
| `src/game` | `mygame`, the executable, which uses the fetched GLFW dependency |

On Linux and Windows, GLFW 3.4 is downloaded (with a checksum) and built by buildy itself, from
`buildy_config/dependencies/glfw3_build.yaml`. On macOS it is Homebrew's.

The root `buildy.yaml` stages the game, its libraries, this README and the shaders into
`build/<platform>-<arch>-<config>/staging/`, and packs that into a zip in `dist/`.

## Needs

- A C and C++ compiler: GCC on Linux, MSVC on Windows, Apple Clang on macOS
- On Linux, the X11 headers GLFW's X11 backend includes (Debian and Ubuntu: `libx11-dev
  libxrandr-dev libxinerama-dev libxcursor-dev libxi-dev`)
- `glslc` on the `PATH` (from the Vulkan SDK, or a `glslc` or `shaderc` package)
- On macOS, GLFW from Homebrew: `brew install glfw`
- The network, the first time (GLFW is fetched once, then used offline)

## Build

```bash
cd examples/game_engine
buildy
./build/linux-x86_64-debug/bin/mygame
```

`buildy -r` builds the release configuration into `build/linux-x86_64-release/`.
