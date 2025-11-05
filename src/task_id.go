package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// TaskIDGenerator generates unique task identifiers scoped to a module
type TaskIDGenerator struct {
	prefix   string
	counters map[string]int
}

// NewTaskIDGenerator creates a generator using the provided module path as prefix
func NewTaskIDGenerator(modulePath string) *TaskIDGenerator {
	cleanPath := strings.ReplaceAll(modulePath, string(filepath.Separator), "_")
	cleanPath = strings.ReplaceAll(cleanPath, "/", "_")
	cleanPath = strings.ReplaceAll(cleanPath, "\\", "_")
	prefix := sanitizeToken(cleanPath)
	if prefix == "" {
		prefix = "root"
	}

	return &TaskIDGenerator{
		prefix:   prefix,
		counters: make(map[string]int),
	}
}

// Next returns a unique task identifier given a task type and logical name
func (gen *TaskIDGenerator) Next(taskType, logicalName string) string {
	sanitizedType := sanitizeToken(taskType)
	if sanitizedType == "" {
		sanitizedType = "task"
	}
	sanitizedName := sanitizeToken(logicalName)

	key := sanitizedType + ":" + sanitizedName
	gen.counters[key]++
	count := gen.counters[key]

	base := sanitizedType
	if sanitizedName != "" {
		base = base + "_" + sanitizedName
	}

	id := fmt.Sprintf("%s_%03d", base, count)
	return fmt.Sprintf("%s__%s", id, gen.prefix)
}

func sanitizeToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, string(filepath.Separator), "_")
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, "\\", "_")
	value = nonAlphanumeric.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	return strings.ToLower(value)
}
