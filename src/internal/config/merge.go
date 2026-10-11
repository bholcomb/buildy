package config

import (
	"reflect"
)

// Merge returns base with over merged into it, by the merge rules, without changing either
// (the result shares nothing with them):
//
//   - mappings merge key by key (a key in both merges its two values);
//   - lists append: base's items, then over's;
//   - a scalar set in over replaces base's (an empty string or false is "not set"; a field that
//     must be able to say "empty", like output_prefix, is a pointer, and set when present).
//
// The workspace root's settings merge with a module's, and the settings of the build's platform,
// architecture and configuration merge in after them, by these rules.
func Merge[T any](base, over T) T {
	var result T
	merged := merge(reflect.ValueOf(base), reflect.ValueOf(over))
	reflect.ValueOf(&result).Elem().Set(merged)
	return result
}

// orderedMap marks Ordered, which merges like a mapping and keeps its key order.
type orderedMap interface {
	orderedMap()
}

func (Ordered[T]) orderedMap() {}

var orderedType = reflect.TypeOf((*orderedMap)(nil)).Elem()

func merge(base, over reflect.Value) reflect.Value {
	t := base.Type()
	result := reflect.New(t).Elem()
	switch t.Kind() {
	case reflect.Pointer:
		if base.IsNil() && over.IsNil() {
			return result
		}
		p := reflect.New(t.Elem())
		switch {
		case base.IsNil():
			p.Elem().Set(copyValue(over.Elem()))
		case over.IsNil():
			p.Elem().Set(copyValue(base.Elem()))
		default:
			p.Elem().Set(merge(base.Elem(), over.Elem()))
		}
		result.Set(p)
	case reflect.Slice:
		if base.Len()+over.Len() == 0 {
			if !base.IsNil() || !over.IsNil() {
				result.Set(reflect.MakeSlice(t, 0, 0))
			}
			return result
		}
		s := reflect.MakeSlice(t, 0, base.Len()+over.Len())
		for i := 0; i < base.Len(); i++ {
			s = reflect.Append(s, copyValue(base.Index(i)))
		}
		for i := 0; i < over.Len(); i++ {
			s = reflect.Append(s, copyValue(over.Index(i)))
		}
		result.Set(s)
	case reflect.Map:
		if base.IsNil() && over.IsNil() {
			return result
		}
		m := reflect.MakeMap(t)
		for _, k := range base.MapKeys() {
			m.SetMapIndex(k, copyValue(base.MapIndex(k)))
		}
		for _, k := range over.MapKeys() {
			if existing := m.MapIndex(k); existing.IsValid() {
				m.SetMapIndex(k, merge(base.MapIndex(k), over.MapIndex(k)))
			} else {
				m.SetMapIndex(k, copyValue(over.MapIndex(k)))
			}
		}
		result.Set(m)
	case reflect.Struct:
		if t.Implements(orderedType) {
			return mergeOrdered(base, over)
		}
		for i := 0; i < t.NumField(); i++ {
			if !t.Field(i).IsExported() {
				continue
			}
			if t.Field(i).Type == posType {
				// Where the result came from: the later layer that set anything, else the earlier
				pos := over.Field(i)
				if pos.IsZero() {
					pos = base.Field(i)
				}
				result.Field(i).Set(pos)
				continue
			}
			result.Field(i).Set(merge(base.Field(i), over.Field(i)))
		}
	default: // scalars
		if over.IsZero() {
			result.Set(base)
		} else {
			result.Set(over)
		}
	}
	return result
}

// mergeOrdered merges two Ordered values: base's keys in order, then over's new keys.
func mergeOrdered(base, over reflect.Value) reflect.Value {
	result := reflect.New(base.Type()).Elem()
	keys := reflect.MakeSlice(base.Field(0).Type(), 0, base.Field(0).Len()+over.Field(0).Len())
	seen := map[string]bool{}
	for _, side := range []reflect.Value{base, over} {
		for i := 0; i < side.Field(0).Len(); i++ {
			k := side.Field(0).Index(i)
			if !seen[k.String()] {
				seen[k.String()] = true
				keys = reflect.Append(keys, k)
			}
		}
	}
	if keys.Len() == 0 {
		return result
	}
	result.Field(0).Set(keys)
	result.Field(1).Set(merge(base.Field(1), over.Field(1)))
	return result
}

// copyValue returns a deep copy of v.
func copyValue(v reflect.Value) reflect.Value {
	return merge(v, reflect.Zero(v.Type()))
}
