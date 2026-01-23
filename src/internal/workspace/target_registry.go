package workspace

import (
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
)

// TargetReference represents a reference to a target in the workspace
type TargetReference struct {
	Name       string
	ModulePath string
	ModuleInfo *ModuleInfo
}

// FullName returns the fully qualified target name (module:target)
func (tr *TargetReference) FullName() string {
	return fmt.Sprintf("%s:%s", tr.ModulePath, tr.Name)
}

// TargetRegistry manages all targets in a workspace
type TargetRegistry struct {
	workspace       *Workspace
	targetsByName   map[string][]*TargetReference
	targetsByModule map[string]map[string]*TargetReference
	initialized     bool
}

// NewTargetRegistry creates a new TargetRegistry
func NewTargetRegistry(workspace *Workspace) *TargetRegistry {
	return &TargetRegistry{
		workspace:       workspace,
		targetsByName:   make(map[string][]*TargetReference),
		targetsByModule: make(map[string]map[string]*TargetReference),
		initialized:     false,
	}
}

// Initialize initializes the registry by discovering and indexing all targets
func (tr *TargetRegistry) Initialize() error {
	if tr.initialized {
		return nil
	}

	log.Printf("Initializing target registry...")

	// Discover all modules
	modules, err := tr.workspace.DiscoverModules(false)
	if err != nil {
		return err
	}

	// Index all targets
	for modulePath, moduleInfo := range modules {
		tr.targetsByModule[modulePath] = make(map[string]*TargetReference)

		for _, targetName := range moduleInfo.Targets {
			// Create target reference
			ref := &TargetReference{
				Name:       targetName,
				ModulePath: modulePath,
				ModuleInfo: moduleInfo,
			}

			// Index by module
			tr.targetsByModule[modulePath][targetName] = ref

			// Index by name (for simple name lookup)
			tr.targetsByName[targetName] = append(tr.targetsByName[targetName], ref)
		}
	}

	tr.initialized = true
	log.Printf("Indexed %d unique target names", len(tr.targetsByName))
	return nil
}

// ResolveDependency resolves a dependency string to a target reference
func (tr *TargetRegistry) ResolveDependency(depString, currentModule string) (*TargetReference, error) {
	if !tr.initialized {
		if err := tr.Initialize(); err != nil {
			return nil, err
		}
	}

	// Local reference: ":target"
	if strings.HasPrefix(depString, ":") {
		return tr.resolveLocal(depString[1:], currentModule)
	}

	// Scoped reference: "path:target"
	if strings.Contains(depString, ":") {
		return tr.resolveScoped(depString)
	}

	// Simple name: "target"
	return tr.resolveSimple(depString, currentModule)
}

// resolveLocal resolves local reference (:target) within same module
func (tr *TargetRegistry) resolveLocal(targetName, currentModule string) (*TargetReference, error) {
	if currentModule == "" {
		return nil, fmt.Errorf(
			"local reference ':%s' used but current module unknown. "+
				"Local references can only be used within a module",
			targetName,
		)
	}

	// Look up in current module
	moduleTargets := tr.targetsByModule[currentModule]
	if ref, ok := moduleTargets[targetName]; ok {
		log.Printf("Resolved local reference :%s to %s:%s", targetName, currentModule, targetName)
		return ref, nil
	}

	return nil, fmt.Errorf(
		"target '%s' not found (local reference in module '%s')",
		targetName, currentModule,
	)
}

// resolveScoped resolves scoped reference (path:target)
func (tr *TargetRegistry) resolveScoped(depString string) (*TargetReference, error) {
	if strings.Count(depString, ":") != 1 {
		return nil, fmt.Errorf(
			"invalid scoped reference '%s'. Expected format: 'path:target' (exactly one colon)",
			depString,
		)
	}

	parts := strings.SplitN(depString, ":", 2)
	modulePath := strings.Trim(parts[0], string(filepath.Separator))
	targetName := parts[1]

	// Look up in specified module
	moduleTargets := tr.targetsByModule[modulePath]
	if ref, ok := moduleTargets[targetName]; ok {
		log.Printf("Resolved scoped reference %s", depString)
		return ref, nil
	}

	// Target not found - provide helpful error
	if _, exists := tr.targetsByModule[modulePath]; !exists {
		return nil, fmt.Errorf(
			"target '%s' not found: module '%s' does not exist",
			targetName, modulePath,
		)
	}

	available := []string{}
	for name := range moduleTargets {
		available = append(available, name)
	}
	return nil, fmt.Errorf(
		"target '%s' not found in module '%s' (available targets: %v)",
		targetName, modulePath, available,
	)
}

// resolveSimple resolves simple name reference by searching workspace
func (tr *TargetRegistry) resolveSimple(targetName, currentModule string) (*TargetReference, error) {
	// Get all matches
	matches := tr.targetsByName[targetName]

	if len(matches) == 0 {
		return nil, fmt.Errorf("target '%s' not found", targetName)
	}

	// If only one match, return it
	if len(matches) == 1 {
		log.Printf("Resolved simple reference %s to %s", targetName, matches[0].FullName())
		return matches[0], nil
	}

	// Multiple matches - try to resolve by proximity
	if currentModule != "" {
		// 1. Check current module first
		for _, match := range matches {
			if match.ModulePath == currentModule {
				log.Printf("Resolved simple reference %s to %s (current module)", targetName, match.FullName())
				return match, nil
			}
		}

		// 2. Check parent modules (walk up directory tree)
		currentParts := strings.Split(currentModule, string(filepath.Separator))
		for i := len(currentParts) - 1; i > 0; i-- {
			parentPath := strings.Join(currentParts[:i], string(filepath.Separator))
			for _, match := range matches {
				if match.ModulePath == parentPath {
					log.Printf("Resolved simple reference %s to %s (parent module)", targetName, match.FullName())
					return match, nil
				}
			}
		}
	}

	// Still ambiguous - build error message
	matchList := []string{}
	for _, ref := range matches {
		matchList = append(matchList, "    - "+ref.FullName())
	}
	suggestion := fmt.Sprintf("Use scoped name to disambiguate:\n  dependencies:\n    - %s", matches[0].FullName())

	return nil, fmt.Errorf(
		"ambiguous dependency '%s'\n  Found in:\n%s\n\n  %s",
		targetName, strings.Join(matchList, "\n"), suggestion,
	)
}

// GetTarget gets a specific target by module and name
func (tr *TargetRegistry) GetTarget(modulePath, targetName string) *TargetReference {
	if !tr.initialized {
		tr.Initialize()
	}

	if moduleTargets, ok := tr.targetsByModule[modulePath]; ok {
		return moduleTargets[targetName]
	}
	return nil
}

// ListTargets lists all targets, optionally filtered by module
func (tr *TargetRegistry) ListTargets(modulePath string) []*TargetReference {
	if !tr.initialized {
		tr.Initialize()
	}

	if modulePath != "" {
		if moduleTargets, ok := tr.targetsByModule[modulePath]; ok {
			result := make([]*TargetReference, 0, len(moduleTargets))
			for _, ref := range moduleTargets {
				result = append(result, ref)
			}
			return result
		}
		return []*TargetReference{}
	}

	// Return all targets
	allTargets := []*TargetReference{}
	for _, moduleTargets := range tr.targetsByModule {
		for _, ref := range moduleTargets {
			allTargets = append(allTargets, ref)
		}
	}
	return allTargets
}

// GetTargetNames gets set of all target names in workspace
func (tr *TargetRegistry) GetTargetNames() []string {
	if !tr.initialized {
		tr.Initialize()
	}

	names := make([]string, 0, len(tr.targetsByName))
	for name := range tr.targetsByName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// FindTargetsByName finds all targets with given name
func (tr *TargetRegistry) FindTargetsByName(targetName string) []*TargetReference {
	if !tr.initialized {
		tr.Initialize()
	}

	return tr.targetsByName[targetName]
}
