package workspace

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// TaskIDGenerator generates unique task identifiers scoped to a module
type TaskIDGenerator struct {
	prefix   string
	counters map[string]int
}

// NewTaskIDGenerator creates a generator using the provided module path as prefix
func NewTaskIDGenerator(modulePath string) *TaskIDGenerator {
	cleanPath := strings.ReplaceAll(modulePath, string(filepath.Separator), "_")
	cleanPath = strings.ReplaceAll(cleanPath, "/", "_")
	cleanPath = strings.ReplaceAll(cleanPath, "\\", "_")
	prefix := sanitizeToken(cleanPath)
	if prefix == "" {
		prefix = "root"
	}

	return &TaskIDGenerator{
		prefix:   prefix,
		counters: make(map[string]int),
	}
}

// Next returns a unique task identifier given a task type and logical name
func (gen *TaskIDGenerator) Next(taskType, logicalName string) string {
	sanitizedType := sanitizeToken(taskType)
	if sanitizedType == "" {
		sanitizedType = "task"
	}
	sanitizedName := sanitizeToken(logicalName)

	key := sanitizedType + ":" + sanitizedName
	gen.counters[key]++
	count := gen.counters[key]

	base := sanitizedType
	if sanitizedName != "" {
		base = base + "_" + sanitizedName
	}

	id := fmt.Sprintf("%s_%03d", base, count)
	return fmt.Sprintf("%s__%s", id, gen.prefix)
}

func sanitizeToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, string(filepath.Separator), "_")
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, "\\", "_")
	value = nonAlphanumeric.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	return strings.ToLower(value)
}

// generatedNames is what a generation registers by name, workspace-wide: each target's final task
// and each named artifact, for libs, depends_on, copies, staging and --target. One generation owns
// one (ConfigParser.GenerateWorkspaceTasks); target and artifact names are unique across the
// workspace.
type generatedNames struct {
	targets   map[string]*BuildTask // target name -> its final task (link or build)
	artifacts map[string]artifact   // artifact name -> its module, outputs and producing tasks
}

// artifact is a named artifact of a module: generate, transform or copy.
type artifact struct {
	module  string
	outputs []string
	taskIDs []string
}

func newGeneratedNames() *generatedNames {
	return &generatedNames{targets: map[string]*BuildTask{}, artifacts: map[string]artifact{}}
}

// addArtifact records an artifact's outputs and producing tasks. Artifact names are unique across
// the workspace.
func (n *generatedNames) addArtifact(name, module string, outputs, taskIDs []string) error {
	if existing, ok := n.artifacts[name]; ok {
		if existing.module == module {
			return fmt.Errorf("artifact '%s' is defined twice in module %s; artifact names must be unique across the workspace", name, module)
		}
		return fmt.Errorf("artifact '%s' is defined in module %s and in module %s; artifact names must be unique across the workspace", name, existing.module, module)
	}
	n.artifacts[name] = artifact{module: module, outputs: outputs, taskIDs: taskIDs}
	return nil
}
