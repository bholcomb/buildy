package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"

	"buildy/internal/build"
	"buildy/internal/cache"
	"buildy/internal/config"
	"buildy/internal/resource"
	"buildy/internal/workspace"
	"buildy/pkg/util"

	flag "github.com/spf13/pflag"
)

// Version is buildy's version. Bump it in the commit a release tag names: CI fails a tag v<x>
// whose source says another version (doc/RELEASING.md).
var Version = "0.2.0"

// Build information, set with -ldflags -X at build time
var (
	BuildTime   = "unknown"
	GitCommit   = "unknown"
	BuildConfig = "debug"
)

// detectPlatform returns the platform buildy runs on
func detectPlatform() string {
	return config.HostPlatform()
}

// detectArchitecture returns the architecture buildy runs on
func detectArchitecture() string {
	return config.HostArchitecture()
}

// printToolchains prints a table of toolchains to standard output, whatever the log level: the
// output is the point of --list-toolchains.
func printToolchains(toolchains []*resource.ToolchainConfig) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tLANGUAGE\tTARGET\tHOST\tDESCRIPTION")
	for _, tc := range toolchains {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", tc.Name, orDash(tc.Language), platformArch(tc.Target), platformArch(tc.Host), tc.Description)
	}
	w.Flush()
}

// platformArch is a toolchain's target or host as --list-toolchains shows it: linux-x86_64,
// linux (any architecture), or any.
func platformArch(pa config.PlatformArch) string {
	platform, arch := orAny(pa.Platform), orAny(pa.Architecture)
	switch {
	case platform == "any" && arch == "any":
		return "any"
	case arch == "any":
		return platform
	}
	return platform + "-" + arch
}

func orAny(s string) string {
	if s == "" {
		return "any"
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// exitIfInterrupted exits with 130 if the run was interrupted.
func exitIfInterrupted(ctx context.Context) {
	if build.Interrupted(ctx) {
		util.LogProgress("Build interrupted")
		os.Exit(build.ExitInterrupted)
	}
}

func main() {
	// The first Ctrl-C cancels ctx: running commands are killed, and buildy stops and exits with
	// 130; a second one exits at once
	ctx := build.HandleInterrupts()

	// The directory of the buildy executable (symlinks resolved), whose data/ is searched for
	// templates, toolchains and build systems. Not os.Args[0]'s: run from the PATH, that is
	// "buildy", which named the current directory.
	exe, err := util.Executable()
	if err != nil {
		util.LogFatal("Failed to find the buildy executable: %v", err)
	}
	buildyDir := filepath.Dir(exe)

	// Define CLI flags with auto-detected defaults
	defaultPlatform := detectPlatform()
	defaultArch := detectArchitecture()

	platform := flag.StringP("platform", "p", defaultPlatform, "Target platform (auto-detected: "+defaultPlatform+")")
	architecture := flag.StringP("architecture", "a", defaultArch, "Target architecture (auto-detected: "+defaultArch+")")
	configuration := flag.StringP("config", "c", "debug", "Build configuration")
	release := flag.BoolP("release", "r", false, "Build in release mode (shortcut for --config=release)")
	cacheDir := flag.String("cache-dir", workspace.DefaultCacheDir, "Cache directory: relative to the workspace root, or absolute")
	dryRun := flag.BoolP("dry-run", "d", false, "Plan the build: list what would be fetched, built and run, without doing any of it")
	workers := flag.IntP("workers", "j", runtime.NumCPU(), "How many tasks run at once (default: the number of CPUs)")
	cacheStats := flag.Bool("cache-stats", false, "Show cache statistics")
	clean := flag.Bool("clean", false, "Remove the workspace's output directory (build/: every configuration), nothing else")
	cleanAll := flag.Bool("clean-all", false, "--clean, and the cache directory too (fetched dependencies and their builds)")
	toolchain := flag.StringP("toolchain", "t", "", "Specify toolchain to use (overrides config file)")
	listToolchains := flag.Bool("list-toolchains", false, "List available toolchains and exit")
	version := flag.Bool("version", false, "Print buildy's version and exit")
	force := flag.BoolP("force", "f", false, "Rebuild everything: ignore the cache, and build fetched dependencies again")
	compileCommands := flag.Bool("compile-commands", false, "Generate compile_commands.json in buildy_config/ folder")
	notifyLevel := flag.IntP("notify", "n", 2, "Log notify level: 1=error, 2=warning, 3=info, 4=verbose, 5=debug")
	timeout := flag.Duration("timeout", 0, "Kill a task, or a dependency build step, that runs longer than this (e.g. 30m; 0: no limit)")
	updateDeps := flag.Bool("update-deps", false, "Fetch the dependencies that can move again: git branches and tags, and archives without a checksum")

	// Custom flag for multiple defines
	var defines []string
	flag.StringArrayVarP(&defines, "define", "D", []string{}, "Define a variable (can be used multiple times, e.g. -D MY_VAR=value)")

	// Custom flag for multiple targets
	var targets []string
	flag.StringArrayVar(&targets, "target", []string{}, "Build this target and everything it depends on, in any module (can be used multiple times)")

	// Custom flag for additional data directories
	var additionalDataDirs []string
	flag.StringArrayVar(&additionalDataDirs, "add-data-dir", []string{}, "Additional data directory (looks for templates/, toolchains/ and build_systems/ subdirectories)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Buildy v%s - Task-based build system\n", Version)
		fmt.Fprintf(os.Stderr, "  Build:   %s (%s)\n", BuildConfig, BuildTime)
		fmt.Fprintf(os.Stderr, "  Commit:  %s\n\n", GitCommit)
		fmt.Fprintf(os.Stderr, "Usage: buildy [options] [workspace directory]\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *version {
		fmt.Printf("buildy %s (commit %s, %s build, %s)\n", Version, GitCommit, BuildConfig, BuildTime)
		os.Exit(0)
	}

	// Handle -r/--release shortcut
	if *release {
		*configuration = "release"
	}

	// Platform and architecture names are normalised (darwin is macos, aarch64 is arm64); any
	// other name is an error
	if *platform, err = config.CheckPlatform(*platform); err != nil {
		util.LogFatal("--platform: %v", err)
	}
	if *architecture, err = config.CheckArchitecture(*architecture); err != nil {
		util.LogFatal("--architecture: %v", err)
	}

	// Initialize logger with notify level
	util.InitLogger(*notifyLevel)

	// Handle toolchain flag
	selectedToolchain := *toolchain

	// Parse CLI defines
	cliDefines := make(map[string]string)
	for _, define := range defines {
		if !strings.Contains(define, "=") {
			util.LogFatal("Invalid --define format: '%s' (expected VAR=VALUE)", define)
		}
		parts := strings.SplitN(define, "=", 2)
		varName := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		cliDefines[varName] = value
		util.LogVerbose("CLI define: %s=%s", varName, value)
	}

	// Verify embedded data
	if err := resource.VerifyEmbeddedData(); err != nil {
		util.LogFatal("Failed to verify embedded data: %v", err)
	}

	// The directories of templates, toolchains and build systems, lowest priority first, after
	// the built-in ones: data/<kind> next to the executable, then each --add-data-dir's <kind>,
	// then (toolchains and build systems) the workspace's buildy_config/<kind>. A definition
	// replaces one of the same name from an earlier place.
	dataDirs := func(kind string) []string {
		dirs := []string{filepath.Join(buildyDir, "data", kind)}
		for _, dataDir := range additionalDataDirs {
			dirs = append(dirs, filepath.Join(dataDir, kind))
		}
		return dirs
	}

	templateEngine, err := workspace.NewBuildTemplateEngineMulti(dataDirs("templates"))
	if err != nil {
		util.LogFatal("Failed to initialize template engine: %v", err)
	}

	// Get config files from remaining arguments
	configFiles := flag.Args()

	// Everything is a workspace - discover or load from specified path
	var ws *workspace.Workspace

	if len(configFiles) == 0 {
		// No config files specified - discover workspace from current directory
		util.LogVerbose("No config file specified, attempting workspace discovery...")

		cwd, err := os.Getwd()
		if err != nil {
			util.LogFatal("Failed to get current directory: %v", err)
		}
		ws, err = workspace.DiscoverWorkspace(cwd, *cacheDir)
		if err != nil && !(*listToolchains && errors.Is(err, workspace.ErrNoWorkspace)) {
			if *clean || *cleanAll {
				util.LogFatal("Nothing to clean, nothing removed: %v", err)
			}
			util.LogFatal("Failed to load the workspace: %v", err)
		}
	} else {
		if len(configFiles) > 1 {
			util.LogFatal("Only one path may be given, got %d: %v", len(configFiles), configFiles)
		}
		argPath := configFiles[0]
		info, err := os.Stat(argPath)
		if err != nil {
			util.LogFatal("Path not found: %s", argPath)
		}

		if info.IsDir() {
			// A directory is a workspace root: the same build as running buildy inside it
			ws, err = workspace.NewWorkspace(argPath, *cacheDir)
			if err != nil {
				util.LogFatal("Failed to load workspace from %s: %v", argPath, err)
			}
			util.LogVerbose("Loaded workspace from: %s", ws.RootDir)
		} else {
			// A file is a standalone config, built with the current directory as workspace root
			cwd, err := os.Getwd()
			if err != nil {
				util.LogFatal("Failed to get current directory: %v", err)
			}
			ws, err = workspace.NewWorkspaceWithConfig(cwd, argPath, *cacheDir)
			if err != nil {
				util.LogFatal("Failed to load %s: %v", argPath, err)
			}
			util.LogVerbose("Loaded standalone config %s (workspace root %s)", ws.ConfigPath, ws.RootDir)
		}
	}

	// --list-toolchains lists the toolchains a build here could use: the built-in ones, those of
	// the data directories, and the workspace's, if there is one
	if *listToolchains {
		dirs := dataDirs("toolchains")
		if ws != nil {
			dirs = append(dirs, filepath.Join(ws.RootDir, "buildy_config", "toolchains"))
		}
		toolchainManager, err := resource.NewToolchainManagerMulti(dirs)
		if err != nil {
			util.LogFatal("Failed to load the toolchains: %v", err)
		}
		printToolchains(toolchainManager.ListToolchains())
		os.Exit(0)
	}

	// What is being built: every phase (module selection, dependencies, generation) uses it
	buildContext := workspace.BuildContext{
		Platform:      *platform,
		Architecture:  *architecture,
		Configuration: *configuration,
	}

	// Load the modules this build context selects
	if _, err := ws.DiscoverModules(buildContext); err != nil {
		util.LogFatal("Failed to load the workspace's modules: %v", err)
	}
	util.LogVerbose("Found %d module(s)", len(ws.Modules))

	// The cache directory: relative to the workspace root, or absolute
	workspaceCacheDir := ws.CacheDir

	// --clean removes what buildy produced in the workspace, and nothing else
	if *clean || *cleanAll {
		if err := build.Clean(ws, buildContext, cliDefines, workspaceCacheDir, *cleanAll); err != nil {
			util.LogFatal("Clean: %v", err)
		}
		util.LogProgress("Clean complete")
		os.Exit(0)
	}

	// The workspace root's variables, for the dependency definitions: its variables and imported
	// environment variables, the built-in variables and --define
	rootVarEnv, err := workspace.Variables(ws.Config.File, buildContext, ws.RootDir, cliDefines)
	if err != nil {
		util.LogFatal("Failed to read the workspace's variables: %v", err)
	}

	toolchainManager, err := resource.NewToolchainManagerMulti(append(dataDirs("toolchains"), filepath.Join(ws.RootDir, "buildy_config", "toolchains")))
	if err != nil {
		util.LogFatal("Failed to initialize toolchain manager: %v", err)
	}

	// --cache-stats prints what the cache holds, whatever the log level
	if *cacheStats {
		buildCache, err := cache.NewBuildCache(workspaceCacheDir)
		if err != nil {
			util.LogFatal("Failed to open the cache: %v", err)
		}
		fmt.Printf("Cache directory: %s\n", buildCache.CacheDir())
		fmt.Printf("Entries:         %d\n", buildCache.Entries())
		os.Exit(0)
	}

	// Initialize dependency resolver and load dependencies
	depResolver, err := resource.NewDependencyResolver(*platform, *architecture, *toolchain, *configuration, workspaceCacheDir, ws.RootDir, rootVarEnv)
	if err != nil {
		util.LogFatal("Failed to load the build systems: %v", err)
	}

	// Skip dependency resolution in standalone mode (explicit config file provided)
	// When a specific config file is passed on the command line, we only build what's
	// in that file - no module discovery, no dependency resolution
	standaloneMode := ws.ConfigPath != ""
	if !standaloneMode {
		if err := depResolver.LoadBuildSystems(append(dataDirs("build_systems"), filepath.Join(ws.RootDir, "buildy_config", "build_systems"))); err != nil {
			util.LogFatal("Failed to load the build systems: %v", err)
		}

		// Load dependencies from buildy_config/dependencies.yaml or buildy_config/dependencies/
		if err := depResolver.LoadDependencies(ws.RootDir, ws.Config.DependenciesFile); err != nil {
			util.LogFatal("Failed to load dependencies: %v", err)
		}

		// Task generation fetches and builds each dependency the first time a target uses it. A
		// dry run plans this instead: it fetches and builds nothing, and uses what is already on
		// disk.
		depResolver.Timeout = *timeout
		depResolver.Force = *force
		depResolver.Update = *updateDeps
		depResolver.Prepare(ctx, *dryRun)
	}

	// Create workspace-aware config parser
	configParser := workspace.NewConfigParser(
		buildContext,
		cliDefines,
		toolchainManager,
		selectedToolchain,
		templateEngine,
		ws,
		depResolver,
	)

	configParser.CacheDir = workspaceCacheDir

	// Create builder (cache directory already set above for dependency resolution)
	builder, err := build.NewBuilder(workspaceCacheDir, *workers)
	if err != nil {
		util.LogFatal("Failed to create builder: %v", err)
	}

	// Configure builder options
	buildOptions := build.BuildOptions{
		DryRun:          *dryRun,
		Force:           *force,
		CompileCommands: *compileCommands,
		Timeout:         *timeout,
	}

	// Generate, validate, select (--target) and execute (or, with --dry-run, print the plan).
	// Generation fetches and builds the dependencies the targets use (cmake, make, meson, buildy).
	result, err := builder.BuildWorkspace(
		ctx,
		ws,
		configParser,
		targets,
		buildOptions,
	)

	exitIfInterrupted(ctx)

	if err != nil {
		util.LogFatal("Build failed: %v", err)
	}

	// Generate and save build report
	if result != nil {
		reportPath := filepath.Join(workspaceCacheDir, fmt.Sprintf("build_report_%s-%s-%s.json", *platform, *architecture, *configuration))
		if err := result.SaveReport(reportPath); err != nil {
			util.LogWarning("Failed to save build report: %v", err)
		}

		// Print terminal summary
		result.PrintSummary()

		// Generate compile_commands.json if requested
		if *compileCommands && result.Success {
			compileCommandsPath := filepath.Join(ws.RootDir, "buildy_config", "compile_commands.json")
			if err := result.GenerateCompileCommands(compileCommandsPath); err != nil {
				util.LogWarning("Failed to generate compile_commands.json: %v", err)
			} else {
				util.LogInfo("Generated compile_commands.json at %s", compileCommandsPath)
			}
		}
	}

	if result != nil && result.Success {
		os.Exit(0)
	} else {
		os.Exit(1)
	}
}
