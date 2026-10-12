# Tutorial: Fetch Dependencies and Docker Builds

This tutorial covers advanced buildy features: fetching external dependencies from git and URLs, building them with various build systems, and using Docker for reproducible builds.

## What You'll Learn

- Fetching dependencies from git repositories
- Downloading and extracting URL archives
- Configuring build systems (CMake, Meson, etc.)
- Using Docker for dependency builds
- Keeping fetched dependencies reproducible, and updating them
- Header-only library handling

## The Example Project

We'll use the `examples/portable_app` project:

```
portable_app/
├── buildy.yaml
├── src/
│   └── main.cpp
├── buildy_config/
│   └── dependencies.yaml
└── README.md
```

This project builds a simple SDL2 application, fetching SDL2 from git and a JSON library from a URL.

## Step 1: The Root Configuration

```yaml
project:
  name: portable_app
  version: "1.0.0"
  description: "Example demonstrating fetch dependencies and Docker builds"

variables:
  APP_NAME: "PortableApp"

dependencies:
  file: buildy_config/dependencies.yaml

environment:
  toolchains:
    default: docker-cpp-linux
    windows: msvc-cpp-windows
    macos: clang-cpp-macos

  compile:
    cpp_standard: "c++17"
    warnings: extra

  configurations:
    debug:
      optimization: none
      symbols: true
      defines: ["DEBUG=1"]
    release:
      optimization: speed
      defines: ["NDEBUG=1"]

targets:
  executables:
    - name: portable_app
      language: cpp
      sources: ["src/*.cpp"]
      include_dirs: ["src"]
      deps:
        - sdl2
        - nlohmann_json
      defines:
        - "APP_NAME=\"${APP_NAME}\""

staging:
  name: portable_app
  destination: "${out_dir}/staging"
  use_symlinks: true

  contents:
    - folder: "."
      files: ["README.md"]

    - folder: bin
      targets:
        - portable_app

install:
  - name: portable_app_release
    staging: portable_app
    destination: "${out_dir}/dist"
    format: tar.gz
    filename: "${project_name}-${project_version}-${platform}-${arch}"
```

### Key Configuration Points

**Docker toolchain:**

```yaml
environment:
  toolchains:
    default: docker-cpp-linux
```

Uses the Docker-based C++ toolchain for project builds.

**Docker execution for a dependency:** each fetched dependency that should build in Docker says
so in its own `execution` section (Step 2).

## Step 2: Fetch Dependencies

The `buildy_config/dependencies.yaml` defines what to fetch. Each top-level key is a dependency:

```yaml
sdl2:
  description: "Simple DirectMedia Layer"
  version: "2.28.5"
  common:
    git: "https://github.com/libsdl-org/SDL.git"
    ref: "release-2.28.5"
    build:
      system: cmake
      args:
        - "-DSDL_SHARED=OFF"
        - "-DSDL_STATIC=ON"
        - "-DSDL_TEST=OFF"
    include_dirs: ["${dep_install_dir}/include"]
    lib_dirs: ["${dep_install_dir}/lib"]
    libs: ["SDL2"]
    execution:
      type: docker
      image: "gcc:13"
      user: "${UID}:${GID}"

nlohmann_json:
  description: "JSON for Modern C++"
  version: "3.11.3"
  common:
    url: "https://github.com/nlohmann/json/releases/download/v3.11.3/json.tar.xz"
    checksum: "sha256:d6c65aca6b1ed68e7a182f4757f0f7c6c9e4c1b2d69d2f5e2e6b3e8f1a2b3c4d5"
    include_dirs: ["${dep_dir}/include"]
```

A dependency with `git:` or `url:` is fetched into `.buildy_cache/deps/<name>/`, which is
`${dep_dir}`.

### Git Dependencies

| Field | Description |
|-------|-------------|
| `git` | Repository URL |
| `ref` | A branch, a tag, or a full or abbreviated commit ID (default: the default branch) |
| `build.system` | `cmake`, `meson`, `make`, `autoconf`, `cargo`, `go_mod`, `buildy`, `auto` or `none` |
| `build.args` | Extra arguments for the build system, each one argument; `${config}` and the other dependency variables are resolved |
| `build.configurations` | Which of the build system's configurations a buildy configuration it doesn't know is |

The repository is fetched the first time, and checked out at the commit `ref` names. After that the
clone is used as it is, without the network, until the `git` URL or the `ref` changes: builds are
reproducible and work offline.

### URL Dependencies

| Field | Description |
|-------|-------------|
| `url` | Archive URL (`.tar.gz`, `.tgz`, `.tar`, `.zip`) |
| `checksum` | `sha256:<hash>` of the archive; recommended |
| `include_dirs` | Include paths (`${dep_dir}` = extraction directory) |

The archive is downloaded once (`.buildy_cache/downloads/`) and extracted once. A download fails
when the server can't be reached within 30 s or sends nothing for 60 s.

### Header-Only Libraries

A dependency without a `build` section is only fetched:

```yaml
stb:
  common:
    url: "https://github.com/nothings/stb/archive/master.tar.gz"
    include_dirs: ["${dep_dir}"]
```

## Step 3: Build Systems

Buildy supports multiple build systems for fetched dependencies:

| Build System | Detection | Example |
|--------------|-----------|---------|
| `cmake` | CMakeLists.txt | Most C/C++ libraries |
| `meson` | meson.build | GNOME libraries |
| `make` | Makefile | Traditional Unix projects |
| `autoconf` | configure, configure.ac | GNU projects |
| `cargo` | Cargo.toml | Rust crates |
| `go_mod` | go.mod | Go modules |
| `buildy` | buildy.yaml | Buildy itself, with an override config |
| your own | its `detection.marker_files` | [Step 8](#step-8-build-system-definitions) |
| `auto` | Detects automatically; two at the same priority are an error | |

### One Configuration at a Time

A dependency is built for the configuration you build: `buildy` builds SDL2 as cmake's `Debug`,
`buildy -c release` as `Release`, each in its own directory:

- `${dep_build_dir}`: `.buildy_cache/deps/sdl2/_build/<platform>-<arch>-<config>/`
- `${dep_install_dir}`: `.buildy_cache/deps/sdl2/_install/<platform>-<arch>-<config>/`

cmake, meson and autoconf install into `${dep_install_dir}`, and its `include/` and `lib/` are added
to the dependency's dirs anyway. A configuration of your own needs a mapping for a build system
that has configurations:

```yaml
    build:
      system: cmake
      configurations: {portable: Release}
```

### Rebuilds

Each finished build is recorded. The next build skips the dependency while its commit or archive,
build settings, the programs its build system runs and the environment variables it reads are the
same. Change an argument and the dependency is built again, from empty build directories.
`buildy --force` rebuilds every dependency.

### Build Arguments

Pass arguments to the build system:

```yaml
    build:
      system: cmake
      args:
        - "-DBUILD_SHARED_LIBS=OFF"
        - "-DBUILD_TESTING=OFF"
```

For CMake, these are added to the configure command. For make, they are added to the `make`
command line (`CFLAGS=-O2`).

## Step 4: Docker Execution

### Per-Dependency Execution

A dependency builds in Docker when its definition has an `execution` section:

```yaml
complex_lib:
  common:
    git: "https://github.com/example/lib.git"
    build:
      system: cmake
    execution:
      type: docker
      image: "ubuntu:22.04"
```

### Docker Configuration Fields

| Field | Description | Default |
|-------|-------------|---------|
| `type` | `docker` or `native` | `native` |
| `image` | Docker image name | `gcc:13` |
| `volumes` | Extra volume mounts | none |
| `user` | User to run as | `${UID}:${GID}` |

### Variable Expansion

These variables are expanded in Docker configuration:

| Variable | Description |
|----------|-------------|
| `${UID}` | Host user ID |
| `${GID}` | Host group ID |

Using `${UID}:${GID}` ensures files created in the container have correct ownership.

The workspace root, and the cache directory if it lies elsewhere, are mounted at their host paths,
and commands run in the workspace root (a dependency's build phases in its source directory), so the
absolute paths in commands are the same inside the container as outside.

## Step 5: Build the Project

```bash
cd examples/portable_app
buildy
```

The build process:

1. **Fetch SDL2** from git (clones to cache directory)
2. **Build SDL2** with CMake in Docker, for the debug configuration, and install it
3. **Download nlohmann_json** archive
4. **Extract** the header-only library
5. **Compile** the application linking against SDL2

The first build downloads and builds the dependencies. Later builds fetch and build nothing of
theirs; `buildy -c release` builds SDL2's release build once.

## Step 6: Updating and Pinning

A fetched dependency stays at the commit or archive first fetched. To move a branch or a tag to
what it names now, and to download archives without a checksum again:

```bash
buildy --update-deps
```

With `-n 3`, buildy logs the commit each git dependency is at. To pin a dependency across
machines, give a full commit ID as `ref`, or a `checksum` for an archive. There is no lockfile yet.

## Step 7: Native vs Docker Execution

### Native Execution

For local development without Docker:

```yaml
environment:
  toolchains:
    default: gcc-cpp-linux

```

### Docker Toolchains

Use Docker for project compilation (not just dependencies):

```yaml
environment:
  toolchains:
    default: docker-cpp-linux
```

Built-in Docker toolchains:
- `docker-c-linux` - GCC C compiler in Docker
- `docker-cpp-linux` - GCC C++ compiler in Docker

## Step 8: Build System Definitions

Each build system is a YAML definition: its detection files, how buildy's configurations map to
its own, the environment variables it reads, its phases (`clean` for an in-source build system,
`configure`, `build`, `install`) and where its output lands. The built-in ones are in buildy; a
library built by something else gets a definition of your own in `buildy_config/build_systems/`:

```yaml
# buildy_config/build_systems/build_script.yaml
build_system:
  name: build_script
  description: "a build.sh that takes the build type and a prefix"
  detection:
    marker_files: [build.sh]
    priority: 10
  configurations:
    debug: debug
    release: release
  phases:
    build:
      command: "sh ${source_dir}/build.sh ${build_type} ${install_dir} ${args}"
      working_dir: "${build_dir}"
  output:
    include_patterns: ["${install_dir}/include"]
    lib_patterns: ["${install_dir}/lib"]
```

```yaml
# buildy_config/dependencies.yaml
mylib:
  git: "https://example.com/mylib.git"
  ref: "v2.0"
  build:
    system: build_script   # or auto: build.sh selects it
  libs: [mylib]
```

Each configuration builds into its own install directory, and editing the definition rebuilds the
dependency. A definition with a built-in's name (`make.yaml` with `name: make`) replaces the
built-in one; buildy logs it at `-n 3`. See
[User Guide, Custom Build Systems](USER_GUIDE.md#custom-build-systems) and
[DSL Spec, Build Systems](DSL_SPEC.md#12-build-systems).

## Step 9: Troubleshooting

### Dependency Build Failures

A failed phase shows its command and output. The build directory is kept for a look:

```bash
ls .buildy_cache/deps/sdl2/_build/
```

The next build builds the dependency again from scratch.

### Docker Issues

Verify Docker is working:

```bash
docker run --rm gcc:13 gcc --version
```

Ensure your user can run Docker without sudo.

### Cache Problems

Force a rebuild of everything, dependencies included:

```bash
buildy --force
```

### Checksum Mismatches

If a URL archive's checksum doesn't match:

1. Download manually and compute checksum:
   ```bash
   curl -L URL | sha256sum
   ```
2. Update the checksum in dependencies.yaml

## Complete Fetch Dependency Reference

```yaml
mylib:
  description: "My library"
  version: "1.0.0"
  common:                           # or a platform: linux, windows, macos, linux-arm64, ...
    git: "https://..."              # Git URL (or use url:)
    ref: "v1.0.0"                   # Branch, tag, or full or abbreviated commit ID
    url: "https://..."              # Archive URL (or use git:)
    checksum: "sha256:..."          # SHA256 for URL downloads
    build:
      system: cmake                 # auto | cmake | meson | make | autoconf | cargo | go_mod | buildy | none
      args: ["-DFOO=ON"]            # Variables resolved: ${config}, ${dep_dir}, ...
      configurations: {portable: Release}
      override: mylib_build.yaml    # system buildy: a config in buildy_config/dependencies/
    include_dirs: ["${dep_install_dir}/include"]
    lib_dirs: ["${dep_install_dir}/lib"]
    libs: ["mylib"]
    execution:                      # Per-dependency Docker config
      type: docker
      image: "gcc:13"
      user: "${UID}:${GID}"
```

## Key Takeaways

- **Git dependencies**: `git:` and `ref:`; fetched once, then offline
- **URL dependencies**: `url:` with `checksum:` for verification
- **Header-only**: no `build` section
- **Build systems**: built for the configuration you build, once, and again only when something changed
- **Docker execution**: configure per dependency, or use a Docker toolchain for the project
- **Updating**: `--update-deps` moves branches and tags; a full commit ID pins

## Next Steps

- [User Guide](USER_GUIDE.md) - Complete feature reference
- [DSL Specification](DSL_SPEC.md) - Full field documentation
