# Buildy - Remaining Tasks and Recommendations

This file tracks remaining improvements and feature requests for buildy.

## High Priority

### Test Target Support
**Status:** Not Started  
**Priority:** High  
**Description:** Add support for test targets in the DSL with test discovery, runner integration, and result reporting.

**Requirements:**
- Add `tests:` section to DSL specification
- Support common test frameworks (gtest, catch2, go test)
- Test discovery via naming conventions or explicit listing
- JUnit/TAP output for CI integration
- `--test` CLI flag to run tests after build
- Parallel test execution

**Example DSL:**
```yaml
targets:
  tests:
    - name: engine_tests
      framework: gtest
      sources: ["tests/*.cpp"]
      depends_on:
        targets: ["engine_core"]
```

---

### Parallel Dependency Fetching
**Status:** Not Started  
**Priority:** High  
**Description:** Dependencies are currently fetched sequentially. Parallel fetching would significantly speed up initial builds for projects with many external dependencies.

**Implementation:**
- Add goroutine pool for parallel fetches
- Respect dependency ordering (fetch dependencies of dependencies first)
- Add progress reporting for concurrent fetches
- Handle rate limiting for git hosts

---

### Retry Logic for Network Operations
**Status:** Not Started  
**Priority:** High  
**Description:** Add retry logic for transient network failures during dependency fetching.

**Implementation:**
- Exponential backoff for retries
- Configurable retry count (default: 3)
- Skip retry for permanent failures (404, auth errors)

---

## Medium Priority

### Remote/Distributed Build Cache
**Status:** Not Started  
**Priority:** Medium  
**Description:** Allow sharing build cache across machines and CI systems (similar to sccache or Bazel remote cache).

**Features:**
- HTTP-based cache server
- Content-addressable storage
- Authentication support
- Cache eviction policies
- Compression for network transfer

**Configuration:**
```yaml
environment:
  cache:
    remote:
      url: "https://cache.example.com"
      auth: "token:${CACHE_TOKEN}"
```

---

### Toolchain Version Verification
**Status:** Not Started  
**Priority:** Medium  
**Description:** Verify compiler/tool versions match expected versions for reproducibility.

**Features:**
- Define expected versions in toolchain config
- Warn or error if version mismatch
- Support version ranges

---

### Log File Output
**Status:** Not Started  
**Priority:** Medium  
**Description:** Add option to write build logs to a file for CI/debugging.

**Implementation:**
- `--log-file <path>` CLI flag
- Separate stdout (progress) from log file (full details)
- Log rotation support

---

### Better Error Messages
**Status:** In Progress  
**Priority:** Medium  
**Description:** Improve error messages with file:line context and actionable suggestions.

**Done:**
- Schema validation now provides suggestions for unknown keys

**Remaining:**
- Add line numbers to YAML parsing errors
- Improve dependency resolution error messages
- Add "did you mean" suggestions for common typos

---

### Watch Mode
**Status:** Not Started  
**Priority:** Medium  
**Description:** Add `--watch` flag to automatically rebuild on file changes.

**Features:**
- File system watcher (fsnotify)
- Debounce rapid changes
- Only rebuild affected targets
- Clear screen between builds

---

### Build Profiles/Presets
**Status:** Not Started  
**Priority:** Medium  
**Description:** Named configuration bundles for common build scenarios.

**Example:**
```yaml
presets:
  ci-release:
    configuration: release
    defines:
      - CI=1
    flags:
      - "--parallel=4"
  
  local-dev:
    configuration: debug
    compile_commands: true
```

**Usage:**
```bash
buildy --preset ci-release
```

---

## Lower Priority

### Incremental Linking
**Status:** Not Started  
**Priority:** Low  
**Description:** Only relink changed object files for faster iteration.

**Notes:**
- Compiler/linker specific (MSVC /INCREMENTAL, gold --incremental)
- May affect binary reproducibility
- Consider as opt-in feature

---

### Unity Builds
**Status:** Not Started  
**Priority:** Low  
**Description:** Combine source files for faster compilation.

**Features:**
- Automatic unity file generation
- Configurable batch size
- Exclude specific files from unity

---

### Code Coverage Integration
**Status:** Not Started  
**Priority:** Low  
**Description:** Add `--coverage` flag for code coverage builds.

**Features:**
- gcov/llvm-cov support
- Coverage report generation (HTML, lcov)
- Integration with CI coverage services

---

### Sandboxing / Hermetic Builds
**Status:** Not Started  
**Priority:** Low  
**Description:** Isolate builds from host system for reproducibility.

**Implementation:**
- Container-based isolation
- Explicit toolchain paths
- No implicit environment variables

---

### Module-Level Caching
**Status:** Not Started  
**Priority:** Low  
**Description:** Cache entire module outputs, not just individual tasks.

**Benefits:**
- Faster cache restoration for unchanged modules
- Reduced cache index size
- Better for modular monorepo builds

---

## Completed

### Signal Handling / Graceful Shutdown
**Status:** ✅ Completed  
**Description:** Handle SIGINT/SIGTERM gracefully, clean up partial outputs.

### Configuration Schema Validation
**Status:** ✅ Completed  
**Description:** Strict validation of YAML configuration with helpful error messages.

### Response Files for Windows
**Status:** ✅ Completed  
**Description:** Automatic response file generation for long command lines.

### Warnings as Errors
**Status:** ✅ Completed  
**Description:** All warnings are now treated as errors to ensure build reliability.

### Circular Module Detection
**Status:** ✅ Completed  
**Description:** Detect and report circular dependencies in workspace modules.

### Go Module Input Tracking
**Status:** ✅ Completed  
**Description:** Track all .go files as inputs for proper cache invalidation.

### Task ID Registry
**Status:** ✅ Completed  
**Description:** Robust target-to-task mapping without fragile string parsing.

### Header Dependency Tracking
**Status:** ✅ Completed  
**Description:** First-time builds are cache-miss by definition for header tracking.

### Cache Race Condition Fix
**Status:** ✅ Completed  
**Description:** Proper locking in cache index updates.

### Platform Auto-Detection
**Status:** ✅ Completed  
**Description:** Auto-detect host platform/architecture from runtime.

### Build Reports
**Status:** ✅ Completed  
**Description:** JSON and terminal build report generation.

### compile_commands.json
**Status:** ✅ Completed  
**Description:** IDE integration via compile_commands.json generation.

### ConfigParser Refactoring
**Status:** ✅ Completed  
**Description:** Split monolithic ConfigParser into focused components.

### Optional Glob Patterns
**Status:** ✅ Completed  
**Description:** Support optional source patterns that can match zero files.
