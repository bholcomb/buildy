package config

import (
	"encoding/json"
	"reflect"

	"gopkg.in/yaml.v3"
)

// StringList is a list of strings that may also be written as one string: "a" is ["a"].
type StringList []string

func (l *StringList) decodeNode(d *decoder, n *yaml.Node, path string) {
	switch {
	case isNull(n):
	case n.Kind == yaml.ScalarNode:
		if s, ok := d.scalarString(n, path); ok {
			*l = StringList{s}
		}
	case n.Kind == yaml.SequenceNode:
		list := make(StringList, 0, len(n.Content))
		for i, item := range n.Content {
			if s, ok := d.scalarString(resolveAlias(item), elementPath(path, i, item)); ok {
				list = append(list, s)
			}
		}
		*l = list
	default:
		d.errorf(n, "'%s' must be a string or a list of strings, not %s", path, kindName(n))
	}
}

// Scalar is a value written as any scalar (string, number or boolean), kept as its text:
// variable values and environment variables.
type Scalar string

func (s *Scalar) decodeNode(d *decoder, n *yaml.Node, path string) {
	switch {
	case isNull(n):
	case n.Kind == yaml.ScalarNode:
		*s = Scalar(n.Value)
	default:
		d.errorf(n, "'%s' must be a single value, not %s", path, kindName(n))
	}
}

// Ordered is a mapping that keeps its keys in document order: anything whose order shows in a
// command or decides a lookup (tools, template variables, abstract keywords) iterates it in that
// order, never in Go's map order.
type Ordered[T any] struct {
	Keys   []string
	Values map[string]T
}

func (o *Ordered[T]) decodeNode(d *decoder, n *yaml.Node, path string) {
	if isNull(n) {
		return
	}
	if n.Kind != yaml.MappingNode {
		d.errorf(n, "'%s' must be a mapping, not %s", path, kindName(n))
		return
	}
	for _, e := range d.entries(n, path) {
		var value T
		d.decode(e.value, reflect.ValueOf(&value).Elem(), join(path, e.key.Value))
		o.Set(e.key.Value, value)
	}
}

// Get returns the value of key.
func (o Ordered[T]) Get(key string) (T, bool) {
	v, ok := o.Values[key]
	return v, ok
}

// Set sets key's value, adding key at the end if it is new.
func (o *Ordered[T]) Set(key string, value T) {
	if o.Values == nil {
		o.Values = map[string]T{}
	}
	if _, ok := o.Values[key]; !ok {
		o.Keys = append(o.Keys, key)
	}
	o.Values[key] = value
}

// Len is the number of keys.
func (o Ordered[T]) Len() int {
	return len(o.Keys)
}

// MarshalJSON writes the entries in order, so a hash of a model is stable.
func (o Ordered[T]) MarshalJSON() ([]byte, error) {
	type entry struct {
		Key   string `json:"key"`
		Value T      `json:"value"`
	}
	entries := make([]entry, len(o.Keys))
	for i, k := range o.Keys {
		entries[i] = entry{k, o.Values[k]}
	}
	return json.Marshal(entries)
}
