package build

import "syscall"

// argMax is how much of the arguments and environment, strings and pointers, Linux lets a
// program start with: a quarter of the stack size limit, at most 6 MiB and at least 128 KiB.
func argMax() int {
	limit := uint64(8<<20) / 4 * 3
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_STACK, &rl); err == nil && uint64(rl.Cur)/4 < limit {
		limit = uint64(rl.Cur) / 4
	}
	return int(max(limit, 128<<10))
}
