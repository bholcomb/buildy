# Buildy Variable System

## Overview

Buildy includes a powerful hierarchical variable system that allows you to define and use variables throughout your build configuration. Variables are resolved using `${variable_name}` syntax and support:

- **Hierarchical scoping** with clear override priorities
- **Provenance tracking** to see where each variable comes from
- **Strict error handling** that fails immediately on unresolved variables
- **CLI overrides** for build-time customization

## Variable Resolution

Variables are resolved in all string values throughout your configuration, including:
- File paths and patterns
- Compiler flags and defines
- Output directories
- Custom configuration values

### Syntax

Use `${variable_name}` to reference a variable:

```yaml
output:
  base_dir: "build"
  pattern: "${base_dir}/${platform}-${arch}-${config}"
  
defines:
  - "VERSION=${project_version}"
  - "BUILD_TYPE=${config}"
```

## Variable Hierarchy

Variables are resolved with the following priority (lowest to highest):

1. **Workspace level** - Top-level `variables:` section
2. **Project level** - `project.variables:`  
3. **Platform level** - `platforms.<platform>.variables:`
4. **Architecture level** - `architectures.<arch>.variables:`
5. **Configuration level** - `configurations.<config>.variables:`
6. **Built-in variables** - Automatically provided
7. **CLI overrides** - Command-line `--define` flags (highest priority)

Higher priority levels override lower priority levels.

## Built-in Variables

The following variables are always available:

| Variable | Description | Example Value |
|----------|-------------|---------------|
| `${platform}` | Target platform from CLI | `linux`, `windows`, `macos` |
| `${arch}` | Target architecture from CLI | `x86_64`, `arm64` |
| `${config}` | Build configuration from CLI | `debug`, `release` |
| `${project_name}` | From `project.name` | `my-project` |
| `${project_version}` | From `project.version` | `1.0.0` |
| `${base_dir}` | From `output.base_dir` | `build` |

## Defining Variables

### Workspace-Level Variables

Define at the top level of your YAML file:

```yaml
variables:
  src_root: "src"
  include_root: "include"
  custom_flag: "-DFEATURE_X"

project:
  name: "myapp"
```

### Project-Level Variables

Define within the project section:

```yaml
project:
  name: "myapp"
  version: "2.0.0"
  variables:
    app_name: "MyApplication"
    install_prefix: "/usr/local"
```

### Platform-Specific Variables

Override variables per platform:

```yaml
platforms:
  linux:
    variables:
      toolchain: "gcc"
      sys_libs: "pthread dl"
  
  windows:
    variables:
      toolchain: "msvc"
      sys_libs: "kernel32 user32"
```

### Configuration-Specific Variables

Override variables per build configuration:

```yaml
configurations:
  debug:
    variables:
      opt_level: "-O0"
      debug_flag: "-g3"
  
  release:
    variables:
      opt_level: "-O3"
      debug_flag: ""
```

## CLI Variable Overrides

Override any variable from the command line:

```bash
# Override single variable
buildy.py config.yaml --define base_dir=custom_build

# Override multiple variables
buildy.py config.yaml \
  --define base_dir=mybuild \
  --define opt_level=-O2 \
  --define VERSION=1.2.3

# Short form
buildy.py config.yaml -D MY_VAR=value
```

CLI overrides have the highest priority and will override any variable defined in the YAML files.

## Using Variables

### In Output Paths

```yaml
output:
  base_dir: "build"
  pattern: "${base_dir}/${platform}-${arch}-${config}"
# Resolves to: build/linux-x86_64-debug
```

### In Compiler Flags

```yaml
config:
  compiler_flags:
    - "${opt_level}"
    - "${debug_flag}"
    - "-DVERSION=${project_version}"
```

### In Defines

```yaml
config:
  defines:
    - "PLATFORM_${platform}"
    - "APP_NAME=${project_name}"
    - "VERSION_MAJOR=${version_major}"
```

### In Source Paths

```yaml
variables:
  src_dir: "src"
  
libraries:
  - name: "core"
    sources: "${src_dir}/core/*.cpp"
```

## Variable Provenance

When you generate a task graph with `--output`, the JSON includes a `resolved_variables` section showing each variable's value and source:

```json
{
  "resolved_variables": {
    "platform": {
      "value": "linux",
      "source": "built-in"
    },
    "base_dir": {
      "value": "custom_build",
      "source": "cli"
    },
    "opt_level": {
      "value": "-O3",
      "source": "configuration[release]"
    }
  }
}
```

This helps you understand:
- What value each variable has
- Where that value came from
- Which overrides took effect

## Error Handling

### Unresolved Variables

If a variable cannot be resolved, buildy will:
1. Collect ALL unresolved variables
2. Display an error message for each one
3. Stop immediately before executing any tasks

Example error output:
```
ERROR - Unresolved variables found in configuration:
ERROR -   - Unresolved variable '${my_var}' in: ${base_dir}/${my_var}
ERROR -   - Unresolved variable '${other_var}' in: -DVALUE=${other_var}
ValueError: Configuration contains 2 unresolved variable(s)
```

This strict behavior ensures:
- No silent failures
- Build configuration errors are caught early
- Clear error messages guide you to the problem

## Complete Example

```yaml
# Workspace-level variables
variables:
  src_root: "src"
  build_root: "build"
  version_major: "2"
  version_minor: "1"

project:
  name: "my-app"
  version: "${version_major}.${version_minor}.0"
  variables:
    app_prefix: "MyApp"

platforms:
  linux:
    variables:
      platform_define: "LINUX"
      sys_libs: "pthread dl m"
  
  windows:
    variables:
      platform_define: "WINDOWS"
      sys_libs: "kernel32"

configurations:
  debug:
    variables:
      opt_flag: "-O0"
      debug_symbols: "-g3"
  
  release:
    variables:
      opt_flag: "-O3"
      debug_symbols: ""

config:
  compiler_flags:
    - "${opt_flag}"
    - "${debug_symbols}"
  defines:
    - "PLATFORM_${platform_define}"
    - "VERSION=${project_version}"
    - "APP_NAME=${app_prefix}"

libraries:
  - name: "core"
    sources: "${src_root}/core/*.cpp"

executables:
  - name: "${app_prefix}"
    sources: "${src_root}/main.cpp"

output:
  base_dir: "${build_root}"
  pattern: "${base_dir}/${platform}-${arch}-${config}"
```

### Usage Examples

```bash
# Default build
buildy.py config.yaml

# Override build directory
buildy.py config.yaml --define build_root=mybuild

# Override version
buildy.py config.yaml --define version_major=3 --define version_minor=0

# Multiple overrides for custom build
buildy.py config.yaml \
  --platform windows \
  --configuration release \
  --define build_root=dist \
  --define app_prefix=CustomApp
```

## Best Practices

1. **Use descriptive variable names**: `src_root` instead of `s`, `opt_level` instead of `o`

2. **Define common paths as variables**: Makes it easy to restructure your project

3. **Use platform/config-specific variables**: Keep platform differences explicit

4. **Document custom variables**: Add comments explaining what each variable is for

5. **Use CLI overrides for build variations**: Don't create separate config files for minor variations

6. **Check resolved_variables in JSON**: Verify variables resolved as expected

7. **Let built-in variables do the work**: Use `${platform}`, `${arch}`, `${config}` instead of hardcoding

## Migration from Hardcoded Values

### Before
```yaml
output:
  pattern: "build/linux-x86_64-debug"
  
config:
  defines:
    - "VERSION=1.0.0"
```

### After
```yaml
variables:
  version: "1.0.0"

output:
  pattern: "${base_dir}/${platform}-${arch}-${config}"
  
config:
  defines:
    - "VERSION=${version}"
```

Benefits:
- Cross-platform builds work automatically
- Version can be overridden from CLI
- Easier to maintain and understand

