package util

import (
	"fmt"
	"sync"
)

// BuildErrorLevel represents the severity of a build issue
type BuildErrorLevel int

const (
	// ErrorLevelWarning is a non-critical issue that would have been a warning
	// In our strict mode, these are treated as errors and will fail the build
	ErrorLevelWarning BuildErrorLevel = iota
	
	// ErrorLevelError is a critical issue that prevents the build from continuing
	ErrorLevelError
)

// BuildError represents a build error or warning
type BuildError struct {
	Level   BuildErrorLevel
	Message string
	Source  string // File or component that generated the error
}

func (e *BuildError) Error() string {
	prefix := "ERROR"
	if e.Level == ErrorLevelWarning {
		prefix = "ERROR (was warning)"
	}
	if e.Source != "" {
		return fmt.Sprintf("%s [%s]: %s", prefix, e.Source, e.Message)
	}
	return fmt.Sprintf("%s: %s", prefix, e.Message)
}

// BuildErrorCollector collects build errors and warnings
// In strict mode (default), warnings are treated as errors
type BuildErrorCollector struct {
	errors []BuildError
	mu     sync.Mutex
}

// NewBuildErrorCollector creates a new BuildErrorCollector
func NewBuildErrorCollector() *BuildErrorCollector {
	return &BuildErrorCollector{
		errors: []BuildError{},
	}
}

// AddError adds an error to the collector
func (bec *BuildErrorCollector) AddError(source, message string) {
	bec.mu.Lock()
	defer bec.mu.Unlock()
	
	err := BuildError{
		Level:   ErrorLevelError,
		Message: message,
		Source:  source,
	}
	bec.errors = append(bec.errors, err)
	LogError("[%s]: %s", source, message)
}

// AddWarning adds a warning to the collector (treated as error in strict mode)
func (bec *BuildErrorCollector) AddWarning(source, message string) {
	bec.mu.Lock()
	defer bec.mu.Unlock()
	
	err := BuildError{
		Level:   ErrorLevelWarning,
		Message: message,
		Source:  source,
	}
	bec.errors = append(bec.errors, err)
	LogError("[%s]: %s (was: warning)", source, message)
}

// HasErrors returns true if there are any errors (including warnings in strict mode)
func (bec *BuildErrorCollector) HasErrors() bool {
	bec.mu.Lock()
	defer bec.mu.Unlock()
	return len(bec.errors) > 0
}

// GetErrors returns all collected errors
func (bec *BuildErrorCollector) GetErrors() []BuildError {
	bec.mu.Lock()
	defer bec.mu.Unlock()
	
	result := make([]BuildError, len(bec.errors))
	copy(result, bec.errors)
	return result
}

// ErrorCount returns the number of errors
func (bec *BuildErrorCollector) ErrorCount() int {
	bec.mu.Lock()
	defer bec.mu.Unlock()
	return len(bec.errors)
}

// WarningCount returns the number of warnings (that are now treated as errors)
func (bec *BuildErrorCollector) WarningCount() int {
	bec.mu.Lock()
	defer bec.mu.Unlock()
	
	count := 0
	for _, err := range bec.errors {
		if err.Level == ErrorLevelWarning {
			count++
		}
	}
	return count
}

// Clear removes all errors
func (bec *BuildErrorCollector) Clear() {
	bec.mu.Lock()
	defer bec.mu.Unlock()
	bec.errors = []BuildError{}
}

// PrintSummary prints a summary of collected errors
func (bec *BuildErrorCollector) PrintSummary() {
	bec.mu.Lock()
	defer bec.mu.Unlock()
	
	if len(bec.errors) == 0 {
		return
	}
	
	LogError("\n=== Build Errors ===")
	warningCount := 0
	for _, err := range bec.errors {
		if err.Level == ErrorLevelWarning {
			warningCount++
		}
		LogError("  %s", err.Error())
	}
	
	if warningCount > 0 {
		LogError("\nNote: %d issue(s) were warnings that are now treated as errors.", warningCount)
		LogError("All warnings are treated as errors to ensure build reliability.")
	}
	LogError("Total errors: %d", len(bec.errors))
}

// CombinedError returns a single error combining all collected errors
func (bec *BuildErrorCollector) CombinedError() error {
	bec.mu.Lock()
	defer bec.mu.Unlock()
	
	if len(bec.errors) == 0 {
		return nil
	}
	
	if len(bec.errors) == 1 {
		return &bec.errors[0]
	}
	
	return fmt.Errorf("build failed with %d errors", len(bec.errors))
}

// Global error collector instance
var globalErrorCollector = NewBuildErrorCollector()

// GetErrorCollector returns the global error collector
func GetErrorCollector() *BuildErrorCollector {
	return globalErrorCollector
}

// ResetErrorCollector resets the global error collector
func ResetErrorCollector() {
	globalErrorCollector = NewBuildErrorCollector()
}

// Convenience functions for adding errors/warnings to the global collector

// BuildWarning adds a warning to the global collector (treated as error)
func BuildWarning(source, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	globalErrorCollector.AddWarning(source, message)
}

// BuildErrorf adds an error to the global collector
func BuildErrorf(source, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	globalErrorCollector.AddError(source, message)
}
