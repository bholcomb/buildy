# How buildy works

A build runs in four phases. The [build flow](DSL_SPEC.md#16-build-flow) lists them step by step;
the design notes in [design/](design/) explain each decision.

```
┌─────────────────────────────────────────────────────────────┐
│                 buildy.yaml and its modules                 │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  Load                                                       │
│  - Find the workspace root; load the modules the build      │
│    context (--platform, --architecture, --config) selects   │
│  - Validate every file strictly: an unknown key is an error │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  Generate                                                   │
│  - Pick each target's template by language and target type  │
│    ("compile each source, then link them")                  │
│  - Fill its steps in with the toolchain's commands and      │
│    flags, the module's settings merged onto the root's      │
│  - Fetch and build each dependency when a target first      │
│    uses it                                                  │
│  - Then copies, staging and install                         │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  Task graph                                                 │
│  - One producer per output; a task that reads another's     │
│    output runs after it; a cycle is an error                │
│  - With --target, keep the named targets and what they need │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  Execute                                                    │
│  - Start each task as soon as its dependencies finish       │
│  - Skip a task whose cache key (command, input contents,    │
│    tool) and recorded outputs are unchanged                 │
│  - Check a task's inputs exist before it runs, and its      │
│    outputs after; record them in the cache                  │
└─────────────────────────────────────────────────────────────┘
```

**Templates and toolchains.** A template says *what* to build: the steps, their inputs and outputs
(`cpp_executable`: compile each source, link the objects). A toolchain says *how*: the commands
and the flags each abstract keyword (`optimization: full`) becomes. Templates exist for C, C++, Go
and Rust executables, static libraries and shared libraries; toolchains exist per compiler,
platform and architecture. Both are YAML, built into the binary, and a toolchain of your own in
`buildy_config/toolchains/` works like a built-in one
([toolchain system](DSL_SPEC.md#10-toolchain-system), [templates](DSL_SPEC.md#11-build-templates)).

Design notes:

- [The task graph contract](design/task-graph-contract.md): what a task declares, and the rules
  the graph and the executor enforce
- [Typed configuration](design/typed-config.md): the config model, filters and merge rules
- [Execution, cache and interrupts](design/execution.md)
- [Fetched dependencies](design/dependencies.md)
- [Patterns, staging, clean and the workspace root](design/layout.md)
- [Structure](design/structure.md) and [commands](design/commands.md)
