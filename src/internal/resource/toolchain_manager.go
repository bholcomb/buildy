package resource

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"buildy/internal/config"
	"buildy/pkg/util"
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

// NewToolchainManagerMulti loads the built-in toolchains, then the toolchains of each directory
// in order (a later file with the same name replaces an earlier toolchain). Every file must load:
// a toolchain that fails to, built-in or not, is an error.
func NewToolchainManagerMulti(toolchainsDirs []string) (*ToolchainManager, error) {
	tm := &ToolchainManager{
		toolchainsDirs: toolchainsDirs,
		toolchains:     make(map[string]*ToolchainConfig),
	}
	if err := tm.loadEmbeddedToolchains(); err != nil {
		return nil, err
	}
	if err := tm.loadToolchains(); err != nil {
		return nil, err
	}
	return tm, nil
}

// loadEmbeddedToolchains loads the built-in toolchains
func (tm *ToolchainManager) loadEmbeddedToolchains() error {
	files, err := ListEmbeddedFiles("toolchains")
	if err != nil {
		return fmt.Errorf("listing the built-in toolchains: %w", err)
	}
	for _, filePath := range files {
		if !strings.HasSuffix(filePath, ".yaml") {
			continue
		}
		data, err := GetEmbeddedFile(filePath)
		if err != nil {
			return fmt.Errorf("reading built-in toolchain %s: %w", filePath, err)
		}
		tc, err := config.LoadToolchain(data, "built-in "+filePath)
		if err != nil {
			return err
		}
		if other, exists := tm.toolchains[tc.Name]; exists {
			return fmt.Errorf("built-in toolchains %s and %s are both named '%s'", other.Source, tc.Source, tc.Name)
		}
		tm.toolchains[tc.Name] = tc
		util.LogVerbose("Loaded embedded toolchain: %s - %s", tc.Name, tc.Description)
	}
	return nil
}

// loadToolchains loads the toolchains of the filesystem directories
func (tm *ToolchainManager) loadToolchains() error {
	for _, toolchainsDir := range tm.toolchainsDirs {
		entries, err := os.ReadDir(toolchainsDir)
		if os.IsNotExist(err) {
			util.LogVerbose("Additional toolchains directory not found, using built-in: %s", toolchainsDir)
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to read toolchains directory %s: %w", toolchainsDir, err)
		}
		inDir := map[string]string{}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
				continue
			}
			tcFile := filepath.Join(toolchainsDir, entry.Name())
			data, err := os.ReadFile(tcFile)
			if err != nil {
				return fmt.Errorf("failed to read toolchain %s: %w", tcFile, err)
			}
			tc, err := config.LoadToolchain(data, tcFile)
			if err != nil {
				return err
			}
			if other, ok := inDir[tc.Name]; ok {
				return fmt.Errorf("toolchains %s and %s are both named '%s'", other, tcFile, tc.Name)
			}
			inDir[tc.Name] = tcFile
			if previous, exists := tm.toolchains[tc.Name]; exists {
				util.LogInfo("Toolchain '%s' from %s replaces the one from %s", tc.Name, tcFile, previous.Source)
			}
			tm.toolchains[tc.Name] = tc
			util.LogInfo("Loaded toolchain: %s - %s", tc.Name, tc.Description)
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
		if tc.Target.Platform == platform &&
			tc.Target.Architecture == architecture &&
			!tc.Execution.IsDocker() {
			matches = append(matches, tc)
		}
	}

	if len(matches) == 0 {
		util.LogWarning("No native toolchain found for %s-%s", platform, architecture)
		return nil
	}

	// Define platform-specific preferred toolchains
	// GCC is preferred over Clang on Linux since it's more commonly installed
	// Matches are already filtered by architecture, so a platform lists the
	// toolchains of every architecture
	preferredToolchains := map[string][]string{
		"linux":   {"gcc-cpp-linux", "gcc-c-linux", "clang-cpp-linux", "clang-c-linux"},
		"windows": {"msvc-cpp-windows", "msvc-c-windows", "gcc-cpp-mingw", "gcc-c-mingw"},
		"macos":   {"clang-cpp-macos", "clang-c-macos", "clang-cpp-macos-arm64", "clang-c-macos-arm64"},
	}

	// Try to find preferred toolchain for this platform
	if preferences, ok := preferredToolchains[platform]; ok {
		for _, preferredName := range preferences {
			for _, tc := range matches {
				if tc.Name == preferredName {
					util.LogInfo("Auto-detected toolchain: %s", tc.Name)
					return tc
				}
			}
		}
	}

	// Fallback: sort matches by name for deterministic selection
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Name < matches[j].Name
	})

	util.LogInfo("Auto-detected toolchain: %s (fallback)", matches[0].Name)
	return matches[0]
}

// FindByLanguage finds a toolchain by language and platform/architecture
func (tm *ToolchainManager) FindByLanguage(language, platform, architecture string) *ToolchainConfig {
	// Collect matching toolchains
	var matches []*ToolchainConfig
	for _, tc := range tm.toolchains {
		if tc.Language == language &&
			tc.Target.Platform == platform &&
			tc.Target.Architecture == architecture &&
			!tc.Execution.IsDocker() {
			matches = append(matches, tc)
		}
	}

	if len(matches) == 0 {
		// Try with just language match (for any platform toolchains)
		for _, tc := range tm.toolchains {
			if tc.Language == language &&
				tc.Target.Platform == "any" &&
				!tc.Execution.IsDocker() {
				matches = append(matches, tc)
			}
		}
	}

	if len(matches) == 0 {
		util.LogWarning("No toolchain found for language=%s, platform=%s, arch=%s", language, platform, architecture)
		return nil
	}

	// Define platform-specific preferred toolchains by language
	// GCC is preferred over Clang on Linux since it's more commonly installed
	preferredToolchains := map[string]map[string][]string{
		"c": {
			"linux":   {"gcc-c-linux", "clang-c-linux"},
			"windows": {"msvc-c-windows", "gcc-c-mingw"},
			"macos":   {"clang-c-macos", "clang-c-macos-arm64"},
		},
		"cpp": {
			"linux":   {"gcc-cpp-linux", "clang-cpp-linux"},
			"windows": {"msvc-cpp-windows", "gcc-cpp-mingw"},
			"macos":   {"clang-cpp-macos", "clang-cpp-macos-arm64"},
		},
	}

	// Try to find preferred toolchain for this language/platform
	if langPrefs, ok := preferredToolchains[language]; ok {
		if platformPrefs, ok := langPrefs[platform]; ok {
			for _, preferredName := range platformPrefs {
				for _, tc := range matches {
					if tc.Name == preferredName {
						util.LogInfo("Auto-selected toolchain for %s: %s", language, tc.Name)
						return tc
					}
				}
			}
		}
	}

	// Fallback: sort for deterministic selection and return first match
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Name < matches[j].Name
	})

	util.LogInfo("Auto-selected toolchain for %s: %s (fallback)", language, matches[0].Name)
	return matches[0]
}

// ListToolchains lists the toolchains, by name
func (tm *ToolchainManager) ListToolchains() []*ToolchainConfig {
	names := make([]string, 0, len(tm.toolchains))
	for name := range tm.toolchains {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]*ToolchainConfig, len(names))
	for i, name := range names {
		result[i] = tm.toolchains[name]
	}
	return result
}
