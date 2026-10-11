// Package config reads every YAML file buildy reads (buildy.yaml, dependency definitions,
// toolchains, templates and build systems) into typed models. Each file kind has one loader: it
// decodes the YAML strictly (an unknown key, a duplicate key or a value of the wrong type is an
// error naming the file, the line and the key path) and then validates the model.
package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Pos is where a value is in its file.
type Pos struct {
	File string `json:"-"`
	Line int    `json:"-"`
}

func (p Pos) String() string {
	if p.Line == 0 {
		return p.File
	}
	return fmt.Sprintf("%s:%d", p.File, p.Line)
}

// nodeDecoder is implemented by types that decode themselves from a YAML node (string-or-list
// values, filtered lists, ordered maps). They receive null nodes too.
type nodeDecoder interface {
	decodeNode(d *decoder, n *yaml.Node, path string)
}

// decoder decodes YAML nodes into typed values, collecting every problem.
type decoder struct {
	file     string
	problems []problem
	// The filter keys and abstract keywords seen, checked once the toolchains are known
	filterKeys []NameUse
	keywords   []NameUse
}

type problem struct {
	line int
	text string
}

// NameUse is a name used in a file, with where it is: a filter key or an abstract keyword.
type NameUse struct {
	Name string
	Path string
	Pos  Pos
}

func newDecoder(file string) *decoder {
	return &decoder{file: file}
}

func (d *decoder) pos(n *yaml.Node) Pos {
	return Pos{File: d.file, Line: n.Line}
}

// errorf records a problem at node n (nil for one without a line).
func (d *decoder) errorf(n *yaml.Node, format string, args ...any) {
	line := 0
	if n != nil {
		line = n.Line
	}
	d.problems = append(d.problems, problem{line, fmt.Sprintf(format, args...)})
}

// errorAt records a problem at a position (one with no line for the zero Pos).
func (d *decoder) errorAt(pos Pos, format string, args ...any) {
	d.problems = append(d.problems, problem{pos.Line, fmt.Sprintf(format, args...)})
}

// err returns the problems as one error naming the file, or nil.
func (d *decoder) err(kind string) error {
	if len(d.problems) == 0 {
		return nil
	}
	sort.SliceStable(d.problems, func(i, j int) bool { return d.problems[i].line < d.problems[j].line })
	lines := make([]string, len(d.problems))
	for i, p := range d.problems {
		if p.line > 0 {
			lines[i] = fmt.Sprintf("line %d: %s", p.line, p.text)
		} else {
			lines[i] = p.text
		}
	}
	return fmt.Errorf("%s is not a valid %s:\n  - %s", d.file, kind, strings.Join(lines, "\n  - "))
}

// parse parses data into a document's root node. An empty document is an empty mapping.
func parse(data []byte, file string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid YAML in %s: %w", file, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	return doc.Content[0], nil
}

// join builds a key path: "targets" + "executables" is "targets.executables".
func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func isNull(n *yaml.Node) bool {
	return n == nil || (n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null")
}

// resolveAlias follows an alias to the node it names.
func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// kindName names a node's kind for messages.
func kindName(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!str":
			return "a string"
		case "!!int", "!!float":
			return "a number"
		case "!!bool":
			return "a boolean"
		case "!!null":
			return "empty"
		}
		return "a scalar"
	}
	return "a value"
}

// mappingEntry is one key and value of a mapping, after merge keys (<<) are expanded.
type mappingEntry struct {
	key   *yaml.Node
	value *yaml.Node
}

// entries returns a mapping's entries in document order, with the entries of merge keys (<<)
// first. It reports duplicate keys.
func (d *decoder) entries(n *yaml.Node, path string) []mappingEntry {
	var merged, own []mappingEntry
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], resolveAlias(n.Content[i+1])
		if key.Kind == yaml.ScalarNode && key.Value == "<<" && key.ShortTag() == "!!merge" {
			sources := []*yaml.Node{value}
			if value.Kind == yaml.SequenceNode {
				sources = value.Content
			}
			for _, source := range sources {
				source = resolveAlias(source)
				if source.Kind != yaml.MappingNode {
					d.errorf(key, "'%s': a merge key (<<) must name a mapping", join(path, "<<"))
					continue
				}
				merged = append(merged, d.entries(source, path)...)
			}
			continue
		}
		if key.Kind != yaml.ScalarNode {
			d.errorf(key, "'%s' has a key that is not a string", path)
			continue
		}
		if seen[key.Value] {
			d.errorf(key, "duplicate key '%s'", join(path, key.Value))
			continue
		}
		seen[key.Value] = true
		own = append(own, mappingEntry{key, value})
	}
	result := make([]mappingEntry, 0, len(merged)+len(own))
	for _, e := range merged {
		if !seen[e.key.Value] {
			seen[e.key.Value] = true
			result = append(result, e)
		}
	}
	return append(result, own...)
}

// decode decodes node n into v, which must be settable. path names the value in messages.
func (d *decoder) decode(n *yaml.Node, v reflect.Value, path string) {
	n = resolveAlias(n)
	if v.CanAddr() {
		if custom, ok := v.Addr().Interface().(nodeDecoder); ok {
			custom.decodeNode(d, n, path)
			return
		}
	}
	if isNull(n) {
		return // absent: the zero value
	}
	switch v.Kind() {
	case reflect.Pointer:
		elem := reflect.New(v.Type().Elem())
		d.decode(n, elem.Elem(), path)
		v.Set(elem)
	case reflect.String:
		if s, ok := d.scalarString(n, path); ok {
			v.SetString(s)
		}
	case reflect.Bool:
		if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!bool" {
			d.errorf(n, "'%s' must be true or false, not %s", path, kindName(n))
			return
		}
		var b bool
		if err := n.Decode(&b); err != nil {
			d.errorf(n, "'%s': %v", path, err)
			return
		}
		v.SetBool(b)
	case reflect.Int:
		if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!int" {
			d.errorf(n, "'%s' must be a whole number, not %s", path, kindName(n))
			return
		}
		var i int
		if err := n.Decode(&i); err != nil {
			d.errorf(n, "'%s': %v", path, err)
			return
		}
		v.SetInt(int64(i))
	case reflect.Slice:
		if n.Kind != yaml.SequenceNode {
			d.errorf(n, "'%s' must be a list, not %s", path, kindName(n))
			return
		}
		slice := reflect.MakeSlice(v.Type(), len(n.Content), len(n.Content))
		for i, item := range n.Content {
			d.decode(item, slice.Index(i), elementPath(path, i, item))
		}
		v.Set(slice)
	case reflect.Map:
		if n.Kind != yaml.MappingNode {
			d.errorf(n, "'%s' must be a mapping, not %s", path, kindName(n))
			return
		}
		m := reflect.MakeMap(v.Type())
		for _, e := range d.entries(n, path) {
			value := reflect.New(v.Type().Elem()).Elem()
			d.decode(e.value, value, join(path, e.key.Value))
			m.SetMapIndex(reflect.ValueOf(e.key.Value).Convert(v.Type().Key()), value)
		}
		v.Set(m)
	case reflect.Struct:
		d.decodeStruct(n, v, path)
	default:
		panic(fmt.Sprintf("config: cannot decode into %s (%s)", v.Type(), path))
	}
}

// scalarString returns the text of a string scalar. Any other value is a problem: a number or a
// boolean where a string belongs is almost always a mistake, and quoting it says what is meant.
func (d *decoder) scalarString(n *yaml.Node, path string) (string, bool) {
	if n.Kind != yaml.ScalarNode {
		d.errorf(n, "'%s' must be a string, not %s", path, kindName(n))
		return "", false
	}
	if n.ShortTag() != "!!str" {
		d.errorf(n, "'%s' must be a string, not %s (quote it: \"%s\")", path, kindName(n), n.Value)
		return "", false
	}
	return n.Value, true
}

// elementPath names list element i, with the element's name when it has one:
// "targets.executables[0] (app)".
func elementPath(path string, i int, item *yaml.Node) string {
	p := fmt.Sprintf("%s[%d]", path, i)
	item = resolveAlias(item)
	if item != nil && item.Kind == yaml.MappingNode {
		for j := 0; j+1 < len(item.Content); j += 2 {
			if item.Content[j].Value == "name" && item.Content[j+1].Kind == yaml.ScalarNode {
				return fmt.Sprintf("%s (%s)", p, item.Content[j+1].Value)
			}
		}
	}
	return p
}

// structField is a field a struct decodes a key into.
type structField struct {
	index    []int
	required bool
}

// structFields returns a struct type's keys, following inline (embedded) structs, and the index
// of its remain field (a map that takes the keys no field names), if any.
func structFields(t reflect.Type) (map[string]structField, []int) {
	fields := map[string]structField{}
	var remain []int
	var walk func(t reflect.Type, prefix []int)
	walk = func(t reflect.Type, prefix []int) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("yaml")
			if tag == "-" || (!f.IsExported() && !f.Anonymous) {
				continue
			}
			index := append(append([]int{}, prefix...), i)
			name, opts, _ := strings.Cut(tag, ",")
			if opts == "inline" {
				walk(f.Type, index)
				continue
			}
			if opts == "remain" {
				remain = index
				continue
			}
			if name == "" {
				continue // untagged: not read from YAML
			}
			fields[name] = structField{index: index, required: opts == "required"}
		}
	}
	walk(t, nil)
	return fields, remain
}

var posType = reflect.TypeOf(Pos{})

// decodeStruct decodes a mapping into a struct by its yaml tags. A key no field names is an error
// (with the closest known key as a suggestion), unless the struct has a remain map.
func (d *decoder) decodeStruct(n *yaml.Node, v reflect.Value, path string) {
	if n.Kind != yaml.MappingNode {
		d.errorf(n, "'%s' must be a mapping, not %s", path, kindName(n))
		return
	}
	if f := v.FieldByName("Pos"); f.IsValid() && f.Type() == posType {
		f.Set(reflect.ValueOf(d.pos(n)))
	}
	fields, remain := structFields(v.Type())
	present := map[string]bool{}
	var remainMap reflect.Value
	for _, e := range d.entries(n, path) {
		key := e.key.Value
		field, ok := fields[key]
		if !ok {
			if remain != nil {
				if !remainMap.IsValid() {
					remainMap = v.FieldByIndex(remain)
					if remainMap.IsNil() {
						remainMap.Set(reflect.MakeMap(remainMap.Type()))
					}
				}
				value := reflect.New(remainMap.Type().Elem()).Elem()
				d.decode(e.value, value, join(path, key))
				remainMap.SetMapIndex(reflect.ValueOf(key), value)
				continue
			}
			d.unknownKey(e.key, path, key, sortedNames(fields))
			continue
		}
		present[key] = !isNull(resolveAlias(e.value))
		d.decode(e.value, v.FieldByIndex(field.index), join(path, key))
	}
	for _, name := range sortedNames(fields) {
		if fields[name].required && !present[name] {
			d.errorf(n, "%s is missing required '%s'", describe(path), name)
		}
	}
}

// describe names a path for "X is missing ..." messages: the file's top level is "the file".
func describe(path string) string {
	if path == "" {
		return "the file"
	}
	return "'" + path + "'"
}

// unknownKey reports a key outside known, suggesting the closest known key.
func (d *decoder) unknownKey(n *yaml.Node, path, key string, known []string) {
	if near := ClosestName(key, known); near != "" {
		d.errorf(n, "unknown key '%s' (did you mean '%s'?)", join(path, key), near)
		return
	}
	if len(known) > 8 {
		d.errorf(n, "unknown key '%s'", join(path, key))
		return
	}
	d.errorf(n, "unknown key '%s' (expected one of: %s)", join(path, key), strings.Join(known, ", "))
}

func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ClosestName returns the name in known within two edits of name (the first of the closest), or
// "".
func ClosestName(name string, known []string) string {
	best, bestDistance := "", 3
	for _, candidate := range known {
		if d := editDistance(name, candidate); d < bestDistance {
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
