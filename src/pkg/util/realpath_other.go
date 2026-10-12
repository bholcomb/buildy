//go:build !windows

package util

import "path/filepath"

func realPath(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(real)
}
