# Buildy - Task-Based Build System Prototype

A prototype implementation of the ideal C++ build system based on Blizzard's task-based architecture.

## Overview

Buildy implements a modern, task-based build system with the following key features:

- **Task-Based Architecture**: Inspired by Blizzard's proven build system design
- **Content-Addressable Caching**: SHA-256 based caching for maximum reuse
- **Parallel Execution**: Automatic parallel task execution with dependency resolution
- **Platform Matrix Support**: Multi-platform, multi-architecture, multi-configuration builds
- **Incremental Builds**: Smart change detection and minimal rebuilds

## Quick Start

1. **Install Dependencies**:
   ```bash
   pip install pyyaml
   ```

2. **Run the Demo**:
   ```bash
   python demo_buildy.py
   ```

3. **Try Manual Commands**:
   ```bash
   # Dry run to see execution plan
   python buildy.py simple_project.yaml --dry-run

   # Generate task graph
   python buildy.py simple_project.yaml --output tasks.json

   # Execute build
   python buildy.py simple_project.yaml

   # Multi-platform example
   python buildy.py game_project.yaml --platform windows --configuration release --dry-run
   ```

## Configuration File Format

### Simple Example:
```yaml
project:
  name: "my-project"
  version: "1.0.0"

config:
  cpp_standard: "c++20"
  defines:
    - "DEBUG=1"
  compiler_flags:
    - "-Wall"
    - "-Wextra"

libraries:
  - name: "core"
    sources: "src/core/*.cpp"

executables:
  - name: "app"
    sources: "src/main.cpp"
    depends_on:
      - "local(core)"
```

### Advanced Multi-Platform Example:
```yaml
project:
  name: "cross-platform-app"
  version: "2.0.0"

config:
  cpp_standard: "c++20"

platforms:
  linux:
    toolchain: "gcc"
    defines: ["PLATFORM_LINUX=1"]
    compiler_flags: ["-fPIC"]

  windows:
    toolchain: "msvc"  
    defines: ["PLATFORM_WINDOWS=1"]

configurations:
  debug:
    optimization: "-O0"
    defines: ["DEBUG=1"]

  release:
    optimization: "-O3"
    defines: ["NDEBUG=1"]

libraries:
  - name: "engine"
    sources: "src/engine/*.cpp"

executables:
  - name: "game"
    sources: "src/main.cpp"
    depends_on: ["local(engine)"]
```

## Command Line Options

```
usage: buildy.py [-h] [--platform PLATFORM] [--architecture ARCHITECTURE]
                 [--configuration CONFIGURATION] [--cache-dir CACHE_DIR]
                 [--output OUTPUT] [--dry-run] [--workers WORKERS]
                 [--cache-stats]
                 config_files [config_files ...]

positional arguments:
  config_files          Build configuration files

optional arguments:
  -h, --help            show this help message and exit
  --platform PLATFORM   Target platform (default: linux)
  --architecture ARCHITECTURE
                        Target architecture (default: x86_64)
  --configuration CONFIGURATION
                        Build configuration (default: debug)
  --cache-dir CACHE_DIR
                        Cache directory (default: .buildy_cache)
  --output OUTPUT       Output task graph to file
  --dry-run             Generate tasks but don't execute
  --workers WORKERS     Max parallel workers (default: 4)
  --cache-stats         Show cache statistics
```

## Architecture

### Task Structure
Each task follows Blizzard's parse/execute model:
- **Parse Phase**: Analyze dependencies and declare inputs/outputs
- **Execute Phase**: Perform the actual work
- **Caching**: Content-addressable cache for result reuse

### Dependency Resolution
- Topological sorting ensures correct execution order
- Parallel execution maximizes CPU utilization
- Incremental builds minimize unnecessary work

### Platform Support
- Configuration inheritance: Global → Platform → Architecture → Configuration
- Smart defaults minimize configuration overhead
- Platform-specific toolchain and flag management

## Files Generated

- **Task Graph**: JSON file containing all tasks and execution plan
- **Build Outputs**: Organized by platform-architecture-configuration
- **Cache Files**: Content-addressable cache for build artifacts
- **Build Reports**: Summary of build results and statistics

## Example Directory Structure

After running a build:

```
project/
├── buildy.py                    # Build system script
├── simple_project.yaml          # Build configuration
├── src/                         # Source files
├── build/                       # Build outputs
│   └── linux-x86_64-debug/
│       ├── lib/                 # Libraries
│       ├── bin/                 # Executables  
│       └── obj/                 # Object files
└── .buildy_cache/               # Build cache
    ├── cache_index.json         # Cache metadata
    └── [hash]/                  # Cached artifacts
```

## Performance Features

- **Content-Addressable Caching**: Reuse identical work across builds
- **Parallel Execution**: Automatic parallelization based on dependencies
- **Incremental Builds**: Only rebuild changed components
- **Resource Management**: CPU and memory aware task scheduling
- **Distributed Ready**: Architecture supports remote execution

## Limitations of Prototype

This is a prototype to demonstrate the concepts. A production system would need:
- More robust error handling and recovery
- Advanced dependency analysis (header scanning)
- Remote execution and distributed caching
- IDE integration and language server
- More build tool integrations
- Comprehensive test coverage

## Design Inspiration

Based on the comprehensive analysis of modern build systems including:
- Blizzard's GDC 2013 presentation on their internal build system
- Bazel's remote execution and caching model  
- Modern task-based build systems (Buck2, Pants)
- Enterprise-scale build requirements

The goal is to create a build system that scales from simple projects to enterprise 
development while maintaining simplicity and excellent developer experience.
