# Tutorial: Basic Single-Target Project

This tutorial walks through building a simple C++ project with buildy. You'll create a static library and an executable that uses it.

## What You'll Learn

- Creating a `buildy.yaml` configuration
- Defining static library and executable targets
- Understanding build output
- Using debug and release configurations

## The Example Project

We'll use the `examples/math` project, which has this structure:

```
math/
├── buildy.yaml
├── include/
│   └── mymath.h
├── libsrc/
│   └── math.cpp
└── appsrc/
    └── main.cpp
```

The project builds a math library and a calculator application that uses it.

## Step 1: Examine the Source Files

**include/mymath.h** - The library's public header:

```cpp
#ifndef MATH_H
#define MATH_H

int add(int a, int b);
int multiply(int a, int b);

#endif
```

**libsrc/math.cpp** - The library implementation:

```cpp
#include "mymath.h"
#include <iostream>

int add(int a, int b) {
    #ifdef DEBUG
    std::cout << "Debug: Adding " << a << " + " << b << std::endl;
    #endif
    return a + b;
}

int multiply(int a, int b) {
    return a * b;
}
```

**appsrc/main.cpp** - The application:

```cpp
#include "mymath.h"
#include <iostream>

int main() {
    std::cout << "Calculator v1.0" << std::endl;

    int result = add(5, 3);
    std::cout << "5 + 3 = " << result << std::endl;

    result = multiply(4, 7);
    std::cout << "4 * 7 = " << result << std::endl;

    return 0;
}
```

## Step 2: Understand the buildy.yaml

Here's the complete configuration:

```yaml
project:
  name: math-library
  version: "1.0.0"
  description: "Simple math library example"

environment:
  compile:
    cpp_standard: "c++20"
    warnings: ["-Wall", "-Wextra"]

  configurations:
    debug:
      optimization: "-O0"
      defines: ["DEBUG=1", "MATH_LIB_VERSION=1"]
      flags: ["-g"]
    release:
      optimization: "-O3"
      defines: ["NDEBUG=1", "MATH_LIB_VERSION=1"]

targets:
  static_libraries:
    - name: mathcore
      language: cpp
      sources: ["libsrc/*.cpp"]
      include_dirs: ["include"]

  executables:
    - name: calculator
      language: cpp
      sources: ["appsrc/main.cpp"]
      include_dirs: ["include"]
      libs:
        - mathcore
      depends_on:
        targets: ["mathcore"]
```

Let's break this down:

### Project Section

```yaml
project:
  name: math-library
  version: "1.0.0"
```

Every buildy project needs a name. The version is optional but recommended.

### Environment Section

```yaml
environment:
  compile:
    cpp_standard: "c++20"
    warnings: ["-Wall", "-Wextra"]
```

This sets the C++ standard and warning flags for all C++ targets.

### Build Configurations

```yaml
configurations:
  debug:
    optimization: "-O0"
    defines: ["DEBUG=1"]
    flags: ["-g"]
  release:
    optimization: "-O3"
    defines: ["NDEBUG=1"]
```

Debug builds include debug symbols and the `DEBUG` preprocessor macro. Release builds optimize for speed.

### Static Library Target

```yaml
static_libraries:
  - name: mathcore
    language: cpp
    sources: ["libsrc/*.cpp"]
    include_dirs: ["include"]
```

- `name`: The library name (produces `libmathcore.a` on Linux)
- `language`: Must be `cpp` for C++ code
- `sources`: Glob pattern matching source files
- `include_dirs`: Directories to search for header files

### Executable Target

```yaml
executables:
  - name: calculator
    language: cpp
    sources: ["appsrc/main.cpp"]
    include_dirs: ["include"]
    libs:
      - mathcore
    depends_on:
      targets: ["mathcore"]
```

- `include_dirs`: Directories to search for header files
- `libs`: Libraries to link against
- `depends_on.targets`: Ensures mathcore builds first

## Step 3: Build the Project

Navigate to the example directory and build:

```bash
cd examples/math
buildy
```

You'll see output similar to:

```
================================================================================
                              BUILD REPORT
================================================================================
Status: SUCCESS
Duration: 0.234s

Platform: linux / x86_64
Configuration: debug
Toolchain: gcc-cpp-linux

Tasks:
  Total:        3
  Cache hits:   0 (0.0%)
  Cache misses: 3
================================================================================
```

## Step 4: Examine the Build Output

The build creates:

```
build/
└── linux-x86_64-debug/
    ├── bin/
    │   └── calculator
    ├── lib/
    │   └── libmathcore.a
    └── obj/
        ├── main.o
        └── math.o
```

The output directory structure is:
- `build/<platform>-<arch>-<config>/`
- `bin/` for executables
- `lib/` for libraries
- `obj/` for intermediate object files

## Step 5: Run the Application

```bash
./build/linux-x86_64-debug/bin/calculator
```

Output:

```
Calculator v1.0
Debug: Adding 5 + 3
5 + 3 = 8
4 * 7 = 28
```

Notice the "Debug: Adding" message - this comes from the `DEBUG` macro we defined in the debug configuration.

## Step 6: Build in Release Mode

```bash
buildy -c release
```

This creates a separate release directory:

```
build/
├── linux-x86_64-debug/
│   └── ...
└── linux-x86_64-release/
    └── ...
```

Run the release build:

```bash
./build/linux-x86_64-release/bin/calculator
```

Output:

```
Calculator v1.0
5 + 3 = 8
4 * 7 = 28
```

No debug message this time - the `DEBUG` macro isn't defined in release builds.

## Step 7: Incremental Builds

Make a change to `appsrc/main.cpp` and rebuild:

```bash
buildy
```

```
================================================================================
                              BUILD REPORT
================================================================================
Status: SUCCESS
Duration: 0.156s

Tasks:
  Total:        3
  Cache hits:   2 (66.7%)
  Cache misses: 1
================================================================================
```

Only `main.cpp` was recompiled. The library didn't change, so it was retrieved from cache.

## Step 8: Clean and Rebuild

To clean all build artifacts:

```bash
buildy --clean
```

To force a complete rebuild (ignoring cache):

```bash
buildy --force
```

## Key Takeaways

- **Project metadata** goes in the `project:` section
- **Compiler settings** go in `environment.compile:`
- **Build variants** (debug/release) go in `environment.configurations:`
- **Libraries** go in `targets.static_libraries:` or `targets.shared_libraries:`
- **Applications** go in `targets.executables:`
- **include_dirs**: Directories to search for header files
- **depends_on**: Ensures build order
- **libs**: Links the specified libraries

## Next Steps

- [Multi-Module Tutorial](TUTORIAL_MULTIMODULE.md) - Work with larger projects
- [Advanced Tutorial](TUTORIAL_ADVANCED.md) - Fetch dependencies and Docker builds
- [User Guide](USER_GUIDE.md) - Complete feature reference
