package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"buildy/internal/build"
	"buildy/internal/cache"
	"buildy/internal/resource"
	"buildy/internal/workspace"
	"buildy/pkg/util"

	flag "github.com/spf13/pflag"
)

const Version = "0.1.0"

// Build information - set via ldflags at build time
var (
	BuildTime   = "unknown"
	GitCommit   = "unknown"
	BuildConfig = "debug"
)


// detectPlatform returns the current platform name normalized for buildy
func detectPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "linux":
		return "linux"
	case "windows":
		return "windows"
	default:
		return runtime.GOOS
	}
}

// detectArchitecture returns the current architecture name normalized for buildy
func detectArchitecture() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	case "386":
		return "x86"
	default:
		return runtime.GOARCH
	}
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

func main() {
	// Initialize shutdown manager for graceful signal handling
	shutdownManager := build.InitShutdownManager()
	defer shutdownManager.Shutdown()
	
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
	dryRun := flag.BoolP("dry-run", "d", false, "Generate tasks but don't execute")
	workers := flag.IntP("workers", "j", util.DefaultMaxWorkers, "Max parallel workers")
	cacheStats := flag.Bool("cache-stats", false, "Show cache statistics")
	clean := flag.Bool("clean", false, "Clean build artifacts (removes cache and build directories)")
	toolchain := flag.StringP("toolchain", "t", "", "Specify toolchain to use (overrides config file)")
	listToolchains := flag.Bool("list-toolchains", false, "List available toolchains and exit")
	force := flag.BoolP("force", "f", false, "Force full rebuild, ignore cache and build state")
	compileCommands := flag.Bool("compile-commands", false, "Generate compile_commands.json in buildy_config/ folder")
	notifyLevel := flag.IntP("notify", "n", 2, "Log notify level: 1=error, 2=warning, 3=info, 4=verbose, 5=debug")

	// Custom flag for multiple defines
	var defines []string
	flag.StringArrayVarP(&defines, "define", "D", []string{}, "Define a variable (can be used multiple times, e.g. -D MY_VAR=value)")

	// Custom flag for multiple targets
	var targets []string
	flag.StringArrayVar(&targets, "target", []string{}, "Build specific target(s) (workspace mode only, can be used multiple times)")

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

	// Initialize cache
	buildCache, err := cache.NewBuildCache(*cacheDir)
	if err != nil {
		util.LogFatal("Failed to initialize cache: %v", err)
	}

	// Show cache stats if requested
	if *cacheStats {
		stats := buildCache.GetCacheStats()
		util.LogInfo("Cache Statistics:")
		util.LogInfo("  Total entries: %d", stats["total_entries"])
		util.LogInfo("  Total size: %.1f MB", stats["total_size_mb"])
		util.LogInfo("  Cache directory: %s", stats["cache_directory"])
		os.Exit(0)
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

		// Try loading from cache first
		ws, err = workspace.LoadDiscoveryCache(*cacheDir)
		if err == nil && ws != nil {
			util.LogVerbose("Loaded workspace from cache: %s", ws.RootDir)
		} else {
			// Cache miss or invalid, do full discovery
			cwd, _ := os.Getwd()
			ws, err = workspace.DiscoverWorkspace(cwd)
			if err != nil {
				util.LogFatal("No workspace found. Run from a directory with buildy.yaml or specify a path\nError: %v", err)
			}
			util.LogVerbose("Discovered workspace at: %s", ws.RootDir)
		}
	} else {
		// Config file or directory specified - treat as workspace root
		argPath := configFiles[0]
		var workspaceRoot string
		var configPath string

		// Check if it's a file or directory
		info, err := os.Stat(argPath)
		if err != nil {
			util.LogFatal("Path not found: %s", argPath)
		}

		if info.IsDir() {
			// Directory provided - use it as workspace root, look for buildy.yaml
			workspaceRoot = argPath
			configPath = filepath.Join(workspaceRoot, "buildy.yaml")
			if _, err := os.Stat(configPath); os.IsNotExist(err) {
				util.LogFatal("No buildy.yaml found in: %s", workspaceRoot)
			}
		} else {
			// File provided - use file as config, current directory as workspace root
			configPath = argPath
			// Use current working directory as workspace root for dependency builds
			workspaceRoot, err = os.Getwd()
			if err != nil {
				util.LogFatal("Failed to get current directory: %v", err)
			}
		}

		// Load as workspace with explicit config path
		ws, err = workspace.NewWorkspaceWithConfig(workspaceRoot, configPath)
		if err != nil {
			util.LogFatal("Failed to load workspace from %s: %v", workspaceRoot, err)
		}
		util.LogVerbose("Loaded workspace from: %s (config: %s)", ws.RootDir, configPath)

		// Warn about multiple paths (not yet supported)
		if len(configFiles) > 1 {
			util.LogWarning("Multiple paths not yet supported, using first one")
		}
	}

	// Discover modules (will always find at least the root module)
	if _, err := ws.DiscoverModules(false); err != nil {
		util.LogFatal("Failed to discover modules: %v", err)
	}
	util.LogVerbose("Found %d module(s)", len(ws.Modules))

	// Determine target filter
	var targetFilter []string
	if len(targets) > 0 {
		targetFilter = targets
	}

	// Create variable environment for package resolution
	rootVarEnv := util.NewVariableEnvironment(nil)
	rootVarEnv.SetVariable("platform", *platform, "builtin")
	rootVarEnv.SetVariable("architecture", *architecture, "builtin")
	rootVarEnv.SetVariable("configuration", *configuration, "builtin")

	// Import environment variables from workspace config BEFORE dependency resolution
	// This ensures ${VULKAN_SDK} and similar variables are available when resolving dependencies
	if ws.Config.Variables != nil {
		if err := rootVarEnv.ImportEnvVars(ws.Config.Variables, "buildy.yaml"); err != nil {
			util.LogFatal("Failed to import environment variables: %v", err)
		}
	}

	// Initialize toolchain manager with workspace and built-in paths
	toolchainDirs := []string{}
	// 1. Workspace buildy_config/toolchains (highest priority)
	toolchainDirs = append(toolchainDirs, filepath.Join(ws.RootDir, "buildy_config", "toolchains"))
	// 2. Additional data directories from CLI
	for _, dataDir := range additionalDataDirs {
		toolchainDirs = append(toolchainDirs, filepath.Join(dataDir, "toolchains"))
	}
	// 3. Built-in toolchains directory
	toolchainDirs = append(toolchainDirs, filepath.Join(buildyDir, "data", "toolchains"))
	
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

	// Initialize dependency resolver and load dependencies
	workspaceCacheDir := filepath.Join(ws.RootDir, *cacheDir)
	depResolver := resource.NewDependencyResolver(*platform, *architecture, *toolchain, *configuration, workspaceCacheDir, ws.RootDir, rootVarEnv)

	// Skip dependency resolution in standalone mode (explicit config file provided)
	// When a specific config file is passed on the command line, we only build what's
	// in that file - no module discovery, no dependency resolution
	standaloneMode := ws.ConfigPath != ""
	if !standaloneMode {
		// Load dependencies from buildy_config/dependencies.yaml or buildy_config/dependencies/
		if err := depResolver.LoadDependencies(ws.RootDir); err != nil {
			util.LogFatal("Failed to load dependencies: %v", err)
		}

		// Resolve all dependencies
		if err := depResolver.ResolveAll(); err != nil {
			util.LogFatal("Failed to resolve dependencies: %v", err)
		}
	}

	// Create workspace-aware config parser
	configParser := workspace.NewConfigParser(
		*platform,
		*architecture,
		*configuration,
		cliDefines,
		toolchainManager,
		selectedToolchain,
		templateEngine,
		ws,
		depResolver,
		nil, // No parent var env
	)

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
	}

	// Build the workspace
	// Note: All dependencies (cmake, make, meson, buildy) are already built during ResolveAll()
	result, err := builder.BuildWorkspace(
		ws,
		configParser,
		targetFilter,
		buildOptions,
	)
	
	// Check if we were interrupted
	if shutdownManager.IsShuttingDown() {
		util.LogWarning("Build interrupted by signal")
		os.Exit(130) // Standard exit code for SIGINT
	}
	
	if err != nil {
		util.LogFatal("Build failed: %v", err)
	}

	// Generate and save build report
	if result != nil {
		reportPath := filepath.Join(workspaceCacheDir, "build_report.json")
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

