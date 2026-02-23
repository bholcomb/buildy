package workspace

// BuildContext holds the current build parameters for filter matching.
// Filters in YAML arrays are matched against these 4 dimensions.
type BuildContext struct {
	Platform      string // linux, windows, macos
	Architecture  string // x86_64, arm64
	Configuration string // debug, release, or any user-defined value
	Toolchain     string // gcc-cpp-linux, clang-cpp-macos, etc.
}

// ResolveFilteredList extracts items from an array that may contain
// string items (always included) and map items (filters).
//
// Example YAML:
//
//	sources:
//	  - "src/common.c"        # String: always included
//	  - linux:                # Map: included when platform=linux
//	      - "src/linux.c"
//	  - windows:              # Map: included when platform=windows
//	      - "src/windows.c"
func ResolveFilteredList(raw any, ctx BuildContext) []string {
	result := []string{}

	// Handle single string
	if s, ok := raw.(string); ok {
		return []string{s}
	}

	// Handle []string directly
	if ss, ok := raw.([]string); ok {
		return ss
	}

	// Handle array of mixed items
	items, ok := raw.([]any)
	if !ok {
		return result
	}

	for _, item := range items {
		switch v := item.(type) {
		case string:
			// Plain string - always include
			result = append(result, v)
		case map[string]any:
			// Map item - check if any key matches a filter
			for key, value := range v {
				if matchesFilter(key, ctx) {
					result = append(result, extractStrings(value)...)
				}
			}
		}
	}

	return result
}

// matchesFilter returns true if the key matches any of the 4 filter dimensions
func matchesFilter(key string, ctx BuildContext) bool {
	return key == ctx.Platform ||
		key == ctx.Architecture ||
		key == ctx.Configuration ||
		key == ctx.Toolchain
}

// extractStrings extracts a string slice from various input types
func extractStrings(v any) []string {
	result := []string{}
	switch items := v.(type) {
	case []any:
		for _, item := range items {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
	case []string:
		result = items
	case string:
		result = []string{items}
	}
	return result
}
