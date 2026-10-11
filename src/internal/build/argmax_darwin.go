package build

import "syscall"

// argMax is kern.argmax: how much of the arguments and environment macOS lets a program start
// with (1 MiB).
func argMax() int {
	n, err := syscall.SysctlUint32("kern.argmax")
	if err != nil || n == 0 {
		return 256 << 10
	}
	return int(n)
}
