package cache

import (
	"os"
	"syscall"
)

func statOf(info os.FileInfo) fileStat {
	s := fileStat{Size: info.Size(), Mtime: info.ModTime().UnixNano()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		s.Ctime = st.Ctim.Nano()
		s.Ino = st.Ino
		s.Dev = uint64(st.Dev)
	}
	return s
}
