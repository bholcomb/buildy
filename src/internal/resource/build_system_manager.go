package resource

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"buildy/internal/config"
	"buildy/pkg/util"
)

// BuildSystemConfig is a build system definition (config.BuildSystem).
type BuildSystemConfig = config.BuildSystem

// BuildSystemManager manages build system detection and execution
type BuildSystemManager struct {
	systems     map[string]*BuildSystemConfig
	definitions map[string]string // name: SHA-256 of the definition, in each build's marker
}

// NewBuildSystemManager loads the built-in build systems. Every one must load.
func NewBuildSystemManager() (*BuildSystemManager, error) {
	bsm := &BuildSystemManager{
		systems:     make(map[string]*BuildSystemConfig),
		definitions: make(map[string]string),
	}
	files, err := ListEmbeddedFiles("build_systems")
	if err != nil {
		return nil, fmt.Errorf("listing the built-in build systems: %w", err)
	}
	for _, filePath := range files {
		if !strings.HasSuffix(filePath, ".yaml") {
			continue
		}
		data, err := GetEmbeddedFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("reading built-in build system %s: %w", filePath, err)
		}
		if err := bsm.add(data, "built-in "+filePath); err != nil {
			return nil, err
		}
	}
	return bsm, nil
}

// LoadFilesystemSystems loads the build system definitions of directories; a later definition
// with the same name replaces an earlier one.
func (bsm *BuildSystemManager) LoadFilesystemSystems(dirs []string) error {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading build systems directory %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}
			filePath := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("reading build system %s: %w", filePath, err)
			}
			if err := bsm.add(data, filePath); err != nil {
				return err
			}
		}
	}
	return nil
}

func (bsm *BuildSystemManager) add(data []byte, source string) error {
	bs, err := config.LoadBuildSystem(data, source)
	if err != nil {
		return err
	}
	bsm.systems[bs.Name] = bs
	hash := sha256.Sum256(data)
	bsm.definitions[bs.Name] = hex.EncodeToString(hash[:])
	util.LogVerbose("Loaded build system: %s - %s (%s)", bs.Name, bs.Description, source)
	return nil
}

// Detect detects the build system used in a directory
func (bsm *BuildSystemManager) Detect(dir string) *BuildSystemConfig {
	// Collect all matching build systems
	type match struct {
		config   *BuildSystemConfig
		priority int
	}
	var matches []match

	for _, name := range bsm.ListBuildSystems() {
		config := bsm.systems[name]
		for _, marker := range config.Detection.MarkerFiles {
			markerPath := filepath.Join(dir, marker)
			if _, err := os.Stat(markerPath); err == nil {
				matches = append(matches, match{
					config:   config,
					priority: config.Detection.Priority,
				})
				break // Found one marker, no need to check others for this system
			}
		}
	}

	if len(matches) == 0 {
		return nil
	}

	// The highest priority wins; between equal priorities, the first by name
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].priority > matches[j].priority
	})

	return matches[0].config
}

// GetBuildSystem returns a build system by name
func (bsm *BuildSystemManager) GetBuildSystem(name string) *BuildSystemConfig {
	return bsm.systems[name]
}

// definition returns the SHA-256 of a build system's definition.
func (bsm *BuildSystemManager) definition(name string) string {
	return bsm.definitions[name]
}

// ListBuildSystems returns all loaded build system names
func (bsm *BuildSystemManager) ListBuildSystems() []string {
	names := make([]string, 0, len(bsm.systems))
	for name := range bsm.systems {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
