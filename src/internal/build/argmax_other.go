//go:build !linux && !darwin

package build

// argMax is a conservative limit on the arguments and environment for other Unix systems
// (Windows measures its command line instead).
func argMax() int {
	return 256 << 10
}
