// A C-compatible library: Add, and Mode, which a build tag sets.
package main

import "C"

//export Add
func Add(a, b C.int) C.int { return a + b }

//export Mode
func Mode() C.int { return mode }

func main() {}
