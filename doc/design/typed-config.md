# Typed Configuration (review phase 1)

Status: **implemented** on `review-fixes`. This is phase 1 of the fixes for the review of
2026-10-11 (finding 6.4, which fixes 1.2, 1.4, 1.5, 1.6, 5.4 and part of 6.6). It supersedes
decision 5 of the [task graph contract](task-graph-contract.md#11-decisions) ("validation stays
map-based"): configuration is no longer `map[string]any` anywhere.

## The model

`internal/config` holds a typed model of every file buildy reads, and one loader per file kind:

| File | Loader | Model |
|---|---|---|
| `buildy.yaml` (root, module, dependency override config) | `LoadFile` | `File` |
| dependency definitions | `LoadDependencies` | `Dependency`, `DependencySection` |
| toolchain | `LoadToolchain` | `Toolchain`, `Tool`, `CommandParam` |
| template file | `LoadTemplates` | `Template`, `Step` |
| build system | `LoadBuildSystem` | `BuildSystem`, `Phase` |

Each loader parses YAML into a `yaml.Node` tree and decodes it with one strict reflective decoder
(`decode.go`), then validates what the types can't say (C targets need sources, a link tool names
its output type, a template's target types). The decoder reports, with the file, the line and the
key path:

- an unknown key, with the closest known key within two edits;
- a duplicate key (yaml.v3 only rejects those when decoding into maps);
- a value of the wrong type, including a number or boolean where a string belongs (`version: 1.0`).

Merge keys (`<<: *anchor`) and aliases work. Every problem of a file is reported at once, sorted
by line. A built-in file that fails to load is an error, like any other.

Values that YAML can write two ways have types of their own: `StringList` (a string or a list),
`Scalar` (any scalar, kept as its text: variable values), `Keyword` (a string or a boolean),
`OptionalScalar` (present-but-`~` differs from absent: `import_env_vars[].default`), `Ordered`
(a mapping that keeps its document order: tools, flag mappings, template parameters, generate
variables), and `Filtered[T]`.

### Filtered lists

`Filtered[T]` is a list of plain items and filter maps. A filter map's keys stay in document
order, so `{linux: [-DA], debug: [-DB]}` resolves to `-DA -DB` on every run (finding 1.2: Go's map
order gave 8 different compile commands in 20 runs). Values under a key may be lists, single items
or nested filter maps. An item type may also be written as a mapping, identified by a key: a
`Source` is `"pattern"` or `{pattern, optional}` (finding 1.6), a `StagingFile` is `"path"` or
`{source, use_symlink}`, a `ModuleEntry` is `"dir"` or `{path, config}`. `workspace.modules` also
takes the legacy map form, where `common` always matches.

Filter keys are recorded while decoding and checked once the toolchains are loaded (finding 5.4):
a key must be a platform or architecture (aliases included), `debug`, `release`, a configuration
some config of the workspace declares, or a toolchain name. Abstract keywords (any key of a
settings level that is not a field) are recorded the same way and must be a keyword some
toolchain's `flag_mappings` defines.

### Platform names

`platform.go` is the one table of platforms (`linux`, `windows`, `macos`, `wasm`) and
architectures (`x86_64`, `x86`, `arm64`, `arm`, `wasm32`), with aliases (`darwin`, `amd64`,
`aarch64`, `i686`, `i386`). `--platform`, `--architecture`, filter keys, `platforms:` and
`architectures:` keys, `environment.toolchains` keys, dependency sections, toolchain targets and
per-platform tool commands are normalised through it; an unknown name is an error. The
`strings.Contains(platform, "windows")` checks and the darwin special cases are gone.

## Merge rules

`config.Merge` deep-merges two values of one type and returns a new value sharing nothing with
either (finding 1.5; the old merge wrote into the parsed maps and merged one level deep):

- **mappings** (Go maps, `Ordered`, structs) merge key by key;
- **lists** append: the base's items, then the override's;
- **scalars** override when set; an empty string or `false` is not set, so a field that must be
  able to say "empty" is a pointer (`output_prefix`).

Merging happens twice:

1. **Root into module** (`Inherit`): the root config's `variables`, `environment`, `config`, and
   the settings of `platforms`, `architectures` and `configurations` merge with the module's. The
   root's platform target overrides don't. A module's project, targets, artifacts, staging,
   install, output and toolchain are its own. The root module itself (discovery mode, a standalone
   config) inherits nothing.
2. **Levels** (`File.Settings`): `config`, `environment.compile`/`link`, the platform's section,
   the architecture's, then the configuration's (`configurations`, then
   `environment.configurations`). A level's `compile:` section merges into the level first.

Then filters resolve (with the module's toolchain), abstract keywords become flags (sorted by
keyword, first in the list), and `remove_flags` / `remove_defines` apply last. A target appends
its dependencies' defines, its own defines and flags, then its `compile:` and `link:` sections,
then its removals, to a copy.

A platform's target override (`platforms.<p>.targets`) replaces each field it sets, except
`compile` and `link`, which merge. Overriding a target the module doesn't have is an error.

## Variables

`workspace.Variables` builds one chain per module, lowest priority first, as DSL_SPEC documents:
built-ins (`platform`, `arch`, `config`, `workspace`, `project_name`, `project_version`,
`base_dir`), the merged `variables` (the root's, overridden by the module's: finding 1.4),
platform variables, architecture variables, configuration variables, imported environment
variables, `--define`. The generator adds `output_dir`/`out_dir`, `gen_dir` and `cache_dir`.
Dependency definitions see the root's chain plus `name`, `version`, `platform`, `arch`, `config`,
`toolchain`, `root`, `dep_dir` and `workspace_root`; the undocumented `${architecture}` and
`${configuration}` are gone. An unresolved variable in a target, a setting or a dependency is an
error naming where it is used.

## Determinism

Nothing iterates a Go map where the order shows: tools and template parameters keep document
order; toolchains, templates, build systems, keywords and modules are visited by name; command
placeholders are filled in name order; a stage's tasks are sorted. A toolchain with two tools for
one action and extension, two link tools for one output type or two build tools, and two templates
for one language and target type, fail to load instead of one being picked at random. A transform
or generate artifact runs the tool whose `input_extensions` takes each input.

## What the typed models made dead, and removed

The map schema and validator (`yaml_parser.go`), `getMergedConfig` and the target override merge,
`ResolveFilteredList`, `ParseSourceEntries` and the deprecated `ResolveSources*` wrappers, the
three toolchain parsers, the map-based variable importers, `prependMkdir`, the dependency
resolver's map parsing and `extract` field, the metadata-less `assets` and `shaders` templates,
and the Go and Rust `supports` keys no code read. The doxygen toolchain keeps one generate tool.

## Left for later phases

- Template references (`${item.X}`, `${config.X}`) are not checked against the model: the
  templates' view of a target is still a name-keyed map.
- `output:` is the module's own; whether the root's should reach modules belongs with phase 5
  (defaults). *Done in [phase 5](layout.md#one-output-layout): the root's `output:` merges into
  every module's.*
- Toolchain preference (which toolchain a platform gets by default) is still a list in Go.
- `supports.build_tags`, `features` and the other Go/Rust supports flags are documented but
  unread; deleting them changes the documented toolchain format.
- Tool `flags` keys (`common`, configuration names) are not checked against the workspace's
  configurations.
