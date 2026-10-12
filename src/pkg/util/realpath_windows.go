//go:build windows

package util

import (
	"os"
	"syscall"
	"unsafe"
)

var procGetFinalPathNameByHandleW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

func realPath(path string) (string, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	// No access asked: the handle only names the file. FILE_FLAG_BACKUP_SEMANTICS opens a
	// directory too.
	h, err := syscall.CreateFile(p, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer syscall.CloseHandle(h)
	buf := make([]uint16, syscall.MAX_PATH)
	for {
		// Flags 0: FILE_NAME_NORMALIZED, VOLUME_NAME_DOS (a drive letter)
		n, _, callErr := procGetFinalPathNameByHandleW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
		if n == 0 {
			return "", &os.PathError{Op: "GetFinalPathNameByHandle", Path: path, Err: callErr}
		}
		if int(n) < len(buf) {
			return trimExtendedPrefix(syscall.UTF16ToString(buf[:n])), nil
		}
		buf = make([]uint16, n) // n is the size needed, with the terminating NUL
	}
}
