# Buildy Toolchain System

Buildy uses a flexible toolchain abstraction system that separates build logic from compiler-specific details. This allows you to build the same project with different compilers (GCC, Clang, MSVC) or even in different environments (Docker, WSL).

## Available Toolchains

List all available toolchains:
```bash
python3 buildy.py --list-toolchains
```

### Native Toolchains

- **gcc-linux** - GCC compiler for Linux x86_64
- **clang-linux** - Clang/LLVM compiler for Linux x86_64
- **msvc-windows** - Microsoft Visual C++ for Windows x86_64
- **clang-macos** - Apple Clang for macOS (ARM64)

### Cross-Compilation Toolchains

- **gcc-mingw** - MinGW-w64 GCC for Windows cross-compilation (from Linux)
- **gcc-arm-cross** - GCC ARM cross-compiler for embedded systems
- **emscripten** - Emscripten for WebAssembly compilation

### Container-Based Toolchains

- **docker-linux** - GCC in Docker for cross-platform builds (works on any platform with Docker)

## Toolchain Selection

Buildy uses a **hierarchical toolchain selection** system:

### 1. Auto-Detection (Default)
If no toolchain is specified, Buildy auto-detects based on platform and architecture:

```bash
python3 buildy.py project.yaml
# Auto-detects gcc-linux on Linux x86_64
```

### 2. CLI Override (Highest Priority)
Specify toolchain via command line:

```bash
python3 buildy.py project.yaml --toolchain clang-linux
python3 buildy.py project.yaml -t gcc-mingw
```

### 3. Project-Level Configuration
Specify in your project YAML file:

```yaml
project:
  name: "my-project"
  toolchain: "clang-linux"  # Use Clang for this project
```

### 4. Workspace-Level Configuration
For multi-project workspaces:

```yaml
toolchain: "gcc-linux"  # Default for all projects

project:
  name: "my-project"
  # Uses gcc-linux unless overridden
```

## Usage Examples

### Example 1: Build with Default Toolchain
```bash
# Auto-detects gcc-linux on Linux
python3 buildy.py examples/math/simple_project.yaml
```

### Example 2: Build with Clang
```bash
# Use Clang instead of GCC
python3 buildy.py examples/math/simple_project.yaml --toolchain clang-linux
```

### Example 3: Cross-Compile for Windows
```bash
# Cross-compile from Linux to Windows using MinGW
python3 buildy.py examples/math/simple_project.yaml --toolchain gcc-mingw
```

### Example 4: Build in Docker
```bash
# Build using GCC in Docker (works on any platform)
python3 buildy.py examples/math/simple_project.yaml --toolchain docker-linux
```

### Example 5: Compile to WebAssembly
```bash
# Compile to WebAssembly using Emscripten
python3 buildy.py examples/math/simple_project.yaml --toolchain emscripten
```

## Creating Custom Toolchains

Create a new YAML file in the `toolchains/` directory:

```yaml
toolchain:
  name: "my-custom-toolchain"
  description: "My custom compiler setup"
  
  target:
    platform: linux
    architecture: x86_64
  
  host:
    platform: linux
    architecture: x86_64
  
  execution:
    type: native  # or docker, wsl
  
  tools:
    c_compiler: gcc
    cxx_compiler: g++
    linker: g++
    archiver: ar
  
  compile:
    command: "{cxx_compiler} {dep_flags} -std={std} {flags} {defines} {pic} {includes} -c {input} -o {output}"
    
    flags:
      common: ["-Wall", "-Wextra"]
      debug: ["-g", "-O0"]
      release: ["-O3", "-DNDEBUG"]
    
    dep_flags: "-MMD -MP -MF {dep_file}"
    pic_flag: "-fPIC"
    include_flag: "-I"
    define_flag: "-D"
  
  link:
    shared_library:
      command: "{linker} -shared {pic} {objects} {lib_dirs} {libs} -o {output}"
      output_pattern: "lib{name}.so"
      pic_flag: "-fPIC"
    
    executable:
      command: "{linker} {objects} {lib_dirs} {libs} -o {output}"
      output_pattern: "{name}"
    
    lib_dir_flag: "-L"
    lib_flag: "-l"
  
  extensions:
    object: ".o"
    dependency: ".d"
    shared_library: ".so"
    executable: ""
```

## Execution Environments

### Native Execution
Commands run directly on the host system:
```yaml
execution:
  type: native
```

### Docker Execution
Commands run inside a Docker container:
```yaml
execution:
  type: docker
  image: "gcc:13"
  volumes:
    - "${PWD}:/workspace"
  working_dir: "/workspace"
  user: "${UID}:${GID}"
```

### WSL Execution
Commands run inside Windows Subsystem for Linux:
```yaml
execution:
  type: wsl
  distribution: "Ubuntu"
  user: "myuser"
```

## Benefits

1. **Portability** - Same project config works with different compilers
2. **Flexibility** - Easy to switch between toolchains
3. **Isolation** - Docker-based builds for reproducibility
4. **Cross-Platform** - Build for multiple platforms from one machine
5. **Extensibility** - Easy to add new toolchains

## Troubleshooting

### Toolchain Not Found
```
ERROR: No suitable toolchain found for linux-x86_64
```

**Solution**: Ensure the `toolchains/` directory exists and contains toolchain YAML files. Use `--toolchains-dir` to specify a custom location:
```bash
python3 buildy.py project.yaml --toolchains-dir /path/to/toolchains
```

### Compiler Not Installed
```
ERROR: /bin/sh: 1: clang++: not found
```

**Solution**: Install the required compiler or use a different toolchain:
```bash
# Install Clang
sudo apt install clang

# Or use GCC instead
python3 buildy.py project.yaml --toolchain gcc-linux
```

### Docker Not Available
```
ERROR: docker: command not found
```

**Solution**: Install Docker or use a native toolchain instead.

