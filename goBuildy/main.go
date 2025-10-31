package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const Version = "0.1.0-go"

func main() {
	// Get the directory where the buildy executable is located
	buildyDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Fatalf("Failed to get buildy directory: %v", err)
	}

	// Define CLI flags
	platform := flag.String("platform", "linux", "Target platform")
	architecture := flag.String("architecture", "x86_64", "Target architecture")
	configuration := flag.String("configuration", "debug", "Build configuration")
	cacheDir := flag.String("cache-dir", ".buildy_cache", "Cache directory")
	dryRun := flag.Bool("dry-run", false, "Generate tasks but don't execute")
	workers := flag.Int("workers", DefaultMaxWorkers, "Max parallel workers")
	cacheStats := flag.Bool("cache-stats", false, "Show cache statistics")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	verboseShort := flag.Bool("v", false, "Enable verbose logging (short)")
	toolchain := flag.String("toolchain", "", "Specify toolchain to use (overrides config file)")
	toolchainShort := flag.String("t", "", "Specify toolchain to use (short)")
	listToolchains := flag.Bool("list-toolchains", false, "List available toolchains and exit")
	toolchainsDir := flag.String("toolchains-dir", filepath.Join(buildyDir, "data", "toolchains"), "Directory containing toolchain configurations")
	templatesDir := flag.String("templates-dir", filepath.Join(buildyDir, "data", "templates"), "Directory containing build template files")
	force := flag.Bool("force", false, "Force full rebuild, ignore cache and build state")
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

	// Verify embedded data
	if err := VerifyEmbeddedData(); err != nil {
		log.Fatalf("Failed to verify embedded data: %v", err)
	}

	// Initialize toolchain manager with base directory and additional directories
	toolchainDirs := []string{*toolchainsDir}
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
	templateDirs := []string{*templatesDir}
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
		nil, // No parent var env
	)

	// Set cache directory relative to workspace root
	workspaceCacheDir := filepath.Join(workspace.RootDir, *cacheDir)
	builder, err := NewBuilder(workspaceCacheDir, *workers)
	if err != nil {
		log.Fatalf("Failed to create builder: %v", err)
	}

	// Build the workspace
	success, err := builder.BuildWorkspace(
		workspace,
		configParser,
		targetFilter,
		*dryRun,
		*force,
	)
	if err != nil {
		log.Fatalf("Build failed: %v", err)
	}

	if success {
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
