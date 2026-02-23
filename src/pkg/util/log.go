package util

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// LogLevel represents the severity of a log message
type LogLevel int

const (
	LogLevelError   LogLevel = 1
	LogLevelWarning LogLevel = 2
	LogLevelInfo    LogLevel = 3
	LogLevelVerbose LogLevel = 4
	LogLevelDebug   LogLevel = 5
)

// Logger is the global logger instance
var (
	logMutex     sync.RWMutex
	notifyLevel  LogLevel = LogLevelWarning // Default to warning level
	stdLogger    *log.Logger
	initialized  bool
)

// InitLogger initializes the logging system with the specified notify level
// notifyLevel: 1=error, 2=warning, 3=info, 4=verbose, 5=debug
func InitLogger(level int, verbose bool) {
	logMutex.Lock()
	defer logMutex.Unlock()

	// Clamp level to valid range
	if level < 1 {
		level = 1
	}
	if level > 5 {
		level = 5
	}
	notifyLevel = LogLevel(level)

	// Set log flags based on verbose mode
	var flags int
	if verbose {
		flags = log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile
	} else {
		flags = log.Ldate | log.Ltime
	}

	stdLogger = log.New(os.Stdout, "", flags)
	initialized = true
}

// SetNotifyLevel sets the current notify level
func SetNotifyLevel(level int) {
	logMutex.Lock()
	defer logMutex.Unlock()

	if level < 1 {
		level = 1
	}
	if level > 5 {
		level = 5
	}
	notifyLevel = LogLevel(level)
}

// GetNotifyLevel returns the current notify level
func GetNotifyLevel() LogLevel {
	logMutex.RLock()
	defer logMutex.RUnlock()
	return notifyLevel
}

// shouldLog returns true if the given level should be logged
func shouldLog(level LogLevel) bool {
	logMutex.RLock()
	defer logMutex.RUnlock()
	return level <= notifyLevel
}

// getLogger returns the logger, initializing with defaults if needed
func getLogger() *log.Logger {
	logMutex.RLock()
	if initialized && stdLogger != nil {
		defer logMutex.RUnlock()
		return stdLogger
	}
	logMutex.RUnlock()

	// Initialize with defaults
	logMutex.Lock()
	defer logMutex.Unlock()
	if !initialized {
		stdLogger = log.New(os.Stdout, "", log.Ldate|log.Ltime)
		initialized = true
	}
	return stdLogger
}

// LogDebug logs a debug-level message (level 5)
func LogDebug(format string, v ...any) {
	if shouldLog(LogLevelDebug) {
		getLogger().Output(2, fmt.Sprintf("[DEBUG] "+format, v...))
	}
}

// LogVerbose logs a verbose-level message (level 4)
func LogVerbose(format string, v ...any) {
	if shouldLog(LogLevelVerbose) {
		getLogger().Output(2, fmt.Sprintf(format, v...))
	}
}

// LogInfo logs an info-level message (level 3)
func LogInfo(format string, v ...any) {
	if shouldLog(LogLevelInfo) {
		getLogger().Output(2, fmt.Sprintf(format, v...))
	}
}

// LogWarning logs a warning-level message (level 2)
func LogWarning(format string, v ...any) {
	if shouldLog(LogLevelWarning) {
		getLogger().Output(2, fmt.Sprintf("WARNING: "+format, v...))
	}
}

// LogProgress logs a progress message at warning level (level 2) without prefix
// Use this for build progress messages that should show by default
func LogProgress(format string, v ...any) {
	if shouldLog(LogLevelWarning) {
		getLogger().Output(2, fmt.Sprintf(format, v...))
	}
}

// LogError logs an error-level message (level 1)
func LogError(format string, v ...any) {
	if shouldLog(LogLevelError) {
		getLogger().Output(2, fmt.Sprintf("ERROR: "+format, v...))
	}
}

// LogFatal logs an error-level message and exits with code 1
func LogFatal(format string, v ...any) {
	// Fatal always logs regardless of level
	getLogger().Output(2, fmt.Sprintf("FATAL: "+format, v...))
	os.Exit(1)
}

// LogLevelFromInt converts an integer to a LogLevel
func LogLevelFromInt(level int) LogLevel {
	if level < 1 {
		return LogLevelError
	}
	if level > 5 {
		return LogLevelDebug
	}
	return LogLevel(level)
}

// LogLevelString returns the string representation of a log level
func LogLevelString(level LogLevel) string {
	switch level {
	case LogLevelError:
		return "error"
	case LogLevelWarning:
		return "warning"
	case LogLevelInfo:
		return "info"
	case LogLevelVerbose:
		return "verbose"
	case LogLevelDebug:
		return "debug"
	default:
		return "unknown"
	}
}
