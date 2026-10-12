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
  description: "Simple math library example demonstrating filtered lists"

environment:
  compile:
    cpp_standard: "c++20"
    warnings: extra
    
  configurations:
    debug:
      optimization: none
      symbols: true
    release:
      optimization: full

targets:
  static_libraries:
    - name: mathcore
      language: cpp
      sources: ["libsrc/*.cpp"]
      include_dirs: ["include"]
      defines:
        # Common defines (always included)
        - "MATH_LIB_VERSION=1"
        # Configuration-specific defines using filtered list syntax
        - debug:
            - "DEBUG=1"
            - "ENABLE_ASSERTS=1"
        - release:
            - "NDEBUG=1"

  executables:
    - name: calculator
      language: cpp
      sources: ["appsrc/main.cpp"]
      include_dirs: ["include"]
      libs:
        - mathcore
      defines:
        - debug:
            - "DEBUG=1"
        - release:
            - "NDEBUG=1"
```

Let's break this down:

### Project Section

```yaml
project:
  name: math-library
  version: "1.0.0"
```

Every buildy project needs a name. The version is optional.

### Environment Section

```yaml
environment:
  compile:
    cpp_standard: "c++20"
    warnings: extra
```

This sets the C++ standard and warning level for all C++ targets. The `warnings: extra` keyword
becomes the flags of whichever toolchain is active (`-Wall -Wextra` for GCC and Clang, `/W4` for
MSVC). Without `cpp_standard`, the compiler uses its own default standard.

### Build Configurations

```yaml
  configurations:
    debug:
      optimization: none
      symbols: true
    release:
      optimization: full
```

Debug builds have debug symbols and no optimization; release builds are fully optimized. The
toolchain turns `optimization` and `symbols` into compiler flags (`-O0 -g`, `-O3` with GCC).

### Static Library Target

```yaml
  static_libraries:
    - name: mathcore
      language: cpp
      sources: ["libsrc/*.cpp"]
      include_dirs: ["include"]
      defines:
        - "MATH_LIB_VERSION=1"
        - debug:
            - "DEBUG=1"
            - "ENABLE_ASSERTS=1"
        - release:
            - "NDEBUG=1"
```

- `name`: the library's name (`libmathcore.a` on Linux, `mathcore.lib` with MSVC)
- `language`: `cpp` for C++ code
- `sources`: glob patterns of the source files
- `include_dirs`: directories searched for headers
- `defines`: a filtered list. Plain items always apply; the items under `debug:` only in a debug
  build, those under `release:` only in a release build. A filter can also name a platform
  (`linux:`), an architecture or a toolchain.

### Executable Target

```yaml
  executables:
    - name: calculator
      language: cpp
      sources: ["appsrc/main.cpp"]
      include_dirs: ["include"]
      libs:
        - mathcore
```

- `libs`: libraries to link. `mathcore` is a target of the workspace, so buildy links
  `libmathcore.a` and builds it first; no other declaration is needed.

## Step 3: Build the Project

Go to the example's directory and build:

```bash
cd examples/math
buildy
```

buildy prints each task as it runs, then a summary:

```
╔════════════════════════════════════════════════════════════╗
║                        BUILD REPORT                        ║
╠════════════════════════════════════════════════════════════╣
║  Status:           SUCCESS                                 ║
║  Platform:         linux-x86_64                            ║
║  Configuration:    debug                                   ║
║  Toolchain:        gcc-cpp-linux                           ║
...
║  Total Tasks:      4                                       ║
║  Executed:         4                                       ║
║  Cache Hits:       0 (0.0%)                                ║
...
║    compile:        2                                       ║
║    link:           2                                       ║
╚════════════════════════════════════════════════════════════╝
```

Four tasks: a compile for each source file, the library's archive, and the executable's link.

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
        ├── calculator/
        │   └── appsrc/
        │       ├── main.cpp.d
        │       └── main.cpp.o
        └── mathcore/
            └── libsrc/
                ├── math.cpp.d
                └── math.cpp.o
```

The output directory structure is:
- `build/<platform>-<arch>-<config>/`
- `bin/` for executables (and shared libraries)
- `lib/` for static libraries
- `obj/<target>/` for object files, by source path (`obj/<module>/<target>/` in a module of a
  workspace). The `.d` files list the headers each source included.

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

The "Debug: Adding" line comes from the `DEBUG` macro, which only the debug configuration defines.

## Step 6: Build in Release Mode

```bash
buildy -r          # or: buildy -c release
```

This builds into a separate directory:

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

No debug message this time: `DEBUG` isn't defined in release builds.

## Step 7: Incremental Builds

Run `buildy` again without changing anything: all four tasks are cache hits, and nothing runs.

Now add a comment to `appsrc/main.cpp` and build:

```
║  Total Tasks:      4                                       ║
║  Executed:         1                                       ║
║  Cache Hits:       3 (75.0%)                               ║
```

`main.cpp` was compiled again. Its object file came out the same, so the link was a cache hit too:
buildy keys each task by the content of its inputs, not by file times. Change the code itself, and
the link runs as well. Edit `include/mymath.h`, and both sources that include it compile again.

## Step 8: Clean and Rebuild

To remove everything buildy built (the `build/` directory):

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
- **libs**: Links the specified libraries, and builds the workspace's ones first
- **Filtered lists** (`- debug: [...]`) apply items to one configuration or platform

## Next Steps

- [Multi-Module Tutorial](TUTORIAL_MULTIMODULE.md) - Work with larger projects
- [Advanced Tutorial](TUTORIAL_ADVANCED.md) - Fetch dependencies and Docker builds
- [User Guide](USER_GUIDE.md) - Complete feature reference
