# Patterns, Staging, Clean, Workspace Root and Defaults (review phase 5)

Status: **implemented** on `review-fixes`. This is phase 5 of the fixes for the review of
2026-10-11: findings 1.7 (`**` not recursive), 1.8 (install archives shipped stale files), 1.9
(patterns over build outputs missed them on a clean build), 1.11 (`-c release` built like
`-c debug`), 5.2 (`--clean` deleted directories it didn't own), 5.3 (buildy inside a module built
it as a workspace of its own), the `--cache-dir` gap of section 7, staging symlinks decided by the
target, and the root's `output:` not reaching modules. It follows
[dependencies](dependencies.md) (phase 4).

## File patterns

Every pattern buildy reads (sources, copy sources, transform and generate inputs, staging files)
goes through one helper, `util.Glob`, built on doublestar (already a dependency, used for module
discovery). Before, all but module discovery used `filepath.Glob`, whose `**` is `*`: `src/**/*.c`
missed `src/top.c` and everything two or more levels down, although the documentation promised
recursion.

- `**` as a whole component matches any number of directories, none included; `*`, `?`, `[...]`
  and `{a,b}` are doublestar's.
- A pattern is split into the literal directory it starts from and the rest. The module directory
  is never read as a pattern, so a checkout under `proj [v1]` works.
- Patterns match files, never directories (`filepath.Glob` returned directories, which then failed
  to compile or copy), and the result is sorted.

## Patterns over build outputs

A pattern matched the disk at generation time, so `${out_dir}/bin/*` staged nothing on a clean
build and the binary on the next one, and a file a previous build left in the output directory was
staged forever. Three fixes were possible: expand such patterns after their producers run (a
dynamic graph), forbid patterns into the output directory, or match them against the outputs the
graph declares. The last keeps the graph static and complete before anything runs, under
`--dry-run` too, so it was chosen.

`DeclaredOutputs` holds the outputs of every task generated so far and the build's output
directories. A pattern names:

- the files on disk **outside** the output directories, and
- the declared outputs it matches, **wherever** they are.

In an output directory a pattern (or a plain path) that names no declared output is an error
naming it: it would name nothing on a clean build. Elsewhere a plain path is staged as written, as
before (a missing file fails the staging task), so a dependency's library in `.buildy_cache` still
stages under `--dry-run`. The task that reads a match runs after its producer through the graph's
producer edges, so the hand-wired lookups (`outputToTaskID`, the registry's output-path map) and the
"virtual files" of `depends_on.artifacts` became dead and are gone: a source pattern matches every
generated output of its module, and `depends_on.artifacts` now only orders the target's tasks after
the artifact (for generated headers, which are no declared input).

Outputs are known in generation order, which became: every module's generates, transforms and
targets (modules by path), then every module's copies, then every module's staging and install,
then the root config's. Copies used to be generated with their module's staging, so a staging
pattern couldn't name another module's copies.

## Install archives

The archive walked the staging directory, so a file removed from the config stayed in it (and
anything else in the directory shipped too). Two fixes were possible: own the staging directory and
empty it to match, or archive an explicit list. The staging destination can be any directory:
Mobius stages into `${workspace}` itself, so emptying it is out of the question. So an install's
inputs are now exactly the files its staging tasks produce under the staging directory, and
`util.WriteArchive(root, files, ...)` packs those and the directories leading to them, in the same
order and format as before (name order, each directory before its contents, fixed times). The
archive's cache key covers every staged file's path and hash, so staging one more or one fewer
file builds it again. A file staged outside the staging directory (a folder with an absolute path)
is not in the archive, as before.

What the staging directory holds on disk is not cleaned: a file an earlier build staged stays
there until `--clean`, but it never reaches an archive. The "archive inside the directory it packs"
check is gone (an archive that is also a staged file is two producers of one output, which graph
validation rejects).

Directory inputs remain only for Go packages and Cargo crates (`${item.path}`), which hash as a tree.

## Symlinks

Staging decided "always copy" by the **target** platform. Whether a symlink can be made depends on
the **host** (`runtime.GOOS`: a Windows host needs administrator rights or developer mode), so
building for Windows on Linux now links, and a Windows host always copies. Whether a link is right
in a package depends on the target: an install stores what links point to unless
`follow_symlinks: false`, and `follow_symlinks: false` for a Windows target is an error. (A staging
link points at an absolute path on the building machine, so `follow_symlinks: false` only suits a
package used where it was built.)

## The workspace root

`findWorkspaceRoot` stopped at the first `buildy.yaml` going up, so buildy run in a module built the
module alone, without the root's variables, settings, dependencies or the other modules, with a
cache of its own. Now, like Cargo, the nearest directory with a `buildy.yaml` at or above the
current one is a module, and the root is the **outermost** directory above it whose `buildy.yaml`
includes it: listed in `workspace.modules` (directly or through child modules, under any filter
key, since a module for another platform is still a member) or, without `workspace.modules`,
discovered by its discovery and exclude patterns. Only the configs' `workspace:` sections are read
for this (`config.LoadWorkspaceSection`), strictly: a broken one is an error. A `buildy.yaml` that
nothing above includes is a workspace of its own. `-n 3` logs the root and why.

**What it builds: the whole workspace**, as from the root. The alternative, that module's targets,
would make the result depend on the directory, and would need rules for the root's staging and
install and the module's own copies; `--target` already selects part of a workspace. A path
argument is unchanged: a directory is the root, a file a standalone config.

A dependency built with `build.system: buildy` and no override is now passed its source directory
(`buildy <source_dir>`), so it is always its own workspace, wherever the cache directory is.

## --clean

`--clean` removed `build/`, `dist/`, `staging/` and the cache under the current directory, without
finding the workspace and ignoring `output.base_dir`: run in a module it deleted the module's own
`build/` and `dist/` and left the real output alone. It now loads the workspace exactly as a build
does (refusing, and removing nothing, when there is none) and removes, inside the workspace root
only:

1. the **output directories**: for the root config and each module, `output.base_dir` (`build/`,
   every configuration, with the staging and install directories in it, where they are by
   default) when the output directory lies in it, as in the default layout, else the output
   directory the pattern gives for the build context;
2. with **`--clean-all`**, the cache directory (fetched dependencies, their builds, the records).

It never removes anything outside the workspace root, a directory buildy didn't configure, or an
output directory that holds a module's config (`output.pattern: ${workspace}/src`), and `--clean-all`
refuses a cache directory outside the root before removing anything. `output.base_dir` may no
longer be `.`.

What staging, installs and copies put **outside** the output directory stays. Removing the files
the cache records producing there was tried and rejected: Mobius stages into `${workspace}`, and
copies its tracked `src/packages/glfw/glfw.mob` onto its tracked `packages/glfw/glfw.mob`; on a copy
of Mobius that `--clean` deleted six tracked files. A staged file standing on a user's file can't
be told apart from one buildy alone made, so only directories buildy owns outright go.

## --cache-dir

An absolute `--cache-dir` was joined onto the workspace root. Now an absolute one is used as it
is, and a relative one is relative to the workspace root (not the current directory), as `--help`
says.

## One output layout

The root config's `output:` (base_dir, pattern) didn't reach modules, and the root's own staging
and install used a hard-coded `build/<context>`. `output` now merges root into module like
`environment` (`config.Inherit`), and the root's staging and install use the root's output
directory. A module may still set its own (documented), which overrides the root's field by field.

## Default configurations

With no `configurations:`, `-c release` built exactly like `-c debug`. buildy now has its own
`debug` (`optimization: none`, `symbols: true`) and `release` (`optimization: speed`, `NDEBUG`),
the **lowest** level of every config:

- a keyword set at any level wins. Had they been `configurations:` entries merged at the
  configuration level, buildy's `optimization: speed` would have overridden a config's
  `environment.compile: {optimization: full}`, silently changing what that config means;
- a built-in keyword value a toolchain's `flag_mappings` doesn't have is skipped (a custom
  toolchain mapping only `fast` keeps working); a value a config sets is still checked;
- the defines merge first, and a macro defined more than once is now passed once, by its last
  definition, where that one is (`UniqueDefines`): the compiler's own rule, so nothing changes
  meaning. So Monstro's `NDEBUG=1` replaces buildy's `NDEBUG`, and Mobius's own `NDEBUG` isn't
  doubled; `remove_defines: [NDEBUG]` removes it.

Transform and generate tools get the keywords too, as their own toolchains map them (glslc: `-O0 -g`
in debug, `-O` in release). Other configurations (Mobius's `portable`) get nothing from buildy.

## Tests

- Unit: patterns (recursion, alternatives, literal paths, directories never match, a base
  directory with glob characters, matching declared outputs: `pkg/util/glob_test.go`); archives of
  an explicit list, stale and unlisted files left out, the same bytes in any input order
  (`archive_test.go`); symlinks by host and the Windows package rule (`staging_test.go`); the root
  found from a module, below one, through child modules, under a filter key, in discovery mode,
  in an excluded directory, and a broken parent (`workspace_test.go`); `--clean` refusing an output
  directory that holds a module and paths outside the root (`internal/build/clean_test.go`);
  output inheritance and `base_dir` validation, the built-in configurations and `UniqueDefines`
  (`settings_test.go`).
- End to end (`e2e_layout_test.go`), each failing with the binary from before this phase:
  `recursive_glob`, `output_globs` (a clean build stages and copies `${out_dir}/bin/*`, a stale file
  is never picked up, a pattern into the output directory naming nothing fails under `--dry-run`),
  `stale_staging`, `symlink_staging`, `module_dir` (built from `app/` and `app/src/`),
  `clean_layout` (unrelated `build/`, `dist/` and `staging/` directories survive, and what staging
  and install put in `dist/`; outside a workspace nothing is removed), `--cache-dir` absolute and relative,
  `root_output`, `default_configurations`.

## Left for later

- Staging directories outside the output directory are not emptied of what earlier builds staged,
  by a build or by `--clean` (archives are right regardless).
- Staging onto a file the user tracks (Mobius's `packages/*/*.mob`) rewrites it on every staging
  run: the task deletes its declared output first. Harmless while the content is identical;
  graph validation could reject a staging output that is also another staging item's source.
- `--clean` with a custom `output.pattern` outside `base_dir` removes the output directory of the
  build context given, not every configuration's.
- Windows and macOS: symlink staging on a Windows host, the case-insensitive path comparisons and
  `--clean` there are built and vetted, not run.
