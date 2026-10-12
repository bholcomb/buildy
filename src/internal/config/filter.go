package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Context is the build a filtered list is resolved for.
type Context struct {
	Platform      string
	Architecture  string
	Configuration string
	Toolchain     string
}

// Matches reports whether a filter key selects this build.
func (c Context) Matches(key string) bool {
	return key == c.Platform || key == c.Architecture || key == c.Configuration ||
		(c.Toolchain != "" && key == c.Toolchain)
}

// Filtered is a filtered list: plain items are always included, and a filter map
// ({linux: [...], debug: [...]}) includes the items of each key that matches the build. Keys are
// kept in document order, so the result is the same on every run: a map item with several
// matching keys contributes them in the order the author wrote them.
type Filtered[T any] []FilterItem[T]

// FilterItem is a plain item (Value) or a filter map (Branches).
type FilterItem[T any] struct {
	Value    *T
	Branches []FilterBranch[T]
}

// FilterBranch is one key of a filter map and the items it selects. A value under a key can be a
// list, a single item, or another filter map (nested filters: debug: {gcc-cpp-linux: [...]}).
type FilterBranch[T any] struct {
	Key    string
	Pos    Pos
	Always bool // "common" in the legacy map form of workspace.modules
	Items  Filtered[T]
}

// leafKeyer is implemented by item types that may be written as a mapping: a mapping with the
// key is an item ({pattern: ..., optional: true}), any other mapping is a filter map.
type leafKeyer interface {
	leafKey() string
}

// Plain makes a filtered list of plain items.
func Plain[T any](values ...T) Filtered[T] {
	f := make(Filtered[T], len(values))
	for i := range values {
		f[i] = FilterItem[T]{Value: &values[i]}
	}
	return f
}

// Resolve returns the items the build selects, in document order.
func (f Filtered[T]) Resolve(ctx Context) []T {
	result := []T{}
	for _, item := range f {
		if item.Value != nil {
			result = append(result, *item.Value)
			continue
		}
		for _, b := range item.Branches {
			if b.Always || ctx.Matches(b.Key) {
				result = append(result, b.Items.Resolve(ctx)...)
			}
		}
	}
	return result
}

// All returns every item, whatever its filter keys: everything some build may select.
func (f Filtered[T]) All() []T {
	result := []T{}
	for _, item := range f {
		if item.Value != nil {
			result = append(result, *item.Value)
			continue
		}
		for _, b := range item.Branches {
			result = append(result, b.Items.All()...)
		}
	}
	return result
}

func (f *Filtered[T]) decodeNode(d *decoder, n *yaml.Node, path string) {
	switch {
	case isNull(n):
	case n.Kind == yaml.SequenceNode:
		list := make(Filtered[T], 0, len(n.Content))
		for i, item := range n.Content {
			if it, ok := decodeFilterItem[T](d, resolveAlias(item), fmt.Sprintf("%s[%d]", path, i)); ok {
				list = append(list, it)
			}
		}
		*f = list
	case n.Kind == yaml.ScalarNode || (n.Kind == yaml.MappingNode && isLeafMapping[T](n)):
		// One item for a list of one, as YAML lets a list of one be written
		if it, ok := decodeFilterItem[T](d, n, path); ok {
			*f = Filtered[T]{it}
		}
	default:
		d.errorf(n, "'%s' must be a list, not %s", path, kindName(n))
	}
}

// isLeafMapping reports whether a mapping node is an item of type T rather than a filter map.
func isLeafMapping[T any](n *yaml.Node) bool {
	keyer, ok := any(new(T)).(leafKeyer)
	if !ok {
		return false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == keyer.leafKey() {
			return true
		}
	}
	return false
}

func decodeFilterItem[T any](d *decoder, n *yaml.Node, path string) (FilterItem[T], bool) {
	if n.Kind == yaml.MappingNode && !isLeafMapping[T](n) {
		return FilterItem[T]{Branches: decodeBranches[T](d, n, path, false)}, true
	}
	if isNull(n) {
		d.errorf(n, "'%s' is empty", path)
		return FilterItem[T]{}, false
	}
	var value T
	before := len(d.problems)
	d.decode(n, reflect.ValueOf(&value).Elem(), path)
	return FilterItem[T]{Value: &value}, len(d.problems) == before
}

// decodeBranches decodes a filter map. In the legacy map form of workspace.modules (legacy), the
// key "common" is always included.
func decodeBranches[T any](d *decoder, n *yaml.Node, path string, legacy bool) []FilterBranch[T] {
	var branches []FilterBranch[T]
	for _, e := range d.entries(n, path) {
		b := FilterBranch[T]{Key: normalizeKey(e.key.Value), Pos: d.pos(e.key)}
		if legacy && e.key.Value == "common" {
			b.Always = true
		} else {
			d.filterKeys = append(d.filterKeys, NameUse{Name: b.Key, Path: path, Pos: b.Pos})
		}
		value, branchPath := e.value, join(path, e.key.Value)
		switch {
		case isNull(value):
		case value.Kind == yaml.SequenceNode:
			b.Items.decodeNode(d, value, branchPath)
		default:
			if it, ok := decodeFilterItem[T](d, value, branchPath); ok {
				b.Items = Filtered[T]{it}
			}
		}
		branches = append(branches, b)
	}
	return branches
}

// Names is what a filter key may name: a platform, an architecture, a configuration of the
// workspace or a toolchain.
type Names struct {
	Configurations []string
	Toolchains     []string
	Keywords       []string // abstract keywords the toolchains map (flag_mappings)
}

// CheckFilterKeys reports every filter key that names nothing the build could match.
func CheckFilterKeys(uses []NameUse, names Names) []string {
	configs := toSet(names.Configurations)
	toolchains := toSet(names.Toolchains)
	all := append(append(append(append([]string{}, Platforms...), Architectures...), names.Configurations...), names.Toolchains...)
	sort.Strings(all)
	var problems []string
	for _, use := range uses {
		_, platform := NormalizePlatform(use.Name)
		_, arch := NormalizeArchitecture(use.Name)
		if platform || arch || configs[use.Name] || toolchains[use.Name] {
			continue
		}
		msg := fmt.Sprintf("%s: filter key '%s' in '%s' is not a platform, architecture, configuration or toolchain",
			use.Pos, use.Name, use.Path)
		if near := ClosestName(use.Name, all); near != "" {
			msg += fmt.Sprintf(" (did you mean '%s'?)", near)
		} else {
			msg += fmt.Sprintf(" (platforms: %s; architectures: %s; configurations: %s; a custom configuration is declared under environment.configurations)",
				strings.Join(Platforms, ", "), strings.Join(Architectures, ", "), strings.Join(names.Configurations, ", "))
		}
		problems = append(problems, msg)
	}
	return problems
}

func toSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}
