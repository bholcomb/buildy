# Portable App Example

This example demonstrates buildy's advanced features:

- **Fetch Dependencies**: Automatically downloads and builds external libraries
- **Docker Builds**: Uses Docker containers for reproducible builds
- **Build Systems**: Integrates with CMake to build SDL2

## Dependencies

- **SDL2**: Fetched from git, built with CMake
- **nlohmann/json**: Header-only library fetched from URL

## Building

```bash
cd examples/portable_app
buildy
```

### Build Options

```bash
# Release build
buildy -c release

# Force rebuild (ignore cache)
buildy --force

# Generate lockfile
buildy --update-lock
```

## Docker Requirements

This example uses Docker for reproducible builds. Ensure Docker is installed
and your user has permission to run Docker commands.

The Docker configuration:
- Uses `gcc:13` image
- Mounts current directory to `/workspace`
- Runs as your user (avoids permission issues)

## What Gets Built

1. SDL2 static library (fetched and built via CMake)
2. The portable_app executable linking against SDL2
3. Staging area with the executable
4. Distribution archive (tar.gz)
