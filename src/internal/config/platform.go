package config

import (
	"fmt"
	"runtime"
	"strings"
)

// The platforms and architectures buildy builds for. Every name buildy compares against a
// platform or an architecture (a --platform argument, a filter key, a dependency section, a
// toolchain's target) is normalised through this table first, so "darwin" and "macos" are the
// same platform everywhere.
var (
	Platforms     = []string{"linux", "windows", "macos", "wasm"}
	Architectures = []string{"x86_64", "x86", "arm64", "arm", "wasm32"}

	platformAliases     = map[string]string{"darwin": "macos"}
	architectureAliases = map[string]string{"amd64": "x86_64", "aarch64": "arm64", "i686": "x86", "i386": "x86"}
)

const (
	Linux   = "linux"
	Windows = "windows"
	MacOS   = "macos"
)

// NormalizePlatform returns the platform name, or its alias's, and whether it is a platform.
func NormalizePlatform(name string) (string, bool) {
	if alias, ok := platformAliases[name]; ok {
		return alias, true
	}
	for _, p := range Platforms {
		if p == name {
			return p, true
		}
	}
	return name, false
}

// NormalizeArchitecture returns the architecture name, or its alias's, and whether it is one.
func NormalizeArchitecture(name string) (string, bool) {
	if alias, ok := architectureAliases[name]; ok {
		return alias, true
	}
	for _, a := range Architectures {
		if a == name {
			return a, true
		}
	}
	return name, false
}

// normalizeKey normalises a name that may be a platform or an architecture (a filter key, a
// section name); any other name is returned as is.
func normalizeKey(name string) string {
	if p, ok := NormalizePlatform(name); ok {
		return p
	}
	if a, ok := NormalizeArchitecture(name); ok {
		return a
	}
	return name
}

// CheckPlatform returns the normalised platform, or an error naming the known ones.
func CheckPlatform(name string) (string, error) {
	if p, ok := NormalizePlatform(name); ok {
		return p, nil
	}
	return "", fmt.Errorf("'%s' is not a platform (expected one of: %s)", name, strings.Join(Platforms, ", "))
}

// CheckArchitecture returns the normalised architecture, or an error naming the known ones.
func CheckArchitecture(name string) (string, error) {
	if a, ok := NormalizeArchitecture(name); ok {
		return a, nil
	}
	return "", fmt.Errorf("'%s' is not an architecture (expected one of: %s)", name, strings.Join(Architectures, ", "))
}

// HostPlatform is the platform buildy runs on, or the Go name of an unsupported one.
func HostPlatform() string {
	p, _ := NormalizePlatform(runtime.GOOS)
	return p
}

// HostArchitecture is the architecture buildy runs on, or the Go name of an unsupported one.
func HostArchitecture() string {
	a, _ := NormalizeArchitecture(runtime.GOARCH)
	return a
}

// LibraryFileNames are the files the linker uses for -l<lib> on a platform, in its search order.
func LibraryFileNames(platform, lib string) []string {
	switch platform {
	case Windows:
		return []string{lib + ".lib", "lib" + lib + ".dll.a", "lib" + lib + ".a"}
	case MacOS:
		return []string{"lib" + lib + ".dylib", "lib" + lib + ".tbd", "lib" + lib + ".a"}
	default:
		return []string{"lib" + lib + ".so", "lib" + lib + ".a"}
	}
}
