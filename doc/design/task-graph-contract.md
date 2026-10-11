# Task Graph Contract

Status: **approved.** The open questions are decided ([section 11](#11-decisions)); the steps in
[section 10](#10-implementation-order) land one commit at a time.

This document answers the external design review of buildy (reviewed at `3fd9618`; the code is
the same as `main` at `b6316c5` apart from `bootstrap.sh`). It records how each finding was
verified, the related problems found along the way, and one design that fixes them together.

The reviewer's central point holds: the task graph does not enforce complete inputs, single
ownership of outputs, the existence of outputs, or where a task runs. Most findings are
symptoms of that. The design below makes those four things part of what a task *is*, checks
them before and after execution, and deletes the code that was standing in for them.

---

## 1. Verification

Every finding was reproduced against a build of `main` in a scratch directory, never in a
checkout. All eleven are **confirmed**. The minimal reproductions are in
[Appendix A](#appendix-a-reproductions).

| # | Finding | Status | Result |
|---|---------|--------|--------|
| 1 | Archives write outside the extraction dir | Confirmed | ZIP entry `pkg/../../x` landed two levels above `dest`; tar symlink `pkg/link -> /outside` followed by `pkg/link/x` wrote into `/outside`; tar `pkg/../x` also escaped. |
| 2 | Targets sharing a source overwrite objects | Confirmed | Executables `one` (`VALUE=1`) and `two` (`VALUE=2`) from one `main.cpp` both print `2`. |
| 3 | Static library change doesn't relink | Confirmed | `add()` changed from `a+b` to `a*b`; `libcalc.a` rebuilt, `link_app` was a cache hit, app still prints `5`. |
| 4 | Go misses imported packages outside the target dir | Confirmed | `cmd/app` imports `internal/greet`; editing `greet.go` gave a cache hit and the old output. Inputs were `cmd/app/main.go` and a nonexistent `cmd/app/go.mod` (hash `""`). |
| 5 | Wrong Rust paths; missing outputs accepted | Confirmed | Declared `…/debug/bin/hello`, Cargo wrote `…/debug/hello`. After deleting the binary: "2 cache hits, 0 executed", binary still absent. |
| 6 | Header tracking drops dependencies | Confirmed | `#include "config"` (no extension): recorded headers were `['other.h']`; editing `config` did not rebuild. |
| 7 | Platform modules never selected | Confirmed | `modules: [linux: [platform-linux]]` with `--platform linux`: "Generated 0 tasks from 0 modules", exit 0. |
| 8 | `--target` drops dependencies in other modules | Confirmed | Clean tree: link fails, `calc` never built. Worse after a full build: it links the **stale** `libcalc.a` and succeeds. |
| 9 | Docker toolchains run on the host | Confirmed | `--toolchain docker-cpp-linux` logs "Execution environment: docker"; the object's `.comment` is the host's Ubuntu GCC 13.3.0. Docker is never invoked. |
| 10 | `--dry-run` builds dependencies | Confirmed | `--dry-run` cloned a git dependency and ran its Makefile twice (debug and release). |
| 11 | CLI bypasses config validation | Confirmed | `bogus_key: 1` at top level and `defnies:` on a target are accepted, exit 0. |

### Related problems found

Same root causes, found while verifying. "Confirmed" means reproduced; "reading" means from the code.

| | Problem | Root cause | How |
|---|---------|-----------|-----|
| S1 | The download cache is keyed by URL basename. Deps at `…/foo/v1.0.tar.gz` and `…/bar/v1.0.tar.gz` share one file; `bar` was extracted from **foo's** archive. | Incomplete identity | Confirmed |
| S2 | Extraction writes straight into `dest`; an interrupted extraction leaves a non-empty dir that `isValidExtraction` accepts on the next run. | Partial output accepted | Reading |
| S3 | `ar rcs` adds to an existing archive. Removing `sub.cpp` from a library left `sub.o` inside `libcalc.a`. | Stale outputs not cleared | Confirmed |
| S4 | Two modules each with an executable `tool` both write `bin/tool`; the build succeeds with one of them. Every module's setup task also declares the same `lib/`, `bin/`, `obj/` dirs. | No output ownership | Confirmed |
| S5 | Sources are made relative to the **current directory** and `source_stem` comes from that. `buildy ..` from a subdir put the object at `obj/<absolute path>/main.o`; sources outside the module give `../` stems that escape `obj/`. | Path identity depends on cwd | Confirmed |
| S6 | Go and Rust manifests are looked for in the target dir (`cmd/app/go.mod`); when missing they hash as `""` silently. Go `vendor/` and `//go:embed` files are never scanned. | Incomplete inputs | Confirmed (in 4) |
| S7 | Rust library outputs are named `${name}`, not `lib<crate>.a`/`.so`; `crate_type` only adds `--lib`. | Wrong declared outputs | Reading |
| S8 | The depfile parser splits escaped spaces (a header under `inc dir/` was not tracked), takes the first `:` as the target separator (breaks `C:/…` on Windows), and the MSVC JSON path has the same extension filter as finding 6. | Incomplete inputs | Confirmed (spaces), reading |
| S9 | A declared input that doesn't exist when its task runs is skipped (hash stays `""`). | Incomplete inputs | Reading |
| S10 | `--target nosuch` builds nothing and exits 0. `--target app` also builds every other target in `app`'s module. | Selection | Confirmed, reading |
| S11 | The cache key uses the toolchain **name**, not its content, and has no execution settings, so changing a Docker image reuses native results. | Incomplete key | Reading |
| S12 | 15 call sites resolve `${…}` with errors ignored; a missing `cpp_standard` produced `-std=${std}`. Config errors in generation (transform without `tool`, unknown toolchain, unknown staging target) log a warning and continue. | Validation | Confirmed, reading |
| S13 | The discovery cache is saved under `<root>/.buildy_cache` but loaded from `<cwd>/.buildy_cache`, and ignores platform. | Context | Reading |
| S14 | Even if the Docker environment reached the executor, it mounts the cwd at `/workspace` while commands use absolute host paths, so it could not work. | Execution settings | Reading |
| S15 | `depends_on.artifacts` orders only the **link** task. Compiles of generated sources and headers are ordered only because generate tasks happen to fall in an earlier stage. | Incomplete edges | Reading |
| S16 | `BuildState` and `ChangeDetector.DetectChanges`/`GetAffectedTasks` are computed and logged but never decide anything. | Dead code | Reading |

---

## 2. The task contract

### What a task declares

A `BuildTask` already has inputs, outputs, dependencies and a command. The contract makes
each of them complete and adds the two things that are missing.

| Field | Meaning |
|-------|---------|
| `Inputs` | Every file the task reads that buildy can name before it runs: sources, manifests, **and every upstream output it consumes** (objects, libraries, generated files, staged files). A directory input (install's staging dir) is hashed as a tree, as today. |
| `DepFile` | Optional `{Path, Format}` (`make` or `msvc-json`). Inputs the tool reports after running. Set by the command builder, which already knows the format; the cache stops guessing from `.d`/`.json` suffixes. |
| `Outputs` | Files this task, and only this task, produces. All paths are absolute and clean. |
| `Exec` | Where the command runs: `native`, or `docker` with image, mounts and user. Taken from the toolchain that produced the task, not the module default. |
| `ToolchainHash` | Content hash of that toolchain's config. `HashToolchainConfig` exists, but today the key uses only the toolchain name. |
| `ToolCached` | True for tasks whose tool keeps its own correct cache (`go build`, `cargo build`, section 3). Set from template metadata, not hard-coded. |
| `Dependencies` | Explicit edges. Edges to the producers of inputs are added automatically (below), so explicit edges remain only for ordering that inputs can't express, such as generated headers. |

### Graph validation

After generation, before execution, including under `--dry-run`. Each failure is an error that
names the tasks and targets involved.

1. **One producer per output.** Map each normalised output path to its task. A second
   producer fails with: `output build/…/obj/app/one/main.o is produced by both
   compile_one_main_cpp (module ., target one) and compile_two_main_cpp (module ., target two)`.
2. **Every dependency exists.** Today an unknown ID becomes a bogus "circular dependency" or
   is dropped silently.
3. **Producer edges.** If a task's input is another task's output, the graph adds the edge.
   Dependencies follow from data rather than from template authors remembering them; this
   fixes S15 for generated sources.
4. **No unresolved variables.** Every `${…}` in a command, input or output resolves, or
   generation fails. The 15 call sites in S12 pass and check an error list.

### Execution rules

- **Before running**, every declared input exists (its producer has run), or the task fails
  naming the input and its producer. Inputs are hashed here, once; generation stops hashing.
- **Before running a buildy-cached task**, the executor deletes its declared output files, so
  whatever exists afterwards came from this run (fixes S3). Tool-cached tasks keep their
  outputs, because the tool uses them for its own incremental work.
- **After exit 0**, every declared output exists, or the task **fails**:
  `cargo_build exited 0 but did not produce declared output …/hello`. A declared depfile must
  exist and parse. Only then is a cache entry written.
- The command runs through `task.Exec`, with the workspace root as working directory.

### Cache key and cache hits

```
key = sha256(task_type, command, outputs, exec, toolchain_hash,
             platform, architecture, configuration, config_hash,
             [(normalised path, content hash) for each declared input])
```

The changes from today are `outputs`, `exec`, `toolchain_hash` in place of the name, and
inputs that are complete. `config_hash` stays: it is blunt but safe, and removing it is an
optimisation for later.

A lookup is a **hit** only if an entry exists for the key, **every declared output** exists with
its recorded hash (today only outputs that happened to exist at record time are checked),
and every input from the depfile is unchanged. That is Ninja's model, which buildy already
half implements.

`BuildState`, `DetectChanges` and `GetAffectedTasks` (S16) are deleted. The lookup rule above
is the only thing that decides whether a task runs.

### How the findings fall out

- **2 (object overwrite):** two compile tasks declare the same `.o`, so rule 1 rejects the
  graph. Section 4 then gives each target its own object path, so the common case builds.
  S4 is rejected by the same rule.
- **3 (stale link):** a link task's inputs include the library files it links that the graph
  produces (`libcalc.a`). Their content is in the key, so a changed library relinks. Rule 3
  adds the edge, which `--target` then follows (finding 8).
- **5 (missing outputs):** a declared output that doesn't appear fails the task on the first
  build instead of caching an empty entry, and a deleted output is a cache miss. The wrong
  Rust path would have been caught on day one.
- **6 (dropped headers):** the depfile is a list of discovered inputs, parsed in full (section
  3). Nothing is filtered by extension.
- **9 (Docker on host):** `Exec` is part of each task and of its key. The executor has no
  module-wide environment to forget.

---

## 3. Input discovery per language

**C and C++: the compiler's depfile, in full.** Keep `-MMD -MP -MF` and `/sourceDependencies`.
The Makefile parser handles continuation lines, `\ ` escaped spaces, `$$`, and `-MP` phony
targets, and finds the target separator as the first `:` followed by whitespace or end of line
(not `C:`). Every listed path is recorded except the source itself, which is already a declared
input. A listed file that no longer exists is recorded as missing, so its return triggers a
rebuild. MSVC: drop the extension filter and keep the system-directory exclusion, the
equivalent of GCC's `-MMD` leaving out system headers.

**Go and Rust: run the tool every time and let its cache decide.** Both `go build` and
`cargo build` keep correct, content-based caches that already account for imports outside the
target dir, `go.mod`/`go.sum`, `go.work`, `vendor/`, `//go:embed`, cgo, `build.rs`, Cargo path
dependencies, environment variables and compiler versions. Buildy can only approximate that
dependency analysis. On this machine a no-op `go build -o` took about 40 ms and a no-op
`cargo build` about 15 ms. `go list -deps`, the cheapest accurate way to *predict* Go inputs,
costs about the same (30 to 50 ms), so predicting saves nothing and risks being wrong.

So Go and Rust tasks are **tool-cached**: never a buildy cache hit, always run, and their outputs
hashed afterwards so downstream tasks (staging copies, C++ links against a Rust static library)
key on the real content. Go builds are reproducible and Cargo leaves a fresh artifact untouched,
so downstream tasks still hit. The contract still applies: exclusive outputs, outputs must exist,
`Exec`. This removes `scanSourceFiles`, `scan_sources_pattern` and `manifest_files`.

Cargo's dep-info `.d` was considered: it would reuse the depfile machinery, but it lists only
crate sources, not `Cargo.toml`, `build.rs` inputs or environment variables, so buildy would need
extra rules to stay correct. Keying an opaque step on `Cargo.lock` plus the source tree misses
path dependencies outside the tree. Neither beats a 15 ms no-op.

Limitation: a Go or Rust toolchain running in Docker would lose the tool cache between `--rm`
containers. No such toolchain exists today. If one is added, mount `GOCACHE` or the Cargo target
dir.

**Rust output paths (5, S7).** Cargo gets its own target dir, `${output_dir}/cargo`, so it
doesn't share `debug/`/`release/` with buildy's layout. Outputs are
`${output_dir}/cargo/<profile>/<bin>` for executables (`bin:` if set, else the target name) and
`lib<name>.a`/`.so` (`.lib`/`.dll` on Windows) for libraries. `<profile>` comes from the same
configuration-to-flag mapping that adds `--release`. If Cargo names the artifact differently,
the output check fails with a message that points at `bin:`.

**Other tasks.** Transform, generate, copy and install tasks already declare their inputs
explicitly. The contract adds output existence and producer edges.

---

## 4. Output identity

Rule: **every output path is a function of (output dir, module, target, source path relative to
the module).** The output dir already contains `platform-arch-config`.

- Object files:
  `${output_dir}/obj/${module}/${target}/${source_rel}${ext}`, where `source_rel` is the source
  path relative to the module directory, computed from absolute paths with the extension removed.
  It no longer depends on the invocation directory (S5). A source outside the module dir has
  each `..` replaced by `_up`. Anything stranger is caught by validation rule 1 rather than by
  ever-cleverer naming.
- Sources and every other task path become absolute (`PathResolver` stops converting to
  cwd-relative), and the executor runs in the workspace root. That also makes Docker
  same-path mounting work (section 5).
- Final artifacts keep `bin/<name>` and `lib/<prefix><name>.<ext>`. Target names must be unique
  across the workspace: `libs: [calc]` already resolves by name, so two `calc`s are ambiguous
  anyway. Validation rule 1 reports a collision with both modules named.
- Setup tasks are deleted. The executor already creates every output's parent directory, and
  their shared directory outputs would break rule 1 in every multi-module workspace.
- Across invocations: a user-set `output.pattern` that drops the configuration makes debug and
  release share files. That stays correct, because a hit needs output hashes to match, but it
  rebuilds each time. It is not worth an error.

---

## 5. Execution settings (finding 9)

`Exec` comes from the toolchain that generated the task (`CurrentToolchain` for target tasks,
the named tool's toolchain for transform and generate tasks). Copy, stage and install tasks
run natively. The executor builds the runner from `task.Exec`; the `ExecEnv` field and the `nil`
argument in `builder.go` go away.

Docker runs mount the workspace root, and the cache dir if it lives elsewhere (fetched deps),
**at the same absolute path**, with `-w <workspace root>`, so absolute host paths in commands are
valid inside the container (S14). Toolchain `volumes` add extra mounts; the `/workspace` and
`working_dir` convention is dropped. Building the `docker run` argument list becomes a pure
function so it can be tested without a daemon.

---

## 6. Build context, target selection, dry-run

**Build context (7).** `BuildContext{Platform, Architecture, Configuration}` (the type already
exists for filters) is built once in `main` from the flags and passed explicitly to workspace
loading, the config parser, the dependency resolver and the builder. Module lists, top-level and
child, use the same filter matching as every other list (`ResolveFilteredList`), so
`linux: [...]`, `x86_64: [...]` and `release: [...]` all work, in both the list and the legacy map
forms. Today the top level gets an empty platform and children match the platform only.

The discovery cache (`workspace.json`) is **deleted** rather than made context-aware. It saves
only a directory walk in legacy discovery mode, since loading from it re-reads every module
anyway, and it is read and written in different places (S13). Diagnostic files are named by the
full context (`tasks_<platform>-<arch>-<config>.json`) so platforms don't overwrite each other.

**Target selection (8).** Generate tasks for **all** modules; generation is cheap and is needed
for name resolution anyway. Validate the whole graph, then select the requested targets' final
tasks plus their transitive dependency closure, and execute only that subgraph. An unknown
target name is an error (S10). The `targetFilter` parameter leaves `processModule`.

**Dry-run (10): thread the flag through one phase, not graph tasks.** The phases become explicit:
load, prepare (fetch and build external dependencies), generate, validate, select, execute.
Dry-run runs load, generate, validate and select, and *plans* prepare: it prints "would fetch X",
"would build X (debug, release)", skips `git fetch`/clone/download and dependency builds, and
resolves what is already on disk. If dependencies are missing, the printed plan says so, because
their include and lib dirs are absent.

Dependency builds as graph tasks were considered and rejected for now. Generation reads their
results from disk (`GetOutputPaths` stats include and lib dirs; buildy-built deps), so making
them tasks means describing dependency layouts declaratively before they exist. That is a
redesign of dependency resolution that fixes nothing here beyond dry-run. Revisit it if per-target
dependency selection or caching of dependency builds is wanted.

**Fetched-dependency libraries (sibling of 3).** Prepare finishes before generation, so a
target's `deps:` libraries can be resolved to files in the dependency's lib dirs and added to the
link task's inputs, just like workspace libraries. System libraries stay unhashed, as in Make or
Ninja.

---

## 7. One loading path with strict validation (11)

All configuration is read through one function, `loadConfigFile(path) (map[string]any, error)`:
read, `yaml.Unmarshal` (yaml.v3 already rejects duplicate keys), then `ValidateConfig`. The
workspace root, every module, and legacy discovery use it, and a load error is fatal (today a
broken module in discovery mode is skipped with a warning). Dependency files get the same
treatment with their own schema (`knownDependencySectionKeys` exists). `ConfigParser.ParseConfigFile`
is dead code and is deleted.

The existing validator **cannot simply be wired in.** Run over this repo, it rejects buildy's own
`buildy.yaml` and every Go and Rust executable ("missing required 'sources'"), rejects `rust` as a
library language, and lacks keys the generator reads: top-level `architectures`,
`configurations`, `toolchain`; target `lib_dirs`, `frameworks`, `output_prefix`, `remove_flags`,
`remove_defines`, `gcflags`, `buildmode`, `type`, and `path` on libraries. Its schema is corrected
to match what the generator reads: `sources` required for C/C++, `path` for Go and Rust. Tests run it
over every file in `examples/` and the root `buildy.yaml`, and over Go and Rust fixtures, so the two
stay together.

Config errors found during generation (S12: a transform without `tool`, an unknown toolchain, an
unknown staging target, an artifact with no outputs) become errors instead of warnings.

`yaml.v3`'s `KnownFields` was considered. It only works when decoding into typed structs, and the
generator consumes `map[string]any` throughout (variable resolution, filtered lists that mix
strings and maps). Typed config would make the schema and the reader one thing, but it is a
rewrite of generator input handling, not needed to make validation real.

---

## 8. Safe extraction (1)

Extract into a **new temporary directory** next to `dest`, then rename it into place. An
interrupted extraction never looks complete (S2), and the extraction root is private, with the
archive as the only writer. For each entry:

1. Clean the name and strip the common prefix. Reject absolute names, volume names, and any `..`
   component.
2. Before creating anything, `Lstat` each existing parent component under the root and refuse
   if one is a symlink. That stops write-through: an archive can only meet a symlink it created
   itself.
3. Symlink entries: resolve the target lexically against the link's directory and reject it if
   it leaves the root. Hard links get the same check, and are created (today they are silently
   dropped).
4. If the target path already exists (a duplicate entry), `Lstat` it and remove it first,
   since it can only be something this archive created. Then create the file with `O_EXCL`, so a
   write never goes through a symlink. Mask permissions to `0o777`, with no setuid or setgid.
5. Any failure removes the temporary dir and fails the fetch. Errors from `Symlink`, `Chmod` and
   `MkdirAll` are checked (today they are ignored).

Because the root is fresh and private, these lexical checks are sufficient: there is no
concurrent writer to race. The download cache is keyed by `sha256(url)` plus the basename for
readability (S1), and the extraction is recorded against that key.

**`os.Root`.** Buildy requires Go 1.25 (`go 1.25.0` in `go.mod`, so a local Go with
`GOTOOLCHAIN=auto` fetches it, and CI's `setup-go` follows `go-version-file: src/go.mod`). The
extraction root is opened as an `os.Root`, which confines every operation, including following
symlinks, to the temporary directory; `Root.MkdirAll`, `Root.Symlink` and `Root.Link` (new in 1.25)
create the tree. That replaces the parent walk of step 2. Steps 1, 3 and 4 stay: they give clear
errors, and `Root.Symlink` does not validate link targets. The extraction is recorded in a marker
file next to `dest` (URL and archive SHA-256); `dest` is reused only when the marker matches.

A dependency's own build scripts run with full user rights anyway, so extraction safety is about
not clobbering files during unpacking, not about sandboxing the dependency.

---

## 9. Test plan

`internal/build` and `internal/cache` have no tests today. Each confirmed finding and sibling gets a
regression test that fails on `main` and passes after its step.

**Unit tests**

| File | Covers |
|------|--------|
| `internal/cache/depfile_test.go` | Table tests: extensionless header, `\ ` escapes, `$$`, continuations, `-MP` phony targets, `C:/` drive letters, MSVC JSON with no extension filter (6, S8). |
| `internal/cache/cache_test.go` | Key includes outputs, exec and toolchain hash; a hit needs every declared output with a matching hash; a missing output or changed or missing discovered input is a miss; recording fails when a declared output is absent (5). |
| `internal/build/executor_test.go` | A task exiting 0 without its output fails and writes no entry (5); a missing input fails before running (S9); declared outputs are deleted before a cached task runs (S3) and kept for a tool-cached one; a task with `Exec.Type=docker` goes to the Docker runner (9). Commands use the Go helper-process pattern so they run on Linux, macOS and Windows CI. |
| `pkg/util/exec_test.go` | Pure Docker argument builder: same-path mounts, working dir, extra volumes (S14). |
| `internal/workspace/task_graph_test.go` (extend) | Duplicate output rejected with both producers named (2, S4); unknown dependency named; producer edges added (S15); closure selection; unknown target errors (8, S10). |
| `internal/workspace/workspace_test.go` | `linux: [m]` selected for linux, not windows, in list and map forms, top level and child (7); unknown key in a module fails to load (11); `examples/**` and root `buildy.yaml` validate; Go and Rust executables without `sources` validate. |
| `internal/workspace/template_engine_test.go` | Object path identity: two targets give distinct paths; the path is independent of cwd; `..` sources stay under `obj/` (2, S5). |
| `internal/resource/extract_test.go` | ZIP `..`, tar `..`, symlink write-through, escaping symlink and hard-link targets, duplicate entries, interrupted extraction not accepted (1, S2); two URLs with the same basename don't share a download (S1). |

**End-to-end tests.** `src/e2e_test.go` builds the binary once in `TestMain`, copies a fixture from
`src/testdata/e2e/<case>/` to `t.TempDir()`, and runs buildy on it. Fixtures are the reproductions
in Appendix A. A test skips when its tool (`g++`, `go`, `cargo`, `docker`) is missing. C++ cases
run on Linux and macOS at first; Docker runs only with `BUILDY_TEST_DOCKER=1`.

| Case | Asserts |
|------|---------|
| `shared_source` | `one` prints 1 and `two` prints 2 (2). |
| `static_relink` | After editing the library source, the app prints the new value (3). |
| `stale_archive` | Removing a library source removes its member (S3). |
| `go_outside_import` | Editing `internal/greet` or an embedded file changes the output (4). |
| `rust_outputs` | The declared binary exists; deleting it triggers a rebuild (5). |
| `extensionless_header` | Editing `config` rebuilds (6). |
| `platform_module` | `--platform linux` builds `linuxonly`; `--platform windows` does not generate it (7). |
| `target_closure` | `--target app` builds `calc` first; `--target nosuch` fails (8, S10). |
| `dry_run_dep` | A local-git dependency is not cloned and its Makefile not run (10). |
| `unknown_key` | Exits non-zero, naming the key and file (11). |
| `cwd_independent` | `buildy .` and `cd sub && buildy ..` give the same object paths (S5). |

---

## 10. Implementation order

Each step lands on its own with its tests, and leaves `go test ./...` and the examples green. The
order puts severity and leverage first.

| Step | Fixes | Touches |
|------|-------|---------|
| 1. Safe fetch | 1, S1, S2 | `resource/fetch_manager.go` (extraction, download key), new `resource/extract_test.go`. Independent of everything else. |
| 2. Cache and executor contract | 5 (acceptance), 6, S3, S8, S9, S16 | `cache/cache.go` (lookup and record rules, depfile parser), delete `BuildState` and change detection, `build/executor.go`, `build/builder.go`, `workspace/models.go` (`DepFile`, no generation-time hashing), `workspace/template_engine.go` (sets `DepFile` from the command builder's dep file). Adds the e2e harness. |
| 3. Output identity and absolute paths | 2, S5 | `templates/c_code.yaml`, `templates/cpp_code.yaml`, `computeSourceStem`, `path_resolver.go`, executor working dir. |
| 4. Graph validation | 2 (rejection), S4, S12 (resolution), S15 | `workspace/task_graph.go` (`Validate`, producer edges), `task_generator.go` (delete setup tasks; artifact edges also on compiles), `template_engine.go`, `builder.go`. |
| 5. Library inputs | 3, fetched-dep libs | Graph assembly in `config_parser.go` (`resolveCrossModuleDependencies`), link step in `template_engine.go`, lib lookup in `dependency_resolver.go`. |
| 6. Go and Rust as tool-cached tasks | 4, 5 (paths), S6, S7 | `templates/go_code.yaml`, `templates/rust_code.yaml` (`tool_cached` metadata, outputs), Rust toolchains (`--target-dir`), `expandBuildStep`, delete `scanSourceFiles`, executor. |
| 7. Execution settings per task | 9, S11, S14 | `models.go` (`Exec`, `ToolchainHash` in key), task creation sites, `util/exec.go`, executor. |
| 8. Build context and module selection | 7, S13 | `main.go`, `workspace.go` (load with context, delete the discovery cache), `config_parser.go`, `builder.go`. |
| 9. One strict loader | 11, S12 (config errors) | `workspace.go` loaders, `yaml_parser.go` schema, dependency file loading, delete `ParseConfigFile`. |
| 10. Target closure and dry-run | 8, 10, S10 | `config_parser.go` (no filter in generation), `task_graph.go` (closure), `builder.go`, `main.go`, `dependency_resolver.go` (plan mode). |

Coordination: the `ci-releases` branch touches `main.go` and `toolchain_manager.go`. The steps are
built on top of it.

---

## 11. Decisions

The open questions, as decided:

1. **Go version.** Move `go.mod` to Go 1.25 and use `os.Root` for extraction (section 8). Extraction
   still goes into a private temporary directory that is renamed into place.
2. **Same-named targets in different modules** are rejected, with an error naming both modules.
3. **Go and Rust always run their tool** and are reported as "ran", not as cache hits.
4. **Docker** mounts the workspace and cache dir at their host paths; the `/workspace` convention
   is dropped.
5. **Validation** stays map-based with a corrected schema.
6. **Dependency builds** stay a phase before generation; dry-run plans them.

Out of scope, deliberately: compiler versions in cache keys, system headers (`-MMD` convention),
Docker image digests, and a remote cache.

---

## Appendix A: Reproductions

All were run with a binary built from `main` (`go build -o $SCRATCH/bin/buildy .` in `src/`), in
fresh scratch directories. C++ fixtures share this environment block:

```yaml
environment:
  compile:
    cpp_standard: "c++17"
```

**1. Extraction.** A test in package `resource` calling `extractZip` and `extractTarReader`:

- ZIP entries `pkg/README` and `pkg/../../zip_escape.txt`, extracted into `root/a/b/dest`:
  `root/a/zip_escape.txt` exists.
- Tar entries `pkg/` (dir), `pkg/link` (symlink to `root/outside`) and `pkg/link/tar_escape.txt`:
  `root/outside/tar_escape.txt` exists. Entry `pkg/../tar_dotdot.txt` lands in `root/`.

**2.** `main.cpp` prints `VALUE`. Executables `one` (`defines: ["VALUE=1"]`) and `two`
(`defines: ["VALUE=2"]`), both with `sources: ["main.cpp"]`. Both compile to `obj/main.o`; both
print `2`.

**3.** Static library `calc` (`add.cpp`: `return a + b;`) and executable `app` (`libs: [calc]`)
printing `add(2,3)`. Build, change to `a * b`, build again: `compile_calc` and `link_calc` execute,
`link_app` is a cache hit, the app prints `5`.

**4.** `go.mod` (`module example.com/r4`), `internal/greet/greet.go` returning `"v1"`,
`cmd/app/main.go` printing `greet.Hello()`. Target `{name: app, language: go, path: cmd/app}`.
Build, change to `"v2"`, build: "2 cache hits", prints `v1`.

**5.** Cargo package `hello/` and target `{name: hello, language: rust, path: hello}`. Declared
output `…/debug/bin/hello`; Cargo writes `…/debug/hello`. Build twice so `Cargo.lock` exists, delete
`…/debug/hello`, build: "2 cache hits, 0 executed", file absent.

**6.** `main.cpp` includes `"config"` (`#define VALUE 1`) and `"other.h"`. Editing `config` leaves the
output at `11`; editing `other.h` rebuilds. The cache entry's `header_dependencies` is `['other.h']`.
The same happens with `#include "inc dir/v.h"` (S8).

**7.** Root `workspace: {modules: [{linux: ["platform-linux"]}]}`; `platform-linux/buildy.yaml`
defines executable `linuxonly`. `buildy --platform linux`: "Generated 0 tasks from 0 modules", exit 0.

**8.** Modules `libmod` (static library `calc`) and `appmod` (executable `app`, `libs: [calc]`).
`buildy --target app` on a clean tree: link fails. After a full build, change `add.cpp`, delete the
app, and run `--target app`: the stale `libcalc.a` is linked, the app prints `5`, exit 0.

**9.** Any C++ executable, `buildy --toolchain docker-cpp-linux` with a running daemon: the log says
"Execution environment: docker"; `readelf -p .comment main.o` shows the host's
`GCC: (Ubuntu 13.3.0-6ubuntu2~24.04.1) 13.3.0`.

**10.** A local git repo with a Makefile that appends to `side_effect.log`, and
`buildy_config/dependencies.yaml` with `mydep: {git: <repo>, build: {system: make}}`.
`buildy --dry-run`: the dependency is cloned into `.buildy_cache/deps/mydep` and `side_effect.log` has
two lines.

**11.** `bogus_key: 1` at top level and `defnies: ["X=1"]` on an executable: the build succeeds,
exit 0.

**S1.** Two URL dependencies served locally, `/foo/v1.0.tar.gz` and `/bar/v1.0.tar.gz`, with no
checksums: `.buildy_cache/deps/bar/WHO` contains `FOO`.

**S3.** A library built from `add.cpp` and `sub.cpp`; remove `sub.cpp` and rebuild: `ar t libcalc.a`
still lists `sub.o`.

**S4.** Modules `m1` and `m2`, each with an executable `tool`: one `bin/tool` exists, exit 0.

**S5.** The fixture from 2, built with `cd sub && buildy ..`: the object lands at
`obj/<absolute path of the fixture>/main.o`.

**S10.** `buildy --target nosuch`: "Generated 0 tasks", exit 0.
