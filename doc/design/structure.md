# Structure, Dead Code and Fail-Fast Gaps (review phase 6)

Status: **implemented** on `review-fixes`. This is phase 6, the last, of the fixes for the review
of 2026-10-11: sections 6.5 (process-global state and hand-wired dependencies), 6.6 (platform and
toolchain preferences in Go) and 7 (dead code, duplication, fail-fast gaps, traps). It follows
[layout](layout.md) (phase 5).

## What a generation owns

Target and artifact names were registered in a package global, `globalTaskRegistry`, cleared at
the start of each generation; `--target` read it after generation. Two generations in one process
(tests in parallel) wiped each other's names.

A generation now owns them (`generatedNames`, created by `ConfigParser.GenerateWorkspaceTasks` and
passed to each module's task generator): each target's final task (its link or build) and each
named artifact's module, outputs and tasks. `GenerateWorkspaceTasks` returns the tasks with each
target's final task by name (`Generated.Targets`), which `TaskGraph.Select` takes for `--target`.
The output path of a target is its final task's first output, not a copy kept beside it.

The target registry's scoped (`module:target`) and local (`:target`) resolution was never called;
what was used of it, the check that target names are unique across the workspace, is a function
(`checkTargetNames`). An artifact name defined twice in one module is now an error, as across
modules: the second silently replaced the first.

## Edges: producer edges, and the explicit ones that remain

Graph validation adds an edge from every task to the producer of each of its inputs. Hand-wired
edges that did the same are gone:

- a staging task copying or linking a target's output, or an artifact's output, depended on the
  producing task by ID. For artifacts the outputs were matched to task IDs **by index**, so a
  generate step with several outputs per input staged files after the wrong tasks. The staged
  file is the staging task's input; its producer edge is the right one, and the only one;
- a copy of a target's output (`artifacts.copy` with `target:`) depended on the target's final
  task: the output is the copy's input.

What stays explicit, because there is no file relation to derive it from:

- `depends_on.targets`: build order only (a code generator run before a target, no linking);
- `depends_on.artifacts`: a target's tasks, compiles included, run after an artifact's tasks; a
  generated header is no declared input of a compile;
- `artifacts.copy`'s `depends_on`;
- a template step's `depends_on` (the built-in templates' link steps name `${compile.task_ids}`,
  which the producer edges already give; the key stays for templates of one's own).

A workspace target named in `libs:` becomes an input of the link (its library file), so its edge
is a producer edge too.

## Toolchain priority

The default toolchain of a platform and architecture came from lists of names in Go (one for a
module, one per language for a target in another language), then from the first match by name.
Adding the Apple Silicon toolchains took a Go change.

A toolchain file now says `priority:` (default 0). Of the native toolchains whose `target` is the
platform and architecture (and, for a target's language, whose `language` is it; failing that,
those with `target.platform: any`), the one with the highest priority is the default; two with the
highest priority are an error that names both and says how to choose. The built-in priorities
reproduce every choice buildy made before, for every platform and architecture
(`TestDefaultToolchains`): GCC C++ on Linux (40, then GCC C 30, Clang C++ 20, Clang C 10), MSVC C++
on Windows (40, 30, then MinGW 20, 10), Apple Clang C++ on macOS (20, then C 10), for each
architecture. On linux-arm and wasm the first by name used to win, which is the C toolchain;
`gcc-c-arm-cross` and `emscripten-c` carry priority 10 to keep that, and say so.

A target in another language than its module's toolchain (a C target in a C++ module) now resolves
its toolchain filter keys, and the module's abstract keywords, with the toolchain that compiles it;
it got the module toolchain's name and flag_mappings. Transform and generate tools resolve their
filter keys with their own toolchain too.

## Object names

Objects were `${source_rel}${tool.output_ext}`: `a.c` and `a.cpp` in one target were both `a.o`,
which validation rejected. The built-in templates now keep the source's extension, as CMake does:
`${source_rel}${source_ext}${tool.output_ext}` (`a.cpp.o`, with `a.cpp.d`). `source_rel` keeps
its meaning (without the extension) for templates of one's own; `source_ext` is new. Task IDs are
unchanged. The first build after this compiles every C and C++ source again.

## Discovery and the cache directory

Discovery and the workspace root search skipped the literal `.buildy_cache/**`, so with
`--cache-dir` elsewhere inside the workspace the fetched dependencies' configs became modules. The
workspace now knows its cache directory (`NewWorkspace(root, cacheDir)`) and skips it by path,
whatever its name. `.buildy_cache` is skipped only when it is the cache directory.

A discover or exclude pattern that can't match was a warning while walking and matched nothing; it
is an error when the config loads.

## Fail-fast gaps

- `NewBuildTask` stamped every task `linux`/`x86_64`/`debug`. A task's context is set by its
  generator, and validation fails a task without one, so a forgotten context can't record a cache
  entry under the wrong one. Generation's other silent defaults went too: a missing task ID
  generator (a module named "workspace") and the template engine's fallback target names.
- Errors that were dropped: a copied file's `Close` (a full disk), the removal of a bad cached
  download (whose failure extracted the bad archive) and of a stale extraction marker, an
  unreadable or corrupt download metadata file (treated as "none"), and the lookup of a generate
  tool's command template.
- Stale extraction directories were found with a glob over a path that may hold glob syntax;
  names are compared instead.
- Already gone before this phase: `resolveVariables` exiting from library code, the ignored
  `SetToolchain` and `MkdirAll` errors, and `generateTargetTasks` replacing the module's command
  builder with another language's (it keeps a local one).

## Dead code

Deleted: the lockfile manager (never constructed), the error collector (nothing ever added to it,
so the "warnings are treated as errors" check after a build could not fail), the target registry,
`GetGitCommit`, download metadata and checksum helpers, unused log, string, hash and variable
helpers, `NewToolchainManager` and `NewBuildTemplateEngine` (single-directory wrappers), the
template engine's unused list of earlier tasks and parameters, the command builder's tool matcher,
and the path resolver's workspace root. `prependMkdir`, the `ResolveSources*` wrappers,
`ParseSourceEntries` and the `CacheEntry` platform fields had gone in earlier phases.
`golang.org/x/tools/cmd/deadcode` (with and without tests) now reports only
`BuildSystemManager.LoadFilesystemSystems`, kept on purpose (below).

Kept, undecided: `LoadFilesystemSystems` and the documented `buildy_config/build_systems/` (not
loaded), and the Go and Rust tools' `supports` flags (`build_tags`, `features`, ...), documented
and unread. *The `supports` flags are deleted since ([Go and Rust options](commands.md#go-and-rust-options)).*

## Duplication

`LoadToolchainConfig` and its copies became one parser in phase 1 (`config.LoadToolchain`). This
phase shares five copies of "is this path inside that directory" (`util.PathWithin`, which compares
as `NormalizePath` does, case-insensitively on Windows), the `build/` and `unknown`/`0.0.0`
defaults, the link and build steps' output pattern, and download hashing (`util.HashFile`).

## Tests

- Generations running at once share nothing (`TestGenerationsShareNoState`, under `-race`);
  artifact names unique within a module (`task_id_test.go`); a staging task depends on its
  producer only, with two outputs per generate input (`TestStagingDependsOnItsProducer`); a task
  without a build context fails validation (`task_graph_test.go`).
- The default toolchain of every platform and architecture, and of every language, as before;
  a higher priority wins; a tie is an error naming both (`toolchain_manager_test.go`).
- A C target in a C++ module gets its own toolchain's filter keys
  (`TestTargetFiltersUseItsOwnToolchain`); `a.c` and `a.cpp` in one target are two objects
  (`TestObjectsKeepTheSourceExtension`); discovery skips the cache directory `--cache-dir` names,
  relative or absolute (`TestDiscoveryExcludesTheCacheDirectory`); a bad exclude pattern fails
  to load (`TestWorkspacePatternsAreChecked`).
- Mobius and Monstro, built as their CI builds them: every task's arguments are unchanged apart
  from the object and dependency file names, and so are their dependencies, contexts and
  toolchains.

## Left for later

- `build.system: auto` picks the first by name between build systems of equal detection
  priority (cmake and meson are both 10), where toolchains now fail on a tie.
- The template engine still passes the target to templates as name-keyed maps (`${item.X}`,
  `${config.X}`), unchecked against the model.
- Windows and macOS: toolchain selection is tested as a table for every platform; it has not run
  there.
