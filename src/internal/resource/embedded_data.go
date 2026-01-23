package resource

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
)

// Embed the entire data directory
// Note: embed paths must be relative to the package directory
// We'll need to copy or symlink the data directory into goBuildy
//go:embed data
var embeddedData embed.FS

// GetEmbeddedFile reads a file from the embedded data directory
func GetEmbeddedFile(path string) ([]byte, error) {
	fullPath := "data/" + path
	return embeddedData.ReadFile(fullPath)
}

// ListEmbeddedFiles lists all files in a directory within the embedded data
func ListEmbeddedFiles(dir string) ([]string, error) {
	fullPath := "data/" + dir
	var files []string
	
	err := fs.WalkDir(embeddedData, fullPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			// Remove the "data/" prefix from the path
			relativePath := path[len("data/"):]
			files = append(files, relativePath)
		}
		return nil
	})
	
	return files, err
}

// VerifyEmbeddedData checks that the embedded data is accessible
func VerifyEmbeddedData() error {
	// Check for templates
	templates, err := ListEmbeddedFiles("templates")
	if err != nil {
		return fmt.Errorf("failed to list templates: %w", err)
	}
	log.Printf("Found %d template file(s)", len(templates))
	
	// Check for toolchains
	toolchains, err := ListEmbeddedFiles("toolchains")
	if err != nil {
		return fmt.Errorf("failed to list toolchains: %w", err)
	}
	log.Printf("Found %d toolchain file(s)", len(toolchains))
	
	// Check for build systems
	buildSystems, err := ListEmbeddedFiles("build_systems")
	if err != nil {
		return fmt.Errorf("failed to list build systems: %w", err)
	}
	log.Printf("Found %d build system file(s)", len(buildSystems))
	
	// Verify we can read at least one template file
	if len(templates) == 0 {
		return fmt.Errorf("no template files found in embedded data")
	}
	
	// Verify we can read at least one toolchain file
	if len(toolchains) == 0 {
		return fmt.Errorf("no toolchain files found in embedded data")
	}
	
	// Verify we can read at least one build system file
	if len(buildSystems) == 0 {
		return fmt.Errorf("no build system files found in embedded data")
	}
	
	log.Println("Successfully verified embedded data access")
	
	return nil
}

