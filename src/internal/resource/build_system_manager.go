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

// BuildSystemManager holds the build system definitions: the built-in ones, then those of the
// data directories (LoadFilesystemSystems).
type BuildSystemManager struct {
	systems     map[string]*BuildSystemConfig
	definitions map[string]string // name: SHA-256 of the definition, in each build's marker
	sources     map[string]string // name: the file that defines it ("built-in ..." for a built-in)
}

// NewBuildSystemManager loads the built-in build systems. Every one must load.
func NewBuildSystemManager() (*BuildSystemManager, error) {
	bsm := &BuildSystemManager{
		systems:     make(map[string]*BuildSystemConfig),
		definitions: make(map[string]string),
		sources:     make(map[string]string),
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
		bs, err := config.LoadBuildSystem(data, "built-in "+filePath)
		if err != nil {
			return nil, err
		}
		bsm.add(bs, data, "built-in "+filePath)
		util.LogVerbose("Loaded build system: %s - %s (%s)", bs.Name, bs.Description, filePath)
	}
	return bsm, nil
}

// LoadFilesystemSystems loads the build system definitions (*.yaml) of directories, lowest
// priority first: a definition replaces one of the same name from an earlier directory or a
// built-in one, and says so. Two in one directory with the same name are an error, and so is a
// file that doesn't load. A directory that doesn't exist is skipped.
func (bsm *BuildSystemManager) LoadFilesystemSystems(dirs []string) error {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading build systems directory %s: %w", dir, err)
		}
		inDir := map[string]string{}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}
			filePath := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("reading build system %s: %w", filePath, err)
			}
			bs, err := config.LoadBuildSystem(data, filePath)
			if err != nil {
				return err
			}
			if other, ok := inDir[bs.Name]; ok {
				return fmt.Errorf("build systems %s and %s are both named '%s'", other, filePath, bs.Name)
			}
			inDir[bs.Name] = filePath
			previous := bsm.sources[bs.Name]
			bsm.add(bs, data, filePath)
			if previous != "" {
				util.LogInfo("Build system '%s' from %s replaces the one from %s", bs.Name, filePath, previous)
			}
			util.LogInfo("Loaded build system: %s - %s (%s)", bs.Name, bs.Description, filePath)
		}
	}
	return nil
}

// add records a definition loaded from data, replacing one of the same name.
func (bsm *BuildSystemManager) add(bs *BuildSystemConfig, data []byte, source string) {
	bsm.systems[bs.Name] = bs
	hash := sha256.Sum256(data)
	bsm.definitions[bs.Name] = hex.EncodeToString(hash[:])
	bsm.sources[bs.Name] = source
}

// Detect returns the build system of the source tree in dir: of those with a marker file there,
// the one with the highest detection priority. None is nil; two at the highest priority are an
// error naming them.
func (bsm *BuildSystemManager) Detect(dir string) (*BuildSystemConfig, error) {
	var best []*BuildSystemConfig
	for _, name := range bsm.ListBuildSystems() {
		bs := bsm.systems[name]
		found := false
		for _, marker := range bs.Detection.MarkerFiles {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				found = true
				break
			}
		}
		switch {
		case !found:
		case len(best) == 0 || bs.Detection.Priority > best[0].Detection.Priority:
			best = []*BuildSystemConfig{bs}
		case bs.Detection.Priority == best[0].Detection.Priority:
			best = append(best, bs)
		}
	}
	if len(best) > 1 {
		var names []string
		for _, bs := range best {
			names = append(names, fmt.Sprintf("%s (%s, markers %s)", bs.Name, bsm.sources[bs.Name], strings.Join(bs.Detection.MarkerFiles, ", ")))
		}
		return nil, fmt.Errorf("build systems %s detect %s at the same priority (%d): set build.system, or change a detection.priority", strings.Join(names, " and "), dir, best[0].Detection.Priority)
	}
	if len(best) == 0 {
		return nil, nil
	}
	return best[0], nil
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
