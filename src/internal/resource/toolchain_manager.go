package resource

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ToolchainManager manages toolchain selection and loading
type ToolchainManager struct {
	toolchainsDirs []string
	toolchains     map[string]*ToolchainConfig
}

// NewToolchainManager creates a new ToolchainManager from a single directory
func NewToolchainManager(toolchainsDir string) (*ToolchainManager, error) {
	return NewToolchainManagerMulti([]string{toolchainsDir})
}

// NewToolchainManagerMulti creates a new ToolchainManager from multiple directories
func NewToolchainManagerMulti(toolchainsDirs []string) (*ToolchainManager, error) {
	tm := &ToolchainManager{
		toolchainsDirs: toolchainsDirs,
		toolchains:     make(map[string]*ToolchainConfig),
	}

	// First load embedded toolchains (built-in)
	if err := tm.loadEmbeddedToolchains(); err != nil {
		log.Printf("WARNING: Failed to load embedded toolchains: %v", err)
	}

	// Then load from filesystem directories (can override built-in)
	if err := tm.loadToolchains(); err != nil {
		return nil, err
	}

	return tm, nil
}

// loadEmbeddedToolchains loads all toolchain configurations from embedded data
func (tm *ToolchainManager) loadEmbeddedToolchains() error {
	// List all embedded toolchain files
	files, err := ListEmbeddedFiles("toolchains")
	if err != nil {
		return err
	}

	for _, filePath := range files {
		if !strings.HasSuffix(filePath, ".yaml") {
			continue
		}

		// Read the embedded file
		data, err := GetEmbeddedFile(filePath)
		if err != nil {
			log.Printf("WARNING: Failed to read embedded toolchain %s: %v", filePath, err)
			continue
		}

		// Parse the toolchain
		tc, err := LoadToolchainConfigFromData(data, filePath)
		if err != nil {
			log.Printf("WARNING: Failed to parse embedded toolchain %s: %v", filePath, err)
			continue
		}

		tm.toolchains[tc.Name] = tc
		log.Printf("Loaded embedded toolchain: %s - %s", tc.Name, tc.Description)
	}

	return nil
}

// loadToolchains loads all toolchain configurations from filesystem directories
func (tm *ToolchainManager) loadToolchains() error {
	// Load toolchains from each directory in order
	for _, toolchainsDir := range tm.toolchainsDirs {
		// Check if directory exists
		if _, err := os.Stat(toolchainsDir); os.IsNotExist(err) {
			log.Printf("WARNING: Toolchains directory not found: %s", toolchainsDir)
			continue // Not fatal, try next directory
		}

		// Read all .yaml files
		entries, err := os.ReadDir(toolchainsDir)
		if err != nil {
			log.Printf("WARNING: Failed to read toolchains directory %s: %v", toolchainsDir, err)
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			if filepath.Ext(entry.Name()) == ".yaml" {
				tcFile := filepath.Join(toolchainsDir, entry.Name())
				tc, err := LoadToolchainConfig(tcFile)
				if err != nil {
					log.Printf("ERROR: Failed to load toolchain %s: %v", tcFile, err)
					continue
				}

				if _, exists := tm.toolchains[tc.Name]; exists {
					log.Printf("Toolchain '%s' from %s overrides built-in", tc.Name, toolchainsDir)
				}

				tm.toolchains[tc.Name] = tc
				log.Printf("Loaded toolchain: %s - %s", tc.Name, tc.Description)
			}
		}
	}

	return nil
}

// GetToolchain gets a toolchain by name
func (tm *ToolchainManager) GetToolchain(name string) *ToolchainConfig {
	return tm.toolchains[name]
}

// AutoDetect auto-detects the best toolchain for the given platform/architecture
func (tm *ToolchainManager) AutoDetect(platform, architecture string) *ToolchainConfig {
	// Collect matching toolchains
	var matches []*ToolchainConfig
	for _, tc := range tm.toolchains {
		if tc.TargetPlatform == platform &&
			tc.TargetArchitecture == architecture &&
			tc.ExecutionType == "native" {
			matches = append(matches, tc)
		}
	}

	if len(matches) == 0 {
		log.Printf("WARNING: No native toolchain found for %s-%s", platform, architecture)
		return nil
	}

	// Define platform-specific preferred toolchains
	preferredToolchains := map[string][]string{
		"linux":   {"gcc-linux", "clang-linux"},
		"windows": {"msvc-windows", "gcc-mingw", "clang-windows"},
		"macos":   {"clang-macos", "gcc-macos"},
		"darwin":  {"clang-macos", "gcc-macos"}, // darwin is macOS
	}

	// Try to find preferred toolchain for this platform
	if preferences, ok := preferredToolchains[platform]; ok {
		for _, preferredName := range preferences {
			for _, tc := range matches {
				if tc.Name == preferredName {
					log.Printf("Auto-detected toolchain: %s", tc.Name)
					return tc
				}
			}
		}
	}

	// Fallback: sort matches by name for deterministic selection
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Name < matches[j].Name
	})

	log.Printf("Auto-detected toolchain: %s (fallback)", matches[0].Name)
	return matches[0]
}

// FindByLanguage finds a toolchain by language and platform/architecture
func (tm *ToolchainManager) FindByLanguage(language, platform, architecture string) *ToolchainConfig {
	// Collect matching toolchains
	var matches []*ToolchainConfig
	for _, tc := range tm.toolchains {
		if tc.Language == language &&
			tc.TargetPlatform == platform &&
			tc.TargetArchitecture == architecture &&
			tc.ExecutionType == "native" {
			matches = append(matches, tc)
		}
	}

	if len(matches) == 0 {
		// Try with just language match (for any platform toolchains)
		for _, tc := range tm.toolchains {
			if tc.Language == language &&
				tc.TargetPlatform == "any" &&
				tc.ExecutionType == "native" {
				matches = append(matches, tc)
			}
		}
	}

	if len(matches) == 0 {
		log.Printf("WARNING: No toolchain found for language=%s, platform=%s, arch=%s", language, platform, architecture)
		return nil
	}

	// Sort for deterministic selection and return first match
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Name < matches[j].Name
	})

	log.Printf("Auto-selected toolchain for %s: %s", language, matches[0].Name)
	return matches[0]
}

// ListToolchains lists all available toolchains with descriptions
func (tm *ToolchainManager) ListToolchains() []struct {
	Name        string
	Description string
} {
	result := []struct {
		Name        string
		Description string
	}{}

	for _, tc := range tm.toolchains {
		result = append(result, struct {
			Name        string
			Description string
		}{
			Name:        tc.Name,
			Description: tc.Description,
		})
	}

	return result
}
