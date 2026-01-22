package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ResponseFileConfig contains settings for response file handling
type ResponseFileConfig struct {
	// MaxCommandLength is the maximum command line length before using a response file
	// Windows: 8191 chars (cmd.exe limit), Linux/macOS: much higher (typically 128KB+)
	MaxCommandLength int
	
	// ResponseFileDir is the directory to store response files
	ResponseFileDir string
	
	// UseResponseFiles enables response file generation
	UseResponseFiles bool
}

// DefaultResponseFileConfig returns the default configuration
func DefaultResponseFileConfig(cacheDir string) *ResponseFileConfig {
	maxLen := 131072 // 128KB for Linux/macOS
	if runtime.GOOS == "windows" {
		maxLen = 8000 // Leave some margin below 8191
	}
	
	return &ResponseFileConfig{
		MaxCommandLength: maxLen,
		ResponseFileDir:  filepath.Join(cacheDir, "response_files"),
		UseResponseFiles: runtime.GOOS == "windows", // Auto-enable on Windows
	}
}

// ResponseFileManager handles response file creation for long command lines
type ResponseFileManager struct {
	config *ResponseFileConfig
	files  []string // Track created files for cleanup
}

// NewResponseFileManager creates a new ResponseFileManager
func NewResponseFileManager(config *ResponseFileConfig) *ResponseFileManager {
	if config == nil {
		config = DefaultResponseFileConfig(".buildy_cache")
	}
	
	// Ensure response file directory exists
	os.MkdirAll(config.ResponseFileDir, 0755)
	
	return &ResponseFileManager{
		config: config,
		files:  []string{},
	}
}

// ProcessCommand checks if a command needs a response file and creates one if necessary
// Returns the potentially modified command and an error if any
func (rfm *ResponseFileManager) ProcessCommand(command string, taskID string) (string, error) {
	if !rfm.config.UseResponseFiles {
		return command, nil
	}
	
	if len(command) <= rfm.config.MaxCommandLength {
		return command, nil
	}
	
	// Parse command to find the executable and arguments
	parts := parseCommandLine(command)
	if len(parts) < 2 {
		return command, nil // No arguments to put in response file
	}
	
	executable := parts[0]
	args := parts[1:]
	
	// Determine response file format based on the executable
	responseFormat := detectResponseFormat(executable)
	
	// Create response file
	responseFile, err := rfm.createResponseFile(taskID, args, responseFormat)
	if err != nil {
		return command, fmt.Errorf("failed to create response file: %w", err)
	}
	
	rfm.files = append(rfm.files, responseFile)
	
	// Build new command with response file
	var newCommand string
	switch responseFormat {
	case "msvc":
		// MSVC uses @file
		newCommand = fmt.Sprintf("%s @%s", executable, responseFile)
	case "gcc":
		// GCC/Clang also uses @file for response files
		newCommand = fmt.Sprintf("%s @%s", executable, responseFile)
	default:
		// Default to @ syntax
		newCommand = fmt.Sprintf("%s @%s", executable, responseFile)
	}
	
	return newCommand, nil
}

// createResponseFile creates a response file with the given arguments
func (rfm *ResponseFileManager) createResponseFile(taskID string, args []string, format string) (string, error) {
	// Sanitize taskID for filename
	safeTaskID := strings.ReplaceAll(taskID, "/", "_")
	safeTaskID = strings.ReplaceAll(safeTaskID, "\\", "_")
	safeTaskID = strings.ReplaceAll(safeTaskID, ":", "_")
	
	filename := filepath.Join(rfm.config.ResponseFileDir, safeTaskID+".rsp")
	
	// Format arguments based on the response file format
	var content string
	switch format {
	case "msvc":
		// MSVC: one argument per line, quote paths with spaces
		lines := make([]string, len(args))
		for i, arg := range args {
			if strings.Contains(arg, " ") && !strings.HasPrefix(arg, "\"") {
				lines[i] = "\"" + arg + "\""
			} else {
				lines[i] = arg
			}
		}
		content = strings.Join(lines, "\r\n")
	case "gcc":
		// GCC/Clang: one argument per line, escape special characters
		lines := make([]string, len(args))
		for i, arg := range args {
			if strings.Contains(arg, " ") || strings.Contains(arg, "\"") {
				// Quote the argument
				escaped := strings.ReplaceAll(arg, "\\", "\\\\")
				escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
				lines[i] = "\"" + escaped + "\""
			} else {
				lines[i] = arg
			}
		}
		content = strings.Join(lines, "\n")
	default:
		content = strings.Join(args, "\n")
	}
	
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		return "", err
	}
	
	return filename, nil
}

// Cleanup removes all created response files
func (rfm *ResponseFileManager) Cleanup() {
	for _, file := range rfm.files {
		os.Remove(file)
	}
	rfm.files = nil
}

// detectResponseFormat determines the response file format based on the executable
func detectResponseFormat(executable string) string {
	exe := strings.ToLower(filepath.Base(executable))
	exe = strings.TrimSuffix(exe, ".exe")
	
	// MSVC tools
	msvcTools := []string{"cl", "link", "lib", "ml", "ml64", "armasm"}
	for _, tool := range msvcTools {
		if exe == tool {
			return "msvc"
		}
	}
	
	// GCC/Clang tools
	gccTools := []string{"gcc", "g++", "clang", "clang++", "cc", "c++", "ld", "ar"}
	for _, tool := range gccTools {
		if exe == tool || strings.HasPrefix(exe, tool+"-") {
			return "gcc"
		}
	}
	
	// Default to GCC format
	return "gcc"
}

// parseCommandLine splits a command line into executable and arguments
// Handles quoted arguments
func parseCommandLine(command string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)
	
	for _, r := range command {
		switch {
		case r == '"' || r == '\'':
			if inQuote && r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else if !inQuote {
				inQuote = true
				quoteChar = r
			}
			current.WriteRune(r)
		case r == ' ' || r == '\t':
			if inQuote {
				current.WriteRune(r)
			} else if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	
	return parts
}
