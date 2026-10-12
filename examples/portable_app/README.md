# Portable app example

A C++ program that uses two fetched dependencies, built in Docker:

- **SDL2**, cloned from git at `release-2.28.5` and built with its own `configure` script (the
  `autoconf` build system) in the `gcc:13` image, as a static library.
- **nlohmann/json**, a header-only library: a release archive, checked against its SHA-256.

The program itself is compiled and linked in the same image (the `docker-cpp-linux` toolchain), so
the build needs nothing on the host but Docker and git. Each command runs as your user, with the
workspace mounted at its own path.

## Needs

- Linux on x86-64, with Docker (your user can run `docker`) and git
- The network, the first time: SDL2 and the JSON archive are fetched once, then used offline

## Build

```bash
cd examples/portable_app
buildy
./build/linux-x86_64-debug/bin/portable_app
```

The first build clones SDL2 and builds it (about half a minute); the next ones reuse it.

```bash
buildy -r              # release: SDL2 is built again for it, once
buildy --force         # ignore the cache: rebuild the program and SDL2
buildy --update-deps   # fetch SDL2's ref again (a branch or tag can move)
```

## What a build makes

1. SDL2 for the configuration, in `.buildy_cache/deps/sdl2/_install/linux-x86_64-<config>/`
2. The `portable_app` executable, in `build/linux-x86_64-<config>/bin/`
3. A staging directory with the executable and this README, in `build/linux-x86_64-<config>/staging/`
4. `portable_app-1.0.0-linux-x86_64.tar.gz` of it, in `build/linux-x86_64-<config>/dist/`
