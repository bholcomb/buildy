package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Settings are a module's compile and link settings for one build, resolved: its levels merged,
// filtered lists resolved, abstract keywords turned into flags and removals applied.
type Settings struct {
	CppStandard  string
	CStandard    string
	OutputPrefix *string
	Defines      []string
	Flags        []string
	LinkFlags    []string
}

// builtinConfiguration is one of buildy's own configurations: abstract keywords that apply where
// no level of a config sets them, and defines that come first.
type builtinConfiguration struct {
	keywords map[string]Keyword
	defines  []string
}

// builtinConfigurations are buildy's debug and release configurations, the lowest level of every
// config: what a config sets anywhere (environment.compile, configurations.<name>, ...) wins, and
// its defines of the same macro replace these. A toolchain whose flag_mappings lack a keyword's
// value here gets nothing for it.
var builtinConfigurations = map[string]builtinConfiguration{
	"debug":   {keywords: map[string]Keyword{"optimization": "none", "symbols": "true"}},
	"release": {keywords: map[string]Keyword{"optimization": "speed"}, defines: []string{"NDEBUG"}},
}

// flat is one level's settings with its compile: section merged into it.
type flat struct {
	Compile CompileSettings
	Link    LinkSettings
}

func flatten(l Level) flat {
	return flat{Compile: Merge(l.CompileSettings, l.Compile), Link: l.Link}
}

// merged merges a config's levels for a build, in order: buildy's built-in configuration's defines,
// the config section, environment.compile and environment.link, the platform's section, the
// architecture's, then the configuration's (configurations, then environment.configurations).
// Each level's compile: section merges into its own settings first. The file is a module's config
// with the root's merged in (Inherit).
func (f *File) merged(ctx Context) flat {
	layers := []flat{
		{Compile: CompileSettings{Defines: Plain(builtinConfigurations[ctx.Configuration].defines...)}},
		{Compile: f.Config},
		{Compile: f.Environment.Compile, Link: f.Environment.Link},
	}
	if p := f.Platforms[ctx.Platform]; p != nil {
		layers = append(layers, flatten(p.Level))
	}
	if a := f.Architectures[ctx.Architecture]; a != nil {
		layers = append(layers, flatten(*a))
	}
	for _, levels := range []map[string]*Level{f.Configurations, f.Environment.Configurations} {
		if c := levels[ctx.Configuration]; c != nil {
			layers = append(layers, flatten(*c))
		}
	}
	var result flat
	for _, l := range layers {
		result = Merge(result, l)
	}
	return result
}

// Settings resolves the config's compile and link settings for a build with a toolchain's
// flag_mappings. The flags of the abstract keywords (sorted by keyword) come first; remove_flags
// and remove_defines apply last, so they remove flags from any level, keywords included.
func (f *File) Settings(ctx Context, mappings Ordered[Ordered[[]string]]) (Settings, error) {
	m := f.merged(ctx)
	s := Settings{
		CppStandard:  m.Compile.CppStandard,
		CStandard:    m.Compile.CStandard,
		OutputPrefix: m.Compile.OutputPrefix,
		Defines:      m.Compile.Defines.Resolve(ctx),
		Flags:        m.Compile.Flags.Resolve(ctx),
		LinkFlags:    m.Link.Flags.Resolve(ctx),
	}

	keywordFlags, err := keywordFlags(m.Compile.Keywords, builtinConfigurations[ctx.Configuration].keywords, mappings)
	if err != nil {
		return Settings{}, err
	}
	s.Flags = append(keywordFlags, s.Flags...)

	s.Flags = Remove(s.Flags, m.Compile.RemoveFlags.Resolve(ctx))
	s.Defines = UniqueDefines(Remove(s.Defines, m.Compile.RemoveDefines.Resolve(ctx)))
	s.LinkFlags = Remove(s.LinkFlags, m.Link.RemoveFlags.Resolve(ctx))
	return s, nil
}

// KeywordFlags returns the flags of the config's abstract keywords for a build as a toolchain's
// flag_mappings map them: those of a transform or generate tool's toolchain (optimization: speed
// is -O for glslc). Keywords the mappings don't have are skipped.
func (f *File) KeywordFlags(ctx Context, mappings Ordered[Ordered[[]string]]) ([]string, error) {
	return keywordFlags(f.merged(ctx).Compile.Keywords, builtinConfigurations[ctx.Configuration].keywords, mappings)
}

// keywordFlags maps keywords through mappings, sorted by keyword: those set, else the built-in
// configuration's defaults the mappings have a value for. A value set that the mappings don't
// have for a keyword they map is an error.
func keywordFlags(set, defaults map[string]Keyword, mappings Ordered[Ordered[[]string]]) ([]string, error) {
	keywords := append([]string{}, mappings.Keys...)
	sort.Strings(keywords)
	var result []string
	for _, keyword := range keywords {
		values := mappings.Values[keyword]
		value, ok := set[keyword]
		if !ok {
			if value, ok = defaults[keyword]; !ok {
				continue
			}
			if _, mapped := values.Get(string(value)); !mapped {
				continue
			}
		}
		flags, ok := values.Get(string(value))
		if !ok {
			valid := append([]string{}, values.Keys...)
			sort.Strings(valid)
			return nil, fmt.Errorf("abstract keyword %q has unresolvable value %q; valid values: [%s]",
				keyword, value, strings.Join(valid, ", "))
		}
		result = append(result, flags...)
	}
	return result, nil
}

// UniqueDefines returns defines with each macro defined once, by its last definition, where that
// one is: the compiler's rule (a later -D of a macro replaces an earlier one), without the
// redefinitions. NDEBUG and NDEBUG=1 define the same macro, NDEBUG; so do F(x)=x and F.
func UniqueDefines(defines []string) []string {
	name := func(define string) string {
		if i := strings.IndexAny(define, "=("); i >= 0 {
			return define[:i]
		}
		return define
	}
	last := map[string]int{}
	for i, d := range defines {
		last[name(d)] = i
	}
	result := make([]string, 0, len(last))
	for i, d := range defines {
		if last[name(d)] == i {
			result = append(result, d)
		}
	}
	return result
}

// Remove returns list without the items in removals.
func Remove(list, removals []string) []string {
	if len(removals) == 0 {
		return list
	}
	remove := toSet(removals)
	result := make([]string, 0, len(list))
	for _, item := range list {
		if !remove[item] {
			result = append(result, item)
		}
	}
	return result
}

// CheckKeywords reports every abstract keyword that no toolchain maps: a key of a settings level
// that is neither a field nor a keyword is almost always a typo.
func CheckKeywords(uses []NameUse, keywords []string) []string {
	fields, _ := structFields(reflect.TypeOf(Level{}))
	known := append(sortedNames(fields), keywords...)
	sort.Strings(known)
	var problems []string
	for _, use := range uses {
		if contains(keywords, use.Name) {
			continue
		}
		msg := fmt.Sprintf("%s: unknown key '%s'", use.Pos, use.Path)
		if near := ClosestName(use.Name, known); near != "" {
			msg += fmt.Sprintf(" (did you mean '%s'?)", near)
		} else {
			msg += fmt.Sprintf(" (not a setting, nor an abstract keyword of any toolchain: %s)", strings.Join(keywords, ", "))
		}
		problems = append(problems, msg)
	}
	return problems
}

// WithOverride returns the target with a platform's override of it applied: each field the
// override sets replaces the target's, except compile and link, which merge into the target's
// (lists append). Neither target changes.
func (t *Target) WithOverride(o *Target) *Target {
	result := Merge(*t, Target{}) // a copy
	rv, ov := reflect.ValueOf(&result).Elem(), reflect.ValueOf(o).Elem()
	for i := 0; i < rv.NumField(); i++ {
		switch rv.Type().Field(i).Name {
		case "Pos", "Name":
		case "Compile":
			result.Compile = Merge(t.Compile, o.Compile)
		case "Link":
			result.Link = Merge(t.Link, o.Link)
		default:
			if !ov.Field(i).IsZero() {
				rv.Field(i).Set(copyValue(ov.Field(i)))
			}
		}
	}
	return &result
}
