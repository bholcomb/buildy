# Execution, Cache and Interrupts (review phase 3)

Status: **implemented** on `review-fixes`. This is phase 3 of the fixes for the review of
2026-10-11. It fixes findings 4.1 (the cache index rewritten after every task), 4.2 (ConfigHash),
1.3 and 6.3 (no toolchain identity in the key), 4.4 (no-op builds hash everything), 4.5 (stage
barriers), 5.1 (warnings never shown), 3.2 (timeouts that don't stop children), the signal half of
3.1, 1.12 (transform flags), and the missing frameworks of the macOS C++ toolchains. It follows
[typed configuration](typed-config.md) (phase 1) and [commands as argument lists](commands.md)
(phase 2).

## The cache

### Files

The cache directory holds two files for the cache entries:

| File | What it is |
|---|---|
| `cache_index.json` | A snapshot: `{"version": 2, "entries": [...]}`, written at the end of a run |
| `cache_journal.jsonl` | One entry per line, appended as tasks finish |

An entry is a task's cache key, its task ID, its context (`platform-architecture-configuration`),
the hash of each declared output, and the hash of each input its dependency file reported.

### Recording, loading, compacting

- **Record** appends the entry to the journal as one `write` of one line, under a mutex held only
  for that write. It does not fsync: a lost entry makes its task run again, it never makes a wrong
  hit (a hit needs the outputs on disk with their recorded hashes). Recording is O(1) per task;
  before, every task rewrote the whole index under a global lock.
- **Loading** reads the snapshot, then replays the journal over it in order. A last line without
  its newline was cut short by a crash, and is ignored. Any other line that doesn't parse is an
  error naming the line: delete the file to start afresh.
- **Close** (compaction) runs once per run, after execution, whether the run succeeded, failed or
  was interrupted. It keeps the newest entry of each task ID and context, writes the snapshot to a
  temporary file, fsyncs it, renames it into place, then removes the journal. A crash between the
  rename and the removal leaves a journal whose entries are all in the snapshot; replaying it
  changes nothing.

So a run that is killed keeps the entries of every task that finished (they are in the journal),
and the index doesn't grow with every edit: an older entry of the same task in the same context
can never hit again (its outputs were overwritten) and is dropped. Entries of tasks that no longer
exist stay, one per task.

A snapshot of another version (an index from an earlier buildy, a map of keys to entries) is
discarded with a message: its keys can't match, every task runs again.

Two buildy processes sharing a cache directory can lose each other's entries (the last compaction
wins); the cost is tasks running again, not wrong results.

### Measured

1500 C sources built into a static library and, again, into an executable (3005 tasks), `-j 8`:

| | Before | After |
|---|---|---|
| Clean build, small sources | 34.5 s | 5.0 s |
| Clean build, 200 KB objects (1.5 GB tree) | 36.2 s | 6.3 s |

The old index grew to 3 MB, and was rewritten after each of the 3005 tasks: about 4.5 GB of JSON.

## The cache key

A task's key is a hash of exactly what decides its outputs:

| Part | What |
|---|---|
| `inputs` | each declared input's path and content hash |
| `outputs` | each declared output's path |
| `command` | the arguments (or a shell tool's script) and the working directory |
| `native` | or, for a copy, symlink, template or archive, what buildy does |
| `exec` | natively or in which Docker image, with the toolchain's `env`, and the names it reads |
| `tool` | what the command runs: the program's real path and content hash, or the image's ID |
| `env` | the values of the variables the toolchain reads (`reads_env`) |

What is **not** in it, and why:

- **The module's config hash** (ConfigHash, finding 4.2). Every task carried a hash of its whole
  module config, so adding a target or changing one target's define reran the whole module. The
  configuration reaches a task only through its command and outputs: flags, defines, include and
  library directories, the standard and the output prefix are arguments or paths.
- **The toolchain's YAML hash.** A toolchain change that matters changes a command, an output path,
  the execution settings or the variables read. A changed description no longer rebuilds.
- **The task type and the platform, architecture and configuration names.** They decide nothing
  the command and outputs don't (the output directory is per context).

Tests generate a workspace, change its config, and compare the keys: a define, a flag or a new
target changes no other target's keys, and a define, flag, include directory, standard, module
flag, output prefix, link flag, library or toolchain flag each changes the keys of the tasks it
affects (`internal/workspace/cache_key_test.go`). End to end, adding a target runs exactly that
target's two tasks.

### What a command runs

Once per run per program, before the first task that runs it, the executor resolves the program
(`internal/build/tool_identity.go`):

- a native program is looked up as `os/exec` will run it: on the `PATH` for a bare name, relative
  to the command's directory for a path with a directory part. Its symlinks are resolved, and its
  identity is its real path and SHA-256. The hash goes through the file hashes (below), so an
  unchanged compiler is not read again in later runs;
- a shell tool's script is identified by the shell (`sh`, `cmd.exe`), as the programs inside the
  script aren't known;
- a Docker task by its image's ID (`docker image inspect`, after `docker pull` when the image is
  absent, as `docker run` would).

A program that can't be found fails the task with its name. The path is part of the identity
because a driver finds its own programs relative to itself: gcc's `cc1`, `as` and `ld` are not
hashed, and an upgrade of the compiler package replaces the driver too. A toolchain whose driver
stays byte-identical while its internals change would need its internals listed; none of the
built-in ones does.

### The environment a toolchain reads

A toolchain lists the environment variables its tools read, an optional key:

```yaml
toolchain:
  name: gcc-c-linux
  reads_env: [CPATH, C_INCLUDE_PATH, CPLUS_INCLUDE_PATH, OBJC_INCLUDE_PATH, LIBRARY_PATH,
              COMPILER_PATH, GCC_EXEC_PREFIX, LD_RUN_PATH, SOURCE_DATE_EPOCH]
```

The value each one has for the command (the toolchain's `execution.env`, else buildy's
environment) goes into the key; an unset variable differs from an empty one. A Docker container
sees only the toolchain's `env`, already in the key, so the host's values don't count there. The
built-in toolchains declare:

| Toolchains | Variables |
|---|---|
| GCC, Clang (Linux), MinGW, ARM | `CPATH`, `C_INCLUDE_PATH`, `CPLUS_INCLUDE_PATH`, `OBJC_INCLUDE_PATH`, `LIBRARY_PATH`, `COMPILER_PATH`, `GCC_EXEC_PREFIX`, `LD_RUN_PATH`, `SOURCE_DATE_EPOCH` |
| Clang (macOS) | `CPATH`, `C_INCLUDE_PATH`, `CPLUS_INCLUDE_PATH`, `OBJC_INCLUDE_PATH`, `LIBRARY_PATH`, `SDKROOT`, `MACOSX_DEPLOYMENT_TARGET`, `DEVELOPER_DIR`, `ZERO_AR_DATE`, `SOURCE_DATE_EPOCH` |
| Emscripten | `CPATH`, `C_INCLUDE_PATH`, `CPLUS_INCLUDE_PATH`, `EMCC_CFLAGS`, `EM_CONFIG`, `SOURCE_DATE_EPOCH` |
| MSVC | `INCLUDE`, `LIB`, `LIBPATH`, `CL`, `_CL_`, `LINK`, `_LINK_`, `EXTERNAL_INCLUDE` |

Go and Rust tasks always run (their tools keep their own caches), so their toolchains declare
none. `PATH` is not in the key by default: the program's resolved path covers which compiler runs,
and a `PATH` change (activating a virtualenv) shouldn't rebuild everything. A toolchain whose tools
find other programs on the `PATH` can list it.

End to end: changing `CPATH` so that a same-named header comes from another directory rebuilds,
and so does a `gcc` wrapper earlier on the `PATH`; both kept the stale binary before.

## File hashes

Inputs, outputs, discovered inputs and programs are hashed through the cache's `FileHashes`
(`internal/cache/filehash.go`):

- **Within a run** a file is read once, however many tasks name it (an object is a compile's
  output and a link's input; a header is in many dependency files). The stat is taken before the
  read, and a later lookup compares it, so a file that changes during the run is read again. A
  task's outputs are forgotten before it runs.
- **Across runs** `file_hashes.json` keeps each hash with the file's size, modification time,
  change time, inode and device (on Windows: size, last write time and creation time). A file whose
  stat is unchanged is not read; any difference reads it again. The change time can't be set back
  by tools that restore an mtime.
- **Racy files.** A write within the same timestamp tick as the one that set a file's times leaves
  its stat unchanged (git's "racily clean" problem). A hash is kept for later runs only when the
  file's times were more than 2 s older than the moment buildy read it, which covers the coarsest
  timestamps in use (FAT's 2 s) and the clock ticks behind Linux's. An output written just before
  it was hashed, the usual case, is read once more by the next run that comes 2 s later or more.
- Saving keeps the hashes of the files this run used, and of earlier files it didn't use while
  their stat is unchanged (another configuration's outputs); deleted files drop out.

No-op build of the 1.5 GB tree (3005 tasks, everything a cache hit): 1.10 s before, 0.55 s after,
of which execution is 0.04 s; the rest is generation and writing `tasks_*.json`. A small tree's
no-op (0.4 s) is all generation.

## The scheduler

Tasks ran in stages, each waiting for the slowest task of the one before, and the parallelism was
divided by guessed memory needs (100 to 512 MB per task against a fixed 8 GB) that measured
nothing.

Now the executor keeps a ready queue of the tasks whose dependencies have all finished, smallest
task ID first, and runs up to `-j` of them at once; a task starts as soon as its last dependency
finishes. After a failure, or an interrupt, it starts no more tasks, waits for the running ones,
and says how many never started (`--keep-going` was not added).

`TaskGraph.Order` gives a topological order (the same ready-queue rule), numbers the tasks for the
logs, and fails on a cycle or an unknown dependency. `--dry-run` prints the tasks in that order,
each with what it waits for:

```
Dry run: 6 tasks, in an order they can run in (nothing is executed)
  [1] compile_app_main_cpp_001__appmod
  [2] compile_calc_add_cpp_001__libmod
  [3] compile_other_broken_cpp_001__appmod
  [4] link_calc_001__libmod  (after compile_calc_add_cpp_001__libmod)
  [5] link_app_001__appmod  (after compile_app_main_cpp_001__appmod, link_calc_001__libmod)
  [6] link_other_001__appmod  (after compile_other_broken_cpp_001__appmod)
```

A task with more than four dependencies lists four and "and N more".

`tasks_*.json` records the order as `execution_order`. Removed as dead: the tasks' estimated times,
CPU and memory and disk requirements, the plan's statistics (estimated time, CPU hours), and the
memory throttle.

## Command output

A command's standard output and error go to one buffer, written in order by one goroutine
(`cl.exe` writes diagnostics to stdout, gcc to stderr). A task that succeeds with output prints it
at the default log level, indented under its completion line, in one log message, so tasks running
in parallel don't interleave:

```
[11/163] compile_io_io_platform_posix_cpp_001__src_modules (0.2s) - completed, with output:
    .../io_stream.h:56:13: warning: 'void stream_destructor(void*)' defined but not used [-Wunused-function]
```

A failure shows the exit code, the command line and the output the same way. A dependency build
phase that fails reports its output. Mobius's build shows 25 tasks' warnings that were never seen.

## Timeouts and interrupts

### Process groups

Every command buildy runs (tasks, dependency build phases, git) runs in a process group of its own
(`Setpgid`; on Windows `CREATE_NEW_PROCESS_GROUP`). When its timeout passes or the run's context is
done, the whole group is killed (`kill(-pgid, SIGKILL)`; on Windows `taskkill /T /F`, as Windows
has no group kill and a job object would need the process created suspended). Before, only the
program buildy started was killed, and a grandchild kept the output pipes open: a task whose child
slept past the timeout failed only when the child finished.

`cmd.WaitDelay` is 5 s: a process that left the group (`setsid`) and keeps the output open after
the command exits or is killed can't hang buildy; the task fails saying so.

A Docker task runs with `--init` (signals forwarded, zombies reaped) and `--name buildy-<pid>-<n>`,
and is stopped with `docker kill <name>`: killing the `docker` client leaves its container running.

### `--timeout`

`--timeout` (a Go duration: `90s`, `30m`, `2h`) bounds each task and each dependency build phase.
The default is **no limit**. The old fixed 300 s (600 s per dependency phase) was hidden and wrong
in both directions: a build step's time has no natural bound (an LTO link of a large program, a
slow CI machine, a large dependency's build), a default that kills legitimate builds is worse than
none, a hung step can now be interrupted with Ctrl-C, and CI systems bound a job's time
themselves. CI that wants a tighter bound passes `--timeout`.

### Ctrl-C

The first SIGINT or SIGTERM cancels the run's context. Everything that runs a command or fetches
takes that context: dependency resolution, git, downloads (the HTTP request), dependency build
phases, `pkg-config`, Docker image lookups, and tasks. So, at any point of a build:

- running commands' process groups (and containers) are killed;
- an interrupted task's partial outputs are removed;
- no task starts;
- the cache is compacted, keeping the entries of the tasks that finished;
- buildy exits with 130.

A second signal exits at once, also with 130. Even then a partial output can't pass for a built
one: an output counts only with a recorded entry whose hash it matches, and a task's outputs are
deleted before it runs.

Before, the signal goroutine called the shutdown, which waited for the running tasks, before it
could see a second signal; and nothing passed its context on, so Ctrl-C did nothing while a
dependency was fetched or built. The shutdown manager, its cleanup registry and task tracking are
gone. The HTTP download still has no timeout of its own (phase 4).

## Transform and generate flags

A transform tool got its configuration's flags or, when the configuration had none, its common
flags; a generate tool got none. Both now get, like compile tools, the module's abstract keywords
as the tool's own toolchain's `flag_mappings` map them (glslc's map `optimization: speed` to `-O`;
they were never read), then the tool's `common` flags, then the configuration's. The built-in
compressonator, ImageMagick and texconv toolchains repeated their common flags in each
configuration and now list them once. A user toolchain written for the old rule ("the
configuration's flags replace the common ones") now gets both.

## macOS frameworks

The C toolchains for macOS linked with `${frameworks}`; the C++ ones didn't, so a C++ program using
GLFW from Homebrew (Mobius's `glfw3` dependency: Cocoa, IOKit, CoreVideo, OpenGL) would link
without them. Both C++ toolchains now take a target's and its dependencies' frameworks.

## Tests

- Cache: the journal is appended to and replayed, an interrupted run keeps its entries, an
  incomplete last line is ignored, a damaged line is an error, compaction keeps the newest entry per
  task and context, replay over a compacted index, an old index is discarded
  (`internal/cache/journal_test.go`). File hashes: one read per run, a changed size, mtime or change
  time reads again, racy files aren't trusted, deleted files drop out (`filehash_test.go`).
- Keys: the key holds the tool and the declared environment, unset differs from empty, Docker
  ignores the host's (`cache_test.go`); config edits (`internal/workspace/cache_key_test.go`); the
  program found on the `PATH`, an upgrade, a symlink, a missing program, one resolution per run
  (`internal/build/tool_identity_test.go`).
- Scheduler: a task starts when its dependencies finish while an unrelated one still runs; after a
  failure nothing starts and the running task finishes (`executor_test.go`); `Order`
  (`task_graph_test.go`).
- Processes: on timeout and on interrupt the group is killed, grandchild included; a process that
  escaped the group doesn't hang `Run`; a Docker container is killed on timeout
  (`pkg/util/exec_unix_test.go`); the executor times a task out and removes its partial output; a
  second interrupt exits with 130 while the first one's handling hangs (`signal_handler_test.go`).
- Flags: transform and generate tools get keywords, common and configuration flags
  (`tool_flags_test.go`); every macOS link tool and a generated C++ executable and shared library
  pass `-framework` arguments (`command_builder_test.go`, `tool_flags_test.go`).
- End to end: adding a target or a define reruns only that target; `CPATH` and another `gcc` on the
  `PATH` rebuild; warnings appear under their task with `-j`; Ctrl-C during a task, a download from a
  server that never answers, and a dependency's `make` exits with 130 within seconds, kills the
  children and removes the partial output, and the finished tasks are cache hits next time;
  `many_objects` also links an executable from 1500 objects with a response file and rebuilds
  nothing the second time. Each of these fails with the binary from before this phase.

## Real-world check

Copies of the `ci-releases` branches of Mobius and Monstro (their `name: {file: ...}` dependency
index entries removed), built as their CI does, on Linux x86-64:

| | Before | After |
|---|---|---|
| Mobius `buildy -c release -j 4`, clean (includes GLFW's cmake builds, ~58 s) | 96.2 s | 95.5 s |
| Mobius, no-op | 0.17 s | 0.13 s |
| Mobius tests (`run_tests.mob`, C# binding, portable build) | pass | pass |
| Monstro `ci/build.py` (library, headless suite plain and sanitized, C# binding) | pass, 110 s | pass, 111 s |
| Monstro library alone, clean | 3.0 s | 3.1 s |
| Monstro library, no-op | 0.03 s | 0.03 s |

Their clean builds are bound by compile time and, for Mobius, by its dependencies' cmake builds,
which run on every build in both configurations until phase 4.

## Left for later phases

- The HTTP download timeout, and fetching and building dependencies incrementally (phase 4).
- Windows (`taskkill`, `CREATE_NEW_PROCESS_GROUP`, MSVC environment variables, file hashes without
  a change time) and macOS (`reads_env` values, frameworks) are built and vetted for those targets
  but have not run there.
- A job object on Windows would also catch processes that outlive their parent; `taskkill /T`
  finds a tree by parent process IDs at the moment it runs.
