package resource

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"buildy/pkg/util"

	"gopkg.in/yaml.v3"
)

// BuildSystemConfig represents a build system definition loaded from YAML
type BuildSystemConfig struct {
	Name        string                    `yaml:"name"`
	Description string                    `yaml:"description"`
	Detection   DetectionConfig           `yaml:"detection"`
	Phases      map[string]PhaseConfig    `yaml:"phases"`
	Output      OutputConfig              `yaml:"output"`
}

// DetectionConfig defines how to detect this build system
type DetectionConfig struct {
	MarkerFiles []string `yaml:"marker_files"`
	Priority    int      `yaml:"priority"`
}

// PhaseConfig defines a build phase (configure, build, test, install, clean)
type PhaseConfig struct {
	Command     string   `yaml:"command"`
	DefaultArgs []string `yaml:"default_args"`
	Required    bool     `yaml:"required"`
	WorkingDir  string   `yaml:"working_dir"` // defaults to source_dir
}

// OutputConfig defines where to find build outputs
type OutputConfig struct {
	IncludePatterns []string `yaml:"include_patterns"`
	LibPatterns     []string `yaml:"lib_patterns"`
}

// BuildSystemManager manages build system detection and execution
type BuildSystemManager struct {
	systems map[string]*BuildSystemConfig
}

// NewBuildSystemManager creates a new BuildSystemManager and loads embedded systems
func NewBuildSystemManager() (*BuildSystemManager, error) {
	bsm := &BuildSystemManager{
		systems: make(map[string]*BuildSystemConfig),
	}

	// Load embedded build systems first
	if err := bsm.loadEmbeddedSystems(); err != nil {
		log.Printf("WARNING: Failed to load embedded build systems: %v", err)
	}

	return bsm, nil
}

// loadEmbeddedSystems loads all build system definitions from embedded data
func (bsm *BuildSystemManager) loadEmbeddedSystems() error {
	files, err := ListEmbeddedFiles("build_systems")
	if err != nil {
		return fmt.Errorf("failed to list embedded build systems: %w", err)
	}

	for _, filePath := range files {
		if !strings.HasSuffix(filePath, ".yaml") {
			continue
		}

		data, err := GetEmbeddedFile(filePath)
		if err != nil {
			log.Printf("WARNING: Failed to read embedded build system %s: %v", filePath, err)
			continue
		}

		config, err := parseBuildSystemConfig(data, filePath)
		if err != nil {
			log.Printf("WARNING: Failed to parse embedded build system %s: %v", filePath, err)
			continue
		}

		bsm.systems[config.Name] = config
		log.Printf("Loaded embedded build system: %s - %s", config.Name, config.Description)
	}

	return nil
}

// LoadFilesystemSystems loads build system definitions from filesystem directories
func (bsm *BuildSystemManager) LoadFilesystemSystems(dirs []string) error {
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			log.Printf("WARNING: Failed to read build systems directory %s: %v", dir, err)
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}

			filePath := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(filePath)
			if err != nil {
				log.Printf("WARNING: Failed to read build system file %s: %v", filePath, err)
				continue
			}

			config, err := parseBuildSystemConfig(data, filePath)
			if err != nil {
				log.Printf("WARNING: Failed to parse build system file %s: %v", filePath, err)
				continue
			}

			if _, exists := bsm.systems[config.Name]; exists {
				log.Printf("Build system '%s' from %s overrides existing", config.Name, dir)
			}

			bsm.systems[config.Name] = config
			log.Printf("Loaded build system: %s - %s", config.Name, config.Description)
		}
	}

	return nil
}

// parseBuildSystemConfig parses a build system YAML file
func parseBuildSystemConfig(data []byte, sourceName string) (*BuildSystemConfig, error) {
	var rawConfig map[string]any
	if err := yaml.Unmarshal(data, &rawConfig); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	bsData, ok := rawConfig["build_system"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing 'build_system' section in %s", sourceName)
	}

	config := &BuildSystemConfig{
		Phases: make(map[string]PhaseConfig),
	}

	// Parse basic fields
	if name, ok := bsData["name"].(string); ok {
		config.Name = name
	} else {
		return nil, fmt.Errorf("build system missing 'name' field")
	}

	if desc, ok := bsData["description"].(string); ok {
		config.Description = desc
	}

	// Parse detection
	if detData, ok := bsData["detection"].(map[string]any); ok {
		if markers, ok := detData["marker_files"].([]any); ok {
			for _, m := range markers {
				if s, ok := m.(string); ok {
					config.Detection.MarkerFiles = append(config.Detection.MarkerFiles, s)
				}
			}
		}
		if priority, ok := detData["priority"].(int); ok {
			config.Detection.Priority = priority
		}
	}

	// Parse phases
	if phasesData, ok := bsData["phases"].(map[string]any); ok {
		for phaseName, phaseDataRaw := range phasesData {
			phaseData, ok := phaseDataRaw.(map[string]any)
			if !ok {
				continue
			}

			phase := PhaseConfig{}
			if cmd, ok := phaseData["command"].(string); ok {
				phase.Command = cmd
			}
			if wd, ok := phaseData["working_dir"].(string); ok {
				phase.WorkingDir = wd
			}
			if req, ok := phaseData["required"].(bool); ok {
				phase.Required = req
			}
			if args, ok := phaseData["default_args"].([]any); ok {
				for _, a := range args {
					if s, ok := a.(string); ok {
						phase.DefaultArgs = append(phase.DefaultArgs, s)
					}
				}
			}

			config.Phases[phaseName] = phase
		}
	}

	// Parse output
	if outputData, ok := bsData["output"].(map[string]any); ok {
		if patterns, ok := outputData["include_patterns"].([]any); ok {
			for _, p := range patterns {
				if s, ok := p.(string); ok {
					config.Output.IncludePatterns = append(config.Output.IncludePatterns, s)
				}
			}
		}
		if patterns, ok := outputData["lib_patterns"].([]any); ok {
			for _, p := range patterns {
				if s, ok := p.(string); ok {
					config.Output.LibPatterns = append(config.Output.LibPatterns, s)
				}
			}
		}
	}

	return config, nil
}

// Detect detects the build system used in a directory
func (bsm *BuildSystemManager) Detect(dir string) *BuildSystemConfig {
	// Collect all matching build systems
	type match struct {
		config   *BuildSystemConfig
		priority int
	}
	var matches []match

	for _, config := range bsm.systems {
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

	// Sort by priority (highest first)
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].priority > matches[j].priority
	})

	return matches[0].config
}

// GetBuildSystem returns a build system by name
func (bsm *BuildSystemManager) GetBuildSystem(name string) *BuildSystemConfig {
	return bsm.systems[name]
}

// Execute executes specified phases of a build system
func (bsm *BuildSystemManager) Execute(
	config *BuildSystemConfig,
	sourceDir, buildDir, installDir string,
	execEnv *util.ExecutionEnvironment,
	phases []string,
	extraArgs []string,
) error {
	// Get number of parallel jobs
	jobs := runtime.NumCPU()

	// If no phases specified, run required phases in order
	if len(phases) == 0 {
		phases = []string{"configure", "build"}
	}

	// Execute each phase in order
	for _, phaseName := range phases {
		phase, ok := config.Phases[phaseName]
		if !ok {
			if phaseName == "configure" || phaseName == "build" {
				return fmt.Errorf("required phase '%s' not defined for build system '%s'", phaseName, config.Name)
			}
			log.Printf("Skipping undefined phase '%s' for build system '%s'", phaseName, config.Name)
			continue
		}

		// Skip no-op commands
		if phase.Command == "true" || phase.Command == "" {
			continue
		}

		// Build the command with variable substitution
		cmd := bsm.substituteVariables(phase.Command, sourceDir, buildDir, installDir, jobs, extraArgs, phase.DefaultArgs)

		// Determine working directory
		workDir := sourceDir
		if phase.WorkingDir != "" {
			workDir = bsm.substituteVariables(phase.WorkingDir, sourceDir, buildDir, installDir, jobs, nil, nil)
		}

		log.Printf("Executing %s phase: %s", phaseName, cmd)

		// Execute the command
		result, err := execEnv.Execute(cmd, workDir, 600) // 10 minute timeout
		if err != nil {
			return fmt.Errorf("phase '%s' failed: %w", phaseName, err)
		}

		if result.ReturnCode != 0 {
			return fmt.Errorf("phase '%s' failed with exit code %d: %s", phaseName, result.ReturnCode, result.Stderr)
		}

		log.Printf("Phase '%s' completed successfully", phaseName)
	}

	return nil
}

// substituteVariables replaces placeholders in a command string
func (bsm *BuildSystemManager) substituteVariables(
	template string,
	sourceDir, buildDir, installDir string,
	jobs int,
	extraArgs, defaultArgs []string,
) string {
	// Combine default args with extra args
	allArgs := append(defaultArgs, extraArgs...)
	argsStr := strings.Join(allArgs, " ")

	result := template
	result = strings.ReplaceAll(result, "${source_dir}", sourceDir)
	result = strings.ReplaceAll(result, "${build_dir}", buildDir)
	result = strings.ReplaceAll(result, "${install_dir}", installDir)
	result = strings.ReplaceAll(result, "${jobs}", fmt.Sprintf("%d", jobs))
	result = strings.ReplaceAll(result, "${args}", argsStr)

	return result
}

// GetOutputPaths returns the resolved include and lib directories for a built dependency
func (bsm *BuildSystemManager) GetOutputPaths(
	config *BuildSystemConfig,
	sourceDir, buildDir, installDir string,
) (includeDirs, libDirs []string) {
	jobs := 1 // Not used for path substitution

	for _, pattern := range config.Output.IncludePatterns {
		resolved := bsm.substituteVariables(pattern, sourceDir, buildDir, installDir, jobs, nil, nil)
		if _, err := os.Stat(resolved); err == nil {
			includeDirs = append(includeDirs, resolved)
		}
	}

	for _, pattern := range config.Output.LibPatterns {
		resolved := bsm.substituteVariables(pattern, sourceDir, buildDir, installDir, jobs, nil, nil)
		if _, err := os.Stat(resolved); err == nil {
			libDirs = append(libDirs, resolved)
		}
	}

	return includeDirs, libDirs
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
