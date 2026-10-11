//go:build !linux && !darwin && !windows

package cache

import "os"

// statOf: elsewhere, the size and modification time.
func statOf(info os.FileInfo) fileStat {
	return fileStat{Size: info.Size(), Mtime: info.ModTime().UnixNano()}
}
