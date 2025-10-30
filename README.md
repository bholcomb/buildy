# Buildy - Task-Based Build System

A modern, cache-aware build system with support for multiple toolchains, parallel execution, and universal build templates.

## Features

- **Content-Addressable Caching**: Git-style sharded cache for fast incremental builds
- **Header Dependency Tracking**: Automatic rebuild when header files change
- **Parallel Execution**: Resource-aware task scheduling
- **Multiple Toolchains**: Support for GCC, Clang, MSVC, Emscripten, and more
- **Universal Build Templates**: Data-driven build patterns for libraries, executables, shaders, and textures
- **Hierarchical Variables**: Flexible variable system with provenance tracking
- **Execution Environments**: Native, Docker, and WSL support

## Quick Start

### Setup Virtual Environment

```bash
# Create virtual environment
python3 -m venv venv

# Activate virtual environment
source venv/bin/activate  # On Linux/macOS
# or
venv\Scripts\activate  # On Windows

# Install dependencies
pip install -r requirements.txt
```

### Build a Project

```bash
# Build with default settings (auto-detected toolchain)
python3 buildy.py examples/math/simple_project.yaml

# Build with specific toolchain
python3 buildy.py --toolchain gcc-linux examples/math/simple_project.yaml

# Build with custom variables
python3 buildy.py -D MY_VAR=value examples/math/simple_project.yaml

# Dry run (show what would be built)
python3 buildy.py --dry-run examples/math/simple_project.yaml

# Verbose output
python3 buildy.py -v examples/math/simple_project.yaml
```

### List Available Toolchains

```bash
python3 buildy.py --list-toolchains
```

### View Cache Statistics

```bash
python3 buildy.py --cache-stats
```

## Running Tests

```bash
# Run all tests
pytest tests/ -v

# Run with coverage
pytest tests/ --cov=buildy_lib --cov-report=html

# Run specific test file
pytest tests/test_cache.py -v
```

## Project Structure

```
buildy/
├── buildy.py                    # Main entry point
├── buildy_lib/                  # Core library modules
│   ├── __init__.py
│   ├── cache.py                 # Build cache with header tracking
│   ├── config_parser.py         # YAML config parser
│   ├── constants.py             # System constants
│   ├── execution.py             # Execution environments
│   ├── executor.py              # Task executor
│   ├── models.py                # Data models
│   ├── task_graph.py            # Dependency graph
│   ├── template_engine.py       # Build template engine
│   ├── toolchain.py             # Toolchain system
│   └── variables.py             # Variable environment
├── buildy_templates.yaml        # Universal build templates
├── toolchains/                  # Toolchain configurations
│   ├── gcc-linux.yaml
│   ├── clang-linux.yaml
│   ├── msvc-windows.yaml
│   └── ...
├── examples/                    # Example projects
│   └── math/
│       └── simple_project.yaml
├── tests/                       # Test suite
│   ├── conftest.py
│   ├── test_cache.py
│   ├── test_task_graph.py
│   ├── test_toolchain.py
│   ├── test_template_engine.py
│   └── test_variables.py
└── requirements.txt             # Python dependencies
```

## Configuration Example

```yaml
project:
  name: my_project
  version: 1.0.0

toolchain: gcc-linux

variables:
  MY_DEFINE: "VALUE"

output:
  base_dir: build
  pattern: ${base_dir}/${platform}-${arch}-${config}

libraries:
  - name: mylib
    type: shared_library
    sources: "src/*.cpp"
    include_dirs: ["include"]

executables:
  - name: myapp
    sources: "app/*.cpp"
    include_dirs: ["include"]
    depends_on: ["local(mylib)"]

configurations:
  debug:
    defines: ["DEBUG"]
    compiler_flags: ["-g", "-O0"]
  release:
    defines: ["NDEBUG"]
    compiler_flags: ["-O3"]
```

## Command-Line Options

```
usage: buildy.py [-h] [--platform PLATFORM] [--architecture ARCHITECTURE]
                 [--configuration CONFIGURATION] [--cache-dir CACHE_DIR]
                 [--dry-run] [--workers WORKERS] [--cache-stats] [--verbose]
                 [--define VAR=VALUE] [--toolchain NAME] [--list-toolchains]
                 [--toolchains-dir TOOLCHAINS_DIR]
                 [config_files ...]

positional arguments:
  config_files          Build configuration files

options:
  -h, --help            show this help message and exit
  --platform PLATFORM   Target platform (default: linux)
  --architecture ARCHITECTURE
                        Target architecture (default: x86_64)
  --configuration CONFIGURATION
                        Build configuration (default: debug)
  --cache-dir CACHE_DIR
                        Cache directory (default: .buildy_cache)
  --dry-run             Generate tasks but don't execute
  --workers WORKERS     Max parallel workers (default: 4)
  --cache-stats         Show cache statistics
  --verbose, -v         Enable verbose logging
  --define VAR=VALUE, -D VAR=VALUE
                        Define a variable
  --toolchain NAME, -t NAME
                        Specify toolchain to use
  --list-toolchains     List available toolchains and exit
  --toolchains-dir TOOLCHAINS_DIR
                        Directory containing toolchain configurations
```

## Architecture

### Module Responsibilities

- **cache.py**: Content-addressable caching with Git-style sharding and header dependency tracking
- **config_parser.py**: Parse YAML configs, resolve variables, generate tasks using templates
- **execution.py**: Execution environment abstraction (native, Docker, WSL)
- **executor.py**: Parallel task execution with resource-aware scheduling
- **models.py**: Data models (BuildTask, TaskInput, ResourceRequirements)
- **task_graph.py**: Dependency graph with topological sorting
- **template_engine.py**: Expand universal build templates into concrete tasks
- **toolchain.py**: Toolchain management, tool matching, command building
- **variables.py**: Hierarchical variable resolution with provenance tracking

### Build Flow

1. **Parse Config**: Load YAML, resolve variables hierarchically
2. **Select Toolchain**: CLI > Project > Workspace > Auto-detect
3. **Generate Tasks**: Expand templates into concrete build tasks
4. **Build Graph**: Create dependency graph, topological sort
5. **Execute**: Parallel execution with caching and resource management

## License

MIT License (or your preferred license)

