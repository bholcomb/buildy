// A C-compatible library: Add, Mode, which a build tag sets, and LIB_LEVEL, which reaches C
// only through the header go writes (the preamble is copied into it).
package main

// #define LIB_LEVEL 1
import "C"

//export Add
func Add(a, b C.int) C.int { return a + b }

//export Mode
func Mode() C.int { return mode }

func main() {}
