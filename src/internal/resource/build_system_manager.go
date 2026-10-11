package resource

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"buildy/internal/config"
	"buildy/pkg/util"
)

// BuildSystemConfig is a build system definition (config.BuildSystem).
type BuildSystemConfig = config.BuildSystem

// BuildSystemManager manages build system detection and execution
type BuildSystemManager struct {
	systems map[string]*BuildSystemConfig
}

// NewBuildSystemManager loads the built-in build systems. Every one must load.
func NewBuildSystemManager() (*BuildSystemManager, error) {
	bsm := &BuildSystemManager{
		systems: make(map[string]*BuildSystemConfig),
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

// ExecuteWithContext runs phases of a build system, with additional variables. ctx stops it (an
// interrupt), and each phase may run for timeout (0: no limit).
func (bsm *BuildSystemManager) ExecuteWithContext(
	ctx context.Context,
	bsConfig *BuildSystemConfig,
	sourceDir, buildDir, installDir string,
	execSpec util.ExecSpec,
	mounts []string,
	phases []string,
	extraArgs []string,
	variables map[string]string,
	timeout time.Duration,
) error {
	// Get number of parallel jobs
	jobs := runtime.NumCPU()

	// If no phases specified, run required phases in order
	if len(phases) == 0 {
		phases = []string{"configure", "build"}
	}

	// Execute each phase in order
	for _, phaseName := range phases {
		phase := bsConfig.Phases[phaseName]
		if phase == nil {
			if phaseName == "configure" || phaseName == "build" {
				return fmt.Errorf("required phase '%s' not defined for build system '%s'", phaseName, bsConfig.Name)
			}
			util.LogInfo("Skipping undefined phase '%s' for build system '%s'", phaseName, bsConfig.Name)
			continue
		}

		// "true" is the no-op of a phase a build system doesn't need (make's configure)
		if phase.Command.Text == "true" {
			continue
		}

		// Determine working directory
		workDir := sourceDir
		if phase.WorkingDir != "" {
			workDir = bsm.substituteVariablesWithContext(phase.WorkingDir, sourceDir, buildDir, installDir, jobs, nil, nil, variables)
		}

		values := map[string][]string{
			"source_dir":  {sourceDir},
			"build_dir":   {buildDir},
			"install_dir": {installDir},
			"jobs":        {fmt.Sprint(jobs)},
			"args":        append(append([]string{}, phase.DefaultArgs...), extraArgs...),
		}
		for key, value := range variables {
			values[key] = []string{value}
		}
		args, err := phase.Command.Expand(config.MapValues(values))
		if err != nil {
			return fmt.Errorf("the %s phase of build system %s: %w", phaseName, bsConfig.Name, err)
		}
		cmd := util.Command{Args: args, Dir: workDir}

		util.LogInfo("Executing %s phase: %s", phaseName, cmd)

		// Execute the command
		result, err := util.Run(ctx, execSpec, cmd, workDir, mounts, timeout)
		if err != nil {
			return fmt.Errorf("phase '%s' failed: %w", phaseName, err)
		}

		if result.ReturnCode != 0 {
			return fmt.Errorf("phase '%s' failed with exit code %d:\n%s", phaseName, result.ReturnCode, result.Output)
		}

		util.LogInfo("Phase '%s' completed successfully", phaseName)
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
	return bsm.substituteVariablesWithContext(template, sourceDir, buildDir, installDir, jobs, extraArgs, defaultArgs, nil)
}

// substituteVariablesWithContext replaces placeholders with additional context variables
func (bsm *BuildSystemManager) substituteVariablesWithContext(
	template string,
	sourceDir, buildDir, installDir string,
	jobs int,
	extraArgs, defaultArgs []string,
	context map[string]string,
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

	// Apply additional context variables, in name order
	keys := make([]string, 0, len(context))
	for key := range context {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = strings.ReplaceAll(result, "${"+key+"}", context[key])
	}

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
