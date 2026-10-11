package config

import (
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Template is a build template: the steps that build a target of a language and type.
type Template struct {
	Name        string           `yaml:"-"`
	Description string           `yaml:"description"`
	Metadata    TemplateMetadata `yaml:"metadata,required"`
	Steps       []*Step          `yaml:"steps,required"`
	Source      string           `yaml:"-"` // the file it was loaded from
}

// TemplateMetadata says which targets a template builds and how buildy prepares them.
type TemplateMetadata struct {
	Language          string         `yaml:"language,required"`
	TargetTypes       []string       `yaml:"target_types,required"`
	ToolchainLanguage string         `yaml:"toolchain_language"` // default: language
	LibraryOutputDir  string         `yaml:"library_output_dir"` // default: bin
	ToolCached        bool           `yaml:"tool_cached"`        // the tool keeps its own cache: always run it
	PreProcessing     PreProcessing  `yaml:"pre_processing"`
	PostProcessing    PostProcessing `yaml:"post_processing"`
}

// PreProcessing says what buildy resolves before expanding the steps.
type PreProcessing struct {
	ResolveDeps        bool `yaml:"resolve_deps"`
	ResolveSources     bool `yaml:"resolve_sources"`
	ResolveIncludeDirs bool `yaml:"resolve_include_dirs"`
	ResolvePath        bool `yaml:"resolve_path"`
}

// PostProcessing says what buildy does with the expanded tasks.
type PostProcessing struct {
	RegisterTarget bool `yaml:"register_target"`
}

// Step is one step of a template.
type Step struct {
	Name       string         `yaml:"name,required"`
	Action     string         `yaml:"action,required"`
	ForEach    string         `yaml:"for_each"`
	Output     string         `yaml:"output"`
	Inputs     StringList     `yaml:"inputs"`
	DependsOn  StringList     `yaml:"depends_on"`
	OutputType string         `yaml:"output_type"`
	WorkingDir string         `yaml:"working_dir"`
	ToolParams Ordered[Param] `yaml:"tool_params"`
}

// Param is a tool parameter's value: a string, a list of strings or a boolean.
type Param struct {
	Value any // string, []string or bool
}

func (p *Param) decodeNode(d *decoder, n *yaml.Node, path string) {
	switch {
	case isNull(n):
		p.Value = []string{}
	case n.Kind == yaml.ScalarNode && n.ShortTag() == "!!bool":
		var b bool
		d.decode(n, reflect.ValueOf(&b).Elem(), path)
		p.Value = b
	case n.Kind == yaml.ScalarNode:
		if s, ok := d.scalarString(n, path); ok {
			p.Value = s
		}
	case n.Kind == yaml.SequenceNode:
		var list StringList
		list.decodeNode(d, n, path)
		p.Value = []string(list)
	default:
		d.errorf(n, "'%s' must be a string, a list of strings or a boolean, not %s", path, kindName(n))
	}
}

// LoadTemplates reads a template file: a mapping of template names to templates, in order.
func LoadTemplates(data []byte, source string) ([]*Template, error) {
	root, err := parse(data, source)
	if err != nil {
		return nil, err
	}
	d := newDecoder(source)
	var file struct {
		Templates Ordered[*Template] `yaml:"templates,required"`
	}
	d.decode(root, reflect.ValueOf(&file).Elem(), "")
	var templates []*Template
	for _, name := range file.Templates.Keys {
		t := file.Templates.Values[name]
		if t == nil {
			d.errorf(nil, "template '%s' is empty", name)
			continue
		}
		t.Name, t.Source = name, source
		t.validate(d)
		templates = append(templates, t)
	}
	if err := d.err("template file"); err != nil {
		return nil, err
	}
	return templates, nil
}

func (t *Template) validate(d *decoder) {
	where := "templates." + t.Name
	if t.Metadata.ToolchainLanguage == "" {
		t.Metadata.ToolchainLanguage = t.Metadata.Language
	}
	if t.Metadata.LibraryOutputDir == "" {
		t.Metadata.LibraryOutputDir = "bin"
	}
	for _, tt := range t.Metadata.TargetTypes {
		if !contains(outputTypes, tt) {
			d.errorf(nil, "%s.metadata.target_types: '%s' is not one of: %s", where, tt, strings.Join(outputTypes, ", "))
		}
	}
	if len(t.Steps) == 0 {
		d.errorf(nil, "%s has no steps", where)
	}
	for i, step := range t.Steps {
		if step == nil {
			d.errorf(nil, "%s.steps[%d] is empty", where, i)
			continue
		}
		if step.ForEach != "" && step.ForEach != "source" {
			d.errorf(nil, "%s.steps[%d].for_each '%s' is not supported (only 'source')", where, i, step.ForEach)
		}
		if !contains(toolActions, step.Action) {
			d.errorf(nil, "%s.steps[%d].action '%s' is not one of: %s", where, i, step.Action, strings.Join(toolActions, ", "))
		}
		if step.OutputType != "" && !contains(outputTypes, step.OutputType) {
			d.errorf(nil, "%s.steps[%d].output_type '%s' is not one of: %s", where, i, step.OutputType, strings.Join(outputTypes, ", "))
		}
	}
}
