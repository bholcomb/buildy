# Fetched Dependencies (review phase 4)

Status: **implemented** on `review-fixes`. This is phase 4 of the fixes for the review of
2026-10-11: findings 1.1 (release builds linked the debug build of a dependency), 1.10 (git
dependencies), 3.1 (downloads without a timeout), 4.3 (dependencies fetched and built on every
run), 5.5 (pkg-config flags dropped) and 6.2 (dependency builds outside any record). It follows
[execution](execution.md) (phase 3), whose tool identities and interrupts it uses.

## The phase

Dependencies are not tasks (decision 6 of the [task graph contract](task-graph-contract.md)).
Task generation resolves each dependency the first time a target's `deps:` names it, before it
generates that target's tasks; a dependency no target of the build uses is never fetched, built or
looked up (in 0.2.0 and before, every defined dependency was, in name order). To resolve one, buildy:

1. **Fetches** it, if it has `git:` or `url:`, into `.buildy_cache/deps/<name>/` (`${dep_dir}`);
2. **builds** it, if it has a `build.system`, for the configuration being built only;
3. **resolves** what it provides: the definition's `include_dirs`, `lib_dirs`, `libs`, `frameworks`
   and `defines`, then the build system's output directories that exist and aren't listed yet
   (or, for `pkg_config:`, pkg-config's flags).

Before, every run fetched every git dependency again and built every dependency in debug and in
release, whatever `-c` asked for, and took the include and lib dirs from the debug build: a release
build linked the debug library. The phase is now incremental: a no-op build fetches nothing and
runs no command of a dependency's, and `-c release` builds and uses the release build.

## On disk

```
.buildy_cache/
├── downloads/<hash>-<file>                 # archives, by a hash of the URL (unchanged)
└── deps/
    ├── <name>/                             # ${dep_dir}: the clone or the extracted archive
    │   ├── _build/<platform>-<arch>-<config>/            # ${dep_build_dir}
    │   ├── _build/<platform>-<arch>-<config>.built.json  # the build's marker
    │   └── _install/<platform>-<arch>-<config>/          # ${dep_install_dir}
    ├── <name>.git.json                     # what the clone was fetched for
    └── <name>.extracted.json               # what the archive was extracted from (unchanged)
```

Each configuration has its own build and install directories, so switching between debug and
release reuses both builds. The platform and architecture are in the name too: a cross build
doesn't take another target's build.

`${dep_build_dir}` and `${dep_install_dir}` are new dependency variables, set for a dependency
that has a build system. The examples that said `${dep_dir}/_install/include` named a directory
that stayed empty (the install phase never ran); they now say `${dep_install_dir}/include`. The
build system's output directories are added anyway, so a cmake dependency that installs needs no
`include_dirs` or `lib_dirs` at all.

## Configurations

A build system's definition may map buildy's configurations to its own (`configurations:`); the
mapped value is the phases' `${build_type}`:

| Build system | debug | release | Other configurations |
|---|---|---|---|
| cmake | `Debug` | `Release` | an error, until mapped |
| meson | `debug` | `release` | an error, until mapped |
| cargo | `dev` (output in `debug/`) | `release` | an error, until mapped |
| buildy | `${config}` as is | | any: the dependency's config defines them |
| make, autoconf, go_mod | no configurations | | |

A dependency maps a configuration its build system doesn't know in `build.configurations`, which
adds to and overrides the build system's map:

```yaml
dv:
  git: "https://example.com/dv.git"
  build:
    system: cmake
    configurations: {portable: Release}
```

Building `-c portable` without that mapping fails, naming the configuration and showing the entry
to add. Guessing (`Release` for anything not called debug) would silently build the wrong thing.
`build.configurations` on a build system without configurations is an error too: it would mean
nothing. For those (make, autoconf, Go), `build.args` see the dependency's variables, so
`args: ["CFLAGS=-O2 -DMODE=${config}"]` passes a configuration's settings.

cmake gets `-DCMAKE_BUILD_TYPE=${build_type}` exactly once (the old definition had a default
`-DCMAKE_BUILD_TYPE=Release` and buildy appended `-DCMAKE_BUILD_TYPE=Debug` after it), and its build
and install get `--config ${build_type}`: a multi-configuration generator (Visual Studio, cmake's
default on Windows, or Xcode) ignores `CMAKE_BUILD_TYPE` and builds Debug unless `--config` says
otherwise; a single-configuration generator ignores `--config`. The install prefix is
`${dep_install_dir}` with `CMAKE_INSTALL_LIBDIR=lib`, so libraries land in `lib/` and not `lib64/`.

## Phases

A build system's phases run in this order, each one it defines:

| Phase | Runs |
|---|---|
| `clean` | only for an `in_source` build system, before a build of a tree that was built before |
| `configure` | if defined |
| `build` | always (required: a definition without it doesn't load) |
| `install` | if defined, into `${dep_install_dir}` |

There is no magic command any more: a phase whose command was the text `true` was skipped (make's
and cargo's configure). A build system simply doesn't define a phase it doesn't need. `test` was
never run and is gone, and so is `required:`, which nothing read. The built-in build systems:

- **cmake**: configure, build, install (above).
- **meson**: `meson setup` with `--buildtype`, `--prefix=${install_dir}` and `--libdir=lib`;
  `meson install` (the old `--destdir` installed under `${install_dir}/usr/local`).
- **autoconf**: a VPATH build: `configure` runs from the build directory, then `make` and
  `make install` there.
- **make**: `in_source: true`; build and clean, no install (a Makefile's `install` target and its
  prefix variable aren't standard, and a wrong guess installs into `/usr/local`).
- **cargo**: `cargo build --profile ${build_type} --target-dir ${build_dir}`; no install
  (`cargo install` builds again and installs binaries only).
- **go_mod**: `go build`; no configure (`go mod download` fetched on every build) and no install
  (`go install` writes to the user's `GOBIN`).
- **buildy**: buildy itself on the dependency's tree (or its `build.override` config), with
  `--config ${config}`; it writes `build/<platform>-<arch>-<config>/` and keeps its own cache.

### In-source builds

make builds in the source tree, which can hold one configuration's objects at a time. Its marker
is `_build/in-source.built.json`, one for the tree: building another configuration (or the same
one with other arguments) doesn't match it, and runs `make clean` before `make`. While a build runs
the marker holds `{}`, so a tree whose build failed or was interrupted is cleaned first next time.
A freshly cloned or extracted tree isn't cleaned (a Makefile without a `clean` target builds once).

## Build systems of your own

*Added after phase 6, on `build-systems`.* Libraries with other build systems (SCons, a Makefile
with an install target, a script) are built with a definition of your own: the model the
built-in ones use, loaded from files. Nothing new is described; what the model said is loaded
and checked.

**Search order**, one for templates, toolchains and build systems (lowest priority first; a
later definition replaces an earlier one of the same name):

1. built-in;
2. `data/build_systems/` next to the buildy executable (`os.Executable()`, symlinks resolved:
   `os.Args[0]` named the current directory when buildy ran from the PATH);
3. each `--add-data-dir`'s `build_systems/`, in order;
4. the workspace's `buildy_config/build_systems/`.

Build systems are loaded with a workspace's dependencies; standalone mode (a config file) has
none.

**Overriding: a definition with a built-in's name replaces it**, like a toolchain or a template,
and buildy logs it at info level naming both files (`Build system 'make' from ... replaces the one
from built-in build_systems/make.yaml`). Two definitions of a name in one directory are an error.
An explicit opt-in (`replaces: make`) was considered and not done: the three kinds would differ,
and a definition in your workspace named like a built-in can only mean to replace it.

**Strict loading.** Beyond unknown keys, types and the phase rules above, a definition fails to
load, naming the file, for:

- a missing or empty `name`, or `auto` or `none` (they mean something else in `build.system`);
- a placeholder that isn't one of `config.BuildSystemPlaceholders`: `source_dir`, `build_dir`,
  `install_dir`, `config`, `platform`, `arch`, `build_type`, `jobs`, `args`, `buildy_exe`,
  `config_file`. A test checks a build gives each one a value;
- `${build_type}` without `configurations` (it would fail at build time), `${args}` in a
  `working_dir` or output pattern (a path is one word), a placeholder in `default_args` (they are
  values, passed as written: before, `${config}` there reached the tool as the text `${config}`),
  and an empty `configurations` map;
- with `shell: true`, a placeholder inside double quotes, as for a tool.

`${buildy_exe}` is now given to every build system, not only to the one named buildy, so a
definition that replaces buildy's (or wraps buildy) can use it.

**Shell phases.** A phase may say `shell: true`, as a tool may (`commands.md`): its command is a
script whose placeholder values are quoted for `sh` (in Docker too) or `cmd.exe`. The record holds
the shell as the phase's program; what the script runs isn't identified.

**Detection.** `build.system: auto` considers every definition. Two build systems that detect a
tree at the highest priority are an error naming them, their files and markers, where the first
by name was taken: cmake, meson, cargo and go_mod are all 10, so a tree with a `CMakeLists.txt`
and a `Cargo.toml` built with cargo. This is phase 6's toolchain priority rule.

**Records.** A definition's SHA-256 is computed from its file, built-in or not, and is in each
build's marker, so editing a definition rebuilds the dependencies it builds (any edit, a comment
included). Replacing a built-in rebuilds the dependencies it built too.

## The marker

A finished build is recorded in its marker, like an archive's extraction. The marker holds
everything that decides what the build produces:

| Field | What |
|---|---|
| `source` | the fetched tree: `git <commit>`, or `sha256 <archive hash>` |
| `dir` | the source tree's path: build trees hold absolute paths (CMakeCache.txt) |
| `build_system` | its name and the SHA-256 of its definition file: a buildy upgrade that changes a built-in one, or an edit of your own, rebuilds |
| `platform`, `architecture`, `configuration`, `build_type` | what is built |
| `args` | `build.args`, variables resolved |
| `override` | `build.override`'s path and content hash |
| `execution` | native or Docker, with its image, volumes, user and environment |
| `tools` | what each phase runs: the program's resolved path and content hash, or the Docker image ID (phase 3's tool identity) |
| `env` | the values of the variables the build system's `reads_env` lists (cmake: `CC`, `CXX`, `CFLAGS`, `CXXFLAGS`, `LDFLAGS`, `CMAKE_GENERATOR`, `CMAKE_PREFIX_PATH`, `SDKROOT`, `MACOSX_DEPLOYMENT_TARGET`) |

When the marker on disk is the same, the build is skipped. Otherwise the marker is removed, the
build and install directories are emptied (an out-of-source build starts from nothing, so an
argument removed from `build.args` doesn't linger in a CMake cache), the phases run, and the marker
is written only when the last one succeeds: a failed or interrupted build leaves none and runs
again next time. `--force` rebuilds every dependency, whatever its marker says.

What the marker doesn't see:

- the compiler a build system finds by itself (cmake's compiler detection), unless it comes from a
  variable in `reads_env`; an upgraded system compiler doesn't rebuild dependencies;
- edits to files under `deps/` (buildy's, not yours: to work on a dependency, use a `root:`
  dependency or a module);
- a build or install directory deleted by hand;
- for build system buildy, the nested build's toolchain: its tasks are keyed by the nested buildy
  when it runs, but it runs only when the marker doesn't match.

`--force` covers these. Phase 3's tool identity is reused as is (`util.ToolIdentities`, moved out of
`internal/build` for this): each program is hashed once per run.

## Git

**Policy: a fetched clone is used as it is, without the network, until the dependency names
another URL or ref; `--update-deps` fetches the refs that can move.**

- A build is reproducible: it doesn't change the dependency's code because someone pushed to a
  branch, and every build of the same configuration uses the same commit.
- A build works offline once its dependencies are fetched (before, every build ran `git fetch` and
  failed without a network).
- Updating is explicit: `buildy --update-deps` fetches every git ref that isn't a full commit ID
  (branches and tags), and downloads every archive without a `checksum` again.

`<name>.git.json` records the URL, the ref as written and the commit it resolved to. A build reuses
the clone when the URL and ref match and `HEAD` is that commit (`git rev-parse HEAD`, no network).
Otherwise:

- **another URL** (or no record): the clone is removed and a new one made, so a changed URL never
  builds the old repository's code;
- **another ref, or `--update-deps`**: the ref is fetched into the existing clone.

Every ref is fetched the same way: `git fetch --depth 1 origin <ref>`, which works for a branch, a
tag and a full commit ID (git servers allow fetching a commit by its full ID), then
`git checkout --force --detach <commit>`. So a branch moves when it is updated (before,
`git checkout main` on a stale clone stayed on the old commit forever), and a branch named like a
tag (`v2-dev`) is just a ref (before, `--branch` was used unless the ref started with `v` or was 40
characters long, so `v2-dev` and short commit IDs failed on the first clone). An abbreviated commit
ID can't be fetched by name: when the shallow fetch fails, the whole history is fetched (with
`--unshallow` if needed) and the ref is resolved locally. A ref starting with `-` is rejected.
`-n 3` logs the commit each git dependency uses.

The record is removed before the clone is touched and written after the checkout, so an
interrupted fetch is not taken for a checkout of the ref.

## Archives

Unchanged: an archive is downloaded once per URL and extracted when `<name>.extracted.json`
doesn't match its URL and hash. The build marker's `source` is the archive's hash, and an
extraction replaces the whole tree, build directories included. `--update-deps` downloads an
archive without a `checksum` again (its content may have changed); one with a checksum can't
change.

## Timeouts and Ctrl-C

- A dependency build phase obeys `--timeout`, like a task: no limit by default (phase 3 already
  replaced the old hidden 600 s).
- A download fails when the server can't be connected to within 30 s (TCP connect or TLS
  handshake), or sends nothing (headers or body) for 60 s. There is no limit on a download's total
  time: a large archive over a slow link keeps arriving and completes. Before, a server that
  stopped answering hung buildy forever.
- Ctrl-C stops a download, a git command and a build phase (phase 3).

## pkg-config

A `pkg_config:` dependency keeps every flag pkg-config gives, in order:

- `--cflags` become the dependency's compile flags, ahead of the target's flags: `-I`, `-D`,
  `-pthread`, `-isystem DIR`, `-f...` and anything else;
- `--libs` become its libraries, placed where `${libs}` is in the link command: `-L`, `-l`,
  `-Wl,...`, `-pthread`, a library's path, each as written; `-framework X` becomes a framework.

Before, only `-I`, `-D`, `-L` and `-l` were kept. Output is split the way pkg-config quotes it (a
backslash escapes a space; quotes are honoured), not on every space. A `--libs` word that is
neither a flag nor a path (the argument of a two-word flag other than `-framework`) fails, naming
it.

To pass flags through in place, a `libs` item that starts with `-` or is an absolute path is passed
to the linker as written; before, it became `-l-pthread`.

## Tests

- Build systems: cmake's argument lists for each phase, `CMAKE_BUILD_TYPE` once and `--config` for
  the build and the install (`dependency_build_test.go`); no built-in phase runs `true`, make and
  cargo have no configure; an in-source build system needs a clean phase, others can't have one
  (`config_test.go`).
- Marker: another source, configuration, argument, environment value or tool is another build
  (`dependency_build_test.go`).
- Downloads: a server that never answers and one that stops mid-body fail after the idle timeout; a
  slow server that keeps sending succeeds although it takes longer; an interrupt is reported as one
  (`fetch_manager_test.go`).
- pkg-config: splitting, and every flag of a `.pc` file with spaces, `-isystem`, `-Wl,`,
  `-framework` and a library path, in order (`dependency_resolver_test.go`); flags and paths in
  `libs` pass through (`command_builder_test.go`).
- End to end (`e2e_deps_test.go`, local git repositories as remotes, no network):
  - a cmake dependency returning 222 under `NDEBUG` and 111 otherwise: the release app prints 222,
    the debug app 111; cmake got `CMAKE_BUILD_TYPE=Release` once; the install phase filled
    `_install/<platform>-<arch>-release/`; a no-op build runs no command and fetches nothing;
    `-c portable` fails until mapped, then prints 222; a broken argument fails and leaves no
    marker, and the next build builds; `--force` rebuilds;
  - git: a short commit ID, a branch named `v2-dev` and `main` on a first clone; a build with the
    remote moved away works and doesn't fetch; a new commit on the branch is used only after
    `--update-deps`, and its commit is logged; another URL under the same name is a new clone;
  - make: two builds of debug run `make` once; release runs `make clean` then `make`, with
    `${config}` in `build.args`;
  - `--dry-run` plans nothing for an up-to-date dependency and a build for another configuration;
    an interrupted dependency build leaves no marker (`e2e_test.go`).
- Build systems of your own:
  - loading: a later directory's definition replaces a built-in or an earlier one; the hash
    changes with an edit; two of a name in one directory and an unknown key are errors naming
    the files; detection takes the highest priority and fails on a tie, two built-ins included
    (`build_system_manager_test.go`); placeholders, names, `default_args` and empty
    configurations are checked (`config_test.go`); every placeholder has a value, and a shell
    phase quotes them (`dependency_build_test.go`);
  - end to end (`e2e_build_systems_test.go`): a `plain` build system (a `build.sh` that builds a
    static library, a shell phase that installs it and its header) picked by `auto` builds a git
    dependency; the app prints 1 in debug and 2 in release, from separate build and install
    directories; every placeholder reaches the configure phase; a no-op build runs nothing of
    it; editing the definition or a `reads_env` variable rebuilds; `-c portable` fails; a `make`
    definition logs that it replaces the built-in; a second definition detecting the tree at the
    same priority, and an unknown key, fail naming the files; definitions next to the
    executable (run from the PATH), in an `--add-data-dir` and in the workspace replace each
    other in that order, and the current directory's `data/` is not read; with
    `BUILDY_TEST_DOCKER=1`, a phase runs in the dependency's Docker execution.

## Real-world check

Copies of the `ci-releases` branches of Mobius and Monstro (their `name: {file: ...}` dependency
index entries removed), built as their CI does, on Linux x86-64, with the binary from before this
phase (859b136) and after. Downloads were seeded, so no time is network time.

| | Before | After |
|---|---|---|
| Mobius `buildy -c release`, clean | 94.7 s | 72.3 s |
| of which its dependencies (`glfw3`, `sqlite3`, both build system buildy) | 57.2 s: debug and release | 34.9 s: release |
| Mobius, no-op (twice) | 0.14 s, 0.13 s | 0.06 s, 0.06 s |
| Mobius tests (`run_tests.mob`: 331 passed, 2 skipped; C# binding; `build_portable.mob`) | pass | pass |
| Mobius `build_portable.mob`, then again (no-op) | 68.2 s, 0.14 s | 68.1 s, 0.07 s |
| Monstro `ci/build.py` (library, headless suite plain and sanitized: 190 tests each, C# binding: 1121 checks) | pass, 110.7 s | pass, 109.4 s |
| Monstro library alone, clean; no-op | 3.0 s; 0.03 s | 3.0 s; 0.03 s |
| Monstro root workspace `--target libmonstro` (its `glfw3` dependency first), clean; no-op | 4.2 s; 0.09 s | 3.9 s; 0.05 s |

A no-op build runs no command of a dependency's: before, each one ran the nested buildy for debug
and for release (each a no-op of its own). Mobius's dependency
YAML loaded unchanged, `portable` included (its dependencies use build system buildy, which takes
any configuration by name). `build_portable.mob` still builds the portable variant of each
dependency by hand first, because the old buildy built only debug and release; buildy now builds
it itself, so that step can go.

## Left for later

- Dependencies are resolved whatever `--target` selects: a target that needs none still fetches
  and builds them the first time (then the markers make it cheap).
- ~~Build system files in `buildy_config/build_systems/` are documented but not loaded~~: loaded
  ([above](#build-systems-of-your-own)).
- Windows and macOS: the cmake `--config` arguments are tested as argument lists; neither platform
  has run a dependency build in this phase. A shell phase under `cmd.exe`, and finding `data/`
  next to the executable on macOS (where `os.Executable()` may return the symlink, resolved by
  `EvalSymlinks`), are built and vetted, not run; the end-to-end build system tests skip Windows
  (they run `sh`).
- Templates have no workspace directory (`buildy_config/templates/`); toolchains and build
  systems do.
