package util

import "strings"

// ToStringSliceOptions configures ToStringSlice behavior
type ToStringSliceOptions struct {
	SkipEmpty bool // If true, skip empty/whitespace-only strings
	Recursive bool // If true, recursively flatten nested slices
}

// ToStringSlice converts various types to a string slice.
// Handles string, []string, []any (with optional recursive flattening).
// This is the canonical implementation - use this instead of duplicating logic.
func ToStringSlice(value any, opts ToStringSliceOptions) []string {
	return toStringSliceInternal(value, opts)
}

func toStringSliceInternal(value any, opts ToStringSliceOptions) []string {
	switch v := value.(type) {
	case string:
		if opts.SkipEmpty && strings.TrimSpace(v) == "" {
			return []string{}
		}
		return []string{v}
	case []string:
		if !opts.SkipEmpty {
			return v
		}
		result := make([]string, 0, len(v))
		for _, s := range v {
			if strings.TrimSpace(s) != "" {
				result = append(result, s)
			}
		}
		return result
	case []any:
		result := []string{}
		for _, item := range v {
			if str, ok := item.(string); ok {
				if opts.SkipEmpty && strings.TrimSpace(str) == "" {
					continue
				}
				result = append(result, str)
			} else if opts.Recursive {
				nested := toStringSliceInternal(item, opts)
				result = append(result, nested...)
			}
		}
		return result
	}
	return []string{}
}

// ExtractStringList extracts a string slice from various input types.
// It skips empty/whitespace-only strings. For including empty strings, use ExtractStringSlice.
func ExtractStringList(value any) []string {
	return ToStringSlice(value, ToStringSliceOptions{SkipEmpty: true, Recursive: false})
}

// ExtractStringSlice extracts a string slice from an interface.
// Unlike ExtractStringList, this includes empty strings.
func ExtractStringSlice(value any) []string {
	return ToStringSlice(value, ToStringSliceOptions{SkipEmpty: false, Recursive: false})
}

// FlattenStringSlice converts value to []string with recursive flattening of nested slices.
// Skips empty strings.
func FlattenStringSlice(value any) []string {
	return ToStringSlice(value, ToStringSliceOptions{SkipEmpty: true, Recursive: true})
}

// LookupPath traverses a nested map using a path (slice of keys).
// Returns the value and whether it was found.
func LookupPath(m map[string]any, path []string) (any, bool) {
	if len(path) == 0 || m == nil {
		return nil, false
	}

	current := any(m)
	for _, key := range path {
		currentMap, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		val, exists := currentMap[key]
		if !exists {
			return nil, false
		}
		current = val
	}

	return current, true
}

// LookupDottedPath traverses a nested map using a dotted path string (e.g., "item.includes").
// Returns the value and whether it was found.
func LookupDottedPath(m map[string]any, dottedPath string) (any, bool) {
	parts := strings.Split(dottedPath, ".")
	if len(parts) == 0 {
		return nil, false
	}
	return LookupPath(m, parts)
}

// SliceContains checks if a string slice contains a value.
func SliceContains(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}
