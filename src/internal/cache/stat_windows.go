package cache

import (
	"os"
	"syscall"
)

// statOf: a FileInfo on Windows has no change time or file ID; the size, the last write time and
// the creation time are what tell a change.
func statOf(info os.FileInfo) fileStat {
	s := fileStat{Size: info.Size(), Mtime: info.ModTime().UnixNano()}
	if attr, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		s.Ctime = attr.CreationTime.Nanoseconds()
	}
	return s
}
