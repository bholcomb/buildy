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

// AutoDetect returns the default toolchain for a platform and architecture: of the native
// toolchains that target them, the one with the highest priority (toolchain.priority). None is
// nil; two with the highest priority are an error.
func (tm *ToolchainManager) AutoDetect(platform, architecture string) (*ToolchainConfig, error) {
	tc, err := tm.highestPriority(func(tc *ToolchainConfig) bool {
		return tc.Target.Platform == platform && tc.Target.Architecture == architecture
	})
	if err != nil {
		return nil, fmt.Errorf("choosing the toolchain for %s-%s: %w", platform, architecture, err)
	}
	if tc != nil {
		util.LogInfo("Auto-detected toolchain: %s", tc.Name)
	}
	return tc, nil
}

// FindByLanguage returns the default toolchain for a language on a platform and architecture: of
// the native toolchains for the language that target them, or else that target any platform, the
// one with the highest priority. None is nil; two with the highest priority are an error.
func (tm *ToolchainManager) FindByLanguage(language, platform, architecture string) (*ToolchainConfig, error) {
	tc, err := tm.highestPriority(func(tc *ToolchainConfig) bool {
		return tc.Language == language && tc.Target.Platform == platform && tc.Target.Architecture == architecture
	})
	if err == nil && tc == nil {
		tc, err = tm.highestPriority(func(tc *ToolchainConfig) bool {
			return tc.Language == language && tc.Target.Platform == "any"
		})
	}
	if err != nil {
		return nil, fmt.Errorf("choosing the %s toolchain for %s-%s: %w", language, platform, architecture, err)
	}
	if tc != nil {
		util.LogInfo("Auto-selected toolchain for %s: %s", language, tc.Name)
	}
	return tc, nil
}

// highestPriority returns the native toolchain with the highest priority of those that match, or
// nil if none does. Two with the highest priority are an error naming both.
func (tm *ToolchainManager) highestPriority(match func(*ToolchainConfig) bool) (*ToolchainConfig, error) {
	var best, tied *ToolchainConfig
	for _, tc := range tm.ListToolchains() {
		if tc.Execution.IsDocker() || !match(tc) {
			continue
		}
		switch {
		case best == nil || tc.Priority > best.Priority:
			best, tied = tc, nil
		case tc.Priority == best.Priority:
			tied = tc
		}
	}
	if tied != nil {
		return nil, fmt.Errorf("toolchains %s (%s) and %s (%s) both have priority %d; give one a higher toolchain.priority, or choose one with --toolchain",
			best.Name, best.Source, tied.Name, tied.Source, best.Priority)
	}
	return best, nil
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
