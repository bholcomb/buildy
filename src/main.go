package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"buildy/internal/build"
	"buildy/internal/cache"
	"buildy/internal/config"
	"buildy/internal/resource"
	"buildy/internal/workspace"
	"buildy/pkg/util"

	flag "github.com/spf13/pflag"
)

// Build information - set via ldflags at build time (Version by release builds)
var (
	Version     = "0.1.0"
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

// getAutoBuildyDirs returns automatic buildy_config/ directory search paths
func getAutoBuildyDirs(workspaceRoot, buildyDir, subdir string) []string {
	dirs := []string{}
	
	// 1. Workspace buildy_config/<subdir>
	if workspaceRoot != "" {
		dirs = append(dirs, filepath.Join(workspaceRoot, "buildy_config", subdir))
	}
	
	// 2. System locations
	switch runtime.GOOS {
	case "linux":
		dirs = append(dirs,
			filepath.Join("/usr/local/lib/buildy", subdir),
			filepath.Join("/usr/lib/buildy", subdir),
			filepath.Join("/opt/buildy", subdir),
		)
	case "darwin": // macOS
		dirs = append(dirs,
			filepath.Join("/usr/local/lib/buildy", subdir),
			filepath.Join("/opt/buildy", subdir),
		)
	case "windows":
		dirs = append(dirs,
			filepath.Join(os.Getenv("ProgramFiles"), "buildy", subdir),
			filepath.Join("C:\\buildy", subdir),
		)
	}
	
	// 3. Executable location
	dirs = append(dirs, filepath.Join(buildyDir, "buildy_config", subdir))
	
	return dirs
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

	// Get the directory where the buildy executable is located
	buildyDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		util.LogFatal("Failed to get buildy directory: %v", err)
	}

	// Define CLI flags with auto-detected defaults
	defaultPlatform := detectPlatform()
	defaultArch := detectArchitecture()
	
	platform := flag.StringP("platform", "p", defaultPlatform, "Target platform (auto-detected: "+defaultPlatform+")")
	architecture := flag.StringP("architecture", "a", defaultArch, "Target architecture (auto-detected: "+defaultArch+")")
	configuration := flag.StringP("config", "c", "debug", "Build configuration")
	release := flag.BoolP("release", "r", false, "Build in release mode (shortcut for --config=release)")
	cacheDir := flag.String("cache-dir", ".buildy_cache", "Cache directory")
	dryRun := flag.BoolP("dry-run", "d", false, "Plan the build: list what would be fetched, built and run, without doing any of it")
	workers := flag.IntP("workers", "j", util.DefaultMaxWorkers, "Max parallel workers")
	cacheStats := flag.Bool("cache-stats", false, "Show cache statistics")
	clean := flag.Bool("clean", false, "Clean build artifacts (removes cache and build directories)")
	toolchain := flag.StringP("toolchain", "t", "", "Specify toolchain to use (overrides config file)")
	listToolchains := flag.Bool("list-toolchains", false, "List available toolchains and exit")
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
	flag.StringArrayVar(&additionalDataDirs, "add-data-dir", []string{}, "Additional data directory (looks for templates/ and toolchains/ subdirectories)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Buildy v%s - Task-based build system\n", Version)
		fmt.Fprintf(os.Stderr, "  Build:   %s (%s)\n", BuildConfig, BuildTime)
		fmt.Fprintf(os.Stderr, "  Commit:  %s\n\n", GitCommit)
		fmt.Fprintf(os.Stderr, "Usage: %s [options] [config_files...]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

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

	// Handle --clean flag
	if *clean {
		// Determine workspace root
		var workspaceRoot string
		configFiles := flag.Args()
		
		if len(configFiles) == 0 {
			// No config files specified - use current directory
			workspaceRoot, err = os.Getwd()
			if err != nil {
				util.LogFatal("Failed to get current directory: %v", err)
			}
		} else {
			// Use directory of first config file
			configPath := configFiles[0]
			absPath, err := filepath.Abs(configPath)
			if err != nil {
				util.LogFatal("Failed to resolve config path: %v", err)
			}
			
			// If it's a file, use its directory; if it's a directory, use it
			info, err := os.Stat(absPath)
			if err != nil {
				util.LogFatal("Failed to stat config path: %v", err)
			}
			
			if info.IsDir() {
				workspaceRoot = absPath
			} else {
				workspaceRoot = filepath.Dir(absPath)
			}
		}
		
		// Clean cache directory
		cachePath := filepath.Join(workspaceRoot, *cacheDir)
		if _, err := os.Stat(cachePath); err == nil {
			util.LogProgress("Removing cache directory: %s", cachePath)
			if err := os.RemoveAll(cachePath); err != nil {
				util.LogFatal("Failed to remove cache directory: %v", err)
			}
			util.LogProgress("✓ Cache directory removed")
		} else {
			util.LogVerbose("Cache directory not found: %s", cachePath)
		}
		
		// Clean build directory
		buildPath := filepath.Join(workspaceRoot, "build")
		if _, err := os.Stat(buildPath); err == nil {
			util.LogProgress("Removing build directory: %s", buildPath)
			if err := os.RemoveAll(buildPath); err != nil {
				util.LogFatal("Failed to remove build directory: %v", err)
			}
			util.LogProgress("✓ Build directory removed")
		} else {
			util.LogVerbose("Build directory not found: %s", buildPath)
		}

		// Clean staging directories (check common locations)
		stagingPaths := []string{
			filepath.Join(workspaceRoot, "staging"),
			filepath.Join(buildPath, "staging"),
		}
		for _, stagingPath := range stagingPaths {
			if _, err := os.Stat(stagingPath); err == nil {
				util.LogProgress("Removing staging directory: %s", stagingPath)
				if err := os.RemoveAll(stagingPath); err != nil {
					util.LogWarning("Failed to remove staging directory: %v", err)
				} else {
					util.LogProgress("✓ Staging directory removed: %s", stagingPath)
				}
			}
		}

		// Clean dist directory (install outputs)
		distPath := filepath.Join(workspaceRoot, "dist")
		if _, err := os.Stat(distPath); err == nil {
			util.LogProgress("Removing dist directory: %s", distPath)
			if err := os.RemoveAll(distPath); err != nil {
				util.LogWarning("Failed to remove dist directory: %v", err)
			} else {
				util.LogProgress("✓ Dist directory removed")
			}
		}
		
		util.LogProgress("✓ Clean complete")
		os.Exit(0)
	}

	// Verify embedded data
	if err := resource.VerifyEmbeddedData(); err != nil {
		util.LogFatal("Failed to verify embedded data: %v", err)
	}

	// Initialize template engine with base directory and additional directories
	templatesDir := filepath.Join(buildyDir, "data", "templates")
	templateDirs := []string{templatesDir}
	for _, dataDir := range additionalDataDirs {
		templateDirs = append(templateDirs, filepath.Join(dataDir, "templates"))
	}
	templateEngine, err := workspace.NewBuildTemplateEngineMulti(templateDirs)
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
		ws, err = workspace.DiscoverWorkspace(cwd)
		if err != nil {
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
			ws, err = workspace.NewWorkspace(argPath)
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
			ws, err = workspace.NewWorkspaceWithConfig(cwd, argPath)
			if err != nil {
				util.LogFatal("Failed to load %s: %v", argPath, err)
			}
			util.LogVerbose("Loaded standalone config %s (workspace root %s)", ws.ConfigPath, ws.RootDir)
		}
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

	// The workspace root's variables, for the dependency definitions: its variables and imported
	// environment variables, the built-in variables and --define
	rootVarEnv, err := workspace.Variables(ws.Config.File, buildContext, ws.RootDir, cliDefines)
	if err != nil {
		util.LogFatal("Failed to read the workspace's variables: %v", err)
	}

	// The toolchain directories, lowest priority first: a toolchain in a later directory replaces
	// one of the same name in an earlier one, and the workspace's replace every other
	toolchainDirs := []string{filepath.Join(buildyDir, "data", "toolchains")}
	for _, dataDir := range additionalDataDirs {
		toolchainDirs = append(toolchainDirs, filepath.Join(dataDir, "toolchains"))
	}
	toolchainDirs = append(toolchainDirs, filepath.Join(ws.RootDir, "buildy_config", "toolchains"))

	toolchainManager, err := resource.NewToolchainManagerMulti(toolchainDirs)
	if err != nil {
		util.LogFatal("Failed to initialize toolchain manager: %v", err)
	}

	// Handle --list-toolchains (after workspace is loaded so we include workspace toolchains)
	if *listToolchains {
		util.LogInfo("Available toolchains:")
		for _, tc := range toolchainManager.ListToolchains() {
			util.LogInfo("  %-20s - %s", tc.Name, tc.Description)
		}
		os.Exit(0)
	}

	workspaceCacheDir := filepath.Join(ws.RootDir, *cacheDir)

	// Show cache stats if requested
	if *cacheStats {
		buildCache, err := cache.NewBuildCache(workspaceCacheDir)
		if err != nil {
			util.LogFatal("Failed to open the cache: %v", err)
		}
		util.LogProgress("Cache Statistics:")
		util.LogProgress("  Total entries: %d", buildCache.Entries())
		util.LogProgress("  Cache directory: %s", buildCache.CacheDir())
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
		// Load dependencies from buildy_config/dependencies.yaml or buildy_config/dependencies/
		if err := depResolver.LoadDependencies(ws.RootDir, ws.Config.DependenciesFile); err != nil {
			util.LogFatal("Failed to load dependencies: %v", err)
		}

		// Prepare: fetch and build the dependencies. A dry run plans this instead: it fetches and
		// builds nothing, and uses what is already on disk.
		depResolver.Timeout = *timeout
		depResolver.Force = *force
		depResolver.Update = *updateDeps
		if err := depResolver.ResolveAll(ctx, *dryRun); err != nil {
			exitIfInterrupted(ctx)
			util.LogFatal("Failed to resolve dependencies: %v", err)
		}
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
	// The dependencies (cmake, make, meson, buildy) were built by ResolveAll.
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

