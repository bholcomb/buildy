package util

import "strings"

// ExtractStringList extracts a string slice from various input types
// It handles string, []any, and []string inputs
func ExtractStringList(value any) []string {
	result := []string{}
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			result = append(result, v)
		}
	case []any:
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
	case []string:
		result = v
	}
	return result
}

// ExtractStringSlice extracts a string slice from an interface
// Unlike ExtractStringList, this includes empty strings
func ExtractStringSlice(value any) []string {
	result := []string{}
	switch v := value.(type) {
	case string:
		result = append(result, v)
	case []any:
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
	case []string:
		result = v
	}
	return result
}
