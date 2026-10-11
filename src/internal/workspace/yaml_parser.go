package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// loadConfigFile reads and validates a buildy.yaml: the workspace root (root true) or a module.
// It is the only way configuration is read. yaml.v3 rejects duplicate keys; ValidateConfig
// rejects unknown keys and missing required fields. Every error names the file.
func loadConfigFile(path string, root bool) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("invalid YAML in %s: %w", path, err)
	}
	if config == nil {
		config = map[string]any{}
	}
	if problems := NewYAMLParser().ValidateConfig(config, root); len(problems) > 0 {
		return nil, fmt.Errorf("%s is not a valid buildy config:\n  - %s", path, strings.Join(problems, "\n  - "))
	}
	return config, nil
}

// keySet is the set of keys a mapping may have.
type keySet map[string]bool

func keys(names ...string) keySet {
	set := keySet{}
	for _, n := range names {
		set[n] = true
	}
	return set
}

// The schema: the keys the generator reads. A key outside it would be ignored, so it is an error.
var (
	knownTopLevelKeys = keys("project", "variables", "workspace", "dependencies", "environment",
		"targets", "artifacts", "staging", "install", "output", "toolchain",
		"platforms", "architectures", "configurations", "config")
	knownProjectKeys     = keys("name", "version", "description", "toolchain")
	knownWorkspaceKeys   = keys("modules", "discover", "exclude")
	knownDependenciesKey = keys("file")
	knownEnvironmentKeys = keys("toolchains", "compile", "link", "configurations")
	knownOutputKeys      = keys("base_dir", "pattern")
	knownTargetsKeys     = keys("static_libraries", "shared_libraries", "executables")
	knownTargetKeys      = keys("name", "type", "language", "sources", "path",
		"include_dirs", "lib_dirs", "libs", "frameworks", "deps", "depends_on",
		"defines", "flags", "remove_flags", "remove_defines", "compile", "link", "output_prefix",
		// Go
		"build_tags", "ldflags", "gcflags", "buildmode",
		// Rust
		"features", "bin")
	knownDependsOnKeys    = keys("targets", "artifacts")
	knownTargetCompileKey = keys("flags", "remove_flags", "defines")
	knownTargetLinkKeys   = keys("flags", "remove_flags")
	knownArtifactsKeys    = keys("copy", "transform", "generate")
	knownTransformKeys    = keys("name", "tool", "inputs", "outputs", "args")
	knownGenerateKeys     = keys("name", "template", "output", "variables", "tool", "inputs", "outputs", "args")
	knownCopyKeys         = keys("name", "sources", "source", "target", "destination", "dest", "depends_on")
	knownStagingKeys      = keys("name", "destination", "use_symlinks", "contents")
	knownStagingEntryKeys = keys("folder", "targets", "artifacts", "files", "contents")
	knownInstallKeys      = keys("name", "staging", "destination", "format", "follow_symlinks", "filename")
)

// sectionsReplacedBy names the legacy sections the generator never built, with what replaces them.
var sectionsReplacedBy = map[string]string{
	"library":    "targets.static_libraries or targets.shared_libraries",
	"executable": "targets.executables",
	"tasks":      "targets",
}

// YAMLParser validates parsed configuration.
type YAMLParser struct{}

// NewYAMLParser creates a new YAMLParser
func NewYAMLParser() *YAMLParser {
	return &YAMLParser{}
}

// ValidateConfig checks a parsed buildy.yaml against the schema and returns every problem.
// root is true for the workspace's root config: it alone may have a dependencies section, and it
// must name the project.
func (yp *YAMLParser) ValidateConfig(config map[string]any, root bool) []string {
	v := &validator{}

	for _, key := range sortedKeys(config) {
		if replacement, ok := sectionsReplacedBy[key]; ok {
			v.add("'%s' is not supported: define targets under %s", key, replacement)
			continue
		}
		if !knownTopLevelKeys[key] {
			v.unknown("", key, knownTopLevelKeys)
		}
	}

	if project, ok := v.mapping(config, "project", ""); ok {
		v.keys(project, "project", knownProjectKeys)
		for _, field := range []string{"name", "version", "description", "toolchain"} {
			v.optionalString(project, field, "project")
		}
		if name, _ := project["name"].(string); strings.TrimSpace(name) == "" && root {
			v.add("project.name is required in the workspace's root config")
		}
	} else if root && config["project"] == nil {
		v.add("the workspace's root config needs a project section with a name")
	}

	if workspace, ok := v.mapping(config, "workspace", ""); ok {
		v.keys(workspace, "workspace", knownWorkspaceKeys)
	}

	if deps, ok := v.mapping(config, "dependencies", ""); ok {
		if !root {
			v.add("dependencies: only the workspace's root config declares dependencies (in buildy_config/)")
		}
		v.keys(deps, "dependencies", knownDependenciesKey)
		v.optionalString(deps, "file", "dependencies")
	}

	if env, ok := v.mapping(config, "environment", ""); ok {
		v.keys(env, "environment", knownEnvironmentKeys)
		v.mapping(env, "toolchains", "environment")
		v.mapping(env, "compile", "environment")
		v.mapping(env, "link", "environment")
		if configs, ok := v.mapping(env, "configurations", "environment"); ok {
			for _, name := range sortedKeys(configs) {
				v.mapping(configs, name, "environment.configurations")
			}
		}
	}

	if output, ok := v.mapping(config, "output", ""); ok {
		v.keys(output, "output", knownOutputKeys)
		if baseDir, ok := output["base_dir"].(string); ok && (strings.Contains(baseDir, "..") || filepath.IsAbs(baseDir)) {
			v.add("output.base_dir '%s' must be relative and not contain '..'", baseDir)
		}
	}

	v.optionalString(config, "toolchain", "")
	for _, section := range []string{"platforms", "architectures", "configurations", "config", "variables"} {
		v.mapping(config, section, "")
	}

	if targets, ok := v.mapping(config, "targets", ""); ok {
		v.keys(targets, "targets", knownTargetsKeys)
		for _, section := range []string{"static_libraries", "shared_libraries", "executables"} {
			for i, target := range v.list(targets, section, "targets") {
				v.target(target, fmt.Sprintf("targets.%s[%d]", section, i))
			}
		}
	}

	if artifacts, ok := v.mapping(config, "artifacts", ""); ok {
		v.keys(artifacts, "artifacts", knownArtifactsKeys)
		for i, item := range v.list(artifacts, "transform", "artifacts") {
			where := fmt.Sprintf("artifacts.transform[%d]", i)
			v.keys(item, where, knownTransformKeys)
			v.required(item, where, "tool", "inputs", "outputs")
		}
		for i, item := range v.list(artifacts, "generate", "artifacts") {
			where := fmt.Sprintf("artifacts.generate[%d]", i)
			v.keys(item, where, knownGenerateKeys)
			if _, ok := item["template"]; ok {
				v.required(item, where, "output")
			} else if _, ok := item["tool"]; ok {
				v.required(item, where, "inputs", "outputs")
			} else {
				v.add("%s needs either 'template' (with 'output') or 'tool' (with 'inputs' and 'outputs')", where)
			}
		}
		for i, item := range v.list(artifacts, "copy", "artifacts") {
			where := fmt.Sprintf("artifacts.copy[%d]", i)
			v.keys(item, where, knownCopyKeys)
			_, sources := item["sources"]
			_, source := item["source"]
			_, target := item["target"]
			_, destination := item["destination"]
			_, dest := item["dest"]
			if !sources && !source && !target {
				v.add("%s needs 'sources', 'source' or 'target'", where)
			}
			if !destination && !dest {
				v.add("%s needs 'destination'", where)
			}
		}
	}

	if staging, ok := v.mapping(config, "staging", ""); ok {
		v.keys(staging, "staging", knownStagingKeys)
		v.stagingContents(staging, "staging")
	}

	for i, item := range v.list(config, "install", "") {
		where := fmt.Sprintf("install[%d]", i)
		v.keys(item, where, knownInstallKeys)
		v.required(item, where, "name")
	}

	return v.problems
}

// validator collects schema problems.
type validator struct {
	problems []string
}

func (v *validator) add(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

func join(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// unknown reports a key outside the schema, suggesting the closest known key.
func (v *validator) unknown(parent, key string, known keySet) {
	if near := closestKey(key, known); near != "" {
		v.add("unknown key '%s' (did you mean '%s'?)", join(parent, key), near)
		return
	}
	v.add("unknown key '%s' (expected one of: %s)", join(parent, key), strings.Join(sortedKeys(known), ", "))
}

// keys reports every key of m outside known.
func (v *validator) keys(m map[string]any, where string, known keySet) {
	for _, key := range sortedKeys(m) {
		if !known[key] {
			v.unknown(where, key, known)
		}
	}
}

// mapping returns m[key] if it is a mapping. It reports a value of another type; a missing key
// is not a problem.
func (v *validator) mapping(m map[string]any, key, where string) (map[string]any, bool) {
	value, ok := m[key]
	if !ok || value == nil {
		return nil, false
	}
	section, ok := value.(map[string]any)
	if !ok {
		v.add("'%s' must be a mapping", join(where, key))
	}
	return section, ok
}

// list returns the mappings in the list m[key], reporting a value that isn't a list of mappings.
func (v *validator) list(m map[string]any, key, where string) []map[string]any {
	value, ok := m[key]
	if !ok || value == nil {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		v.add("'%s' must be a list", join(where, key))
		return nil
	}
	var result []map[string]any
	for i, item := range items {
		mapping, ok := item.(map[string]any)
		if !ok {
			v.add("%s[%d] must be a mapping", join(where, key), i)
			continue
		}
		result = append(result, mapping)
	}
	return result
}

func (v *validator) optionalString(m map[string]any, key, where string) {
	if value, ok := m[key]; ok && value != nil {
		if _, ok := value.(string); !ok {
			v.add("'%s' must be a string", join(where, key))
		}
	}
}

func (v *validator) required(m map[string]any, where string, fields ...string) {
	for _, field := range fields {
		if value, ok := m[field]; !ok || value == nil {
			v.add("%s is missing required '%s'", where, field)
		}
	}
}

// target checks a target: C and C++ targets list sources; Go and Rust targets name the path of
// their package or crate.
func (v *validator) target(target map[string]any, where string) {
	name, _ := target["name"].(string)
	if name == "" {
		v.add("%s is missing required 'name'", where)
	} else {
		where = fmt.Sprintf("%s (%s)", where, name)
	}
	v.keys(target, where, knownTargetKeys)

	language, ok := target["language"].(string)
	if !ok || language == "" {
		v.add("%s is missing required 'language'", where)
	}
	switch language {
	case "c", "cpp":
		v.required(target, where, "sources")
	case "go", "rust":
		v.required(target, where, "path")
	}

	if dependsOn, ok := target["depends_on"]; ok {
		switch d := dependsOn.(type) {
		case map[string]any:
			v.keys(d, where+".depends_on", knownDependsOnKeys)
		case []any:
		default:
			v.add("%s.depends_on must be a mapping (targets, artifacts) or a list of targets", where)
		}
	}
	if compile, ok := v.mapping(target, "compile", where); ok {
		v.keys(compile, where+".compile", knownTargetCompileKey)
	}
	if link, ok := v.mapping(target, "link", where); ok {
		v.keys(link, where+".link", knownTargetLinkKeys)
	}
}

// stagingContents checks the nested folder entries of a staging section.
func (v *validator) stagingContents(m map[string]any, where string) {
	for i, entry := range v.list(m, "contents", where) {
		entryWhere := fmt.Sprintf("%s.contents[%d]", where, i)
		v.keys(entry, entryWhere, knownStagingEntryKeys)
		v.stagingContents(entry, entryWhere)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	result := make([]string, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// closestKey returns the known key within two edits of key, if there is one.
func closestKey(key string, known keySet) string {
	best, bestDistance := "", 3
	for _, candidate := range sortedKeys(known) {
		if d := editDistance(key, candidate); d < bestDistance {
			best, bestDistance = candidate, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// ExtractSection extracts a typed section from config
func (yp *YAMLParser) ExtractSection(config map[string]any, key string) map[string]any {
	if section, ok := config[key].(map[string]any); ok {
		return section
	}
	return make(map[string]any)
}
