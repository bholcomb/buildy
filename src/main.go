package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const Version = "0.1.0-go"

// Global verbose flag and logger
var Verbose bool

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

// getAutoBuildyDirs returns automatic buildy/ directory search paths
func getAutoBuildyDirs(workspaceRoot, buildyDir, subdir string) []string {
	dirs := []string{}
	
	// 1. Workspace buildy/<subdir>
	if workspaceRoot != "" {
		dirs = append(dirs, filepath.Join(workspaceRoot, "buildy", subdir))
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
	dirs = append(dirs, filepath.Join(buildyDir, "buildy", subdir))
	
	return dirs
}

func main() {
	// Initialize shutdown manager for graceful signal handling
	shutdownManager := InitShutdownManager()
	defer shutdownManager.Shutdown()
	
	// Get the directory where the buildy executable is located
	buildyDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Fatalf("Failed to get buildy directory: %v", err)
	}

	// Define CLI flags with auto-detected defaults
	defaultPlatform := detectPlatform()
	defaultArch := detectArchitecture()
	
	platform := flag.String("platform", defaultPlatform, "Target platform (auto-detected: "+defaultPlatform+")")
	architecture := flag.String("architecture", defaultArch, "Target architecture (auto-detected: "+defaultArch+")")
	configuration := flag.String("configuration", "debug", "Build configuration")
	cacheDir := flag.String("cache-dir", ".buildy_cache", "Cache directory")
	dryRun := flag.Bool("dry-run", false, "Generate tasks but don't execute")
	workers := flag.Int("workers", DefaultMaxWorkers, "Max parallel workers")
	cacheStats := flag.Bool("cache-stats", false, "Show cache statistics")
	clean := flag.Bool("clean", false, "Clean build artifacts (removes cache and build directories)")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	verboseShort := flag.Bool("v", false, "Enable verbose logging (short)")
	toolchain := flag.String("toolchain", "", "Specify toolchain to use (overrides config file)")
	toolchainShort := flag.String("t", "", "Specify toolchain to use (short)")
	listToolchains := flag.Bool("list-toolchains", false, "List available toolchains and exit")
	force := flag.Bool("force", false, "Force full rebuild, ignore cache and build state")
	compileCommands := flag.Bool("compile-commands", false, "Generate compile_commands.json in buildy/ folder")
	_ = flag.Bool("all", false, "Build all targets in workspace (default if no --target specified)")

	// Custom flag for multiple defines
	var defines multiStringFlag
	flag.Var(&defines, "define", "Define a variable (can be used multiple times, e.g. -define MY_VAR=value)")
	flag.Var(&defines, "D", "Define a variable (short)")

	// Custom flag for multiple targets
	var targets multiStringFlag
	flag.Var(&targets, "target", "Build specific target(s) (workspace mode only, can be used multiple times)")

	// Custom flag for additional data directories
	var additionalDataDirs multiStringFlag
	flag.Var(&additionalDataDirs, "add-data-dir", "Additional data directory (looks for templates/ and toolchains/ subdirectories, can be used multiple times, parsed in order)")

	// Custom flag for package directories
	var packageDirs multiStringFlag
	flag.Var(&packageDirs, "package-dir", "Additional package directory (can be used multiple times, parsed in order)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Buildy v%s - Task-based build system\n\n", Version)
		fmt.Fprintf(os.Stderr, "Usage: %s [options] [config_files...]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	// Handle verbose flag (either -v or --verbose)
	if *verbose || *verboseShort {
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	} else {
		log.SetFlags(log.Ldate | log.Ltime)
	}

	// Handle toolchain flag (either -t or --toolchain)
	selectedToolchain := *toolchain
	if selectedToolchain == "" && *toolchainShort != "" {
		selectedToolchain = *toolchainShort
	}

	// Parse CLI defines
	cliDefines := make(map[string]string)
	for _, define := range defines {
		if !strings.Contains(define, "=") {
			log.Fatalf("Invalid --define format: '%s' (expected VAR=VALUE)", define)
		}
		parts := strings.SplitN(define, "=", 2)
		varName := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		cliDefines[varName] = value
		log.Printf("CLI define: %s=%s", varName, value)
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
				log.Fatalf("Failed to get current directory: %v", err)
			}
		} else {
			// Use directory of first config file
			configPath := configFiles[0]
			absPath, err := filepath.Abs(configPath)
			if err != nil {
				log.Fatalf("Failed to resolve config path: %v", err)
			}
			
			// If it's a file, use its directory; if it's a directory, use it
			info, err := os.Stat(absPath)
			if err != nil {
				log.Fatalf("Failed to stat config path: %v", err)
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
			log.Printf("Removing cache directory: %s", cachePath)
			if err := os.RemoveAll(cachePath); err != nil {
				log.Fatalf("Failed to remove cache directory: %v", err)
			}
			log.Printf("✓ Cache directory removed")
		} else {
			log.Printf("Cache directory not found: %s", cachePath)
		}
		
		// Clean build directory
		buildPath := filepath.Join(workspaceRoot, "build")
		if _, err := os.Stat(buildPath); err == nil {
			log.Printf("Removing build directory: %s", buildPath)
			if err := os.RemoveAll(buildPath); err != nil {
				log.Fatalf("Failed to remove build directory: %v", err)
			}
			log.Printf("✓ Build directory removed")
		} else {
			log.Printf("Build directory not found: %s", buildPath)
		}
		
		log.Printf("✓ Clean complete")
		os.Exit(0)
	}

	// Verify embedded data
	if err := VerifyEmbeddedData(); err != nil {
		log.Fatalf("Failed to verify embedded data: %v", err)
	}

	// Initialize toolchain manager with base directory and additional directories
	toolchainsDir := filepath.Join(buildyDir, "data", "toolchains")
	toolchainDirs := []string{toolchainsDir}
	for _, dataDir := range additionalDataDirs {
		toolchainDirs = append(toolchainDirs, filepath.Join(dataDir, "toolchains"))
	}
	toolchainManager, err := NewToolchainManagerMulti(toolchainDirs)
	if err != nil {
		log.Fatalf("Failed to initialize toolchain manager: %v", err)
	}

	// Handle --list-toolchains
	if *listToolchains {
		log.Printf("Available toolchains:")
		for _, tc := range toolchainManager.ListToolchains() {
			log.Printf("  %-20s - %s", tc.Name, tc.Description)
		}
		os.Exit(0)
	}

	// Initialize cache
	cache, err := NewBuildCache(*cacheDir)
	if err != nil {
		log.Fatalf("Failed to initialize cache: %v", err)
	}

	// Show cache stats if requested
	if *cacheStats {
		stats := cache.GetCacheStats()
		log.Printf("Cache Statistics:")
		log.Printf("  Total entries: %d", stats["total_entries"])
		log.Printf("  Total size: %.1f MB", stats["total_size_mb"])
		log.Printf("  Cache directory: %s", stats["cache_directory"])
		os.Exit(0)
	}

	// Initialize template engine with base directory and additional directories
	templatesDir := filepath.Join(buildyDir, "data", "templates")
	templateDirs := []string{templatesDir}
	for _, dataDir := range additionalDataDirs {
		templateDirs = append(templateDirs, filepath.Join(dataDir, "templates"))
	}
	templateEngine, err := NewBuildTemplateEngineMulti(templateDirs)
	if err != nil {
		log.Fatalf("Failed to initialize template engine: %v", err)
	}

	// Get config files from remaining arguments
	configFiles := flag.Args()

	// Everything is a workspace - discover or load from specified path
	var workspace *Workspace

	if len(configFiles) == 0 {
		// No config files specified - discover workspace from current directory
		log.Printf("No config file specified, attempting workspace discovery...")

		// Try loading from cache first
		workspace, err = LoadDiscoveryCache(*cacheDir)
		if err == nil && workspace != nil {
			log.Printf("Loaded workspace from cache: %s", workspace.RootDir)
		} else {
			// Cache miss or invalid, do full discovery
			cwd, _ := os.Getwd()
			workspace, err = DiscoverWorkspace(cwd)
			if err != nil {
				log.Fatalf("No workspace found. Run from a directory with buildy.yaml or specify a path\nError: %v", err)
			}
			log.Printf("Discovered workspace at: %s", workspace.RootDir)
		}
	} else {
		// Config file or directory specified - treat as workspace root
		workspaceRoot := configFiles[0]
		
		// If it's a file, use its directory as workspace root
		info, err := os.Stat(workspaceRoot)
		if err != nil {
			log.Fatalf("Path not found: %s", workspaceRoot)
		}
		if !info.IsDir() {
			workspaceRoot = filepath.Dir(workspaceRoot)
		}
		
		// Verify buildy.yaml exists
		configPath := filepath.Join(workspaceRoot, "buildy.yaml")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			log.Fatalf("No buildy.yaml found in: %s", workspaceRoot)
		}

		// Load as workspace
		workspace, err = NewWorkspace(workspaceRoot)
		if err != nil {
			log.Fatalf("Failed to load workspace from %s: %v", workspaceRoot, err)
		}
		log.Printf("Loaded workspace from: %s", workspace.RootDir)

		// Warn about multiple paths (not yet supported)
		if len(configFiles) > 1 {
			log.Printf("WARNING: Multiple paths not yet supported, using first one")
		}
	}

	// Discover modules (will always find at least the root module)
	if _, err := workspace.DiscoverModules(false); err != nil {
		log.Fatalf("Failed to discover modules: %v", err)
	}
	log.Printf("Found %d module(s)", len(workspace.Modules))

	// Determine target filter
	var targetFilter []string
	if len(targets) > 0 {
		targetFilter = targets
	}

	// Create variable environment for package resolution
	rootVarEnv := NewVariableEnvironment(nil)
	rootVarEnv.SetVariable("platform", *platform, "builtin")
	rootVarEnv.SetVariable("architecture", *architecture, "builtin")
	rootVarEnv.SetVariable("configuration", *configuration, "builtin")

	// Initialize package manager
	var workspacePackagePaths []string
	if workspace.Config != nil {
		workspacePackagePaths = workspace.Config.PackagePaths
	}
	allPackageDirs := append(workspacePackagePaths, packageDirs...)
	packageManager := NewPackageManager(workspace.RootDir, allPackageDirs, rootVarEnv)

	// Create workspace-aware config parser
	configParser := NewConfigParser(
		*platform,
		*architecture,
		*configuration,
		cliDefines,
		toolchainManager,
		selectedToolchain,
		templateEngine,
		workspace,
		packageManager,
		nil, // No parent var env
	)

	// Set cache directory relative to workspace root
	workspaceCacheDir := filepath.Join(workspace.RootDir, *cacheDir)
	builder, err := NewBuilder(workspaceCacheDir, *workers)
	if err != nil {
		log.Fatalf("Failed to create builder: %v", err)
	}

	// Configure builder options
	buildOptions := BuildOptions{
		DryRun:          *dryRun,
		Force:           *force,
		CompileCommands: *compileCommands,
	}

	// Build the workspace
	result, err := builder.BuildWorkspace(
		workspace,
		configParser,
		targetFilter,
		buildOptions,
	)
	
	// Check if we were interrupted
	if shutdownManager.IsShuttingDown() {
		log.Printf("Build interrupted by signal")
		os.Exit(130) // Standard exit code for SIGINT
	}
	
	if err != nil {
		log.Fatalf("Build failed: %v", err)
	}

	// Generate and save build report
	if result != nil {
		reportPath := filepath.Join(workspaceCacheDir, "build_report.json")
		if err := result.SaveReport(reportPath); err != nil {
			log.Printf("WARNING: Failed to save build report: %v", err)
		}
		
		// Print terminal summary
		result.PrintSummary()
		
		// Generate compile_commands.json if requested
		if *compileCommands && result.Success {
			compileCommandsPath := filepath.Join(workspace.RootDir, "buildy", "compile_commands.json")
			if err := result.GenerateCompileCommands(compileCommandsPath); err != nil {
				log.Printf("WARNING: Failed to generate compile_commands.json: %v", err)
			} else {
				log.Printf("Generated compile_commands.json at %s", compileCommandsPath)
			}
		}
	}

	if result != nil && result.Success {
		os.Exit(0)
	} else {
		os.Exit(1)
	}
}

// multiStringFlag implements flag.Value for multiple string flags
type multiStringFlag []string

func (m *multiStringFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiStringFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}
