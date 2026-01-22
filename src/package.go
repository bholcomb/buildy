package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// Package represents a third-party library package configuration
type Package struct {
	Name        string
	Description string
	IncludeDirs []string
	LibDirs     []string
	Libs        []string
	Frameworks  []string // macOS only
	Defines     []string
	Sources     []string
	Packages    []string // Transitive dependencies
}

// PackageConfig represents the raw package configuration from YAML
type PackageConfig struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	IncludeDirs any            `yaml:"include_dirs"`
	LibDirs     any            `yaml:"lib_dirs"`
	Libs        any            `yaml:"libs"`
	Frameworks  any            `yaml:"frameworks"`
	Defines     any            `yaml:"defines"`
	Sources     any            `yaml:"sources"`
	Packages    any            `yaml:"packages"`
}

// PackageManager manages package loading and resolution
type PackageManager struct {
	packageDirs []string
	packages    map[string]*Package
	varEnv      *VariableEnvironment
}

// NewPackageManager creates a new PackageManager
func NewPackageManager(workspaceRoot string, packageDirs []string, varEnv *VariableEnvironment) *PackageManager {
	pm := &PackageManager{
		packageDirs: []string{},
		packages:    make(map[string]*Package),
		varEnv:      varEnv,
	}

	// Build package search paths in order:
	// 1. Workspace buildy/packages
	if workspaceRoot != "" {
		pm.packageDirs = append(pm.packageDirs, filepath.Join(workspaceRoot, "buildy", "packages"))
	}

	// 2. Command-line --package-dir
	pm.packageDirs = append(pm.packageDirs, packageDirs...)

	// 3. System locations
	pm.packageDirs = append(pm.packageDirs, getSystemPackageDirs()...)

	// 4. Executable location
	if execDir, err := filepath.Abs(filepath.Dir(os.Args[0])); err == nil {
		pm.packageDirs = append(pm.packageDirs, filepath.Join(execDir, "buildy", "packages"))
	}

	return pm
}

// getSystemPackageDirs returns platform-specific system package directories
func getSystemPackageDirs() []string {
	switch runtime.GOOS {
	case "linux":
		return []string{
			"/usr/local/lib/buildy/packages",
			"/usr/lib/buildy/packages",
			"/opt/buildy/packages",
		}
	case "darwin": // macOS
		return []string{
			"/usr/local/lib/buildy/packages",
			"/opt/buildy/packages",
		}
	case "windows":
		return []string{
			filepath.Join(os.Getenv("ProgramFiles"), "buildy", "packages"),
			"C:\\buildy\\packages",
		}
	default:
		return []string{}
	}
}

// LoadPackage loads a package by name, resolving it from package directories
func (pm *PackageManager) LoadPackage(name string, platform, architecture string, packageVars map[string]string) (*Package, error) {
	// Check if already loaded
	if pkg, exists := pm.packages[name]; exists {
		return pkg, nil
	}

	// Search for package file
	var packageFile string
	for _, dir := range pm.packageDirs {
		candidate := filepath.Join(dir, name+".yaml")
		if _, err := os.Stat(candidate); err == nil {
			packageFile = candidate
			break
		}
	}

	if packageFile == "" {
		return nil, fmt.Errorf("package '%s' not found in search paths: %v", name, pm.packageDirs)
	}

	log.Printf("Loading package '%s' from %s", name, packageFile)

	// Read and parse package file
	data, err := os.ReadFile(packageFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read package file: %w", err)
	}

	var rawConfig map[string]any
	if err := yaml.Unmarshal(data, &rawConfig); err != nil {
		return nil, fmt.Errorf("failed to parse package YAML: %w", err)
	}

	packageData, ok := rawConfig["package"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("package file missing 'package:' section")
	}

	// Create package-specific variable environment
	pkgVarEnv := pm.varEnv.CreateChild()
	
	// Add package-specific variables
	for key, value := range packageVars {
		pkgVarEnv.SetVariable(key, value, "package-config")
	}

	// Parse package configuration with platform/architecture resolution
	pkg := &Package{
		Name:        name,
		IncludeDirs: []string{},
		LibDirs:     []string{},
		Libs:        []string{},
		Frameworks:  []string{},
		Defines:     []string{},
		Sources:     []string{},
		Packages:    []string{},
	}

	if desc, ok := packageData["description"].(string); ok {
		pkg.Description = desc
	}

	// Check for new format with top-level platform subsections (common, linux, windows, etc.)
	// vs old format with per-field platform subsections
	if _, hasCommon := packageData["common"]; hasCommon {
		// New format: top-level platform subsections
		pm.resolveNewPackageFormat(pkg, packageData, platform, architecture, pkgVarEnv)
	} else {
		// Legacy format: per-field platform subsections
		pkg.IncludeDirs = pm.resolveHierarchicalStringList(packageData, "include_dirs", platform, architecture, pkgVarEnv)
		pkg.LibDirs = pm.resolveHierarchicalStringList(packageData, "lib_dirs", platform, architecture, pkgVarEnv)
		pkg.Libs = pm.resolveHierarchicalStringList(packageData, "libs", platform, architecture, pkgVarEnv)
		pkg.Frameworks = pm.resolveHierarchicalStringList(packageData, "frameworks", platform, architecture, pkgVarEnv)
		pkg.Defines = pm.resolveHierarchicalStringList(packageData, "defines", platform, architecture, pkgVarEnv)
		pkg.Sources = pm.resolveHierarchicalStringList(packageData, "sources", platform, architecture, pkgVarEnv)
		pkg.Packages = pm.resolveHierarchicalStringList(packageData, "packages", platform, architecture, pkgVarEnv)
	}

	// Cache the package
	pm.packages[name] = pkg

	// Load transitive dependencies
	for _, depName := range pkg.Packages {
		if _, err := pm.LoadPackage(depName, platform, architecture, nil); err != nil {
			return nil, fmt.Errorf("failed to load transitive dependency '%s': %w", depName, err)
		}
	}

	return pkg, nil
}

// resolveNewPackageFormat resolves package data from new format with top-level platform subsections
func (pm *PackageManager) resolveNewPackageFormat(pkg *Package, packageData map[string]any, platform, architecture string, varEnv *VariableEnvironment) {
	// Helper to extract string list from a section
	extractStringList := func(section map[string]any, field string) []string {
		result := []string{}
		if value, ok := section[field]; ok {
			switch v := value.(type) {
			case string:
				resolved := pm.resolveVariables(v, varEnv)
				result = append(result, resolved)
			case []any:
				for _, item := range v {
					if str, ok := item.(string); ok {
						resolved := pm.resolveVariables(str, varEnv)
						result = append(result, resolved)
					}
				}
			}
		}
		return result
	}

	// Process sections in inheritance order: common -> platform -> platform-arch
	sectionsToProcess := []string{"common", platform, platform + "-" + architecture}

	for _, sectionName := range sectionsToProcess {
		section, ok := packageData[sectionName].(map[string]any)
		if !ok {
			continue
		}

		pkg.IncludeDirs = append(pkg.IncludeDirs, extractStringList(section, "include_dirs")...)
		pkg.LibDirs = append(pkg.LibDirs, extractStringList(section, "lib_dirs")...)
		pkg.Libs = append(pkg.Libs, extractStringList(section, "libs")...)
		pkg.Frameworks = append(pkg.Frameworks, extractStringList(section, "frameworks")...)
		pkg.Defines = append(pkg.Defines, extractStringList(section, "defines")...)
		pkg.Sources = append(pkg.Sources, extractStringList(section, "sources")...)
		pkg.Packages = append(pkg.Packages, extractStringList(section, "packages")...)
	}
}

// resolveHierarchicalStringList resolves a field with platform/architecture hierarchy (legacy format)
func (pm *PackageManager) resolveHierarchicalStringList(data map[string]any, field, platform, architecture string, varEnv *VariableEnvironment) []string {
	result := []string{}

	fieldData, ok := data[field]
	if !ok {
		return result
	}

	// Helper to resolve and append strings
	appendStrings := func(value any) {
		switch v := value.(type) {
		case string:
			resolved := pm.resolveVariables(v, varEnv)
			result = append(result, resolved)
		case []any:
			for _, item := range v {
				if str, ok := item.(string); ok {
					resolved := pm.resolveVariables(str, varEnv)
					result = append(result, resolved)
				}
			}
		}
	}

	// Process based on type
	switch v := fieldData.(type) {
	case string:
		// Simple string value
		appendStrings(v)
	case []any:
		// Simple list
		appendStrings(v)
	case map[string]any:
		// Hierarchical structure
		// 1. Add root-level items (non-platform keys)
		for key, value := range v {
			if !isPlatformOrArchKey(key) {
				appendStrings(value)
			}
		}

		// 2. Add platform-specific items
		if platformData, ok := v[platform]; ok {
			appendStrings(platformData)
		}

		// 3. Add architecture-specific items
		if archData, ok := v[architecture]; ok {
			appendStrings(archData)
		}

		// 4. Add platform-architecture combined items
		combinedKey := platform + "-" + architecture
		if combinedData, ok := v[combinedKey]; ok {
			appendStrings(combinedData)
		}
	}

	return result
}

// isPlatformOrArchKey checks if a key is a known platform or architecture
func isPlatformOrArchKey(key string) bool {
	platforms := []string{"linux", "windows", "darwin", "macos", "android", "ios"}
	architectures := []string{"x86_64", "x86", "arm64", "arm", "aarch64", "i386"}
	
	// Check exact match
	for _, p := range platforms {
		if key == p {
			return true
		}
	}
	for _, a := range architectures {
		if key == a {
			return true
		}
	}
	
	// Check combined (e.g., "linux-x86_64")
	if strings.Contains(key, "-") {
		parts := strings.Split(key, "-")
		if len(parts) == 2 {
			for _, p := range platforms {
				for _, a := range architectures {
					if parts[0] == p && parts[1] == a {
						return true
					}
				}
			}
		}
	}
	
	return false
}

// resolveVariables resolves variables in a string using the variable environment
func (pm *PackageManager) resolveVariables(text string, varEnv *VariableEnvironment) string {
	errors := []string{}
	resolved := varEnv.ResolveString(text, &errors, 10)
	
	if len(errors) > 0 {
		log.Printf("WARNING: Variable resolution errors: %v", errors)
	}
	
	return resolved
}

// ResolvePackages resolves a list of package names and returns merged package settings
func (pm *PackageManager) ResolvePackages(packageNames []string, platform, architecture string, workspacePackages map[string]map[string]string) (*Package, error) {
	merged := &Package{
		Name:        "merged",
		IncludeDirs: []string{},
		LibDirs:     []string{},
		Libs:        []string{},
		Frameworks:  []string{},
		Defines:     []string{},
		Sources:     []string{},
		Packages:    []string{},
	}

	// Track loaded packages to handle transitive dependencies
	loaded := make(map[string]bool)
	toLoad := append([]string{}, packageNames...)

	for len(toLoad) > 0 {
		// Pop first package
		name := toLoad[0]
		toLoad = toLoad[1:]

		if loaded[name] {
			continue
		}

		// Get package variables from workspace config
		var packageVars map[string]string
		if workspacePackages != nil {
			packageVars = workspacePackages[name]
		}

		// Load package
		pkg, err := pm.LoadPackage(name, platform, architecture, packageVars)
		if err != nil {
			return nil, err
		}

		// Merge settings
		merged.IncludeDirs = append(merged.IncludeDirs, pkg.IncludeDirs...)
		merged.LibDirs = append(merged.LibDirs, pkg.LibDirs...)
		merged.Libs = append(merged.Libs, pkg.Libs...)
		merged.Frameworks = append(merged.Frameworks, pkg.Frameworks...)
		merged.Defines = append(merged.Defines, pkg.Defines...)
		merged.Sources = append(merged.Sources, pkg.Sources...)

		// Add transitive dependencies to load queue
		toLoad = append(toLoad, pkg.Packages...)

		loaded[name] = true
	}

	return merged, nil
}

