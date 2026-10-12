// The program prints what its build options gave it: -X values, the build tags' files, and the
// build settings go recorded.
package main

import (
	"fmt"
	"runtime/debug"
	"strings"
)

var (
	version = "unset"
	msg     = "unset"
	mode    = "debug"
	tags    []string // each build tag's file adds it
)

func main() {
	fmt.Printf("version=%s msg=%s mode=%s tags=%s\n", version, msg, mode, strings.Join(tags, ","))
	info, _ := debug.ReadBuildInfo()
	for _, s := range info.Settings {
		switch s.Key {
		case "-buildmode", "-gcflags", "-trimpath":
			fmt.Printf("%s=%s\n", s.Key, s.Value)
		}
	}
}
