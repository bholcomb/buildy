package util

import (
	"os"
	"strings"
)

// RealPath returns the absolute path of the file or directory at path with every symlink resolved:
// filepath.EvalSymlinks on Linux and macOS. On Windows it asks the file system for the final path
// of the open file (GetFinalPathNameByHandle), which also resolves junctions and gives short (8.3)
// names in full: filepath.EvalSymlinks fails on a path through a junction (Go 1.23 and later), such
// as the tool cache of a GitHub Actions Windows runner.
func RealPath(path string) (string, error) {
	return realPath(path)
}

// Executable returns the path of the running buildy executable, symlinks resolved (RealPath).
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return RealPath(exe)
}

// trimExtendedPrefix turns a final path as Windows gives it (\\?\C:\dir, \\?\UNC\server\share)
// into the usual form (C:\dir, \\server\share).
func trimExtendedPrefix(path string) string {
	switch {
	case strings.HasPrefix(path, `\\?\UNC\`):
		return `\\` + path[len(`\\?\UNC\`):]
	case strings.HasPrefix(path, `\\?\`):
		return path[len(`\\?\`):]
	}
	return path
}
