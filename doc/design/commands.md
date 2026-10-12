# Commands as Argument Lists (review phase 2)

Status: **implemented** on `review-fixes`. This is phase 2 of the fixes for the review of
2026-10-11: finding 6.1, which fixes 2.1 (paths with spaces), 2.2 (long command lines) and 2.3
(Windows quoting). It follows the [typed configuration](typed-config.md) (phase 1).

## The problem

Every command was a shell string. Paths, defines and flags were pasted into the toolchain's
command text (`gcc ... -c ${input} -o ${output}`) and the result ran through `sh -c`, or
`cmd.exe /S /C` on Windows. Nothing was quoted, so:

- a workspace under a path with a space failed to compile, link and install, and an install's
  `mkdir -p` made stray directories (2.1);
- a define such as `MSG="hello world"` reached the compiler split and with its quotes eaten;
- the whole command was one `sh -c` argument, which Linux limits to 128 KiB: archiving ~1500
  objects failed (2.2); response files existed only on Windows, chose their syntax from the
  executable's name and re-split the command string, so `cd X && cl ...` put `X && cl ...` in
  the file;
- Go escapes an argument for the C runtime's rules (`\"`), which cmd.exe doesn't follow (2.3);
- template generation, symlinks and install archives were `sed`/PowerShell, `ln -sf` and
  `tar`/`zip`/`Compress-Archive` text with the same splicing, and copy tasks carried a `cp`
  command they never ran.

## The design

### A task runs an argument list

`util.Command` is what a task runs: `Args` (the program, then its arguments), or, for a tool
that asks for a shell, `Script`; and `Dir`, its working directory (the workspace root if empty).
The executor runs `Args` with `exec.Command(Args[0], Args[1:]...)`: no shell anywhere. Go's
`os/exec` quotes each argument correctly for `CreateProcess` on Windows, which is what MSVC's
tools parse. Docker runs `docker run ... image Args...`, the arguments as arguments.

The cache key and the tasks file hold the command as this structure. Logs show it as one line,
quoted for the host's shell (`util.JoinArgs`), and `compile_commands.json` gets its
`arguments` array and `directory`. `go build` and `cargo build` get a `Dir` instead of a
`cd X &&` prefix.

### Command templates

A toolchain's `command` (and a build system phase's `command`, a tool's
`supports.dependencies`, and a `command_params` `format`) is a template, split into words when
the file loads (`config.CommandTemplate`). Syntax errors are load errors with the file and
line. The rules:

- Words are separated by unquoted spaces, tabs and newlines.
- `'...'` keeps everything as written: `'$ORIGIN'`, `'${x}'`. `"..."` keeps spaces in a word and
  still fills placeholders in. A backslash is an ordinary character (Windows paths).
- A word that is one unquoted placeholder, `${includes}`, becomes the placeholder's values,
  each one argument: none for an empty value, several for a list.
- A placeholder inside a word, `-I${dir}`, `/OUT:${output}`, `-std=${std}`, `"${x}"`, is
  replaced by its value as text, and the word stays one argument. It must have exactly one
  value; an empty or several-valued placeholder there is an error, because no choice (drop the
  word? repeat it? join the values?) is right everywhere.
- `$` not followed by `{` is an ordinary character: `-Wl,-rpath,$ORIGIN` reaches the linker as
  written, quoted or not.

Every value buildy computes is a list of arguments: `${flags}`, `${defines}`, `${includes}`,
`${objects}`, `${libs}`, `${lib_dirs}`, `${frameworks}`, `${args}` and a build system's
`${args}` are lists, each item one argument. A path with spaces stays one argument. A flag a
user writes (`flags: ["-DMSG=\"a b\""]`) is one argument, passed as written; shell quoting in it
(`'$ORIGIN'`) is no longer removed by a shell. `define_flag`, `include_flag` and the like are
joined to each value (`-I` and `dir` give `-Idir`); one that ends in a space (`"-framework "`)
is an argument of its own before each value. An unset `*_flag` has its default; one set to `""`
is no flag (MSVC names libraries as they are).

A `command_params` entry gives arguments: its `format` is a template whose only placeholder is
`${value}` (`"-tags ${value}"` is two arguments, `-ldflags "${value}"` passes the joined value as
one), or without a format the value is one argument. `quote_if_spaces` is gone.

### Shell tools

A tool that needs pipes, redirection or `&&` says `shell: true`. Its command is then shell
text: buildy replaces each placeholder by its values, each quoted for the shell that runs it,
and runs the result with `sh -c` (on Linux and macOS hosts, and always in Docker) or
`cmd.exe /d /s /c "..."` on a Windows host, with the command line written out as is
(`SysProcAttr.CmdLine`). A placeholder inside double quotes is a load error in a shell tool:
buildy does the quoting. POSIX quoting (`util.QuotePOSIX`) single-quotes anything that isn't
plainly safe. cmd.exe has no escape that works inside quotes, so `util.QuoteCmd` double-quotes
and refuses a value holding `"`, `%`, `!` or a line break rather than pass it on changed. A
shell tool can't take a response file. No built-in toolchain needs a shell; the Docker
end-to-end test's stand-in compiler (`test -f /.dockerenv && cp ...`) is the one user.

### Work buildy does itself

Copies, staging symlinks, template generation and install archives are native: the task has a
`NativeOp` instead of a command, which its cache key and the tasks file record (the template's
substitutions, the archive's format and `follow_symlinks`).

- A template's variables replace `@NAME@` and `${NAME}` in order, values as is; the output keeps
  the template's mode.
- A symlink points at the staged file's absolute path.
- tar, tar.gz and zip archives are written with Go's archive packages and are deterministic:
  entries in name order (each directory before its contents), paths relative to the staging
  directory with `/` and no `./` prefix, mtime 2000-01-01 UTC, owner 0, the files' permission
  bits, gzip without a name or time. `follow_symlinks` (default true) stores what a link points
  to, walking linked directories (a loop is an error), else the link; zip honours it too. An
  archive inside the directory it packs is an error.

### Response files

A tool declares how it reads an `@file`: `response_file: gnu` (GCC, Clang, GNU ar and ld:
whitespace separates arguments, a backslash escapes any character) or `msvc` (cl, link, lib:
arguments quoted as on a Windows command line; UTF-16 with a byte order mark when they aren't
ASCII). Before running a task the executor measures the command against the host:

| Host | Limit |
|---|---|
| Linux | the argument and environment strings and their pointers within ¾ of ARG_MAX (a quarter of the stack limit, between 128 KiB and 6 MiB: the kernel's rule), and no argument over 128 KiB |
| macOS | the same within ¾ of `kern.argmax` (1 MiB) |
| Windows | the `CreateProcess` command line under 32000 of its 32767 characters |

The margin covers what the kernel counts and buildy doesn't. Over the limit, the
arguments after the program go to `<cache dir>/response_files/<task>.rsp` (the cache directory
is mounted in Docker) and the command becomes `program @file`; the file is removed when the
command ends. Over the limit without a declared syntax is an error naming the size and the
limit. The built-in GCC, Clang, MinGW, ARM, Emscripten and Docker toolchains declare `gnu` for
compile and link tools, MSVC `msvc`; the macOS `ar` (cctools) reads no response files and
declares none.

## Tests

- Unit: template tokenizing and expansion and their errors (`internal/config/command_test.go`),
  the built-in toolchains' commands as argument lists, MSVC words, frameworks, shell tools and
  command parameters (`internal/resource/command_builder_test.go`), quoting
  (`pkg/util/quote_test.go`), Docker arguments (`pkg/util/exec_test.go`), archives and
  templates (`pkg/util/archive_test.go`), response file syntax, a GCC round trip through a gnu
  file, and the executor's use of them (`internal/build/response_file_test.go`).
- End to end: `spaced_paths` builds a static library, a shared library with
  `MSG="hello world"`, an executable, a copy, a transform, a template, symlink and copy staging
  and tar.gz and zip installs, all in a directory named `it's a "spaced" dir`; the program prints
  the define, `readelf -d` shows `[$ORIGIN]`, `compile_commands.json` has the define as one
  argument, and a second build runs nothing. `many_objects` archives 1500 objects under a
  1 MiB stack limit (256 KiB of arguments), so `ar` reads a response file. Both fail with the
  binary from before this phase.

## Left for later phases

- Build system phases still skip a command of exactly `true` (make's and cargo's configure).
- The Windows and macOS code paths (cmd.exe scripts, MSVC response files, `kern.argmax`) are
  built and vetted for those targets but have not run there.
- Whether a staging symlink is made is decided by the target platform, not the host. *The host
  decides since [phase 5](layout.md#symlinks).*
