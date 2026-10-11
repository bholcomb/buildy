package greet

import _ "embed"

//go:embed message.txt
var message string

// Hello returns the greeting and the embedded message.
func Hello() string {
	return "v1 " + message
}
