# Tutorial: Fetch Dependencies and Docker Builds

This tutorial covers advanced buildy features: fetching external dependencies from git and URLs, building them with various build systems, and using Docker for reproducible builds.

## What You'll Learn

- Fetching dependencies from git repositories
- Downloading and extracting URL archives
- Configuring build systems (CMake, Meson, etc.)
- Using Docker for dependency builds
- Managing lockfiles for reproducibility
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

  dependency_builds:
    execution:
      type: docker
      image: "gcc:13"
      volumes:
        - "${PWD}:/workspace"
      working_dir: "/workspace"
      user: "${UID}:${GID}"
    default_phases: [configure, build, install]

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
      packages:
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

**Global Docker execution for dependencies:**

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
    default_phases: [configure, build, install]
```

All fetched dependencies build in Docker by default.

## Step 2: Fetch Dependencies

The `buildy_config/dependencies.yaml` defines what to fetch:

```yaml
dependencies:
  fetch:
    - name: sdl2
      git: "https://github.com/libsdl-org/SDL.git"
      ref: "release-2.28.5"
      build_system: cmake
      build_phases: [configure, build, install]
      build_args:
        - "-DSDL_SHARED=OFF"
        - "-DSDL_STATIC=ON"
        - "-DSDL_TEST=OFF"
      execution:
        type: docker
        image: "gcc:13"
        volumes:
          - "${PWD}:/workspace"
        working_dir: "/workspace"
        user: "${UID}:${GID}"

    - name: nlohmann_json
      url: "https://github.com/nlohmann/json/releases/download/v3.11.3/json.tar.xz"
      checksum: "sha256:d6c65aca6b1ed68e7a182f4757f0f7c6c9e4c1b2d69d2f5e2e6b3e8f1a2b3c4d5"
      type: header_only
      include_dirs:
        - "${dest}/include"
```

### Git Dependencies

```yaml
- name: sdl2
  git: "https://github.com/libsdl-org/SDL.git"
  ref: "release-2.28.5"
  build_system: cmake
  build_phases: [configure, build, install]
  build_args:
    - "-DSDL_SHARED=OFF"
    - "-DSDL_STATIC=ON"
```

| Field | Description |
|-------|-------------|
| `name` | Identifier used in packages/depends_on |
| `git` | Repository URL |
| `ref` | Tag, branch, or commit hash |
| `build_system` | `cmake`, `meson`, `make`, `autoconf`, `cargo`, `go_mod`, or `auto` |
| `build_phases` | Phases to run: `configure`, `build`, `test`, `install`, `clean` |
| `build_args` | Extra arguments for the build system |

### URL Dependencies

```yaml
- name: nlohmann_json
  url: "https://github.com/nlohmann/json/releases/download/v3.11.3/json.tar.xz"
  checksum: "sha256:d6c65aca..."
  type: header_only
  include_dirs:
    - "${dest}/include"
```

| Field | Description |
|-------|-------------|
| `url` | Archive URL (`.tar.gz`, `.tar.xz`, `.zip`, `.tar`) |
| `checksum` | SHA256 checksum for verification |
| `type` | `source` (build it) or `header_only` (just extract) |
| `include_dirs` | Include paths (`${dest}` = extraction directory) |

### Header-Only Libraries

For header-only libraries, use `type: header_only`:

```yaml
- name: stb
  url: "https://github.com/nothings/stb/archive/master.tar.gz"
  type: header_only
  include_dirs: ["${dest}"]
```

No build step is needed - the library is just extracted and made available.

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
| `auto` | Detects automatically | Default |

### Build Phases

Each build system supports these phases:

| Phase | Description |
|-------|-------------|
| `configure` | Configure/setup the build |
| `build` | Compile the project |
| `test` | Run tests |
| `install` | Install to prefix |
| `clean` | Clean build artifacts |

Default phases are `configure` and `build`. Add `install` if you need headers/libraries installed to a prefix:

```yaml
build_phases: [configure, build, install]
```

### Build Arguments

Pass arguments to the build system:

```yaml
build_args:
  - "-DBUILD_SHARED_LIBS=OFF"
  - "-DBUILD_TESTING=OFF"
```

For CMake, these become `-D` flags. For other build systems, they're passed appropriately.

## Step 4: Docker Execution

### Global Default

Set a default Docker environment for all dependency builds:

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

### Per-Dependency Override

Override for specific dependencies:

```yaml
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

### Docker Configuration Fields

| Field | Description | Default |
|-------|-------------|---------|
| `type` | `docker` or `native` | `native` |
| `image` | Docker image name | `gcc:13` |
| `volumes` | Volume mounts | `["${PWD}:/workspace"]` |
| `working_dir` | Working directory in container | `/workspace` |
| `user` | User to run as | `${UID}:${GID}` |

### Variable Expansion

These variables are expanded in Docker configuration:

| Variable | Description |
|----------|-------------|
| `${PWD}` | Current working directory |
| `${UID}` | Host user ID |
| `${GID}` | Host group ID |

Using `${UID}:${GID}` ensures files created in the container have correct ownership.

## Step 5: Build the Project

```bash
cd examples/portable_app
buildy
```

The build process:

1. **Fetch SDL2** from git (clones to cache directory)
2. **Build SDL2** with CMake in Docker
3. **Download nlohmann_json** archive
4. **Extract** the header-only library
5. **Compile** the application linking against SDL2

First build downloads and compiles dependencies. Subsequent builds use cached results.

## Step 6: Lockfiles

Generate a lockfile for reproducible builds:

```bash
buildy --update-lock
```

This creates `buildy_config/dependencies.lock`:

```yaml
version: 1
generated: "2026-02-20T10:30:00Z"
platform: linux-x86_64

dependencies:
  sdl2:
    git: "https://github.com/libsdl-org/SDL.git"
    ref: "release-2.28.5"
    commit: "abc123def456789..."
    fetched_at: "2026-02-20T10:30:00Z"

  nlohmann_json:
    url: "https://github.com/nlohmann/json/releases/download/v3.11.3/json.tar.xz"
    checksum: "sha256:d6c65aca..."
```

The lockfile pins:
- Git commits (even for tags/branches)
- URL checksums
- Fetch timestamps

### Using Lockfiles

```bash
buildy --require-lock   # Fail if no lockfile exists
buildy --ignore-lock    # Ignore existing lockfile
buildy --update-lock    # Regenerate lockfile
```

In CI, use `--require-lock` to ensure reproducible builds.

## Step 7: Native vs Docker Execution

### Native Execution

For local development without Docker:

```yaml
environment:
  toolchains:
    default: gcc-cpp-linux

  dependency_builds:
    execution:
      type: native
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

## Step 8: Custom Build Systems

For projects with non-standard build systems, create custom definitions:

```yaml
# buildy_config/build_systems/ninja.yaml
build_system:
  name: ninja
  description: "Ninja build system"

  detection:
    marker_files: ["build.ninja"]
    priority: 10

  phases:
    build:
      command: "ninja -j {jobs}"
      working_dir: "{source_dir}"
      required: true

    clean:
      command: "ninja clean"
      working_dir: "{source_dir}"
```

### Phase Variables

| Variable | Description |
|----------|-------------|
| `{source_dir}` | Source directory |
| `{build_dir}` | Build output directory |
| `{install_dir}` | Installation prefix |
| `{jobs}` | Parallel job count |
| `{args}` | Combined build arguments |

## Step 9: Troubleshooting

### Dependency Build Failures

Check the build output in the cache:

```bash
ls .buildy_cache/deps/sdl2/
```

### Docker Issues

Verify Docker is working:

```bash
docker run --rm gcc:13 gcc --version
```

Ensure your user can run Docker without sudo.

### Cache Problems

Force a clean rebuild:

```bash
buildy --clean
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
fetch:
  - name: mylib                     # Required: identifier
    git: "https://..."              # Git URL (or use url:)
    url: "https://..."              # Archive URL (or use git:)
    ref: "v1.0.0"                   # Git ref: tag, branch, commit
    checksum: "sha256:..."          # SHA256 for URL downloads
    dest: "${cache_dir}/custom"     # Custom destination
    type: source                    # source | header_only
    build_system: cmake             # auto | cmake | meson | make | ...
    build_phases: [configure, build]
    build_args: ["-DFOO=ON"]
    include_dirs: ["${dest}/include"]
    execution:                      # Per-dependency Docker config
      type: docker
      image: "gcc:13"
      volumes: ["${PWD}:/workspace"]
      working_dir: "/workspace"
      user: "${UID}:${GID}"
```

## Key Takeaways

- **Git dependencies**: Specify `git:` and `ref:` for version control
- **URL dependencies**: Use `url:` with `checksum:` for verification
- **Header-only**: Set `type: header_only` to skip building
- **Build systems**: Buildy auto-detects or specify explicitly
- **Docker execution**: Configure at global or per-dependency level
- **Lockfiles**: Use `--update-lock` for reproducibility

## Next Steps

- [User Guide](USER_GUIDE.md) - Complete feature reference
- [DSL Specification](DSL_SPEC.md) - Full field documentation
